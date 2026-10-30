package acmelifecycle

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

const maxWorkerFrame = 384 * 1024

type WorkerRequest struct {
	Hostname    string `json:"hostname"`
	Email       string `json:"email"`
	Directory   string `json:"directory"`
	AcceptTerms bool   `json:"accept_terms"`
}

type WorkerResponse struct {
	OK             bool   `json:"ok"`
	CertificatePEM []byte `json:"certificate_pem,omitempty"`
	PrivateKeyPEM  []byte `json:"private_key_pem,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
}

type Worker struct {
	Accounts  AccountStore
	Manager   Manager
	Publisher Publisher
}

func (w Worker) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return err
		}
		go w.serveConnection(connection)
	}
}

func (w Worker) serveConnection(connection net.Conn) {
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok || !peerIsRoot(unixConnection) {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(10 * time.Minute))
	var request WorkerRequest
	if err := readFrame(connection, &request); err != nil {
		_ = writeFrame(connection, WorkerResponse{ErrorCode: "invalid_request"})
		return
	}
	key, err := w.Accounts.LoadOrCreate(request.Directory)
	if err != nil {
		_ = writeFrame(connection, WorkerResponse{ErrorCode: "account_key_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	material, err := w.Manager.Issue(ctx, Request{Hostname: request.Hostname, Email: request.Email, Directory: request.Directory, AcceptTerms: request.AcceptTerms, AccountKey: key}, w.Publisher)
	if err != nil {
		code := "issuance_failed"
		var lifecycleError *Error
		if errors.As(err, &lifecycleError) {
			code = lifecycleError.Code
		}
		_ = writeFrame(connection, WorkerResponse{ErrorCode: code})
		return
	}
	_ = writeFrame(connection, WorkerResponse{OK: true, CertificatePEM: material.CertificatePEM, PrivateKeyPEM: material.PrivateKeyPEM})
}

type WorkerClient struct{ SocketPath string }

func (c WorkerClient) Issue(ctx context.Context, request WorkerRequest) (IssuedMaterial, error) {
	path := c.SocketPath
	if path == "" {
		path = "/run/kitpro/acme-client.sock"
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return IssuedMaterial{}, &Error{Code: "worker_unavailable", Err: errors.New("ACME worker is unavailable")}
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err = writeFrame(connection, request); err != nil {
		return IssuedMaterial{}, &Error{Code: "worker_unavailable", Err: errors.New("ACME worker request failed")}
	}
	var response WorkerResponse
	if err = readFrame(connection, &response); err != nil {
		return IssuedMaterial{}, &Error{Code: "worker_unavailable", Err: errors.New("ACME worker response failed")}
	}
	if !response.OK {
		if !validWorkerErrorCode(response.ErrorCode) {
			response.ErrorCode = "issuance_failed"
		}
		return IssuedMaterial{}, &Error{Code: response.ErrorCode, Err: errors.New("ACME issuance failed")}
	}
	if len(response.CertificatePEM) == 0 || len(response.PrivateKeyPEM) == 0 {
		return IssuedMaterial{}, &Error{Code: "invalid_material", Err: errors.New("ACME worker returned invalid material")}
	}
	return IssuedMaterial{CertificatePEM: response.CertificatePEM, PrivateKeyPEM: response.PrivateKeyPEM}, nil
}

func validWorkerErrorCode(value string) bool {
	switch value {
	case "invalid_request", "account_key_unavailable", "invalid_hostname", "invalid_directory", "not_configured", "hostname_unresolved", "issuance_failed", "account_registration_failed", "order_failed", "authorization_failed", "challenge_unavailable", "challenge_failed", "challenge_publish_failed", "key_generation_failed", "csr_failed", "key_encoding_failed", "invalid_material":
		return true
	default:
		return false
	}
}

func peerIsRoot(connection *net.UnixConn) bool {
	file, err := connection.File()
	if err != nil {
		return false
	}
	defer file.Close()
	credentials, err := unix.GetsockoptUcred(int(file.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	return err == nil && credentials.Uid == 0
}

func writeFrame(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maxWorkerFrame {
		return fmt.Errorf("invalid ACME worker frame")
	}
	if err = binary.Write(writer, binary.BigEndian, uint32(len(encoded))); err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func readFrame(reader io.Reader, destination any) error {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 || length > maxWorkerFrame {
		return fmt.Errorf("invalid ACME worker frame")
	}
	encoded := make([]byte, length)
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid ACME worker frame")
	}
	return nil
}
