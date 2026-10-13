package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	testToken   = "cloudflare-test-token-0123456789"
	testAccount = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testZone    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testTunnel  = "12345678-1234-4123-8123-123456789abc"
	testRecord  = "cccccccccccccccccccccccccccccccc"
)

type requestLog struct {
	mu      sync.Mutex
	entries []string
	bodies  []map[string]any
}

func (l *requestLog) add(request *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, request.Method+" "+request.URL.RequestURI())
	if request.Body != nil {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		l.bodies = append(l.bodies, body)
	}
}

func respond(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": status >= 200 && status < 300, "result": result, "errors": []any{}})
}

func TestValidateCredentialChecksTokenTunnelAndDNSPermissions(t *testing.T) {
	log := &requestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		log.add(request)
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Fatal("authorization header missing")
		}
		switch request.URL.Path {
		case "/user/tokens/verify":
			respond(w, 200, map[string]any{"status": "active"})
		case "/accounts/" + testAccount + "/cfd_tunnel/" + testTunnel:
			respond(w, 200, Tunnel{ID: testTunnel, AccountID: testAccount, ConfigSrc: "cloudflare", Status: "healthy"})
		case "/zones/" + testZone + "/dns_records":
			respond(w, 200, []DNSRecord{})
		default:
			respond(w, 404, nil)
		}
	}))
	defer server.Close()
	tunnel, err := (Client{BaseURL: server.URL, HTTP: server.Client()}).ValidateCredential(context.Background(), testToken, testAccount, testZone, testTunnel)
	if err != nil || tunnel.Status != "healthy" || len(log.entries) != 3 {
		t.Fatalf("tunnel=%+v calls=%v err=%v", tunnel, log.entries, err)
	}
}

func TestValidateCredentialClassifiesFailuresWithoutLeakingToken(t *testing.T) {
	for name, test := range map[string]struct {
		status int
		code   string
	}{
		"invalid":     {http.StatusUnauthorized, "invalid_token"},
		"permissions": {http.StatusForbidden, "insufficient_permissions"},
		"unavailable": {http.StatusServiceUnavailable, "api_unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				respond(w, test.status, map[string]any{"message": testToken})
			}))
			defer server.Close()
			_, err := (Client{BaseURL: server.URL, HTTP: server.Client()}).ValidateCredential(context.Background(), testToken, testAccount, testZone, testTunnel)
			var providerError *Error
			if !errors.As(err, &providerError) || providerError.Code != test.code || strings.Contains(err.Error(), testToken) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAddAndRemoveRoutePreserveCatchallAndCheckOwnership(t *testing.T) {
	log := &requestLog{}
	configured := []IngressRule{{Service: "http_status:404"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		log.add(request)
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/configurations"):
			respond(w, 200, map[string]any{"config": map[string]any{"ingress": configured}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/"+testZone+"/dns_records":
			respond(w, 200, []DNSRecord{})
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/configurations"):
			body := log.bodies[len(log.bodies)-1]
			encoded, _ := json.Marshal(body["config"])
			var config struct {
				Ingress []IngressRule `json:"ingress"`
			}
			_ = json.Unmarshal(encoded, &config)
			configured = config.Ingress
			respond(w, 200, map[string]any{})
		case request.Method == http.MethodPost && request.URL.Path == "/zones/"+testZone+"/dns_records":
			respond(w, 200, DNSRecord{ID: testRecord, Name: "app.example.test", Type: "CNAME", Content: testTunnel + ".cfargotunnel.com", Comment: "Managed by KITPro Server"})
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/"+testZone+"/dns_records/"+testRecord:
			respond(w, 200, map[string]any{})
		default:
			respond(w, 404, nil)
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	record, err := client.AddRoute(context.Background(), testToken, testAccount, testZone, testTunnel, "app.example.test")
	if err != nil || record.ID != testRecord || len(configured) != 2 || configured[0].Service != originService || configured[1].Service != "http_status:404" {
		t.Fatalf("record=%+v configured=%+v err=%v", record, configured, err)
	}
	if err = client.RemoveRoute(context.Background(), testToken, testAccount, testZone, testTunnel, "app.example.test", testRecord); err != nil || len(configured) != 1 || configured[0].Service != "http_status:404" {
		t.Fatalf("configured=%+v err=%v", configured, err)
	}
}

func TestRouteCollisionAndOwnershipConflictAreFailClosed(t *testing.T) {
	for name, test := range map[string]struct {
		rule IngressRule
		code string
	}{
		"existing KITPro route": {rule: IngressRule{Hostname: "app.example.test", Service: originService}, code: "route_conflict"},
		"foreign route":         {rule: IngressRule{Hostname: "app.example.test", Service: "http://127.0.0.1:9999"}, code: "route_ownership_conflict"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/configurations") {
					respond(w, 200, map[string]any{"config": map[string]any{"ingress": []IngressRule{test.rule, {Service: "http_status:404"}}}})
					return
				}
				respond(w, 200, map[string]any{})
			}))
			defer server.Close()
			client := Client{BaseURL: server.URL, HTTP: server.Client()}
			var err error
			if test.code == "route_conflict" {
				_, err = client.AddRoute(context.Background(), testToken, testAccount, testZone, testTunnel, "app.example.test")
			} else {
				err = client.RemoveRoute(context.Background(), testToken, testAccount, testZone, testTunnel, "app.example.test", testRecord)
			}
			var providerError *Error
			if !errors.As(err, &providerError) || providerError.Code != test.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestInvalidDNSResponseRollsBackTunnelAndOwnedRecord(t *testing.T) {
	configured := []IngressRule{{Service: "http_status:404"}}
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/configurations"):
			respond(w, 200, map[string]any{"config": map[string]any{"ingress": configured}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/"+testZone+"/dns_records":
			respond(w, 200, []DNSRecord{})
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/configurations"):
			var body struct {
				Config struct {
					Ingress []IngressRule `json:"ingress"`
				} `json:"config"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			configured = body.Config.Ingress
			respond(w, 200, map[string]any{})
		case request.Method == http.MethodPost && request.URL.Path == "/zones/"+testZone+"/dns_records":
			respond(w, 200, DNSRecord{ID: testRecord, Name: "wrong.example.test", Type: "CNAME", Content: testTunnel + ".cfargotunnel.com", Comment: "Managed by KITPro Server"})
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/"+testZone+"/dns_records/"+testRecord:
			deleted = true
			respond(w, 200, map[string]any{})
		default:
			respond(w, 404, nil)
		}
	}))
	defer server.Close()
	_, err := (Client{BaseURL: server.URL, HTTP: server.Client()}).AddRoute(context.Background(), testToken, testAccount, testZone, testTunnel, "app.example.test")
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Code != "invalid_provider_response" {
		t.Fatalf("error=%v", err)
	}
	if !deleted || len(configured) != 1 || configured[0].Service != "http_status:404" {
		t.Fatalf("deleted=%v configured=%+v", deleted, configured)
	}
}
