package operations

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRecoverAcceptedPreservesProjection(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE operations (id TEXT PRIMARY KEY,type TEXT,requested_at TEXT,status TEXT,instance_id TEXT,summary TEXT); INSERT INTO operations VALUES('one','InstallApplication','now','accepted','inst-example01',''),('two','InstallApplication','now','succeeded','inst-example02','ok')`); err != nil {
		t.Fatal(err)
	}
	count, err := RecoverAccepted(context.Background(), db)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var status, summary string
	if err = db.QueryRow(`SELECT status,summary FROM operations WHERE id='one'`).Scan(&status, &summary); err != nil || status != "accepted" || summary != "awaiting helper state" {
		t.Fatalf("status=%q summary=%q err=%v", status, summary, err)
	}
	if err = db.QueryRow(`SELECT status FROM operations WHERE id='two'`).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("completed operation changed: %q %v", status, err)
	}
}
