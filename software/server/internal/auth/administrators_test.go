package auth

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func administratorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE administrator (id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, administrator_id INTEGER NOT NULL, csrf_hash TEXT NOT NULL, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT);
CREATE TABLE administrator_permissions (administrator_id INTEGER NOT NULL, permission TEXT NOT NULL, granted_at TEXT NOT NULL, PRIMARY KEY(administrator_id,permission));
CREATE TABLE administrator_service_scopes (administrator_id INTEGER NOT NULL, unit_name TEXT NOT NULL, action TEXT NOT NULL, granted_at TEXT NOT NULL, PRIMARY KEY(administrator_id,unit_name,action));
CREATE TABLE audit_events (event_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, actor TEXT NOT NULL, target_id TEXT NOT NULL, outcome TEXT NOT NULL, detail_json TEXT NOT NULL, created_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAdministratorLifecycle(t *testing.T) {
	ctx := context.Background()
	db := administratorTestDB(t)
	if _, err := CreateAdministrator(ctx, db, "primary", "primary-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateAdministrator(ctx, db, "qualification", "qualification-password-123"); err != nil {
		t.Fatal(err)
	}
	if !Verify(ctx, db, "qualification", "qualification-password-123") {
		t.Fatal("created administrator could not authenticate")
	}
	removed, err := RemoveAdministrator(ctx, db, "qualification")
	if err != nil {
		t.Fatal(err)
	}
	if removed.AdministratorID == 0 || Verify(ctx, db, "qualification", "qualification-password-123") {
		t.Fatalf("removed=%+v still authenticates", removed)
	}
	if _, err = RemoveAdministrator(ctx, db, "primary"); err == nil {
		t.Fatal("final administrator removal was accepted")
	}
	var created, removedEvents int
	if err = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type='administrator-created'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type='administrator-removed'`).Scan(&removedEvents); err != nil {
		t.Fatal(err)
	}
	if created != 2 || removedEvents != 1 {
		t.Fatalf("audit events created=%d removed=%d", created, removedEvents)
	}
}

func TestCreateAdministratorRejectsInvalidInput(t *testing.T) {
	db := administratorTestDB(t)
	for _, test := range []struct{ user, password string }{
		{"", "valid-password-123"},
		{" spaced ", "valid-password-123"},
		{"short", "too-short"},
	} {
		if _, err := CreateAdministrator(context.Background(), db, test.user, test.password); err == nil {
			t.Fatalf("accepted user=%q", test.user)
		}
	}
}
