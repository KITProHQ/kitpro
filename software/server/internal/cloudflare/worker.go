package cloudflare

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const maxWorkerFrame = 64 * 1024

type WorkerRequest struct {
	Operation     string `json:"operation"`
	ProviderID    string `json:"provider_id"`
	AccountID     string `json:"account_id,omitempty"`
	ZoneID        string `json:"zone_id,omitempty"`
	TunnelID      string `json:"tunnel_id,omitempty"`
	APIToken      string `json:"api_token,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	ConfirmRemote bool   `json:"confirm_remote,omitempty"`
}

type WorkerResponse struct {
	OK        bool            `json:"ok"`
	Config    *ProviderConfig `json:"config,omitempty"`
	Runtime   *RuntimeStatus  `json:"runtime,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

type Worker struct {
	Manager Manager
	mu      sync.Mutex
}

func (w *Worker) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return err
		}
		go w.serveConnection(connection)
	}
}

func (w *Worker) serveConnection(connection net.Conn) {
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok || !rootPeer(unixConnection) {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))
	var request WorkerRequest
	if err := readWorkerFrame(connection, &request); err != nil {
		_ = writeWorkerFrame(connection, WorkerResponse{ErrorCode: "invalid_request"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w.mu.Lock()
	response := w.execute(ctx, request)
	w.mu.Unlock()
	request.APIToken = ""
	_ = writeWorkerFrame(connection, response)
}

func (w *Worker) execute(ctx context.Context, request WorkerRequest) WorkerResponse {
	var config ProviderConfig
	var runtimeStatus RuntimeStatus
	var err error
	switch request.Operation {
	case "configure":
		config, err = w.Manager.Configure(ctx, ProviderConfig{ProviderID: request.ProviderID, AccountID: request.AccountID, ZoneID: request.ZoneID, TunnelID: request.TunnelID, Routes: map[string]string{}}, request.APIToken)
	case "enable":
		config, err = w.Manager.Enable(request.ProviderID)
	case "disable":
		config, err = w.Manager.Disable(request.ProviderID)
	case "add-route":
		config, err = w.Manager.AddRoute(ctx, request.ProviderID, request.Hostname)
	case "remove-remote-route":
		if !request.ConfirmRemote {
			err = &Error{Code: "remote_confirmation_required"}
		} else {
			config, err = w.Manager.RemoveRemoteRoute(ctx, request.ProviderID, request.Hostname)
		}
	case "unlink":
		err = w.Manager.Unlink(request.ProviderID)
	case "delete-remote-tunnel":
		if !request.ConfirmRemote {
			err = &Error{Code: "remote_confirmation_required"}
		} else {
			err = w.Manager.DeleteRemoteTunnel(ctx, request.ProviderID)
		}
	case "health":
		config, runtimeStatus, err = w.Manager.Health(ctx, request.ProviderID)
	default:
		err = &Error{Code: "operation_not_permitted"}
	}
	if err != nil {
		code := "operation_failed"
		var providerError *Error
		if errors.As(err, &providerError) && validErrorCode(providerError.Code) {
			code = providerError.Code
		}
		response := WorkerResponse{ErrorCode: code}
		if request.Operation == "health" {
			config.APITokenHash = ""
			response.Config, response.Runtime = &config, &runtimeStatus
		}
		return response
	}
	config.APITokenHash = ""
	response := WorkerResponse{OK: true}
	if request.Operation != "unlink" && request.Operation != "delete-remote-tunnel" {
		response.Config = &config
	}
	if request.Operation == "health" {
		response.Runtime = &runtimeStatus
	}
	return response
}

type WorkerClient struct{ SocketPath string }

func (c WorkerClient) Do(ctx context.Context, request WorkerRequest) (WorkerResponse, error) {
	path := c.SocketPath
	if path == "" {
		path = "/run/kitpro/cloudflare.sock"
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return WorkerResponse{}, &Error{Code: "worker_unavailable"}
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err = writeWorkerFrame(connection, request); err != nil {
		return WorkerResponse{}, &Error{Code: "worker_unavailable"}
	}
	request.APIToken = ""
	var response WorkerResponse
	if err = readWorkerFrame(connection, &response); err != nil {
		return WorkerResponse{}, &Error{Code: "worker_unavailable"}
	}
	if !response.OK {
		if !validErrorCode(response.ErrorCode) {
			response.ErrorCode = "operation_failed"
		}
		return response, &Error{Code: response.ErrorCode}
	}
	return response, nil
}

func rootPeer(connection *net.UnixConn) bool {
	file, err := connection.File()
	if err != nil {
		return false
	}
	defer file.Close()
	credentials, err := unix.GetsockoptUcred(int(file.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	return err == nil && credentials.Uid == 0
}

func writeWorkerFrame(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maxWorkerFrame {
		return fmt.Errorf("invalid Cloudflare worker frame")
	}
	if err = binary.Write(writer, binary.BigEndian, uint32(len(encoded))); err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func readWorkerFrame(reader io.Reader, destination any) error {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 || length > maxWorkerFrame {
		return fmt.Errorf("invalid Cloudflare worker frame")
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
		return fmt.Errorf("invalid Cloudflare worker frame")
	}
	return nil
}

func validErrorCode(value string) bool {
	switch value {
	case "invalid_request", "invalid_configuration", "invalid_token", "insufficient_permissions", "api_unavailable", "provider_rejected", "invalid_provider_response", "tunnel_mismatch", "invalid_tunnel_token", "invalid_hostname", "route_conflict", "route_ownership_conflict", "route_not_found", "invalid_tunnel_configuration", "provider_not_configured", "provider_state_invalid", "previous_credential_unavailable", "runtime_unavailable", "runtime_restart_failed", "another_provider_active", "provider_disabled", "remote_routes_remain", "provider_must_be_disabled", "provider_unavailable", "remote_confirmation_required", "operation_not_permitted", "operation_failed", "worker_unavailable":
		return true
	default:
		return false
	}
}
