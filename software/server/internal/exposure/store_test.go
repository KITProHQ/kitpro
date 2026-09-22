package exposure

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAssignmentPersistenceAndStableReuse(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE installation_service_exposure (installation_id TEXT NOT NULL, service_id TEXT NOT NULL, transport TEXT NOT NULL CHECK(transport IN ('tcp','udp')), mode TEXT NOT NULL, host_address TEXT NOT NULL DEFAULT '', host_port INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(installation_id,service_id)); CREATE UNIQUE INDEX exposure_bind_unique ON installation_service_exposure(host_address,host_port,transport) WHERE mode <> 'internal'`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := Upsert(ctx, db, "inst-12345678", "web", Assignment{Mode: Loopback, Address: "127.0.0.1", Port: 20000}, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := Upsert(ctx, db, first.InstallationID, first.ServiceID, Assignment{Mode: Internal, Port: first.HostPort}, "10.0.0.2")
	if err != nil || disabled.HostPort != 20000 || disabled.Mode != Internal {
		t.Fatalf("disable lost assignment: %#v %v", disabled, err)
	}
	reenabled, err := Upsert(ctx, db, first.InstallationID, first.ServiceID, Assignment{Mode: Loopback, Address: "127.0.0.1", Port: first.HostPort}, "10.0.0.2")
	if err != nil || reenabled.HostPort != first.HostPort || reenabled.CreatedAt != first.CreatedAt {
		t.Fatalf("assignment was not stable: %#v %v", reenabled, err)
	}
}
