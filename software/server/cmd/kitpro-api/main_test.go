package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/kitpro/kitpro/software/server/internal/operations"
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
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"catalog_schema_version":7`) {
		t.Fatalf("version response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestApplicationBackupAndRestoreUseTypedHelperBoundary(t *testing.T) {
	a, session, csrf := newTestApp(t)
	if _, err := a.db.Exec(`INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES('inst-backup001','busybox','1.37.0','running',3,'now','now')`); err != nil {
		t.Fatal(err)
	}
	var requests []protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		requests = append(requests, request)
		if request.Operation == "CreateApplicationBackup" {
			return protocol.Response{OK: true, RequestID: request.ID, Result: map[string]any{"backup_id": request.ID}}, nil
		}
		return protocol.Response{OK: true, RequestID: request.ID, Result: map[string]any{"backup_id": request.BackupID, "status": "restored"}}, nil
	}

	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-backup001/backup", "", session, csrf)
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusOK || len(requests) != 1 {
		t.Fatalf("backup response: %d %s %#v", recorder.Code, recorder.Body.String(), requests)
	}
	backupID := requests[0].ID
	if requests[0].Operation != "CreateApplicationBackup" || requests[0].ApplicationID != "busybox" || requests[0].ReleaseID != "1.37.0" || requests[0].RuntimeGeneration != 3 || requests[0].BackupID != "" {
		t.Fatalf("unsafe or incomplete backup request: %#v", requests[0])
	}

	recorder = httptest.NewRecorder()
	request = authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-backup001/restore", `{"backup_id":"`+backupID+`"}`, session, csrf)
	request.Header.Set("Content-Type", "application/json")
	a.guard(a.installations)(recorder, request)
	if recorder.Code != http.StatusOK || len(requests) != 2 || requests[1].Operation != "RestoreApplicationBackup" || requests[1].BackupID != backupID {
		t.Fatalf("restore response: %d %s %#v", recorder.Code, recorder.Body.String(), requests)
	}
}

func TestStorageRootRouteSendsOnlyTypedRegistrationFields(t *testing.T) {
	a, session, csrf := newTestApp(t)
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID, Result: map[string]any{"id": "storage-0123456789abcdef"}}, nil
	}
	req := authenticatedRequest(http.MethodPost, "/api/v1/storage-roots", "name=Movies+NAS&path=%2Fmnt%2Fmedia&mode=read-only", session, csrf)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.guard(a.storageRoots)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("storage registration status %d: %s", rec.Code, rec.Body.String())
	}
	if received.Operation != "RegisterStorageRoot" || received.RootName != "Movies NAS" || received.RootPath != "/mnt/media" || received.RootMode != "read-only" || len(received.Storage) != 0 || len(received.ExternalStorage) != 0 {
		t.Fatalf("unexpected storage registration request: %#v", received)
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

func TestLegacyOperationLifecycleRouteCannotDispatchHelperMutation(t *testing.T) {
	a, session, csrf := newTestApp(t)
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	request := authenticatedRequest(http.MethodPost, "/api/v1/operations/op-1234567890abcdef1234567890abcdef/stop", "", session, csrf)
	recorder := httptest.NewRecorder()
	a.guard(a.ops)(recorder, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("legacy lifecycle route status=%d helper_called=%v", recorder.Code, called)
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
	if strings.Contains(body, "BusyBox validation workload") || !strings.Contains(body, "Home Assistant") || !strings.Contains(body, "Paperless-ngx") || !strings.Contains(body, "Forgejo") || !strings.Contains(body, ">FO</span>") || !strings.Contains(body, "Plex") || !strings.Contains(body, ">PL</span>") || !strings.Contains(body, "https://github.com/plexinc/pms-docker") || !strings.Contains(body, "CPU-only") {
		t.Fatalf("catalog presentation is not curated: %s", body)
	}
}

func TestCatalogAPIExposesManifestMetadata(t *testing.T) {
	a, _, _ := newTestApp(t)
	recorder := httptest.NewRecorder()
	a.apps(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("catalog list status %d: %s", recorder.Code, recorder.Body.String())
	}
	var items []struct {
		ID            string   `json:"id"`
		Category      string   `json:"category"`
		Kind          string   `json:"kind"`
		CatalogStatus string   `json:"catalog_status"`
		SourceURL     string   `json:"source_url"`
		Limitations   []string `json:"limitations"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	visible, found := 0, false
	for _, item := range items {
		if catalogVisible(item.ID) {
			visible++
		}
		if item.ID == "forgejo" {
			found = true
			if item.Category != "Developer Tools" || item.Kind != "application" || item.CatalogStatus != "standard" || item.SourceURL != "https://codeberg.org/forgejo/forgejo" || len(item.Limitations) != 3 {
				t.Fatalf("incomplete catalog metadata: %#v", item)
			}
		}
	}
	if visible != 17 {
		t.Fatalf("visible catalog has %d applications, want 17", visible)
	}
	if !found {
		t.Fatal("Forgejo missing from catalog API")
	}

	recorder = httptest.NewRecorder()
	a.apps(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/apps/forgejo", nil))
	var detail struct {
		SchemaVersion int    `json:"schema_version"`
		ID            string `json:"id"`
		Category      string `json:"category"`
		Kind          string `json:"kind"`
		CatalogStatus string `json:"catalog_status"`
		SourceURL     string `json:"source_url"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != manifest.CatalogMetadataSchemaVersion || detail.ID != "forgejo" || detail.Category != "Developer Tools" || detail.Kind != "application" || detail.CatalogStatus != "standard" || detail.SourceURL != "https://codeberg.org/forgejo/forgejo" {
		t.Fatalf("incomplete catalog detail: %#v", detail)
	}
}

func TestCatalogUIUsesManifestMetadataAndLocalLogoFallback(t *testing.T) {
	a, _, csrf := newTestApp(t)
	entry := a.catalog["it-tools"]
	entry.Manifest.Category = "Networking"
	entry.Manifest.CatalogStatus = "experimental"
	entry.Manifest.Logo = "it-tools"
	entry.Manifest.Limitations = []string{"Test-only catalog limitation."}
	a.catalog["it-tools"] = entry

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(csrf)
	a.home(recorder, request)
	body := recorder.Body.String()
	for _, want := range []string{"Networking", "Experimental", "Test-only catalog limitation.", `src="/assets/catalog/it-tools.svg"`, ">FR</span>", "Documentation"} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalog UI missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `class="app-logo" src="https://`) {
		t.Fatalf("catalog UI rendered a remote logo: %s", body)
	}
}

func TestCatalogLogoAssetBoundaryRejectsUnknownPaths(t *testing.T) {
	a, _, _ := newTestApp(t)
	if !validCatalogLogoFilename("reviewed-logo.svg") || validCatalogLogoFilename("Reviewed.svg") {
		t.Fatal("catalog logo filename boundary is incorrect")
	}
	for _, path := range []string{"/assets/catalog/../secret.svg", "/assets/catalog/https://example.com/logo.svg", "/assets/catalog/README.txt", "/assets/catalog/missing.svg"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		request.Host = "127.0.0.1:8080"
		a.catalogAsset(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("logo path %q returned %d", path, recorder.Code)
		}
	}
}

func TestExposureLoopbackRecreatesRuntimeAndPersistsAssignment(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	var received protocol.Request
	a.allocatePort = func(used map[int]bool, address string) (int, error) { return 20000, nil }
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: committedLifecycleResult{Generation: 2, ReleaseID: request.ReleaseID, RuntimeState: "running", ExposureMode: request.ExposureMode, ServiceID: request.ServiceID, HostAddress: request.HostAddress, HostPort: request.HostPort}}, nil
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

func TestPendingExposureDoesNotAdvanceControlProjection(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	a.allocatePort = func(used map[int]bool, address string) (int, error) { return 20000, nil }
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "executing", Phase: "mutation_authorized"}, nil
	}
	recorder := httptest.NewRecorder()
	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	a.guard(a.installations)(recorder, authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("exposure status %d: %s", recorder.Code, recorder.Body.String())
	}
	record, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || record.Mode != exposure.Internal || record.HostPort != 0 {
		t.Fatalf("accepted operation changed exposure projection: %#v %v", record, err)
	}
	var generation int
	if err = a.db.QueryRow(`SELECT runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("accepted operation advanced generation=%d err=%v", generation, err)
	}
}

func TestSucceededExposureWithoutCommittedHelperResultDoesNotProjectIntent(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	a.allocatePort = func(map[int]bool, string) (int, error) { return 20000, nil }
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded"}, nil
	}
	recorder := httptest.NewRecorder()
	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	a.guard(a.installations)(recorder, authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	record, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || record.Mode != exposure.Internal || record.HostPort != 0 {
		t.Fatalf("uncommitted intent projected: %#v %v", record, err)
	}
	var generation int
	if err = a.db.QueryRow(`SELECT runtime_generation FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("generation=%d err=%v", generation, err)
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

func TestMultiContainerInstallUsesTopLevelRelease(t *testing.T) {
	a, session, csrf := newTestApp(t)
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	recorder := httptest.NewRecorder()
	a.guard(a.apps)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/apps/paperless-ngx/install", "", session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", recorder.Code, recorder.Body.String())
	}
	if received.ReleaseID != "2.20.15" || received.Image != "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f" {
		t.Fatalf("unexpected top-level release: %#v", received)
	}
	if len(received.Components) != 2 || received.Components[0].ID != "broker" || received.Components[1].ID != "web" {
		t.Fatalf("unexpected component plan: %#v", received.Components)
	}
}

func TestForgejoInstallUsesConstrainedRuntimePlan(t *testing.T) {
	a, session, csrf := newTestApp(t)
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	recorder := httptest.NewRecorder()
	a.guard(a.apps)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/apps/forgejo/install", "", session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", recorder.Code, recorder.Body.String())
	}
	if received.Image != "codeberg.org/forgejo/forgejo@sha256:523de0217475297d05786d7551c1c1d6b5c8b90d6fee7189e88a234260ec0e74" || received.ReleaseID != "16.0.5" || received.ExposureMode != "internal" || received.RestartPolicy != "unless-stopped" {
		t.Fatalf("unexpected Forgejo release or lifecycle plan: %#v", received)
	}
	if len(received.Components) != 0 || len(received.Hardware) != 0 || len(received.ExternalStorage) != 0 || received.RunAs != nil || len(received.Command) != 0 {
		t.Fatalf("Forgejo plan acquired unexpected runtime authority: %#v", received)
	}
	if len(received.Services) != 1 || received.Services[0] != (protocol.Service{ID: "web", Protocol: "http", ContainerPort: 3000}) {
		t.Fatalf("unexpected Forgejo services: %#v", received.Services)
	}
	if len(received.Storage) != 1 || received.Storage[0].ContainerPath != "/data" || received.Storage[0].ReadOnly || received.Storage[0].OwnerUID != 1000 || received.Storage[0].OwnerGID != 1000 {
		t.Fatalf("unexpected Forgejo storage: %#v", received.Storage)
	}
	if len(received.Environment) != 2 || received.Environment[0] != (protocol.EnvVar{Name: "USER_UID", Value: "1000"}) || received.Environment[1] != (protocol.EnvVar{Name: "USER_GID", Value: "1000"}) {
		t.Fatalf("unexpected Forgejo environment: %#v", received.Environment)
	}
}

func TestPlexInstallUsesReadOnlyExternalMediaPlan(t *testing.T) {
	a, session, csrf := newTestApp(t)
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID}, nil
	}
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/api/v1/apps/plex/install", "storage_media=storage-0123456789abcdef", session, csrf)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.guard(a.apps)(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", recorder.Code, recorder.Body.String())
	}
	if received.Image != "docker.io/plexinc/pms-docker@sha256:dbb879bf58c3fc56635f21ac48c32aa6853aaa23d4a57b102033b6dc6d2d9cee" || received.ReleaseID != "1.43.4.10903-e5521bd8c" || received.ExposureMode != "internal" || received.RestartPolicy != "unless-stopped" {
		t.Fatalf("unexpected Plex release or lifecycle plan: %#v", received)
	}
	if len(received.Components) != 0 || len(received.Hardware) != 0 || received.RunAs != nil || len(received.Command) != 0 || len(received.Environment) != 0 {
		t.Fatalf("Plex plan acquired unexpected runtime authority: %#v", received)
	}
	if len(received.Services) != 1 || received.Services[0] != (protocol.Service{ID: "web", Protocol: "http", ContainerPort: 32400}) {
		t.Fatalf("unexpected Plex services: %#v", received.Services)
	}
	if len(received.Storage) != 1 || received.Storage[0].ID != "config" || received.Storage[0].ContainerPath != "/config" || received.Storage[0].ReadOnly || received.Storage[0].OwnerUID != 0 || received.Storage[0].OwnerGID != 0 {
		t.Fatalf("unexpected Plex managed storage: %#v", received.Storage)
	}
	if len(received.ExternalStorage) != 1 || received.ExternalStorage[0] != (protocol.ExternalStorageBinding{SlotID: "media", RootID: "storage-0123456789abcdef"}) {
		t.Fatalf("unexpected Plex external storage selection: %#v", received.ExternalStorage)
	}
}

func TestAmbiguousInitialInstallTransportFailureDoesNotAdvanceGeneration(t *testing.T) {
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
	if desired != "runtime_removed" || generation != 0 {
		t.Fatalf("ambiguous helper outcome changed desired state to %s generation %d", desired, generation)
	}
}

func TestTransportLossProjectsHelperExecutionWithoutRedispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	mutationCalls := 0
	lookupCalls := 0
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		switch request.Operation {
		case "InstallApplication":
			mutationCalls++
			return protocol.Response{}, fmt.Errorf("connection closed after helper acceptance")
		case "GetOperation":
			lookupCalls++
			return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "executing", Phase: "mutation_authorized"}, nil
		default:
			return protocol.Response{}, fmt.Errorf("unexpected operation %s", request.Operation)
		}
	}
	install := httptest.NewRecorder()
	a.guard(a.apps)(install, authenticatedRequest(http.MethodPost, "/api/v1/apps/uptime-kuma/install", "", session, csrf))
	if install.Code != http.StatusAccepted {
		t.Fatalf("install status %d: %s", install.Code, install.Body.String())
	}
	var result map[string]string
	if err := json.NewDecoder(install.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "accepted" || mutationCalls != 1 || lookupCalls != 1 {
		t.Fatalf("result=%v mutation calls=%d lookup calls=%d", result, mutationCalls, lookupCalls)
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM operations WHERE id=?`, result["id"]).Scan(&status); err != nil || status != "accepted" {
		t.Fatalf("projection status=%q err=%v", status, err)
	}
}

func TestResponseLossAfterHelperCommitProjectsStoredResultWithoutRedispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	mutationCalls := 0
	lookupCalls := 0
	var accepted protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		switch request.Operation {
		case "InstallApplication":
			mutationCalls++
			accepted = request
			return protocol.Response{}, fmt.Errorf("response lost after commit")
		case "GetOperation":
			lookupCalls++
			return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: committedLifecycleResult{Generation: 1, ReleaseID: accepted.ReleaseID, RuntimeState: "running", ExposureMode: accepted.ExposureMode, ServiceID: accepted.ServiceID, HostAddress: accepted.HostAddress, HostPort: accepted.HostPort}}, nil
		default:
			return protocol.Response{}, fmt.Errorf("unexpected operation")
		}
	}
	recorder := httptest.NewRecorder()
	a.guard(a.apps)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/apps/uptime-kuma/install", "", session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if mutationCalls != 1 || lookupCalls != 1 {
		t.Fatalf("mutations=%d lookups=%d", mutationCalls, lookupCalls)
	}
	var generation int
	var desired string
	if err := a.db.QueryRow(`SELECT runtime_generation,desired_state FROM installations WHERE application_id='uptime-kuma'`).Scan(&generation, &desired); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || desired != "running" {
		t.Fatalf("generation=%d desired=%s", generation, desired)
	}
}

func TestOperationStatusRepairsStaleControlProjectionFromCommittedHelperTruth(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	operationID := operations.NewID()
	if err := operations.Insert(context.Background(), a.db, operationID, "UpdateApplication", "inst-12345678"); err != nil {
		t.Fatal(err)
	}
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		if request.Operation != "GetOperation" || request.OperationID != operationID {
			return protocol.Response{}, fmt.Errorf("unexpected request %#v", request)
		}
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: operationID, State: "succeeded", Result: committedLifecycleResult{Generation: 2, ReleaseID: "1.30.0", RuntimeState: "running", ExposureMode: "loopback", ServiceID: "web", HostAddress: "127.0.0.1", HostPort: 20000}}, nil
	}
	recorder := httptest.NewRecorder()
	a.guard(a.ops)(recorder, authenticatedRequest(http.MethodGet, "/api/v1/operations/"+operationID, "", session, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var generation int
	var release, runtimeState, source string
	if err := a.db.QueryRow(`SELECT runtime_generation,release_id,runtime_state,projection_source_operation_id FROM installations WHERE installation_id='inst-12345678'`).Scan(&generation, &release, &runtimeState, &source); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || release != "1.30.0" || runtimeState != "running" || source != operationID {
		t.Fatalf("generation=%d release=%q runtime=%q source=%q", generation, release, runtimeState, source)
	}
	record, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || record.Mode != exposure.Loopback || record.HostPort != 20000 {
		t.Fatalf("exposure=%#v err=%v", record, err)
	}
}

func TestHelperSuccessProjectionFailureSelfHealsWithoutRedispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	a.allocatePort = func(map[int]bool, string) (int, error) { return 20000, nil }
	mutationCalls, lookupCalls := 0, 0
	var operationID string
	committed := committedLifecycleResult{Generation: 2, ReleaseID: "1.29.1", RuntimeState: "running", ExposureMode: "loopback", ServiceID: "web", HostAddress: "127.0.0.1", HostPort: 20000}
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		switch request.Operation {
		case "ConfigureServiceExposure":
			mutationCalls++
			operationID = request.OperationID
			return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: committed}, nil
		case "GetOperation":
			lookupCalls++
			return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: committed}, nil
		default:
			return protocol.Response{}, fmt.Errorf("unexpected helper operation %s", request.Operation)
		}
	}
	a.beforeControlProjection = func() error { return errors.New("injected control projection failure") }
	recorder := httptest.NewRecorder()
	path := "/api/v1/installations/inst-12345678/services/web/exposure"
	a.guard(a.installations)(recorder, authenticatedRequest(http.MethodPost, path, `{"mode":"loopback"}`, session, csrf))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "helper_succeeded_projection_pending" || operationID == "" {
		t.Fatalf("response=%v operation=%q", response, operationID)
	}
	initial, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || initial.Mode != exposure.Internal {
		t.Fatalf("stale projection changed early: %#v err=%v", initial, err)
	}
	a.beforeControlProjection = nil
	for i := 0; i < 2; i++ {
		poll := httptest.NewRecorder()
		a.guard(a.ops)(poll, authenticatedRequest(http.MethodGet, "/api/v1/operations/"+operationID, "", session, csrf))
		if poll.Code != http.StatusOK {
			t.Fatalf("poll %d status=%d body=%s", i, poll.Code, poll.Body.String())
		}
	}
	projected, err := exposure.Get(context.Background(), a.db, "inst-12345678", "web")
	if err != nil || projected.Mode != exposure.Loopback || projected.HostPort != 20000 {
		t.Fatalf("projection=%#v err=%v", projected, err)
	}
	if mutationCalls != 1 || lookupCalls != 1 {
		t.Fatalf("mutation calls=%d lookup calls=%d", mutationCalls, lookupCalls)
	}
	var status string
	if err = a.db.QueryRow(`SELECT status FROM operations WHERE id=?`, operationID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("operation status=%q err=%v", status, err)
	}
}

func TestReconciliationEndpointSeparatesObservationFromDurableMutation(t *testing.T) {
	a, session, csrf := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	var requests []protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		requests = append(requests, request)
		result := reconciliationResult{InstallationID: "inst-12345678", CheckedGeneration: 1, State: "consistent", RuntimeState: "running", ObservedAt: "2026-09-19T00:00:00Z", RecommendedAction: "none"}
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: map[bool]string{true: "succeeded"}[request.OperationID != ""], Result: result}, nil
	}
	get := httptest.NewRecorder()
	a.guard(a.installations)(get, authenticatedRequest(http.MethodGet, "/api/v1/installations/inst-12345678/reconciliation", "", session, csrf))
	if get.Code != http.StatusOK || len(requests) != 1 || requests[0].Operation != "GetReconciliation" || requests[0].OperationID != "" {
		t.Fatalf("GET status=%d requests=%#v", get.Code, requests)
	}
	post := httptest.NewRecorder()
	a.guard(a.installations)(post, authenticatedRequest(http.MethodPost, "/api/v1/installations/inst-12345678/reconciliation", "", session, csrf))
	if post.Code != http.StatusOK || len(requests) != 2 || requests[1].Operation != "ReconcileInstallation" || requests[1].Version != 2 || requests[1].OperationID == "" {
		t.Fatalf("POST status=%d body=%s requests=%#v", post.Code, post.Body.String(), requests)
	}
}

func TestReconciliationProjectionPreservesAdministratorIntentAndIsRepeatable(t *testing.T) {
	a, _, _ := newTestApp(t)
	seedFreshRSSInstallation(t, a, "inst-12345678")
	value := reconciliationResult{InstallationID: "inst-12345678", CheckedGeneration: 1, State: "runtime_missing", RuntimeState: "missing", ObservedAt: "2026-09-19T00:00:00Z", MismatchCodes: []string{"active_container_missing"}, RecommendedAction: "recreate_generation", Projection: &committedLifecycleResult{Generation: 1, ReleaseID: "1.29.1", RuntimeState: "missing", ExposureMode: "internal", ServiceID: "web"}}
	for i := 0; i < 2; i++ {
		if _, err := a.projectReconciliation(context.Background(), "op-reconcile", value); err != nil {
			t.Fatal(err)
		}
	}
	var desired, runtimeState, reconciliationState, codes string
	if err := a.db.QueryRow(`SELECT desired_state,runtime_state,reconciliation_state,reconciliation_codes_json FROM installations WHERE installation_id='inst-12345678'`).Scan(&desired, &runtimeState, &reconciliationState, &codes); err != nil {
		t.Fatal(err)
	}
	if desired != "running" || runtimeState != "missing" || reconciliationState != "runtime_missing" || !strings.Contains(codes, "active_container_missing") {
		t.Fatalf("desired=%q runtime=%q reconciliation=%q codes=%q", desired, runtimeState, reconciliationState, codes)
	}
}

func TestConnectionFailureBeforeAcceptanceIsNotProjectedAsRunning(t *testing.T) {
	a := &app{helper: filepath.Join(t.TempDir(), "missing-helper.sock")}
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "StartApplication", OperationRevision: 1, InstanceID: "inst-example01"})
	if err == nil || response.State != "" {
		t.Fatalf("pre-acceptance failure projected as %#v err=%v", response, err)
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
