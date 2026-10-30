package ingress

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultValidatorSocket = "/run/kitpro/ingress-validator.sock"
	maxValidatorFrame      = 16 * 1024
)

type validatorRequest struct {
	Target     string `json:"target"`
	Generation int64  `json:"generation"`
}

type validatorResponse struct {
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
}

type ValidatorClient struct{ SocketPath string }

func (c ValidatorClient) Check(ctx context.Context, target string, generation int64) error {
	if !validValidationTarget(target, generation) {
		return errors.New("invalid validation target")
	}
	path := c.SocketPath
	if path == "" {
		path = defaultValidatorSocket
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return errors.New("ingress validator is unavailable")
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err = writeValidatorFrame(connection, validatorRequest{Target: target, Generation: generation}); err != nil {
		return errors.New("ingress validation request failed")
	}
	var response validatorResponse
	if err = readValidatorFrame(connection, &response); err != nil {
		return errors.New("ingress validation response failed")
	}
	if !response.OK {
		return errors.New("nginx configuration validation failed")
	}
	return nil
}

type ValidatorWorker struct {
	Root string
	Run  func(context.Context, string, ...string) ([]byte, error)
}

func (w ValidatorWorker) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return err
		}
		go w.serveConnection(connection)
	}
}

func (w ValidatorWorker) serveConnection(connection net.Conn) {
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok || !validatorPeerIsRoot(unixConnection) {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	var request validatorRequest
	if readValidatorFrame(connection, &request) != nil || !validValidationTarget(request.Target, request.Generation) {
		_ = writeValidatorFrame(connection, validatorResponse{ErrorCode: "invalid_request"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.validate(ctx, request); err != nil {
		_ = writeValidatorFrame(connection, validatorResponse{ErrorCode: "validation_failed"})
		return
	}
	_ = writeValidatorFrame(connection, validatorResponse{OK: true})
}

func (w ValidatorWorker) validate(ctx context.Context, request validatorRequest) error {
	if !validValidationTarget(request.Target, request.Generation) {
		return errors.New("invalid validation target")
	}
	root := w.Root
	if root == "" {
		root = defaultIngressRoot
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("invalid ingress runtime")
	}
	prefix, config := root+"/", "current/nginx.conf"
	if request.Target == "generation" {
		config = filepath.Join("generations", fmt.Sprintf("%d", request.Generation), "nginx.conf")
	}
	run := w.Run
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	if _, err := run(ctx, "/usr/sbin/nginx", "-t", "-p", prefix, "-c", config); err != nil {
		return errors.New("nginx configuration validation failed")
	}
	return nil
}

func validValidationTarget(target string, generation int64) bool {
	return (target == "current" && generation == 0) || (target == "generation" && generation > 0)
}

func validatorPeerIsRoot(connection *net.UnixConn) bool {
	file, err := connection.File()
	if err != nil {
		return false
	}
	defer file.Close()
	credentials, err := unix.GetsockoptUcred(int(file.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	return err == nil && credentials.Uid == 0
}

func writeValidatorFrame(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maxValidatorFrame {
		return errors.New("invalid validator frame")
	}
	if err = binary.Write(writer, binary.BigEndian, uint32(len(encoded))); err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func readValidatorFrame(reader io.Reader, destination any) error {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 || length > maxValidatorFrame {
		return errors.New("invalid validator frame")
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
		return errors.New("invalid validator frame")
	}
	return nil
}
