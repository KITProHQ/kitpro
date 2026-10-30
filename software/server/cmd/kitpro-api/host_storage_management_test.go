package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func apiStorageManager(t *testing.T) *hostadmin.StorageManager {
	t.Helper()
	manager, kernel, target := func() (*hostadmin.StorageManager, *apiMountKernel, string) {
		target := t.TempDir()
		kernel := &apiMountKernel{}
		manager := hostadmin.NewStorageManager(hostadmin.StoragePolicyDocument{Version: 1, Resources: []hostadmin.StoragePolicyResource{{ID: "qualification-tmpfs", Source: "tmpfs", Target: target, Filesystem: "tmpfs", Actions: []hostadmin.StorageAction{hostadmin.StorageActionMount, hostadmin.StorageActionUnmount}}}})
		manager.Kernel = kernel
		manager.ValidateTarget = func(string) error { return nil }
		manager.ReadMountInfo = func() ([]byte, error) { return []byte("1 0 0:1 / / rw - rootfs rootfs rw\n"), nil }
		return manager, kernel, target
	}()
	_, _ = kernel, target
	return manager
}

func TestStorageActionPreservesUnknownOutcomeWhenHelperReceiptIsLost(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	a.helperCall = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("connection ended")
	}
	rec := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodPost, "/api/v1/host/storage/qualification-tmpfs/actions", `{"action":"mount","confirmation_resource":"qualification-tmpfs"}`, session, csrf))
	if rec.Code != http.StatusConflict || !containsAll(rec.Body.String(), `"status":"outcome_unknown"`, `"id":"op-`) {
		t.Fatalf("unknown outcome %d %s", rec.Code, rec.Body.String())
	}
}

type apiMountKernel struct{}

func (*apiMountKernel) Mount(string, string, string, uintptr, string) error { return nil }
func (*apiMountKernel) Unmount(string, int) error                           { return nil }
func (*apiMountKernel) Owned(string) (bool, error)                          { return true, nil }

func TestStoragePreviewRequiresReadAndReportsScopedPermission(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	request := authenticatedRequest(http.MethodGet, "/api/v1/host/storage/qualification-tmpfs/actions/mount/preview", "", session, csrf)
	rec := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, request)
	if rec.Code != 200 || !containsAll(rec.Body.String(), `"allowed":true`, `"confirmation":"qualification-tmpfs"`) {
		t.Fatalf("full preview %d %s", rec.Code, rec.Body.String())
	}
	if _, err := auth.CreateAdministrator(context.Background(), a.db, "reader", "reader-password-123"); err != nil {
		t.Fatal(err)
	}
	token, readerCSRFToken, err := auth.NewSession(context.Background(), a.db, 2)
	if err != nil {
		t.Fatal(err)
	}
	reader := &http.Cookie{Name: auth.CookieName, Value: token}
	readerCSRF := &http.Cookie{Name: auth.CSRFCookieName, Value: readerCSRFToken}
	rec = httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodGet, "/api/v1/host/storage/qualification-tmpfs/actions/mount/preview", "", reader, readerCSRF))
	if rec.Code != 200 || !containsAll(rec.Body.String(), `"allowed":false`, `"state":"permission_denied"`) {
		t.Fatalf("reader preview %d %s", rec.Code, rec.Body.String())
	}
}

func TestStorageActionUsesTypedHelperAndRechecksRevocation(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		if request.Operation != "ManageStorageMount" || request.StorageResourceID != "qualification-tmpfs" || request.StorageAction != "mount" || request.ConfirmationResource != request.StorageResourceID {
			t.Fatalf("request=%+v", request)
		}
		return protocol.Response{OK: true, OperationID: request.OperationID, State: "completed", Result: hostadmin.StorageMutationResult{ResourceID: request.StorageResourceID, Action: hostadmin.StorageActionMount, Result: hostadmin.StorageMountState{Mounted: true}}}, nil
	}
	rec := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodPost, "/api/v1/host/storage/qualification-tmpfs/actions", `{"action":"mount","confirmation_resource":"qualification-tmpfs"}`, session, csrf))
	if rec.Code != 200 || !called {
		t.Fatalf("action %d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	a, session, csrf = newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	called = false
	a.helperCall = func(protocol.Request) (protocol.Response, error) { called = true; return protocol.Response{}, nil }
	a.beforeStorageDispatch = func() {
		_, _ = a.db.Exec(`DELETE FROM administrator_permissions WHERE administrator_id=1 AND permission=?`, auth.PermissionStorageManage)
	}
	rec = httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodPost, "/api/v1/host/storage/qualification-tmpfs/actions", `{"action":"mount","confirmation_resource":"qualification-tmpfs"}`, session, csrf))
	if rec.Code != 403 || called {
		t.Fatalf("revocation %d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestStorageActionRejectsOversizedBodyBeforeDispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	called := false
	a.helperCall = func(protocol.Request) (protocol.Response, error) {
		called = true
		return protocol.Response{}, nil
	}
	body := `{"action":"mount","confirmation_resource":"qualification-tmpfs"}` + strings.Repeat(" ", 4096)
	recorder := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(recorder, authenticatedRequest(http.MethodPost, "/api/v1/host/storage/qualification-tmpfs/actions", body, session, csrf))
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d helper_called=%v body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestStorageActionRejectsUnknownConfirmationAndHistoryIsBounded(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiStorageManager(t)
	for _, target := range []string{"/api/v1/host/storage/../etc/actions", "/api/v1/host/storage/qualification-tmpfs/actions"} {
		body := `{"action":"mount","confirmation_resource":"wrong"}`
		rec := httptest.NewRecorder()
		a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodPost, target, body, session, csrf))
		if rec.Code == 200 {
			t.Fatalf("accepted %s", target)
		}
	}
	detail := storageAuditDetail{OperationID: "op-storage-test", ResourceID: "qualification-tmpfs", Action: "mount", AuthorizationOutcome: "allowed", PolicyOutcome: "allowed", Status: "completed"}
	if err := insertStorageAudit(context.Background(), a.db, "admin", detail); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodGet, "/api/v1/host/storage/qualification-tmpfs/history?limit=1&action=mount&result=completed", "", session, csrf))
	if rec.Code != 200 || !containsAll(rec.Body.String(), `"resource_id":"qualification-tmpfs"`, `"status":"completed"`) {
		t.Fatalf("history %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodGet, "/api/v1/host/storage/qualification-tmpfs/history?limit=100", "", session, csrf))
	if rec.Code != 400 {
		t.Fatalf("history limit=%d", rec.Code)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

type apiPersistence struct {
	status hostadmin.PersistentMountStatus
}

func (p *apiPersistence) Status(hostadmin.StoragePolicyResource) (hostadmin.PersistentMountStatus, error) {
	return p.status, nil
}
func (p *apiPersistence) Enable(context.Context, hostadmin.StoragePolicyResource) (hostadmin.PersistentMountResult, error) {
	return hostadmin.PersistentMountResult{}, errors.New("API must not mutate persistence directly")
}
func (p *apiPersistence) Disable(context.Context, hostadmin.StoragePolicyResource) (hostadmin.PersistentMountResult, error) {
	return hostadmin.PersistentMountResult{}, errors.New("API must not mutate persistence directly")
}
func (p *apiPersistence) Recover(context.Context, hostadmin.StoragePolicyResource) (hostadmin.PersistentMountResult, error) {
	return hostadmin.PersistentMountResult{}, errors.New("API must not mutate persistence directly")
}

func apiPersistentStorageManager(t *testing.T) *hostadmin.StorageManager {
	t.Helper()
	target := t.TempDir()
	manager := hostadmin.NewStorageManager(hostadmin.StoragePolicyDocument{Version: 1, Resources: []hostadmin.StoragePolicyResource{{ID: "qualification-ext4", Source: "/dev/disk/by-uuid/1111-2222", UUID: "1111-2222", Target: target, Filesystem: "ext4", Options: []string{"nodev", "nosuid", "noexec"}, Actions: []hostadmin.StorageAction{hostadmin.StorageActionPersistEnable, hostadmin.StorageActionPersistDisable, hostadmin.StorageActionPersistRecover}}}})
	manager.ValidateTarget = func(string) error { return nil }
	manager.ValidateSource = func(string) (string, error) { return "/dev/loop99", nil }
	manager.ReadMountInfo = func() ([]byte, error) { return []byte("1 0 0:1 / / rw - rootfs rootfs rw\n"), nil }
	manager.Persistence = &apiPersistence{status: hostadmin.PersistentMountStatus{State: "disabled", Health: "not_configured"}}
	return manager
}

func TestPersistentStoragePreviewAndTypedDispatch(t *testing.T) {
	a, session, csrf := newTestApp(t)
	a.hostStorageManagementEnabled = true
	a.hostStorageManager = apiPersistentStorageManager(t)
	rec := httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodGet, "/api/v1/host/storage/qualification-ext4/actions/enable_persistence/preview", "", session, csrf))
	if rec.Code != 200 || !containsAll(rec.Body.String(), `"allowed":true`, `"state":"disabled"`, `"expected_result":"KITPro-managed persistent mount enabled"`) {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		if request.Operation != "ManageStorageMount" || request.StorageAction != "enable_persistence" || request.StorageResourceID != "qualification-ext4" {
			t.Fatalf("request=%+v", request)
		}
		return protocol.Response{OK: true, OperationID: request.OperationID, State: "completed", Result: hostadmin.PersistentMountResult{ResourceID: request.StorageResourceID, Action: hostadmin.StorageActionPersistEnable, Previous: hostadmin.PersistentMountStatus{State: "disabled"}, Result: hostadmin.PersistentMountStatus{Enabled: true, State: "enabled"}, Status: "completed"}}, nil
	}
	rec = httptest.NewRecorder()
	a.guard(a.hostStorageResource)(rec, authenticatedRequest(http.MethodPost, "/api/v1/host/storage/qualification-ext4/actions", `{"action":"enable_persistence","confirmation_resource":"qualification-ext4"}`, session, csrf))
	if rec.Code != 200 || !called || !containsAll(rec.Body.String(), `"status":"completed"`) {
		t.Fatalf("action %d called=%v %s", rec.Code, called, rec.Body.String())
	}
}
