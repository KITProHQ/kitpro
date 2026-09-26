package readiness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

type Observation struct {
	State      string    `json:"state"`
	ReasonCode string    `json:"reason_code"`
	Summary    string    `json:"summary"`
	CheckedAt  time.Time `json:"checked_at"`
}

type Prober struct {
	Dialer *net.Dialer
	Client *http.Client
	Now    func() time.Time
}

func (p Prober) Probe(ctx context.Context, check *manifest.ReadinessCheck, address string, port int) Observation {
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	if check == nil {
		return Observation{State: "unknown", ReasonCode: "readiness_not_declared", Summary: "Application has no typed readiness check", CheckedAt: now}
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsLoopback() || port < 1 || port > 65535 {
		return Observation{State: "failed", ReasonCode: "invalid_probe_target", Summary: "Readiness target is not a managed loopback binding", CheckedAt: now}
	}
	timeout := time.Duration(check.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := net.JoinHostPort(address, strconv.Itoa(port))
	switch check.Type {
	case "runtime":
		return Observation{State: "ready", ReasonCode: "runtime_running", Summary: "Managed runtime is running", CheckedAt: now}
	case "tcp":
		dialer := p.Dialer
		if dialer == nil {
			dialer = &net.Dialer{}
		}
		connection, err := dialer.DialContext(probeCtx, "tcp", target)
		if err != nil {
			return Observation{State: "failed", ReasonCode: "tcp_unavailable", Summary: "Application TCP endpoint is unavailable", CheckedAt: now}
		}
		_ = connection.Close()
		return Observation{State: "ready", ReasonCode: "tcp_connected", Summary: "Application TCP endpoint accepted a connection", CheckedAt: now}
	case "http":
		client := p.Client
		if client == nil {
			client = &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+target+check.Path, nil)
		if err != nil {
			return failed(now, "http_probe_invalid")
		}
		response, err := client.Do(request)
		if err != nil {
			return failed(now, "http_unavailable")
		}
		_ = response.Body.Close()
		for _, expected := range check.ExpectedStatus {
			if response.StatusCode == expected {
				return Observation{State: "ready", ReasonCode: "http_status_expected", Summary: fmt.Sprintf("Application returned HTTP %d", response.StatusCode), CheckedAt: now}
			}
		}
		return Observation{State: "failed", ReasonCode: "http_status_unexpected", Summary: fmt.Sprintf("Application returned unexpected HTTP %d", response.StatusCode), CheckedAt: now}
	case "docker-health":
		return Observation{State: "unknown", ReasonCode: "docker_health_requires_runtime_observation", Summary: "Docker health has not been observed yet", CheckedAt: now}
	default:
		return failed(now, "unsupported_readiness_type")
	}
}

func failed(now time.Time, code string) Observation {
	return Observation{State: "failed", ReasonCode: code, Summary: "Application readiness check failed", CheckedAt: now}
}
