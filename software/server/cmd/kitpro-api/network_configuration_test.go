package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

type apiManagedNetworkRunner struct{}

func (apiManagedNetworkRunner) Run(_ context.Context, name string) ([]byte, error) {
	values := map[string]string{"links": `[{"ifindex":9,"ifname":"kitpro-test0","flags":["UP"],"mtu":1500,"operstate":"UP","linkinfo":{"info_kind":"dummy"}}]`, "addresses": `[{"ifindex":9,"ifname":"kitpro-test0","addr_info":[]}]`, "routes4": `[]`, "routes6": `[]`, "rules4": `[]`, "rules6": `[]`}
	return []byte(values[name]), nil
}
func apiManagedNetworkProvider() hostadmin.NetworkProvider {
	return hostadmin.NetworkProvider{Runner: apiManagedNetworkRunner{}, ReadFile: func(string, int64) ([]byte, error) { return []byte(""), nil }, ReadLink: func(string) (string, error) { return "", os.ErrNotExist }, Stat: func(path string) (os.FileInfo, error) {
		if path == "/run/systemd/netif" {
			return os.Stat(".")
		}
		return nil, os.ErrNotExist
	}, Now: time.Now}
}
func apiNetworkManager() (*hostadmin.NetworkManager, hostadmin.NetworkPolicyResource) {
	resource := hostadmin.NetworkPolicyResource{ID: "qualification", Interface: "kitpro-test0", Backend: hostadmin.NetworkBackendNetworkd, Profile: "90-kitpro-qualification.network", AllowedModes: []string{hostadmin.NetworkModeDHCP, hostadmin.NetworkModeStatic}, QualificationOnly: true}
	manager := hostadmin.NewNetworkManager(hostadmin.NetworkPolicy{Version: 1, Resources: []hostadmin.NetworkPolicyResource{resource}}, apiManagedNetworkProvider())
	manager.ReadCurrent = func(context.Context, hostadmin.NetworkPolicyResource) (hostadmin.NetworkCandidate, error) {
		return hostadmin.NetworkCandidate{Mode: hostadmin.NetworkModeDHCP}, nil
	}
	return manager, resource
}

func TestNetworkConfigurationPreviewAndApplyUseScopedTypedRequest(t *testing.T) {
	a, session, csrf := newTestApp(t)
	manager, resource := apiNetworkManager()
	a.hostNetworkProvider = apiManagedNetworkProvider()
	a.hostNetworkManager = manager
	a.hostNetworkManagementEnabled = true
	called := false
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		called = true
		if request.Operation != "ManageNetworkConfiguration" || request.NetworkResourceID != resource.ID || request.NetworkAction != "apply" || request.ConfirmationInterface != resource.Interface || request.NetworkMode != hostadmin.NetworkModeStatic {
			t.Fatalf("request=%+v", request)
		}
		return protocol.Response{OK: true, OperationID: request.OperationID, State: "awaiting_confirmation", Result: hostadmin.NetworkTransaction{ID: request.OperationID, State: "awaiting_confirmation", Candidate: hostadmin.NetworkConfiguration{Digest: request.CandidateDigest}}}, nil
	}
	route := a.guard(a.hostNetworkSubresource)
	preview := httptest.NewRecorder()
	route(preview, authenticatedRequest(http.MethodPost, "/api/v1/host/network/interfaces/kitpro-test0/configuration/preview", `{"candidate":{"mode":"static","addresses":["192.0.2.20/24"]}}`, session, csrf))
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"allowed":true`) {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	digest := hostadmin.NetworkConfigurationDigest(resource, hostadmin.NetworkCandidate{Mode: hostadmin.NetworkModeDHCP})
	apply := httptest.NewRecorder()
	route(apply, authenticatedRequest(http.MethodPost, "/api/v1/host/network/interfaces/kitpro-test0/configuration/apply", `{"candidate":{"mode":"static","addresses":["192.0.2.20/24"]},"confirmation_interface":"kitpro-test0","expected_digest":"`+digest+`"}`, session, csrf))
	if apply.Code != http.StatusAccepted || !called || !strings.Contains(apply.Body.String(), `"state":"awaiting_confirmation"`) {
		t.Fatalf("apply %d called=%v %s", apply.Code, called, apply.Body.String())
	}
}

func TestNetworkConfigurationRejectsRevocationAndProtectedInterface(t *testing.T) {
	a, session, csrf := newTestApp(t)
	manager, _ := apiNetworkManager()
	a.hostNetworkProvider = apiManagedNetworkProvider()
	a.hostNetworkManager = manager
	a.hostNetworkManagementEnabled = true
	called := false
	a.helperCall = func(protocol.Request) (protocol.Response, error) { called = true; return protocol.Response{}, nil }
	a.beforeNetworkDispatch = func() {
		_, _ = a.db.Exec(`DELETE FROM administrator_permissions WHERE administrator_id=1 AND permission=?`, auth.PermissionNetworkManage)
	}
	digest := hostadmin.NetworkConfigurationDigest(manager.Policy.Resources[0], hostadmin.NetworkCandidate{Mode: hostadmin.NetworkModeDHCP})
	rec := httptest.NewRecorder()
	a.guard(a.hostNetworkSubresource)(rec, authenticatedRequest(http.MethodPost, "/api/v1/host/network/interfaces/kitpro-test0/configuration/apply", `{"candidate":{"mode":"dhcp"},"confirmation_interface":"kitpro-test0","expected_digest":"`+digest+`"}`, session, csrf))
	if rec.Code != 403 || called {
		t.Fatalf("revocation %d called=%v %s", rec.Code, called, rec.Body.String())
	}
	manager.Policy.Resources[0].ManagementProtected = true
	preview := httptest.NewRecorder()
	a.guard(a.hostNetworkSubresource)(preview, authenticatedRequest(http.MethodPost, "/api/v1/host/network/interfaces/kitpro-test0/configuration/preview", `{"candidate":{"mode":"dhcp"}}`, session, csrf))
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"state":"protected"`) {
		t.Fatalf("protected %d %s", preview.Code, preview.Body.String())
	}
}

func TestNetworkTransactionReadReconcilesIndependentRollback(t *testing.T) {
	a, session, csrf := newTestApp(t)
	manager, resource := apiNetworkManager()
	a.hostNetworkManager = manager
	id := "op-0123456789abcdef0123456789abcdef"
	detail := networkAuditDetail{TransactionID: id, ResourceID: resource.ID, Interface: resource.Interface, Action: "apply", AuthorizationOutcome: "allowed", PolicyOutcome: "allowed", State: "awaiting_confirmation"}
	if err := insertNetworkAudit(context.Background(), a.db, "qual-admin", detail); err != nil {
		t.Fatal(err)
	}
	a.helperCall = func(request protocol.Request) (protocol.Response, error) {
		if request.NetworkAction != "status" || request.NetworkTransactionID != id || request.NetworkResourceID != resource.ID || request.ConfirmationInterface != resource.Interface {
			t.Fatalf("request=%+v", request)
		}
		return protocol.Response{OK: true, OperationID: request.OperationID, State: "rolled_back", Result: hostadmin.NetworkTransaction{ID: id, ResourceID: resource.ID, Interface: resource.Interface, State: "rolled_back", Rollback: "completed", FailureReason: "confirmation deadline expired"}}, nil
	}
	recorder := httptest.NewRecorder()
	a.guard(a.hostNetworkSubresource)(recorder, authenticatedRequest(http.MethodGet, "/api/v1/host/network/transactions/"+id, "", session, csrf))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"state":"rolled_back"`) || !strings.Contains(recorder.Body.String(), `"rollback_result":"completed"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, _, err := a.loadNetworkAudit(context.Background(), id)
	if err != nil || stored.State != "rolled_back" || stored.RollbackResult != "completed" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
