package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func newTestApp(t *testing.T) (*app, *http.Cookie, *http.Cookie) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = state.Migrate(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	if err = auth.Init(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = auth.Setup(context.Background(), db, "admin", "validation-password-123"); err != nil {
		t.Fatal(err)
	}
	token, csrf, err := auth.NewSession(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, tmpl: template.Must(template.New("page").Parse(page)), authTmpl: template.Must(template.New("auth").Parse(authPage)), authCfg: auth.DefaultConfig, allowedHosts: map[string]bool{"127.0.0.1:8080": true}, catalog: entries}
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	return a, &http.Cookie{Name: auth.CookieName, Value: token}, &http.Cookie{Name: auth.CSRFCookieName, Value: csrf}
}

func seedFreshRSSInstallation(t *testing.T, a *app, installation string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := a.db.Exec(`INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, installation, "freshrss", "1.29.1", "running", 1, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exposure.Upsert(context.Background(), a.db, installation, "web", exposure.Assignment{Mode: exposure.Internal}, ""); err != nil {
		t.Fatal(err)
	}
}

func authenticatedRequest(method, target, body string, session, csrf *http.Cookie) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+target, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf.Value)
	req.AddCookie(session)
	req.AddCookie(csrf)
	return req
}

func TestExposureRouteAuthAndStrictInput(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	path := "/api/v1/installations/inst-12345678/services/web/exposure"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"mode":"loopback"}`))
	request.Host = "127.0.0.1:8080"
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous mutation status %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf)
	request.Header.Set("X-CSRF-Token", "bad")
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF status %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf)
	request.Header.Del("X-CSRF-Token")
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("CSRF cookie without submitted token status %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf)
	request.Header.Del("Origin")
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing Origin status %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = authenticatedRequest(http.MethodPost, path, `{"mode":"loopback","host_address":"0.0.0.0"}`, session, csrf)
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown privileged field status %d", recorder.Code)
	}
}

func TestVersionEndpointReportsBuildAndSchemaMetadata(t *testing.T) {
	a, session, csrf := newTestApp(t)
	req := authenticatedRequest(http.MethodGet, "/api/v1/version", "", session, csrf)
	rec := httptest.NewRecorder()
	a.guard(a.version)(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"catalog_schema_version":2`) {
		t.Fatalf("version response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestApplicationUpdateUsesTrustedReleaseAndPreservesInstallation(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	if _, err := exposure.Upsert(context.Background(), a.db, "inst-12345678", "web", exposure.Assignment{Mode: exposure.Loopback, Address: "127.0.0.1", Port: 20000}, ""); err != nil {
		t.Fatal(err)
	}
	m := a.catalog["freshrss"].Manifest
	m.Releases = append(m.Releases, manifest.Release{Version: "1.29.1-maintenance", Registry: m.Releases[0].Registry, Repository: m.Releases[0].Repository, Digest: m.Releases[0].Digest, Platform: m.Releases[0].Platform})
	a.catalog["freshrss"] = catalog.Entry{Manifest: m}
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		if request.Operation != "UpdateApplication" || request.ReleaseID != "1.29.1-maintenance" || request.InstanceID != "inst-12345678" || request.RuntimeGeneration != 2 || request.ExposureMode != "loopback" || request.HostPort != 20000 {
			t.Fatalf("unexpected update request: %#v", request)
		}
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	t.Setenv("KITPRO_CONTROL_BACKUP_DIR", t.TempDir())
	req := authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-12345678/update", `{"release":"1.29.1-maintenance"}`, session, csrf)
	rec := httptest.NewRecorder()
	a.guard(a.installations)(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("update status %d: %s", rec.Code, rec.Body.String())
	}
	var release string
	var generation int
	if err := a.db.QueryRow(`SELECT release_id,runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&release, &generation); err != nil {
		t.Fatal(err)
	}
	if release != "1.29.1-maintenance" || generation != 2 {
		t.Fatalf("state release=%s generation=%d", release, generation)
	}
}

func TestOperationsEndpointCannotBypassConstrainedExposureRoute(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	request := authenticatedRequest(http.MethodPost, "/api/v1/operations?installation_id=inst-12345678&operation_type=ConfigureServiceExposure&service_id=web&exposure_mode=lan&host_address=10.10.0.115&host_port=20000&container_port=80&service_protocol=http", "", session, csrf)
	recorder := httptest.NewRecorder()
	a.guard(a.ops)(recorder, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("generic operations bypass status=%d helper_called=%v", recorder.Code, called)
	}
}

func TestPasswordFormSubmitsExplicitCSRFToken(t *testing.T) {
	a, _, csrf := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/account/password", nil)
	request.AddCookie(csrf)
	recorder := httptest.NewRecorder()
	a.password(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `name="csrf_token" value="`+csrf.Value+`"`) {
		t.Fatalf("password form lacks explicit CSRF proof: %s", recorder.Body.String())
	}
}

func TestBrandedAssetsAndAuthenticationPages(t *testing.T) {
	a, _, _ := newTestApp(t)
	for path, contentType := range map[string]string{"/assets/kitpro.css": "text/css", "/assets/kitpro.js": "text/javascript"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "127.0.0.1:8080"
		body := webCSS
		if path == "/assets/kitpro.js" {
			body = webJS
		}
		a.asset(contentType+"; charset=utf-8", body)(recorder, request)
		if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), contentType) || recorder.Body.Len() == 0 {
			t.Fatalf("asset %s: status=%d type=%q bytes=%d", path, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Len())
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	request.Host = "127.0.0.1:8080"
	a.login(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "/assets/kitpro.css") || !strings.Contains(recorder.Body.String(), "Welcome back") {
		t.Fatalf("login page is not using the product design system: %s", recorder.Body.String())
	}
}

func TestInstallationLifecycleRouteUsesStableInstallationIdentity(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-12345678/stop", "", session, csrf)
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusAccepted || received.Operation != "StopApplication" || received.InstanceID != "inst-12345678" || received.RuntimeGeneration != 1 {
		t.Fatalf("lifecycle status=%d request=%#v body=%s", recorder.Code, received, recorder.Body.String())
	}
	var desired string
	if err := a.db.QueryRow(`SELECT desired_state FROM installations WHERE installation_id='inst-12345678'`).Scan(&desired); err != nil || desired != "stopped" {
		t.Fatalf("desired state=%q error=%v", desired, err)
	}
}

func TestCatalogUIHidesInternalValidationWorkload(t *testing.T) {
	a, _, csrf := newTestApp(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(csrf)
	a.home(recorder, request)
	body := recorder.Body.String()
	if strings.Contains(body, "BusyBox validation workload") || !strings.Contains(body, "Home Assistant") || !strings.Contains(body, "Paperless-ngx") {
		t.Fatalf("catalog presentation is not curated: %s", body)
	}
}

func TestExposureLoopbackRecreatesRuntimeAndPersistsAssignment(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	var received protocol.Request
	a.allocatePort = func(used map[int]bool, address string) (int, error) { return 20000, nil }
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}

	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	recorder := httptest.NewRecorder()
	a.guard(a.installations)(recorder, authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("exposure status %d: %s", recorder.Code, recorder.Body.String())
	}
	request := received
	if request.Operation != "ConfigureServiceExposure" || request.ExposureMode != "loopback" || request.HostAddress != "127.0.0.1" || request.HostPort < exposure.FirstPort || request.HostPort > exposure.LastPort || request.ContainerPort != 80 || request.ServiceProtocol != "http" {
		t.Fatalf("unexpected helper request: %#v", request)
	}
	record, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || record.HostPort != request.HostPort || record.Mode != exposure.Loopback {
		t.Fatalf("assignment not persisted: %#v %v", record, err)
	}
	var generation int
	if err = a.db.QueryRow(`SELECT runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("runtime generation = %d, err=%v", generation, err)
	}
}

func TestExposureDisableRetainsPortAndReenableReusesIt(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	a.allocatePort = func(used map[int]bool, address string) (int, error) { return 20000, nil }
	var requests []protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		requests = append(requests, request)
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	for _, step := range []struct {
		method string
		body   string
		mode   string
	}{
		{http.MethodPost, `{"mode":"loopback"}`, "loopback"},
		{http.MethodDelete, "", "internal"},
		{http.MethodPost, `{"mode":"loopback"}`, "loopback"},
	} {
		recorder := httptest.NewRecorder()
		a.guard(a.installations)(recorder, authenticatedRequest(step.method, path, step.body, session, csrf))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s status %d: %s", step.mode, recorder.Code, recorder.Body.String())
		}
		record, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
		if err != nil || string(record.Mode) != step.mode || record.HostPort != 20000 {
			t.Fatalf("%s assignment %#v: %v", step.mode, record, err)
		}
	}
	if len(requests) != 3 || requests[1].ExposureMode != "internal" || requests[1].HostAddress != "" || requests[1].HostPort != 20000 || requests[2].HostPort != 20000 {
		t.Fatalf("disable/re-enable helper requests: %#v", requests)
	}
	recreate := httptest.NewRecorder()
	a.guard(a.installations)(recreate, authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-12345678/recreate", "", session, csrf))
	if recreate.Code != http.StatusAccepted || len(requests) != 4 || requests[3].ExposureMode != "loopback" || requests[3].HostPort != 20000 {
		t.Fatalf("recreate did not retain exposure: status=%d requests=%#v", recreate.Code, requests)
	}
	var generation int
	_ = a.db.QueryRow(`SELECT runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation)
	if generation != 5 {
		t.Fatalf("runtime generation %d, want 5", generation)
	}
}

func TestExposureFailureDoesNotAdvanceRuntimeGeneration(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	a.allocatePort = func(used map[int]bool, address string) (int, error) { return 20000, nil }
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{RequestID: request.ID, Error: "port collision"}, nil
	}
	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	recorder := httptest.NewRecorder()
	a.guard(a.installations)(recorder, authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("failure response status %d", recorder.Code)
	}
	var generation int
	_ = a.db.QueryRow(`SELECT runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation)
	if generation != 1 {
		t.Fatalf("failed recreation advanced runtime generation to %d", generation)
	}
	var summary string
	if err := a.db.QueryRow(`SELECT summary FROM operations ORDER BY requested_at DESC LIMIT 1`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary != "host port unavailable" {
		t.Fatalf("unsafe or unexpected failure summary: %q", summary)
	}
}

func TestFailedInitialInstallCanRecreateAtGenerationOne(t *testing.T) {
	a, session, csrf := newTestApp(t)
	calls := 0
	var retry protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		calls++
		if calls == 1 {
			return protocol.Response{RequestID: request.ID, Error: "image acquisition failed"}, nil
		}
		retry = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	install := httptest.NewRecorder()
	a.guard(a.apps)(install, authenticatedRequest(http.MethodPost, "/api/v1/apps/uptime-kuma/install", "", session, csrf))
	if install.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", install.Code, install.Body.String())
	}
	var installation, desired string
	var generation int
	if err := a.db.QueryRow(`SELECT installation_id,desired_state,runtime_generation FROM installations WHERE application_id='uptime-kuma'`).Scan(&installation, &desired, &generation); err != nil {
		t.Fatal(err)
	}
	if desired != "runtime_removed" || generation != 0 {
		t.Fatalf("failed installation state = %s generation %d", desired, generation)
	}
	recreate := httptest.NewRecorder()
	a.guard(a.installations)(recreate, authenticatedRequest(http.MethodPost, "/api/v1/installations/"+installation+"/recreate", "", session, csrf))
	if recreate.Code != http.StatusAccepted || retry.RuntimeGeneration != 1 || retry.InstanceID != installation {
		t.Fatalf("recreate status=%d request=%#v body=%s", recreate.Code, retry, recreate.Body.String())
	}
}

func TestAmbiguousInitialInstallTransportFailureRetainsExpectedGeneration(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, fmt.Errorf("connection closed before response")
	}
	install := httptest.NewRecorder()
	a.guard(a.apps)(install, authenticatedRequest(http.MethodPost, "/api/v1/apps/uptime-kuma/install", "", session, csrf))
	if install.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", install.Code, install.Body.String())
	}
	var desired string
	var generation int
	if err := a.db.QueryRow(`SELECT desired_state,runtime_generation FROM installations WHERE application_id='uptime-kuma'`).Scan(&desired, &generation); err != nil {
		t.Fatal(err)
	}
	if desired != "running" || generation != 1 {
		t.Fatalf("ambiguous helper outcome changed desired state to %s generation %d", desired, generation)
	}
}

func TestOperationFailureSummaryIsSafe(t *testing.T) {
	got := safeOperationSummary(operationErrorCategory(fmt.Errorf("helper: daemon leaked /secret/path")))
	if strings.Contains(got, "/secret/path") || got != "privileged operation failed" {
		t.Fatalf("unsafe helper detail surfaced: %q", got)
	}
}

func TestServiceStatusAndUIAreSafe(t *testing.T) {
	a, _, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	if _, err := exposure.Upsert(context.Background(), a.db, "inst-12345678", "web", exposure.Assignment{Mode: exposure.Loopback, Address: "127.0.0.1", Port: 20000}, ""); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(csrf)
	a.home(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Web Interface") || !strings.Contains(recorder.Body.String(), "http://127.0.0.1:20000") || !strings.Contains(recorder.Body.String(), `name="csrf_token" value="`+csrf.Value+`"`) || strings.Contains(recorder.Body.String(), "/srv/kitpro") || strings.Contains(recorder.Body.String(), `style=`) {
		t.Fatalf("unsafe or incomplete UI: %s", recorder.Body.String())
	}

	statuses, err := a.serviceStatuses(context.Background(), "inst-12345678")
	if err != nil || len(statuses) != 1 || statuses[0].Endpoint != "http://127.0.0.1:20000" {
		encoded, _ := json.Marshal(statuses)
		t.Fatalf("service status %s: %v", encoded, err)
	}
}
