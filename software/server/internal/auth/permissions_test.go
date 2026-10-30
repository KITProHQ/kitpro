package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyAdministratorPermissionsAndRestrictedCreation(t *testing.T) {
	ctx := context.Background()
	db := administratorTestDB(t)
	if _, err := db.Exec(`INSERT INTO administrator(username,password_hash,created_at,updated_at) VALUES('legacy','x','now','now')`); err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	legacy, err := PrincipalFor(ctx, db, 1)
	if err != nil || len(legacy.Permissions) != len(allPermissions) || !legacy.Has(PermissionSecurityAdmin) {
		t.Fatalf("legacy principal=%+v err=%v", legacy, err)
	}
	if _, err = CreateAdministrator(ctx, db, "reader", "reader-password-123"); err != nil {
		t.Fatal(err)
	}
	reader, err := PrincipalFor(ctx, db, 2)
	if err != nil || len(reader.Permissions) != 1 || !reader.Has(PermissionHostRead) {
		t.Fatalf("reader principal=%+v err=%v", reader, err)
	}
}

func TestPermissionRevocationRevokesSessionsAndFinalSecurityAdminIsProtected(t *testing.T) {
	ctx := context.Background()
	db := administratorTestDB(t)
	if err := Setup(ctx, db, "primary", "primary-password-123"); err != nil {
		t.Fatal(err)
	}
	token, _, err := NewSession(ctx, db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = SetPermissions(ctx, db, "primary", []Permission{PermissionHostRead}); err == nil {
		t.Fatal("final security administrator demotion was accepted")
	}
	if _, err = CreateAdministrator(ctx, db, "security-two", "security-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err = RemoveAdministrator(ctx, db, "primary"); err == nil {
		t.Fatal("final security administrator removal was accepted")
	}
	if _, err = SetPermissions(ctx, db, "security-two", AllPermissions()); err != nil {
		t.Fatal(err)
	}
	change, err := SetPermissions(ctx, db, "primary", []Permission{PermissionHostRead, PermissionServicesManage})
	if err != nil || change.SessionsRevoked != 1 {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	if _, ok := Session(ctx, db, request); ok {
		t.Fatal("session survived permission revocation")
	}
}

func TestExactServiceScopes(t *testing.T) {
	ctx := context.Background()
	db := administratorTestDB(t)
	if _, err := CreateAdministrator(ctx, db, "operator", "operator-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPermissions(ctx, db, "operator", []Permission{PermissionHostRead, PermissionServicesManage}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetServiceScopes(ctx, db, "operator", []ServiceScope{{Unit: "alpha15-worker.service", Action: "restart"}}); err != nil {
		t.Fatal(err)
	}
	principal, err := PrincipalFor(ctx, db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !CanManageService(ctx, db, principal, "alpha15-worker.service", "restart") || CanManageService(ctx, db, principal, "alpha15-worker.service", "stop") || CanManageService(ctx, db, principal, "other.service", "restart") {
		t.Fatal("exact service scope was not enforced")
	}
}
