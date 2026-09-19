package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type backupRuntime struct {
	running    map[string]bool
	events     []string
	failStarts int
}

func (runtime *backupRuntime) Name() string                     { return "test" }
func (runtime *backupRuntime) Version() (map[string]any, error) { return map[string]any{}, nil }
func (runtime *backupRuntime) Info() (map[string]any, error)    { return map[string]any{}, nil }
func (runtime *backupRuntime) Pull(string) error                { return nil }
func (runtime *backupRuntime) CreateNetwork(string, map[string]string) (map[string]any, error) {
	return map[string]any{}, nil
}
func (runtime *backupRuntime) CreateContainerPlan(containers.ContainerPlan) (string, error) {
	return "", nil
}
func (runtime *backupRuntime) Start(id string) error {
	if runtime.failStarts > 0 {
		runtime.failStarts--
		runtime.events = append(runtime.events, "start-failed:"+id)
		return errors.New("injected start failure")
	}
	runtime.running[id] = true
	runtime.events = append(runtime.events, "start:"+id)
	return nil
}
func (runtime *backupRuntime) Stop(id string) error {
	runtime.running[id] = false
	runtime.events = append(runtime.events, "stop:"+id)
	return nil
}
func (runtime *backupRuntime) Remove(string) error        { return nil }
func (runtime *backupRuntime) RemoveNetwork(string) error { return nil }
func (runtime *backupRuntime) Inspect(id string) (map[string]any, error) {
	return map[string]any{"State": map[string]any{"Running": runtime.running[id]}}, nil
}
func (runtime *backupRuntime) ListContainers() ([]containers.ContainerSummary, error) {
	return nil, nil
}
func (runtime *backupRuntime) HasForeignNetworkMember(string, string) (bool, error) {
	return false, nil
}
func (runtime *backupRuntime) HasForeignNetworkMembers(string, map[string]bool) (bool, error) {
	return false, nil
}

func TestFilesystemApplicationBackupRestoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))
	runtime := &backupRuntime{running: map[string]bool{"fixture-runtime": true}}
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-busybox01"
	dataPath := filepath.Join(managedApplicationRoot, "busybox", instance, "data")
	if err := os.MkdirAll(dataPath, 0750); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataPath, "known-state.txt")
	if err := os.WriteFile(statePath, []byte("before backup\n"), 0640); err != nil {
		t.Fatal(err)
	}
	image := "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0"
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, "fixture-runtime", "fixture", "network", image, dataPath, "now", 1, "busybox", "1.37.0", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-11111111111111111111111111111111", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "busybox", ReleaseID: "1.37.0", RuntimeGeneration: 1}
	result, err := createApplicationBackup(context.Background(), db, backupRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result["backup_id"] != backupRequest.ID || !runtime.running["fixture-runtime"] {
		t.Fatalf("backup result/runtime: %#v %#v", result, runtime)
	}
	if err := os.WriteFile(statePath, []byte("after backup\n"), 0640); err != nil {
		t.Fatal(err)
	}
	spaceCheck := requireApplicationBackupSpace
	t.Cleanup(func() { requireApplicationBackupSpace = spaceCheck })
	requireApplicationBackupSpace = func(string, int64) error { return errors.New("insufficient backup storage space") }
	rejectedRestore := protocol.Request{Version: 1, ID: "op-22222222222222222222222222222222", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, rejectedRestore); err == nil || len(runtime.events) != 2 {
		t.Fatalf("restore capacity preflight did not fail before quiescence: %v %#v", err, runtime.events)
	}
	requireApplicationBackupSpace = spaceCheck
	restoreRequest := protocol.Request{Version: 1, ID: "op-33333333333333333333333333333333", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, restoreRequest); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(statePath)
	if err != nil || string(contents) != "before backup\n" {
		t.Fatalf("restored state: %q %v", contents, err)
	}
	if !runtime.running["fixture-runtime"] {
		t.Fatal("runtime was not healthy after restore")
	}
	if len(runtime.events) != 4 || runtime.events[0] != "stop:fixture-runtime" || runtime.events[1] != "start:fixture-runtime" || runtime.events[2] != "stop:fixture-runtime" || runtime.events[3] != "start:fixture-runtime" {
		t.Fatalf("unexpected runtime lifecycle: %#v", runtime.events)
	}
}

func TestMetadataOnlyBackupDoesNotStopApplication(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))
	runtime := &backupRuntime{running: map[string]bool{"it-tools-runtime": true}}
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-ittools01"
	image := "docker.io/corentinth/it-tools@sha256:6f177c156b9466610e0f2093e24668b78da501c66f0054f98bccb582b74ab26b"
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, "it-tools-runtime", "it-tools", "network", image, "", "now", 1, "it-tools", "2024.10.22-7ca5933", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{Version: 1, ID: "op-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "it-tools", ReleaseID: "2024.10.22-7ca5933", RuntimeGeneration: 1}
	if _, err := createApplicationBackup(context.Background(), db, request); err != nil {
		t.Fatal(err)
	}
	if len(runtime.events) != 0 || !runtime.running["it-tools-runtime"] {
		t.Fatalf("metadata-only backup interrupted runtime: %#v", runtime.events)
	}
}

func TestSQLiteApplicationBackupRestoresDatabaseAndSecret(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))
	runtime := &backupRuntime{running: map[string]bool{"webui-runtime": true}}
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-webui0001"
	dataPath := filepath.Join(managedApplicationRoot, "open-webui", instance, "data")
	if err := os.MkdirAll(dataPath, 0750); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(dataPath, "webui.db")
	writeApplicationRecord(t, databasePath, "original")
	image := "ghcr.io/open-webui/open-webui@sha256:9cd136effce6bb12a6a1988a35ab3b82cb40c48a6768fceeb17c83baf7cfac9c"
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, "webui-runtime", "webui", "network", image, dataPath, "now", 1, "open-webui", "0.11.3", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	originalSecret := strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO installation_secrets(installation_id,component_id,name,value,created_at) VALUES(?,?,?,?,?)`, instance, "", "WEBUI_SECRET_KEY", originalSecret, "now"); err != nil {
		t.Fatal(err)
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "open-webui", ReleaseID: "0.11.3", RuntimeGeneration: 1}
	result, err := createApplicationBackup(context.Background(), db, backupRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result["databases"] != 1 {
		t.Fatalf("SQLite inventory: %#v", result)
	}
	writeApplicationRecord(t, databasePath, "mutated")
	if _, err := db.Exec(`UPDATE installation_secrets SET value=? WHERE installation_id=?`, strings.Repeat("b", 64), instance); err != nil {
		t.Fatal(err)
	}
	restoreRequest := protocol.Request{Version: 1, ID: "op-cccccccccccccccccccccccccccccccc", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, restoreRequest); err != nil {
		t.Fatal(err)
	}
	if got := readApplicationRecord(t, databasePath); got != "original" {
		t.Fatalf("restored SQLite record = %q", got)
	}
	var secret string
	if err := db.QueryRow(`SELECT value FROM installation_secrets WHERE installation_id=? AND name='WEBUI_SECRET_KEY'`, instance).Scan(&secret); err != nil || secret != originalSecret {
		t.Fatalf("restored secret mismatch: %v", err)
	}
}

func TestMultiContainerBackupRestoresAuthoritativeStateOnly(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))
	runtime := &backupRuntime{running: map[string]bool{"broker-runtime": true, "web-runtime": true}}
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-paperless1"
	base := filepath.Join(managedApplicationRoot, "paperless-ngx", instance)
	for _, relative := range []string{"broker/data", "web/data", "web/media", "web/consume", "web/export"} {
		if err := os.MkdirAll(filepath.Join(base, relative), 0750); err != nil {
			t.Fatal(err)
		}
	}
	databasePath := filepath.Join(base, "web/data/db.sqlite3")
	writeApplicationRecord(t, databasePath, "document-original")
	mediaPath := filepath.Join(base, "web/media/document.pdf")
	brokerPath := filepath.Join(base, "broker/data/dump.rdb")
	if err := os.WriteFile(mediaPath, []byte("original media"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brokerPath, []byte("broker before"), 0640); err != nil {
		t.Fatal(err)
	}
	components := []struct{ id, runtimeID, image string }{
		{"broker", "broker-runtime", "docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7"},
		{"web", "web-runtime", "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f"},
	}
	for _, component := range components {
		if _, err := db.Exec(`INSERT INTO component_ownership(installation_id,component_id,container_id,container_name,network_name,image_digest,runtime_generation,created_at) VALUES(?,?,?,?,?,?,?,?)`, instance, component.id, component.runtimeID, component.id, "network", component.image, 1, "now"); err != nil {
			t.Fatal(err)
		}
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-dddddddddddddddddddddddddddddddd", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "paperless-ngx", ReleaseID: "2.20.15", RuntimeGeneration: 1}
	if _, err := createApplicationBackup(context.Background(), db, backupRequest); err != nil {
		t.Fatal(err)
	}
	writeApplicationRecord(t, databasePath, "document-mutated")
	if err := os.WriteFile(mediaPath, []byte("mutated media"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brokerPath, []byte("broker after"), 0640); err != nil {
		t.Fatal(err)
	}
	restoreRequest := protocol.Request{Version: 1, ID: "op-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, restoreRequest); err != nil {
		t.Fatal(err)
	}
	if got := readApplicationRecord(t, databasePath); got != "document-original" {
		t.Fatalf("restored Paperless SQLite record = %q", got)
	}
	if contents, _ := os.ReadFile(mediaPath); string(contents) != "original media" {
		t.Fatalf("restored media = %q", contents)
	}
	if contents, _ := os.ReadFile(brokerPath); string(contents) != "broker after" {
		t.Fatalf("ephemeral broker state was unexpectedly restored: %q", contents)
	}
	wantEvents := []string{"stop:web-runtime", "stop:broker-runtime", "start:broker-runtime", "start:web-runtime", "stop:web-runtime", "stop:broker-runtime", "start:broker-runtime", "start:web-runtime"}
	if strings.Join(runtime.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("dependency lifecycle = %#v", runtime.events)
	}
}

func TestRestoreRejectsWrongApplicationAndTamperedArchive(t *testing.T) {
	root := t.TempDir()
	db := openBackupTestDB(t, root)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO application_backups(backup_id,installation_id,application_id,release_id,runtime_generation,archive_path,archive_sha256,created_at,status) VALUES(?,?,?,?,?,?,?,?, 'complete')`, "op-33333333333333333333333333333333", "inst-one00001", "busybox", "1.37.0", 1, filepath.Join(root, "missing.tar.gz"), "bad", "now"); err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{ID: "op-44444444444444444444444444444444", InstanceID: "inst-other0001", BackupID: "op-33333333333333333333333333333333"}
	if _, err := restoreApplicationBackup(context.Background(), db, request); err == nil {
		t.Fatal("wrong installation backup accepted")
	}
}

func TestRestoreRestartFailureRollsBackPriorState(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))
	runtime := &backupRuntime{running: map[string]bool{"fixture-runtime": true}}
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-rollback01"
	dataPath := filepath.Join(managedApplicationRoot, "busybox", instance, "data")
	if err := os.MkdirAll(dataPath, 0750); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataPath, "known-state.txt")
	if err := os.WriteFile(statePath, []byte("backup state\n"), 0640); err != nil {
		t.Fatal(err)
	}
	image := "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0"
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, "fixture-runtime", "fixture", "network", image, dataPath, "now", 1, "busybox", "1.37.0", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-56565656565656565656565656565656", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "busybox", ReleaseID: "1.37.0", RuntimeGeneration: 1}
	if _, err := createApplicationBackup(context.Background(), db, backupRequest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("prior live state\n"), 0640); err != nil {
		t.Fatal(err)
	}
	runtime.failStarts = 1
	restoreRequest := protocol.Request{Version: 1, ID: "op-78787878787878787878787878787878", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, restoreRequest); err == nil || !strings.Contains(err.Error(), "start restored application") {
		t.Fatalf("restore restart failure not reported: %v", err)
	}
	contents, err := os.ReadFile(statePath)
	if err != nil || string(contents) != "prior live state\n" {
		t.Fatalf("prior state was not rolled back: %q %v", contents, err)
	}
	if !runtime.running["fixture-runtime"] {
		t.Fatal("prior runtime state was not resumed")
	}
	failedPath := dataPath + ".kitpro-restore-failed-" + restoreRequest.ID
	if contents, err = os.ReadFile(filepath.Join(failedPath, "known-state.txt")); err != nil || string(contents) != "backup state\n" {
		t.Fatalf("failed restored tree was not preserved: %q %v", contents, err)
	}
}

func TestRemoveBackupTreeReclaimsReadOnlyApplicationDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "rollback")
	nested := filepath.Join(target, "read-only")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "state"), []byte("preserved\n"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0555); err != nil {
		t.Fatal(err)
	}
	if err := removeBackupTree(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("backup tree still exists: %v", err)
	}
}

func openBackupTestDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(root, "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Migrate(context.Background(), db, true); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func writeApplicationRecord(t *testing.T, name, value string) {
	t.Helper()
	db, err := state.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS records (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value`, value); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func readApplicationRecord(t *testing.T, name string) string {
	t.Helper()
	db, err := state.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM records WHERE id=1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
