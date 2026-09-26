package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type RuntimeStatus struct {
	State string `json:"state"`
	Code  string `json:"reason_code"`
}

type Runtime interface {
	Start(string) error
	Stop() error
	Status(context.Context) RuntimeStatus
}

type Supervisor struct {
	Store     CredentialStore
	Binary    string
	User      string
	HealthURL string
	HTTP      *http.Client

	mu       sync.Mutex
	command  *exec.Cmd
	provider string
	exited   chan error
}

func (s *Supervisor) Start(providerID string) error {
	if !providerIDPattern.MatchString(providerID) {
		return &Error{Code: "invalid_configuration"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.command != nil {
		if s.provider == providerID {
			return nil
		}
		return &Error{Code: "another_provider_active"}
	}
	tokenFile, err := s.Store.TunnelTokenFile(providerID)
	if err != nil {
		return err
	}
	defer tokenFile.Close()
	binary := s.Binary
	if binary == "" {
		binary = "/usr/bin/cloudflared"
	}
	if binary != "/usr/bin/cloudflared" && s.Binary == "" {
		return &Error{Code: "runtime_unavailable"}
	}
	accountName := s.User
	if accountName == "" {
		accountName = "kitpro-cloudflared"
	}
	account, err := user.Lookup(accountName)
	if err != nil {
		return &Error{Code: "runtime_unavailable"}
	}
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 {
		return &Error{Code: "runtime_unavailable"}
	}
	command := runtimeCommand(binary)
	command.ExtraFiles = []*os.File{tokenFile}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), NoSetGroups: true}, Setpgid: true}
	if err = command.Start(); err != nil {
		return &Error{Code: "runtime_unavailable"}
	}
	s.command, s.provider, s.exited = command, providerID, make(chan error, 1)
	go func(cmd *exec.Cmd, exited chan error) { exited <- cmd.Wait() }(command, s.exited)
	return nil
}

func runtimeCommand(binary string) *exec.Cmd {
	return exec.Command(binary, "tunnel", "--no-autoupdate", "--metrics", "127.0.0.1:20241", "run", "--token-file", "/proc/self/fd/3")
}

func (s *Supervisor) Stop() error {
	s.mu.Lock()
	command, exited := s.command, s.exited
	if command == nil {
		s.mu.Unlock()
		return nil
	}
	s.command, s.provider, s.exited = nil, "", nil
	s.mu.Unlock()
	if command.Process == nil {
		return nil
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
		return nil
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-exited
		return nil
	}
}

func (s *Supervisor) Status(ctx context.Context) RuntimeStatus {
	s.mu.Lock()
	command, exited := s.command, s.exited
	s.mu.Unlock()
	if command == nil || command.Process == nil {
		return RuntimeStatus{State: "offline", Code: "runtime_stopped"}
	}
	select {
	case <-exited:
		s.mu.Lock()
		if s.command == command {
			s.command, s.provider, s.exited = nil, "", nil
		}
		s.mu.Unlock()
		return RuntimeStatus{State: "failed", Code: "runtime_exited"}
	default:
	}
	healthURL := s.HealthURL
	if healthURL == "" {
		healthURL = "http://127.0.0.1:20241/ready"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return RuntimeStatus{State: "degraded", Code: "health_unavailable"}
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return RuntimeStatus{State: "connecting", Code: "health_pending"}
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RuntimeStatus{State: "degraded", Code: "health_failed"}
	}
	return RuntimeStatus{State: "ready", Code: "connected"}
}

func (s *Supervisor) Restart(providerID string) error {
	if err := s.Stop(); err != nil {
		return fmt.Errorf("stop Cloudflare runtime: %w", err)
	}
	return s.Start(providerID)
}
