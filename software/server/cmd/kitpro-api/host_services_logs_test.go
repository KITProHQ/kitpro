package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type apiServiceUnits struct{ name, active, enabled string }

func (f *apiServiceUnits) Close() {}
func (f *apiServiceUnits) ListUnitsByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitStatus, error) {
	return []systemdbus.UnitStatus{{Name: f.name, Description: "Fixture", LoadState: "loaded", ActiveState: f.active, SubState: "running"}}, nil
}
func (f *apiServiceUnits) ListUnitFilesByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitFile, error) {
	return []systemdbus.UnitFile{{Path: "/usr/lib/systemd/system/" + f.name, Type: f.enabled}}, nil
}
func (f *apiServiceUnits) ListUnitsByNamesContext(context.Context, []string) ([]systemdbus.UnitStatus, error) {
	return f.ListUnitsByPatternsContext(context.Background(), nil, nil)
}
func (f *apiServiceUnits) GetUnitPropertiesContext(context.Context, string) (map[string]any, error) {
	return map[string]any{"Id": f.name, "Names": []string{f.name}, "Requires": []string{"basic.target"}, "FragmentPath": "/usr/lib/systemd/system/" + f.name}, nil
}
func (f *apiServiceUnits) GetUnitTypePropertiesContext(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"MainPID": uint32(123), "Result": "success", "CanReload": true}, nil
}

func apiServiceProvider(t *testing.T, unit *apiServiceUnits, policy hostadmin.ServicePolicy) hostadmin.ServiceProvider {
	t.Helper()
	runtime := filepath.Join(t.TempDir(), "systemd")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	return hostadmin.ServiceProvider{SystemdRuntime: runtime, Policy: policy, UnitTrust: func(string) bool { return true }, Open: func(context.Context) (hostadmin.UnitConnection, error) { return unit, nil }}
}

type failingJournal struct{}

func (failingJournal) Run(context.Context, []string) ([]byte, error) {
	return nil, errors.New("secret-token-from-backend")
}
func TestJournalRequiresSessionAndExplicitGate(t *testing.T) {
	db, e := state.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = state.Migrate(context.Background(), db, false); e != nil {
		t.Fatal(e)
	}
	if e = auth.Init(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if e = auth.Setup(context.Background(), db, "admin", "test-password-123"); e != nil {
		t.Fatal(e)
	}
	token, _, e := auth.NewSession(context.Background(), db, 1)
	if e != nil {
		t.Fatal(e)
	}
	a := &app{db: db, authCfg: auth.DefaultConfig, allowedHosts: map[string]bool{"localhost:8080": true}, hostJournalProvider: hostadmin.JournalProvider{Runner: failingJournal{}}}
	route := a.guard(a.hostLogs)
	request := func() *http.Request {
		r := httptest.NewRequest("GET", "http://localhost:8080/api/v1/host/logs", nil)
		r.Host = "localhost:8080"
		return r
	}
	noSession := httptest.NewRecorder()
	route(noSession, request())
	if noSession.Code != 401 {
		t.Fatalf("unauthenticated: %d", noSession.Code)
	}
	authenticated := request()
	authenticated.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	noGate := httptest.NewRecorder()
	route(noGate, authenticated)
	if noGate.Code != 403 {
		t.Fatalf("no journal gate: %d", noGate.Code)
	}
	a.hostLogsEnabled = true
	backendFailure := httptest.NewRecorder()
	route(backendFailure, authenticated)
	if backendFailure.Code != 503 || strings.Contains(backendFailure.Body.String(), "secret-token") {
		t.Fatalf("unsafe backend failure: %d %q", backendFailure.Code, backendFailure.Body.String())
	}
	readerID, err := auth.CreateAdministrator(context.Background(), db, "host-reader", "host-reader-password-123")
	if err != nil {
		t.Fatal(err)
	}
	readerToken, _, err := auth.NewSession(context.Background(), db, readerID)
	if err != nil {
		t.Fatal(err)
	}
	reader := request()
	reader.AddCookie(&http.Cookie{Name: auth.CookieName, Value: readerToken})
	permissionDenied := httptest.NewRecorder()
	route(permissionDenied, reader)
	if permissionDenied.Code != http.StatusForbidden {
		t.Fatalf("reader journal status=%d", permissionDenied.Code)
	}
}
func TestHostQueryBoundaries(t *testing.T) {
	a := &app{}
	for _, url := range []string{"/api/v1/host/services?limit=1000", "/api/v1/host/services?limit=2&limit=3", "/api/v1/host/services?unexpected=1"} {
		w := httptest.NewRecorder()
		a.hostServices(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", url, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.hostServiceDetail(w, httptest.NewRequest("GET", "/api/v1/host/services/../etc/passwd", nil))
	if w.Code != 400 {
		t.Fatalf("invalid service: %d", w.Code)
	}
}

func TestServiceActionPreviewIsReadOnlyAndExplainsEligibility(t *testing.T) {
	a := &app{}
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	w := httptest.NewRecorder()
	a.hostServiceDetail(w, httptest.NewRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/actions/start/preview", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"expected_transition":"inactive to active"`) || !strings.Contains(w.Body.String(), `"allowed":true`) {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.hostServiceDetail(w, httptest.NewRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions/start/preview", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mutation preview method status=%d", w.Code)
	}
}

func TestServiceActionRequiresSessionCSRFConfirmationAndEligibility(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	var received protocol.Request
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		received = request
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: map[string]any{"systemd_result": "done"}}, nil
	}
	route := a.guard(a.hostServiceDetail)
	path := "/api/v1/host/services/kitpro-qualification.service/actions"
	unauth := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+path, strings.NewReader(`{"action":"start","confirmation_unit":"kitpro-qualification.service"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	route(unauth, req)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", unauth.Code)
	}
	missingCSRF := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+path, strings.NewReader(`{"action":"start","confirmation_unit":"kitpro-qualification.service"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.AddCookie(session)
	route(missingCSRF, req)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("csrf=%d", missingCSRF.Code)
	}
	bad := httptest.NewRecorder()
	route(bad, authenticatedRequest(http.MethodPost, path, `{"action":"start","confirmation_unit":"other.service"}`, session, csrf))
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "confirmation_mismatch") {
		t.Fatalf("confirmation=%d %s", bad.Code, bad.Body.String())
	}
	ok := httptest.NewRecorder()
	route(ok, authenticatedRequest(http.MethodPost, path, `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, session, csrf))
	if ok.Code != http.StatusOK {
		t.Fatalf("action=%d %s", ok.Code, ok.Body.String())
	}
	if received.Operation != "ManageSystemService" || received.UnitName != "kitpro-qualification.service" || received.ServiceAction != "start" || received.ConfirmationUnit != received.UnitName {
		t.Fatalf("request=%#v", received)
	}
}

func TestServiceActionRejectsOversizedBodyBeforeDispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	called := false
	a.helperCall = func(protocol.Request) (protocol.Response, error) {
		called = true
		return protocol.Response{}, nil
	}
	body := `{"action":"start","confirmation_unit":"kitpro-qualification.service"}` + strings.Repeat(" ", 4096)
	recorder := httptest.NewRecorder()
	a.guard(a.hostServiceDetail)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", body, session, csrf))
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d helper_called=%v body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestPermissionRevokedAfterPreviewIsDeniedBeforeDispatch(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	operatorID, err := auth.CreateAdministrator(context.Background(), a.db, "dispatch-operator", "dispatch-operator-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.SetPermissions(context.Background(), a.db, "dispatch-operator", []auth.Permission{auth.PermissionHostRead, auth.PermissionServicesManage}); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.SetServiceScopes(context.Background(), a.db, "dispatch-operator", []auth.ServiceScope{{Unit: "kitpro-qualification.service", Action: "start"}}); err != nil {
		t.Fatal(err)
	}
	token, csrf, err := auth.NewSession(context.Background(), a.db, operatorID)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	a.helperCall = func(protocol.Request) (protocol.Response, error) { called = true; return protocol.Response{}, nil }
	a.beforeServiceDispatch = func() {
		if _, revokeErr := auth.SetPermissions(context.Background(), a.db, "dispatch-operator", []auth.Permission{auth.PermissionHostRead}); revokeErr != nil {
			t.Fatal(revokeErr)
		}
	}
	recorder := httptest.NewRecorder()
	a.guard(a.hostServiceDetail)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, &http.Cookie{Name: auth.CookieName, Value: token}, &http.Cookie{Name: auth.CSRFCookieName, Value: csrf}))
	if recorder.Code != http.StatusForbidden || called || !strings.Contains(recorder.Body.String(), `"status":"rejected"`) {
		t.Fatalf("status=%d helper_called=%v body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestServiceActionDeniesProtectedAndUnknownFields(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "docker.service", active: "active", enabled: "enabled"}, hostadmin.NewServicePolicy(true, "docker.service"))
	a.helperCall = func(protocol.Request) (protocol.Response, error) {
		t.Fatal("protected request reached helper")
		return protocol.Response{}, nil
	}
	route := a.guard(a.hostServiceDetail)
	protected := httptest.NewRecorder()
	route(protected, authenticatedRequest(http.MethodPost, "/api/v1/host/services/docker.service/actions", `{"action":"stop","confirmation_unit":"docker.service"}`, session, csrf))
	if protected.Code != http.StatusForbidden || !strings.Contains(protected.Body.String(), "protected") {
		t.Fatalf("protected=%d %s", protected.Code, protected.Body.String())
	}
	unknown := httptest.NewRecorder()
	route(unknown, authenticatedRequest(http.MethodPost, "/api/v1/host/services/docker.service/actions", `{"action":"stop","confirmation_unit":"docker.service","command":"id"}`, session, csrf))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown=%d", unknown.Code)
	}
}

func TestScopedServicePermissionAndRevocation(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	readerID, err := auth.CreateAdministrator(context.Background(), a.db, "service-reader", "service-reader-password-123")
	if err != nil {
		t.Fatal(err)
	}
	readerToken, readerCSRF, err := auth.NewSession(context.Background(), a.db, readerID)
	if err != nil {
		t.Fatal(err)
	}
	readerSession := &http.Cookie{Name: auth.CookieName, Value: readerToken}
	readerCSRFCookie := &http.Cookie{Name: auth.CSRFCookieName, Value: readerCSRF}
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		return protocol.Response{}, nil
	}
	route := a.guard(a.hostServiceDetail)
	denied := httptest.NewRecorder()
	route(denied, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, readerSession, readerCSRFCookie))
	if denied.Code != http.StatusForbidden || called || !strings.Contains(denied.Body.String(), "permission_denied") {
		t.Fatalf("reader denial=%d called=%v body=%s", denied.Code, called, denied.Body.String())
	}
	if _, err = auth.SetPermissions(context.Background(), a.db, "service-reader", []auth.Permission{auth.PermissionHostRead, auth.PermissionServicesManage}); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.SetServiceScopes(context.Background(), a.db, "service-reader", []auth.ServiceScope{{Unit: "kitpro-qualification.service", Action: "start"}}); err != nil {
		t.Fatal(err)
	}
	readerToken, readerCSRF, err = auth.NewSession(context.Background(), a.db, readerID)
	if err != nil {
		t.Fatal(err)
	}
	readerSession.Value, readerCSRFCookie.Value = readerToken, readerCSRF
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		result := hostadmin.ServiceMutationResult{Unit: request.UnitName, Action: hostadmin.ServiceAction(request.ServiceAction), Previous: hostadmin.Service{Active: "inactive"}, Result: hostadmin.Service{Active: "active"}, SystemdResult: "done"}
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: result}, nil
	}
	allowed := httptest.NewRecorder()
	route(allowed, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, readerSession, readerCSRFCookie))
	if allowed.Code != http.StatusOK || !called || !strings.Contains(allowed.Body.String(), `"status":"completed"`) {
		t.Fatalf("scoped action=%d called=%v body=%s", allowed.Code, called, allowed.Body.String())
	}
	if _, err = auth.SetPermissions(context.Background(), a.db, "service-reader", []auth.Permission{auth.PermissionHostRead}); err != nil {
		t.Fatal(err)
	}
	revoked := httptest.NewRecorder()
	route(revoked, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, readerSession, readerCSRFCookie))
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d body=%s", revoked.Code, revoked.Body.String())
	}
}

func TestServiceHistoryIsBoundedFilteredAndPermissionProtected(t *testing.T) {
	a, session, csrf := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	details := []serviceAuditDetail{
		{OperationID: "op-11111111111111111111111111111111", Unit: "kitpro-qualification.service", Action: "start", AuthorizationOutcome: "allowed", PolicyOutcome: "allowed", PreviousState: "inactive", ResultingState: "active", SystemdOutcome: "done", Status: "completed"},
		{OperationID: "op-22222222222222222222222222222222", Unit: "kitpro-qualification.service", Action: "stop", AuthorizationOutcome: "denied", PolicyOutcome: "denied", Status: "rejected", FailureReason: "protected"},
	}
	for index, detail := range details {
		encoded, _ := json.Marshal(detail)
		if _, err := a.db.Exec(`INSERT INTO audit_events(event_id,event_type,actor,target_id,outcome,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`, serviceAuditEventID(detail.OperationID), "system-service-operation", "admin", "systemd:"+detail.Unit, detail.Status, string(encoded), now+string(rune('a'+index))); err != nil {
			t.Fatal(err)
		}
	}
	route := a.guard(a.hostServiceDetail)
	filtered := httptest.NewRecorder()
	route(filtered, authenticatedRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/history?action=stop&result=rejected&limit=1", "", session, csrf))
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), `"action":"stop"`) || strings.Contains(filtered.Body.String(), `"action":"start"`) {
		t.Fatalf("history=%d %s", filtered.Code, filtered.Body.String())
	}
	if strings.Contains(filtered.Body.String(), "password") || strings.Contains(filtered.Body.String(), "cookie") {
		t.Fatal("history leaked sensitive material")
	}
	invalid := httptest.NewRecorder()
	route(invalid, authenticatedRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/history?limit=500", "", session, csrf))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unbounded history status=%d", invalid.Code)
	}
	page := httptest.NewRecorder()
	route(page, authenticatedRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/history?limit=1", "", session, csrf))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"next_offset":1`) {
		t.Fatalf("history pagination status=%d body=%s", page.Code, page.Body.String())
	}
	badTime := httptest.NewRecorder()
	route(badTime, authenticatedRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/history?since=not-a-time", "", session, csrf))
	if badTime.Code != http.StatusBadRequest {
		t.Fatalf("history time boundary status=%d", badTime.Code)
	}
	readerID, err := auth.CreateAdministrator(context.Background(), a.db, "history-reader", "history-reader-password-123")
	if err != nil {
		t.Fatal(err)
	}
	readerToken, readerCSRF, err := auth.NewSession(context.Background(), a.db, readerID)
	if err != nil {
		t.Fatal(err)
	}
	denied := httptest.NewRecorder()
	route(denied, authenticatedRequest(http.MethodGet, "/api/v1/host/services/kitpro-qualification.service/history", "", &http.Cookie{Name: auth.CookieName, Value: readerToken}, &http.Cookie{Name: auth.CSRFCookieName, Value: readerCSRF}))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("history permission status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestServiceActionFailsClosedWhenAuditCannotPersist(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	called := false
	a.helperCall = func(protocol.Request) (protocol.Response, error) { called = true; return protocol.Response{}, nil }
	if _, err := a.db.Exec(`CREATE TRIGGER reject_service_audit BEFORE INSERT ON audit_events WHEN NEW.event_type='system-service-operation' BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	a.guard(a.hostServiceDetail)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, session, csrf))
	if recorder.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("audit predispatch status=%d helper_called=%v body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestPostDispatchAuditFailureReportsOutcomeUnknownWithoutRetry(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	calls := 0
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		calls++
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "succeeded", Result: hostadmin.ServiceMutationResult{Unit: request.UnitName, Action: hostadmin.ServiceAction(request.ServiceAction), Previous: hostadmin.Service{Active: "inactive"}, Result: hostadmin.Service{Active: "active"}, SystemdResult: "done"}}, nil
	}
	a.beforeServiceAuditFinal = func() error { return errors.New("injected final audit failure") }
	recorder := httptest.NewRecorder()
	a.guard(a.hostServiceDetail)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, session, csrf))
	if recorder.Code != http.StatusServiceUnavailable || calls != 1 || !strings.Contains(recorder.Body.String(), `"status":"outcome_unknown"`) {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

func TestCurrentPermissionContractDoesNotExposeOtherAdministrators(t *testing.T) {
	a, _, _ := newTestApp(t)
	id, err := auth.CreateAdministrator(context.Background(), a.db, "reader", "reader-password-123")
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, err := auth.NewSession(context.Background(), a.db, id)
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{Name: auth.CookieName, Value: token}
	csrfCookie := &http.Cookie{Name: auth.CSRFCookieName, Value: csrf}
	recorder := httptest.NewRecorder()
	a.guard(a.accountPermissions)(recorder, authenticatedRequest(http.MethodGet, "/api/v1/account/permissions", "", session, csrfCookie))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"host.read"`) || strings.Contains(recorder.Body.String(), `"host.security.admin"`) || strings.Contains(recorder.Body.String(), `"admin"`) {
		t.Fatalf("permissions status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMalformedHelperReceiptIsOutcomeUnknown(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostServiceManagementEnabled = true
	a.hostServicesProvider = apiServiceProvider(t, &apiServiceUnits{name: "kitpro-qualification.service", active: "inactive", enabled: "disabled"}, hostadmin.NewServicePolicy(true, ""))
	calls := 0
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		calls++
		return protocol.Response{OK: true, RequestID: request.ID, OperationID: "op-ffffffffffffffffffffffffffffffff", State: "succeeded"}, nil
	}
	recorder := httptest.NewRecorder()
	a.guard(a.hostServiceDetail)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/services/kitpro-qualification.service/actions", `{"action":"start","confirmation_unit":"kitpro-qualification.service"}`, session, csrf))
	if recorder.Code != http.StatusConflict || calls != 1 || !strings.Contains(recorder.Body.String(), `"status":"outcome_unknown"`) {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}
