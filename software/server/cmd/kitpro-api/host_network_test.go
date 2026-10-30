package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

type apiNetworkRunner struct{}

func (apiNetworkRunner) Run(_ context.Context, name string) ([]byte, error) {
	values := map[string]string{
		"links":     `[{"ifindex":1,"ifname":"lo","flags":["UP"],"mtu":65536,"operstate":"UNKNOWN","linkinfo":{"info_kind":"loopback"}},{"ifindex":2,"ifname":"ens18","flags":["UP","LOWER_UP"],"mtu":1500,"operstate":"UP","address":"52:54:00:00:00:01"}]`,
		"addresses": `[{"ifindex":1,"addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},{"ifindex":2,"addr_info":[{"family":"inet","local":"192.0.2.10","prefixlen":24,"scope":"global"}]}]`,
		"routes4":   `[{"dst":"default","gateway":"192.0.2.1","dev":"ens18","table":"main","protocol":"dhcp"}]`,
		"routes6":   `[]`, "rules4": `[]`, "rules6": `[]`,
	}
	return []byte(values[name]), nil
}
func apiNetworkProvider() hostadmin.NetworkProvider {
	return hostadmin.NetworkProvider{Runner: apiNetworkRunner{}, ReadFile: func(path string, _ int64) ([]byte, error) {
		if path == "/etc/resolv.conf" {
			return []byte("nameserver 192.0.2.53\n"), nil
		}
		return []byte("0"), nil
	}, ReadLink: func(string) (string, error) { return "", os.ErrNotExist }, Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, Now: time.Now}
}
func TestNetworkEndpointsRequireSessionAndReturnTypedViews(t *testing.T) {
	a, session, _ := newTestApp(t)
	a.hostNetworkProvider = apiNetworkProvider()
	cases := []string{"/api/v1/host/network", "/api/v1/host/network/interfaces", "/api/v1/host/network/interfaces/ens18", "/api/v1/host/network/routes", "/api/v1/host/network/dns", "/api/v1/host/network/stats"}
	for _, path := range cases {
		var handler http.HandlerFunc = a.hostNetwork
		if path != "/api/v1/host/network" {
			handler = a.hostNetworkSubresource
		}
		route := a.guard(handler)
		denied := httptest.NewRecorder()
		route(denied, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil))
		if denied.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous %d", path, denied.Code)
		}
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		request.AddCookie(session)
		response := httptest.NewRecorder()
		route(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}
func TestNetworkInterfaceValidationAndMissingInterface(t *testing.T) {
	a := &app{hostNetworkProvider: apiNetworkProvider()}
	for _, path := range []string{"/api/v1/host/network/interfaces/..%2Fetc%2Fpasswd", "/api/v1/host/network/interfaces/bad%20name"} {
		response := httptest.NewRecorder()
		a.hostNetworkSubresource(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	a.hostNetworkSubresource(response, httptest.NewRequest(http.MethodGet, "/api/v1/host/network/interfaces/missing0", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing status %d", response.Code)
	}
}
func TestNetworkDiagnosticsRequireCSRFAndEnforcePolicy(t *testing.T) {
	a, session, csrf := newTestApp(t)
	network := apiNetworkProvider()
	policy := hostadmin.DefaultDiagnosticPolicy("192.0.2.80", "443")
	diagnostics := hostadmin.NewDiagnosticProvider(network, policy)
	diagnostics.Dial = func(context.Context, string, string) (net.Conn, error) {
		left, right := net.Pipe()
		go right.Close()
		return left, nil
	}
	a.hostDiagnosticProvider = diagnostics
	route := a.guard(a.hostNetworkDiagnostics)
	noSession := httptest.NewRecorder()
	route(noSession, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/host/network/diagnostics", strings.NewReader(`{"operation":"tcp","target":"192.0.2.80","port":443}`)))
	if noSession.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous %d", noSession.Code)
	}
	noCSRF := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/host/network/diagnostics", strings.NewReader(`{"operation":"tcp","target":"192.0.2.80","port":443}`))
	request.AddCookie(session)
	route(noCSRF, request)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("csrf status %d", noCSRF.Code)
	}
	restricted := httptest.NewRecorder()
	route(restricted, authenticatedRequest(http.MethodPost, "/api/v1/host/network/diagnostics", `{"operation":"tcp","target":"169.254.169.254","port":443}`, session, csrf))
	if restricted.Code != http.StatusForbidden {
		t.Fatalf("restricted %d %s", restricted.Code, restricted.Body.String())
	}
	allowed := httptest.NewRecorder()
	route(allowed, authenticatedRequest(http.MethodPost, "/api/v1/host/network/diagnostics", `{"operation":"tcp","target":"192.0.2.80","port":443}`, session, csrf))
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"connected"`) {
		t.Fatalf("allowed %d %s", allowed.Code, allowed.Body.String())
	}
	malformed := httptest.NewRecorder()
	route(malformed, authenticatedRequest(http.MethodPost, "/api/v1/host/network/diagnostics", `{"operation":"dns","target":"example.test","command":"id"}`, session, csrf))
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("unknown field %d", malformed.Code)
	}
}
