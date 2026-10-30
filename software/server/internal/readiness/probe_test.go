package readiness

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

func TestHTTPProbeUsesLoopbackAndExpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := net.LookupPort("tcp", portText)
	observation := (Prober{}).Probe(context.Background(), &manifest.ReadinessCheck{Type: "http", Path: "/health", ExpectedStatus: []int{204}, TimeoutSeconds: 2}, host, port)
	if observation.State != "ready" || observation.ReasonCode != "http_status_expected" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestProbeRejectsNonLoopbackTarget(t *testing.T) {
	observation := (Prober{}).Probe(context.Background(), &manifest.ReadinessCheck{Type: "tcp", TimeoutSeconds: 1}, "192.0.2.1", 80)
	if observation.ReasonCode != "invalid_probe_target" {
		t.Fatalf("observation=%+v", observation)
	}
}
