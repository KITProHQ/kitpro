package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func seedIngressInstallation(t *testing.T, a *app, installation, application string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, installation, application, "test", "running", 1, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := exposure.UpsertBinding(context.Background(), a.db, installation, exposure.ServiceBinding{ServiceID: "web", Transport: "tcp", ContainerPort: 3001, Mode: exposure.Loopback, HostAddress: "127.0.0.1", HostPort: 20001}, "", 0); err != nil {
		t.Fatal(err)
	}
}

func TestIngressCreateUsesTypedHelperAndCommitsGeneration(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedIngressInstallation(t, a, "inst-ingress01", "uptime-kuma")
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: map[string]any{"generation": 1, "previous_generation": 0, "configuration_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, nil
	}
	recorder := httptest.NewRecorder()
	body := `{"installation_id":"inst-ingress01","service_id":"web","hostname":"status.example.test","exposure_mode":"lan","upstream_protocol":"http","tls_policy":"none","certificate_id":"","provider_id":"","enabled":true}`
	a.guard(a.ingressRoutes)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/ingress", body, session, csrf))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if received.Operation != "ApplyIngressGeneration" || len(received.IngressRoutes) != 1 || received.IngressRoutes[0].Hostname != "status.example.test" || received.IngressRoutes[0].InstallationID != "inst-ingress01" {
		t.Fatalf("helper request=%#v", received)
	}
	var desired string
	var generation int
	if err := a.db.QueryRow(`SELECT desired_state,applied_generation FROM ingress_routes WHERE hostname='status.example.test'`).Scan(&desired, &generation); err != nil || desired != "enabled" || generation != 1 {
		t.Fatalf("route desired=%q generation=%d err=%v", desired, generation, err)
	}
}

func TestIngressRejectsIneligibleServiceAndUnknownJSON(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedPiHoleInstallation(t, a, "inst-pihole99", localLANTestAddress(t))
	for _, body := range []string{
		`{"installation_id":"inst-pihole99","service_id":"admin","hostname":"dns.example.test","exposure_mode":"lan","upstream_protocol":"http","tls_policy":"none","enabled":true}`,
		`{"installation_id":"inst-pihole99","service_id":"admin","hostname":"dns.example.test","exposure_mode":"lan","upstream_protocol":"http","tls_policy":"none","enabled":true,"configuration":"evil"}`,
	} {
		recorder := httptest.NewRecorder()
		a.guard(a.ingressRoutes)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/ingress", body, session, csrf))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestIngressFailedCandidateRemovesIntentAndPreservesExistingState(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedIngressInstallation(t, a, "inst-ingress02", "uptime-kuma")
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{RequestID: request.ID, ErrorCode: "Ingressvalidation_failed", Error: "candidate rejected"}, nil
	}
	recorder := httptest.NewRecorder()
	body := `{"installation_id":"inst-ingress02","service_id":"web","hostname":"bad.example.test","exposure_mode":"lan","upstream_protocol":"http","tls_policy":"none","enabled":true}`
	a.guard(a.ingressRoutes)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/ingress", body, session, csrf))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM ingress_routes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed route persisted count=%d err=%v", count, err)
	}
}

func TestCertificateAPIResponseNeverEchoesPrivateMaterial(t *testing.T) {
	a, session, csrf := newTestApp(t)
	const secret = "-----BEGIN PRIVATE KEY-----\nKNOWN-PRIVATE-KEY\n-----END PRIVATE KEY-----"
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		if request.Operation != "InstallCertificateMaterial" || request.PrivateKeyPEM != secret {
			t.Fatalf("request=%#v", request)
		}
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: map[string]any{"certificate_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "not_before": "2026-09-01T00:00:00Z", "not_after": "2026-12-01T00:00:00Z"}}, nil
	}
	recorder := httptest.NewRecorder()
	payload, _ := json.Marshal(map[string]string{"hostname": "app.example.test", "certificate_pem": "certificate", "private_key_pem": secret})
	a.guard(a.certificates)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/certificates", string(payload), session, csrf))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if contains := string(recorder.Body.Bytes()); contains == secret || len(contains) > 0 && json.Valid([]byte(contains)) == false {
		t.Fatalf("unsafe response %q", contains)
	}
}
