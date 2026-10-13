package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/state"
)

func TestVacuum(t *testing.T) {
	d := t.TempDir()
	db, e := state.Open(filepath.Join(d, "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	backupPath, e := Vacuum(context.Background(), db, d, "b.db")
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(backupPath)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode = %04o, want 0600", info.Mode().Perm())
	}
	if e = Verify(context.Background(), filepath.Join(d, "b.db")); e != nil {
		t.Fatal(e)
	}
	if _, e = Vacuum(context.Background(), db, d, "b.db"); e == nil {
		t.Fatal("backup overwrite accepted")
	}
	assertNoPartialBackups(t, d)
}

func TestVacuumRejectsCorruptSourceWithoutFinalOrPartialBackup(t *testing.T) {
	d := t.TempDir()
	databasePath := filepath.Join(d, "corrupt.db")
	if err := os.WriteFile(databasePath, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = Vacuum(context.Background(), db, filepath.Join(d, "backups"), "upgrade.db"); err == nil || !strings.Contains(err.Error(), "source database integrity check failed") {
		t.Fatalf("corrupt source error = %v", err)
	}
	if _, err = os.Stat(filepath.Join(d, "backups", "upgrade.db")); !os.IsNotExist(err) {
		t.Fatalf("failed backup was promoted: %v", err)
	}
	assertNoPartialBackups(t, filepath.Join(d, "backups"))
}

func TestVacuumRejectsUnwritableBackupLocationWithoutChangingSource(t *testing.T) {
	d := t.TempDir()
	databasePath := filepath.Join(d, "source.db")
	db, err := state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(d, "not-a-directory")
	if err = os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Vacuum(context.Background(), db, blocked, "upgrade.db"); err == nil {
		t.Fatal("backup into a non-directory succeeded")
	}
	after, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed backup changed the source database")
	}
}

func TestRestoreControlAtomicVerifiesSourceAndPreservesPriorDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	targetPath := filepath.Join(root, "control.db")
	target, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(`CREATE TABLE value_state(value TEXT NOT NULL); INSERT INTO value_state VALUES('old')`); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	if err = os.Chmod(targetPath, 0640); err != nil {
		t.Fatal(err)
	}
	targetInfoBefore, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "source.db")
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(`CREATE TABLE value_state(value TEXT NOT NULL); INSERT INTO value_state VALUES('new')`); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	recovery, err := RestoreControlAtomic(ctx, sourcePath, targetPath)
	if err != nil || recovery == "" {
		t.Fatalf("recovery=%q err=%v", recovery, err)
	}
	targetInfoAfter, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfoAfter.Mode().Perm() != targetInfoBefore.Mode().Perm() {
		t.Fatalf("restored target mode = %04o, want preserved %04o", targetInfoAfter.Mode().Perm(), targetInfoBefore.Mode().Perm())
	}
	targetStatBefore, beforeOK := targetInfoBefore.Sys().(*syscall.Stat_t)
	targetStatAfter, afterOK := targetInfoAfter.Sys().(*syscall.Stat_t)
	if !beforeOK || !afterOK || targetStatBefore.Uid != targetStatAfter.Uid || targetStatBefore.Gid != targetStatAfter.Gid {
		t.Fatalf("restored target ownership was not preserved")
	}
	for path, expected := range map[string]string{targetPath: "new", recovery: "old"} {
		db, openErr := sql.Open("sqlite", path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		var value string
		queryErr := db.QueryRow(`SELECT value FROM value_state`).Scan(&value)
		_ = db.Close()
		if queryErr != nil || value != expected {
			t.Fatalf("database %s value=%q err=%v", path, value, queryErr)
		}
	}
	corrupt := filepath.Join(root, "corrupt.db")
	if err = os.WriteFile(corrupt, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreControlAtomic(ctx, corrupt, targetPath); err == nil {
		t.Fatal("corrupt restore source accepted")
	}
	if err = os.WriteFile(targetPath+"-wal", []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreControlAtomic(ctx, sourcePath, targetPath); err == nil || !strings.Contains(err.Error(), "active SQLite sidecar") {
		t.Fatalf("active WAL restore error=%v", err)
	}
}

func assertNoPartialBackups(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".partial-") {
			t.Fatalf("partial backup remains: %s", entry.Name())
		}
	}
}

func TestRestoreControlHoldsIngressWithoutChangingBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sourcePath, targetPath := filepath.Join(dir, "source.db"), filepath.Join(dir, "target.db")
	source, err := state.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Migrate(ctx, source, false); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(`INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES('inst-test','memos','0.30.0','running',1,'now','now'); INSERT INTO ingress_routes(route_id,installation_id,service_id,hostname,exposure_mode,upstream_protocol,tls_policy,certificate_id,provider_id,desired_state,applied_generation,created_at,updated_at) VALUES('route-0123456789abcdef','inst-test','web','test.example.test','lan','http','none','','','enabled',5,'now','now'); INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,created_at,updated_at) VALUES(5,'op-test','digest','active','now','now')`); err != nil {
		t.Fatal(err)
	}
	source.Close()
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreControlAtomic(ctx, sourcePath, targetPath); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("archive source changed", err)
	}
	target, err := state.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var holds, active int
	if err = target.QueryRow(`SELECT COUNT(*) FROM ingress_route_holds WHERE reason='application-restore'`).Scan(&holds); err != nil || holds != 1 {
		t.Fatalf("holds=%d err=%v", holds, err)
	}
	if err = target.QueryRow(`SELECT COUNT(*) FROM ingress_generations WHERE status='active'`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active=%d err=%v", active, err)
	}
	if _, err = target.Exec(`INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,created_at,updated_at) VALUES(8,'op-reactivate','digest','active','now','now')`); err != nil {
		t.Fatalf("new timeline cannot activate: %v", err)
	}
}
