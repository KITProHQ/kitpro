package state

import (
	"context"
	"database/sql"
	"os"
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
	if e = b.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='application_backups'").Scan(&table); e != nil || table != "application_backups" {
		t.Fatalf("application backup schema missing: %v", e)
	}
	if e = a.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='installation_service_exposure_binding_unique'").Scan(&table); e != nil {
		t.Fatalf("exposure uniqueness index missing: %v", e)
	}
	if _, e = a.Exec("INSERT INTO installation_service_exposure VALUES('inst-one','web','loopback','127.0.0.1',20000,'now','now'),('inst-two','web','loopback','127.0.0.1',20000,'now','now')"); e == nil {
		t.Fatal("duplicate active exposure binding accepted")
	}
}

func TestHelperV8MigrationPreservesLegacyEvidenceAndOwnership(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "legacy-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version VALUES(8);
		CREATE TABLE receipts (operation_id TEXT PRIMARY KEY,request_hash TEXT NOT NULL,operation_type TEXT NOT NULL,instance_id TEXT NOT NULL,phase TEXT NOT NULL,outcome TEXT NOT NULL,container_id TEXT,created_at TEXT NOT NULL);
		CREATE TABLE ownership (instance_id TEXT PRIMARY KEY,container_id TEXT NOT NULL,container_name TEXT NOT NULL,network_name TEXT NOT NULL,image_digest TEXT NOT NULL,data_path TEXT NOT NULL,created_at TEXT NOT NULL,runtime_generation INTEGER NOT NULL DEFAULT 1,application_id TEXT NOT NULL DEFAULT '',release_id TEXT NOT NULL DEFAULT '',exposure_mode TEXT NOT NULL DEFAULT 'internal',service_id TEXT NOT NULL DEFAULT '',host_address TEXT NOT NULL DEFAULT '',host_port INTEGER NOT NULL DEFAULT 0,container_port INTEGER NOT NULL DEFAULT 0,service_protocol TEXT NOT NULL DEFAULT '');
		INSERT INTO receipts VALUES('legacy-op','legacy-hash','InstallApplication','inst-legacy0001','completed','succeeded','container-one','old-time');
		INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation) VALUES('inst-legacy0001','container-one','kitpro-legacy','kitpro-net','sha256:legacy','/srv/kitpro/apps/legacy/data','old-time',4);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	var hash, outcome, container string
	if err = db.QueryRow(`SELECT request_hash,outcome,container_id FROM receipts WHERE operation_id='legacy-op'`).Scan(&hash, &outcome, &container); err != nil || hash != "legacy-hash" || outcome != "succeeded" || container != "container-one" {
		t.Fatalf("legacy receipt changed: %q %q %q err=%v", hash, outcome, container, err)
	}
	var generation int
	if err = db.QueryRow(`SELECT runtime_generation FROM ownership WHERE instance_id='inst-legacy0001'`).Scan(&generation); err != nil || generation != 4 {
		t.Fatalf("ownership changed: generation=%d err=%v", generation, err)
	}
	for _, table := range []string{"helper_operations", "helper_operation_events", "installation_leases"} {
		var found string
		if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != table {
			t.Fatalf("missing additive table %s: %v", table, err)
		}
	}
	for _, table := range []string{"runtime_generations", "runtime_components"} {
		var found string
		if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != table {
			t.Fatalf("missing lifecycle table %s: %v", table, err)
		}
	}
	var status, componentID, observedID string
	if err = db.QueryRow(`SELECT g.status,c.component_id,c.observed_container_id FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id='inst-legacy0001' AND g.runtime_generation=4`).Scan(&status, &componentID, &observedID); err != nil {
		t.Fatal(err)
	}
	if status != "verification_required" || componentID != "app" || observedID != "container-one" {
		t.Fatalf("bad lifecycle backfill: %s %s %s", status, componentID, observedID)
	}
	for _, absent := range []string{"helper_operation_steps"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, absent).Scan(&count); err != nil || count != 0 {
			t.Fatalf("future table %s unexpectedly activated", absent)
		}
	}
}

func TestRuntimeGenerationAllowsOnlyOneActivePerInstallation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,created_at,cleanup_state) VALUES('inst-one',?,'op','app','release','active',?,'now','clean')`
	if _, err = db.Exec(insert, 1, "network-one"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(insert, 2, "network-two"); err == nil {
		t.Fatal("second active generation accepted")
	}
}

func TestHelperV13AddsLifecycleReconciliationAndRestoreEvidence(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	for _, column := range []string{"topology_hash", "dependencies_json", "start_ordinal"} {
		var count int
		table := "runtime_components"
		if column == "topology_hash" {
			table = "runtime_generations"
		}
		if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing %s.%s: count=%d err=%v", table, column, count, err)
		}
	}
	var table string
	if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='lifecycle_component_steps'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"checked_generation", "runtime_state", "runtime_identity", "mismatch_codes_json", "recommended_action", "originating_operation_id", "evidence_json"} {
		var count int
		if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('reconciliation') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing reconciliation.%s: count=%d err=%v", column, count, err)
		}
	}
	for _, table := range []string{"restore_operations", "restore_storage_steps"} {
		var found string
		if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != table {
			t.Fatalf("missing restore journal table %s: %v", table, err)
		}
	}
}

func TestHelperV10UpgradesInPlaceToMultiComponentEvidence(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "helper-v10.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(10);
		CREATE TABLE helper_operations(operation_id TEXT PRIMARY KEY);
		CREATE TABLE runtime_generations(installation_id TEXT NOT NULL,runtime_generation INTEGER NOT NULL,creating_operation_id TEXT NOT NULL,application_id TEXT NOT NULL,release_id TEXT NOT NULL,status TEXT NOT NULL,network_name TEXT NOT NULL,observed_network_id TEXT NOT NULL DEFAULT '',plan_hash TEXT NOT NULL DEFAULT '',data_path TEXT NOT NULL DEFAULT '',exposure_mode TEXT NOT NULL DEFAULT 'internal',service_id TEXT NOT NULL DEFAULT '',host_address TEXT NOT NULL DEFAULT '',host_port INTEGER NOT NULL DEFAULT 0,container_port INTEGER NOT NULL DEFAULT 0,service_protocol TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,verified_at TEXT,committed_at TEXT,retired_at TEXT,cleanup_state TEXT NOT NULL,PRIMARY KEY(installation_id,runtime_generation));
		CREATE TABLE runtime_components(installation_id TEXT NOT NULL,runtime_generation INTEGER NOT NULL,component_id TEXT NOT NULL,container_name TEXT NOT NULL,observed_container_id TEXT NOT NULL DEFAULT '',image_digest TEXT NOT NULL,observed_image_id TEXT NOT NULL DEFAULT '',configuration_hash TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,created_at TEXT NOT NULL,started_at TEXT,verified_at TEXT,PRIMARY KEY(installation_id,runtime_generation,component_id));
		INSERT INTO runtime_generations VALUES('inst-old',2,'old-op','app','release','active','network','network-id','plan','/data','internal','','',0,0,'','now','now','now',NULL,'clean');
		INSERT INTO runtime_components VALUES('inst-old',2,'app','name','container','image','image-id','config','running','now','now','now');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	var version, ordinal int
	var topology, dependencies string
	if err = db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT topology_hash FROM runtime_generations WHERE installation_id='inst-old'`).Scan(&topology); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT dependencies_json,start_ordinal FROM runtime_components WHERE installation_id='inst-old'`).Scan(&dependencies, &ordinal); err != nil {
		t.Fatal(err)
	}
	if version != 13 || topology != "" || dependencies != "[]" || ordinal != 0 {
		t.Fatalf("version=%d topology=%q dependencies=%q ordinal=%d", version, topology, dependencies, ordinal)
	}
}

func TestExactPublicAlpha11Schema7FixturesUpgradeWithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "application-data", "marker.txt")
	if err := os.MkdirAll(filepath.Dir(marker), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("alpha11 application data\n"), 0640); err != nil {
		t.Fatal(err)
	}
	loadFixture := func(name, fixture string) *sql.DB {
		t.Helper()
		db, err := Open(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		script, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err = db.Exec(string(script)); err != nil {
			db.Close()
			t.Fatal(err)
		}
		return db
	}

	control := loadFixture("control.db", "alpha11_control_schema7.sql")
	defer control.Close()
	if err := Migrate(context.Background(), control, false); err != nil {
		t.Fatal(err)
	}
	var version, generation int
	var desired, exposureMode, username, rootID string
	if err := control.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("control version=%d err=%v", version, err)
	}
	if err := control.QueryRow(`SELECT desired_state,runtime_generation FROM installations WHERE installation_id='inst-paperless1'`).Scan(&desired, &generation); err != nil || desired != "running" || generation != 1 {
		t.Fatalf("control installation desired=%q generation=%d err=%v", desired, generation, err)
	}
	if err := control.QueryRow(`SELECT mode FROM installation_service_exposure WHERE installation_id='inst-paperless1' AND service_id='web'`).Scan(&exposureMode); err != nil || exposureMode != "loopback" {
		t.Fatalf("control exposure=%q err=%v", exposureMode, err)
	}
	if err := control.QueryRow(`SELECT username FROM administrator WHERE id=1`).Scan(&username); err != nil || username != "admin" {
		t.Fatalf("administrator=%q err=%v", username, err)
	}
	if err := control.QueryRow(`SELECT root_id FROM installation_storage_selections WHERE installation_id='inst-paperless1'`).Scan(&rootID); err != nil || rootID != "storage-0123456789abcdef" {
		t.Fatalf("control storage=%q err=%v", rootID, err)
	}

	helper := loadFixture("helper.db", "alpha11_helper_schema7.sql")
	defer helper.Close()
	if err := Migrate(context.Background(), helper, true); err != nil {
		t.Fatal(err)
	}
	if err := helper.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("helper version=%d err=%v", version, err)
	}
	var receiptHash, secret, binding, status string
	if err := helper.QueryRow(`SELECT request_hash FROM receipts WHERE operation_id='op-alpha11-helper'`).Scan(&receiptHash); err != nil || receiptHash != "alpha11-hash" {
		t.Fatalf("receipt=%q err=%v", receiptHash, err)
	}
	if err := helper.QueryRow(`SELECT value FROM installation_secrets WHERE installation_id='inst-paperless1' AND component_id='web' AND name='PAPERLESS_SECRET_KEY'`).Scan(&secret); err != nil || secret != "alpha11-generated-secret" {
		t.Fatalf("secret=%q err=%v", secret, err)
	}
	if err := helper.QueryRow(`SELECT root_id FROM external_storage_bindings WHERE installation_id='inst-paperless1' AND component_id='web' AND slot_id='consume'`).Scan(&binding); err != nil || binding != "storage-0123456789abcdef" {
		t.Fatalf("binding=%q err=%v", binding, err)
	}
	if err := helper.QueryRow(`SELECT status FROM runtime_generations WHERE installation_id='inst-busybox01' AND runtime_generation=1`).Scan(&status); err != nil || status != "verification_required" {
		t.Fatalf("single backfill status=%q err=%v", status, err)
	}
	var activeConstraint int
	if err := helper.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='runtime_generations_one_active'`).Scan(&activeConstraint); err != nil || activeConstraint != 1 {
		t.Fatalf("one-active constraint=%d err=%v", activeConstraint, err)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "alpha11 application data\n" {
		t.Fatalf("application data changed: %q err=%v", contents, err)
	}
}
