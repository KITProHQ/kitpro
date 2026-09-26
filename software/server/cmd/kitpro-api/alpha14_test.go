package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/ingress"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/readiness"
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

func TestACMECertificateLifecycleIsTypedAndCredentialFree(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedIngressInstallation(t, a, "inst-acme0001", "uptime-kuma")
	var certificateID string
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		if request.Operation == "IssueACMECertificate" {
			if request.CertificatePEM != "" || request.PrivateKeyPEM != "" || request.ACMEEmail != "admin@example.test" || request.ACMEDirectory != "letsencrypt-staging" || !request.AcceptACMETerms {
				t.Fatalf("unsafe or incomplete ACME request: %#v", request)
			}
			certificateID = request.CertificateID
			return protocol.Response{OK: true, Result: map[string]any{"certificate_sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "not_before": "2026-09-01T00:00:00Z", "not_after": "2026-12-01T00:00:00Z"}}, nil
		}
		if request.Operation == "RenewACMECertificate" && request.CertificateID == certificateID {
			return protocol.Response{ErrorCode: "ACMERejected", Error: "renewal failed"}, nil
		}
		t.Fatalf("unexpected helper request: %#v", request)
		return protocol.Response{}, nil
	}
	recorder := httptest.NewRecorder()
	body := `{"method":"acme","hostname":"app.example.test","email":"admin@example.test","directory":"letsencrypt-staging","accept_terms":true,"installation_id":"inst-acme0001","service_id":"web"}`
	a.guard(a.certificates)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/certificates", body, session, csrf))
	if recorder.Code != http.StatusCreated || certificateID == "" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "admin@example.test") {
		t.Fatal("ACME contact leaked in certificate response")
	}
	recorder = httptest.NewRecorder()
	a.guard(a.certificates)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/certificates/"+certificateID+"/renew", `{}`, session, csrf))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("renewal status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var state, digest, errorCode string
	if err := a.db.QueryRow(`SELECT state,leaf_sha256,last_error_code FROM certificates WHERE certificate_id=?`, certificateID).Scan(&state, &digest, &errorCode); err != nil || state != "renewal-failed" || len(digest) != 64 || errorCode != "renewal_failed" {
		t.Fatalf("state=%q digest=%q error=%q err=%v", state, digest, errorCode, err)
	}
}

func TestCloudflareProviderCredentialIsWriteOnlyAndLocalDisablePreservesRemoteState(t *testing.T) {
	a, session, csrf := newTestApp(t)
	const token = "known-cloudflare-token-012345678901"
	var operationsSeen []string
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		operationsSeen = append(operationsSeen, request.Operation)
		if request.Operation == "ConfigureCloudflareProvider" {
			if request.ProviderToken != token || request.ProviderAccount == "" || request.ProviderZone == "" || request.ProviderTunnel == "" {
				t.Fatalf("incomplete provider configuration: %#v", request)
			}
		}
		return protocol.Response{OK: true, Result: map[string]any{"provider_id": request.ProviderID}}, nil
	}
	body := `{"account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","tunnel_id":"12345678-1234-4123-8123-123456789abc","api_token":"` + token + `"}`
	recorder := httptest.NewRecorder()
	a.guard(a.exposureProviders)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/exposure-providers", body, session, csrf))
	if recorder.Code != http.StatusCreated || strings.Contains(recorder.Body.String(), token) {
		t.Fatalf("configure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &created) != nil {
		t.Fatal("provider response is invalid")
	}
	providerID, _ := created["provider_id"].(string)
	if providerID == "" {
		t.Fatal("provider identity missing")
	}
	recorder = httptest.NewRecorder()
	a.guard(a.exposureProviders)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/exposure-providers/"+providerID+"/enable", `{}`, session, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	a.guard(a.exposureProviders)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/exposure-providers/"+providerID+"/disable", `{}`, session, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var desired string
	if err := a.db.QueryRow(`SELECT desired_state FROM exposure_providers WHERE provider_id=?`, providerID).Scan(&desired); err != nil || desired != "disabled" {
		t.Fatalf("desired=%q err=%v", desired, err)
	}
	for _, operation := range operationsSeen {
		if strings.Contains(operation, "Delete") || strings.Contains(operation, "Remove") {
			t.Fatalf("ordinary disable invoked remote deletion: %v", operationsSeen)
		}
	}
	recorder = httptest.NewRecorder()
	a.guard(a.exposureProviders)(recorder, authenticatedRequest(http.MethodGet, "/api/v1/exposure-providers", "", session, csrf))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), token) || strings.Contains(recorder.Body.String(), "credential_sha256") {
		t.Fatalf("unsafe provider projection: %s", recorder.Body.String())
	}
}

func TestCloudflareRouteFailureRollsBackLocalIngress(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedIngressInstallation(t, a, "inst-cloudflare1", "uptime-kuma")
	const providerID = "provider-0123456789abcdef"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`INSERT INTO exposure_providers(provider_id,provider_type,account_id,zone_id,tunnel_id,credential_sha256,desired_state,runtime_state,created_at,updated_at) VALUES(?,'cloudflare-tunnel','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','12345678-1234-4123-8123-123456789abc','digest','enabled','ready',?,?)`, providerID, now, now); err != nil {
		t.Fatal(err)
	}
	generation := 0
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		switch request.Operation {
		case "ApplyIngressGeneration":
			generation++
			return protocol.Response{OK: true, Result: map[string]any{"generation": generation, "previous_generation": generation - 1, "configuration_sha256": strings.Repeat("a", 64)}}, nil
		case "AddCloudflareRoute":
			return protocol.Response{ErrorCode: "CloudflareRejected", Error: "provider unavailable"}, nil
		default:
			t.Fatalf("unexpected operation %q", request.Operation)
			return protocol.Response{}, nil
		}
	}
	body := `{"installation_id":"inst-cloudflare1","service_id":"web","hostname":"remote.example.test","exposure_mode":"cloudflare-tunnel","upstream_protocol":"http","tls_policy":"cloudflare","provider_id":"` + providerID + `","enabled":true}`
	recorder := httptest.NewRecorder()
	a.guard(a.ingressRoutes)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/ingress", body, session, csrf))
	if recorder.Code != http.StatusBadGateway || generation != 2 {
		t.Fatalf("status=%d generations=%d body=%s", recorder.Code, generation, recorder.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM ingress_routes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed remote route left local ingress count=%d err=%v", count, err)
	}
}

func TestRestoreQuiescesIngressAndReactivatesOnlyAfterReadiness(t *testing.T) {
	for _, test := range []struct {
		name       string
		ready      bool
		wantStatus int
		wantState  string
		wantApply  int
	}{
		{name: "ready backend", ready: true, wantStatus: http.StatusOK, wantState: "enabled", wantApply: 2},
		{name: "failed backend", ready: false, wantStatus: http.StatusServiceUnavailable, wantState: "enabled", wantApply: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, session, csrf := newTestApp(t)
			seedIngressInstallation(t, a, "inst-restore01", "uptime-kuma")
			if _, err := a.db.Exec(`UPDATE installations SET release_id='2.3.1' WHERE installation_id='inst-restore01'`); err != nil {
				t.Fatal(err)
			}
			store := ingress.Store{DB: a.db}
			route := ingress.Route{ID: "route-0123456789abcdef", InstallationID: "inst-restore01", ServiceID: "web", Hostname: "restore.example.test", ExposureMode: ingress.LAN, UpstreamProtocol: "http", TLSPolicy: ingress.TLSNone, DesiredState: ingress.Enabled}
			if _, err := store.CreateRoute(context.Background(), route); err != nil {
				t.Fatal(err)
			}
			applyCount := 0
			var order []string
			a.helperCall = func(request protocol.Request) (protocol.Response, error) {
				order = append(order, request.Operation)
				if request.Operation == "ApplyIngressGeneration" {
					applyCount++
					return protocol.Response{OK: true, State: "succeeded", Result: map[string]any{"generation": applyCount, "previous_generation": applyCount - 1, "configuration_sha256": strings.Repeat("a", 64)}}, nil
				}
				if request.Operation == "RestoreApplicationBackup" {
					return protocol.Response{OK: true, State: "succeeded", Result: map[string]any{"backup_id": request.BackupID, "status": "restored"}}, nil
				}
				t.Fatalf("unexpected helper operation %q", request.Operation)
				return protocol.Response{}, nil
			}
			a.probeReadiness = func(context.Context, *manifest.ReadinessCheck, string, int) readiness.Observation {
				state, code := "failed", "test_backend_failed"
				if test.ready {
					state, code = "ready", "test_backend_ready"
				}
				return readiness.Observation{State: state, ReasonCode: code, Summary: "test observation", CheckedAt: time.Now().UTC()}
			}
			recorder := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-restore01/restore", `{"backup_id":"op-0123456789abcdef0123456789abcdef"}`, session, csrf)
			request.Header.Set("Content-Type", "application/json")
			a.guard(a.installations)(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s order=%v", recorder.Code, recorder.Body.String(), order)
			}
			var desired string
			if err := a.db.QueryRow(`SELECT desired_state FROM ingress_routes WHERE route_id=?`, route.ID).Scan(&desired); err != nil || desired != test.wantState || applyCount != test.wantApply {
				t.Fatalf("desired=%q apply=%d order=%v err=%v", desired, applyCount, order, err)
			}
			var held int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM ingress_route_holds WHERE route_id=?`, route.ID).Scan(&held); err != nil || held != map[bool]int{true: 0, false: 1}[test.ready] {
				t.Fatalf("restore hold=%d ready=%v err=%v", held, test.ready, err)
			}
			if len(order) < 2 || order[0] != "ApplyIngressGeneration" || order[1] != "RestoreApplicationBackup" {
				t.Fatalf("unsafe restore order: %v", order)
			}
			if !test.ready {
				a.probeReadiness = func(context.Context, *manifest.ReadinessCheck, string, int) readiness.Observation {
					return readiness.Observation{State: "ready", ReasonCode: "test_backend_recovered", Summary: "test observation", CheckedAt: time.Now().UTC()}
				}
				recorder = httptest.NewRecorder()
				a.guard(a.ingressRoutes)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/ingress/reconcile", `{}`, session, csrf))
				if recorder.Code != http.StatusOK || applyCount != 2 {
					t.Fatalf("reconcile status=%d body=%s apply=%d", recorder.Code, recorder.Body.String(), applyCount)
				}
				if err := a.db.QueryRow(`SELECT COUNT(*) FROM ingress_route_holds WHERE route_id=?`, route.ID).Scan(&held); err != nil || held != 0 {
					t.Fatalf("reconcile left restore hold=%d err=%v", held, err)
				}
			}
		})
	}
}
