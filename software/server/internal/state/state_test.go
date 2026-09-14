package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSeparateSchemas(t *testing.T) {
	d := t.TempDir()
	a, e := Open(filepath.Join(d, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = Migrate(context.Background(), a, false); e != nil {
		t.Fatal(e)
	}
	b, e := Open(filepath.Join(d, "helper.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if e = Migrate(context.Background(), b, true); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = a.QueryRow("SELECT count(*) FROM operations").Scan(&n); e != nil {
		t.Fatal(e)
	}
	var table string
	if e = a.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='installations'").Scan(&table); e != nil || table != "installations" {
		t.Fatalf("installations schema missing: %v", e)
	}
	if _, e = b.Exec("INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation) VALUES('i','c','n','net','sha256:'||replace(hex(randomblob(32)), 'A','a'),'/srv/kitpro/apps/a/i/data','now',2)"); e != nil {
		t.Fatal(e)
	}
	if e = a.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='installation_service_exposure_binding_unique'").Scan(&table); e != nil {
		t.Fatalf("exposure uniqueness index missing: %v", e)
	}
	if _, e = a.Exec("INSERT INTO installation_service_exposure VALUES('inst-one','web','loopback','127.0.0.1',20000,'now','now'),('inst-two','web','loopback','127.0.0.1',20000,'now','now')"); e == nil {
		t.Fatal("duplicate active exposure binding accepted")
	}
}
