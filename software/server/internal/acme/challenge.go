package acmelifecycle

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
)

const ChallengePathPrefix = "/.well-known/acme-challenge/"

// ChallengeResponder publishes only in-memory HTTP-01 values. It deliberately
// has no filesystem or proxy fallback, and requires the request Host to match
// the hostname for which the token was registered.
type ChallengeResponder struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewChallengeResponder() *ChallengeResponder {
	return &ChallengeResponder{values: make(map[string]string)}
}

func (r *ChallengeResponder) Present(hostname, token, keyAuthorization string) error {
	hostname, err := ingressHostname(hostname)
	if err != nil || !validChallengeToken(token) || !validKeyAuthorization(token, keyAuthorization) {
		return fmt.Errorf("invalid ACME challenge")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[hostname+"\x00"+token] = keyAuthorization
	return nil
}

func (r *ChallengeResponder) Clear(hostname, token string) {
	hostname, err := ingressHostname(hostname)
	if err != nil || !validChallengeToken(token) {
		return
	}
	r.mu.Lock()
	delete(r.values, hostname+"\x00"+token)
	r.mu.Unlock()
}

func (r *ChallengeResponder) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	hostname, err := ingressHostname(request.Host)
	token := path.Base(request.URL.Path)
	if err != nil || request.Method != http.MethodGet || request.URL.Path != ChallengePathPrefix+token || !validChallengeToken(token) {
		http.NotFound(w, request)
		return
	}
	r.mu.RLock()
	value, ok := r.values[hostname+"\x00"+token]
	r.mu.RUnlock()
	if !ok {
		http.NotFound(w, request)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(value))
}

func (r *ChallengeResponder) Listen(socketPath string, groupID int) (net.Listener, error) {
	if !path.IsAbs(socketPath) || path.Clean(socketPath) != socketPath || groupID < 0 {
		return nil, fmt.Errorf("invalid ACME challenge socket")
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	fail := func(value error) (net.Listener, error) {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, value
	}
	if err = os.Chown(socketPath, 0, groupID); err != nil {
		return fail(err)
	}
	if err = os.Chmod(socketPath, 0660); err != nil {
		return fail(err)
	}
	go func() { _ = http.Serve(listener, r) }()
	return listener, nil
}

func ingressHostname(value string) (string, error) {
	value = strings.TrimSuffix(value, ".")
	if strings.Contains(value, ":") {
		host, port, err := net.SplitHostPort(value)
		if err != nil || port == "" {
			return "", fmt.Errorf("invalid host")
		}
		value = host
	}
	return ingress.NormalizeHostname(value)
}

func validChallengeToken(value string) bool {
	if len(value) < 16 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func validKeyAuthorization(token, value string) bool {
	return len(value) > len(token)+1 && len(value) <= 768 && strings.HasPrefix(value, token+".") && !strings.ContainsAny(value, "\r\n\x00")
}
