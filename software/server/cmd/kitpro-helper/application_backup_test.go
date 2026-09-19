package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type backupRuntime struct {
	running     map[string]bool
	events      []string
	failStarts  int
	beforeStart func() error
	beforeStop  func() error
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
	if runtime.beforeStart != nil {
		if err := runtime.beforeStart(); err != nil {
			return err
		}
	}
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
	if runtime.beforeStop != nil {
		if err := runtime.beforeStop(); err != nil {
			return err
		}
	}
	runtime.running[id] = false
	runtime.events = append(runtime.events, "stop:"+id)
	return nil
}

func TestRestorePersistsRunningIntentBeforeQuiescence(t *testing.T) {
	fixture := newDurableRestoreFixture(t)
	request, token := fixture.begin(t)
	checked := false
	fixture.runtime.beforeStop = func() error {
		var phase, running string
		if err := fixture.db.QueryRow(`SELECT phase,running_components_json FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&phase, &running); err != nil {
			return err
		}
		if phase != "swapping" || !strings.Contains(running, `"app"`) {
			return fmt.Errorf("restore intent phase=%q running=%q", phase, running)
		}
		checked = true
		return nil
	}
	if _, err := restoreApplicationBackupWithFence(context.Background(), fixture.db, request, token); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("runtime stop did not observe durable running intent")
	}
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
		t.Fatal("runtime did not return to the running state after restore")
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

func TestMultiContainerBackupLoadsAuthoritativeGenerationTopology(t *testing.T) {
	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	db := openBackupTestDB(t, root)
	defer db.Close()
	instance := "inst-paperless2"
	for _, relative := range []string{"broker/data", "web/data", "web/media", "web/consume", "web/export"} {
		if err := os.MkdirAll(filepath.Join(managedApplicationRoot, "paperless-ngx", instance, relative), 0750); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,topology_hash,data_path,exposure_mode,created_at,verified_at,committed_at,cleanup_state) VALUES(?,3,'seed','paperless-ngx','2.20.15','active','network','network-id','plan','topology','/srv/kitpro/apps/paperless-ngx/inst-paperless2/data','internal','now','now','now','clean')`, instance); err != nil {
		t.Fatal(err)
	}
	components := []struct {
		id, runtimeID, image, dependencies string
		ordinal                            int
	}{
		{"broker", "broker-runtime", "docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7", "[]", 0},
		{"web", "web-runtime", "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", `["broker"]`, 1},
	}
	for _, component := range components {
		if _, err := db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,dependencies_json,start_ordinal,created_at,verified_at) VALUES(?,3,?,?,?,?, 'image','config','running',?,?,'now','now')`, instance, component.id, component.id, component.runtimeID, component.image, component.dependencies, component.ordinal); err != nil {
			t.Fatal(err)
		}
	}
	request := protocol.Request{Version: 2, ID: "req", OperationID: "op-ffffffffffffffffffffffffffffffff", Operation: "CreateApplicationBackup", OperationRevision: 1, InstanceID: instance, ApplicationID: "paperless-ngx", ReleaseID: "2.20.15", RuntimeGeneration: 3}
	plan, err := loadApplicationBackupPlan(db, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.StartOrder, ",") != "broker,web" || len(plan.Components) != 2 {
		t.Fatalf("unexpected backup topology: %#v", plan)
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

type durableRestoreFixture struct {
	db          *sql.DB
	runtime     *backupRuntime
	coordinator helperops.Coordinator
	instance    string
	statePath   string
	backupID    string
}

func newDurableRestoreFixture(t *testing.T) durableRestoreFixture {
	t.Helper()
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
	t.Cleanup(func() { _ = db.Close() })
	instance := "inst-durable01"
	dataPath := filepath.Join(managedApplicationRoot, "busybox", instance, "data")
	if err := os.MkdirAll(dataPath, 0750); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataPath, "known-state.txt")
	if err := os.WriteFile(statePath, []byte("backup generation\n"), 0640); err != nil {
		t.Fatal(err)
	}
	image := "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0"
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, "fixture-runtime", "fixture", "network", image, dataPath, "now", 1, "busybox", "1.37.0", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-91919191919191919191919191919191", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "busybox", ReleaseID: "1.37.0", RuntimeGeneration: 1}
	if _, err := createApplicationBackup(context.Background(), db, backupRequest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("live generation\n"), 0640); err != nil {
		t.Fatal(err)
	}
	return durableRestoreFixture{db: db, runtime: runtime, coordinator: helperops.Coordinator{DB: db}, instance: instance, statePath: statePath, backupID: backupRequest.ID}
}

func (fixture durableRestoreFixture) begin(t *testing.T) (protocol.Request, int64) {
	t.Helper()
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RestoreApplicationBackup", OperationRevision: 1, InstanceID: fixture.instance, RuntimeGeneration: 1, BackupID: fixture.backupID}
	decision, err := fixture.coordinator.Begin(context.Background(), request, 0, fixture.instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	return request, decision.FencingToken
}

func TestRestoreJournalRecoversProcessInterruptionsByFilesystemIdentity(t *testing.T) {
	for _, test := range []struct {
		point, want string
	}{
		{"before_first_rename", "live generation\n"},
		{"after_original_to_rollback", "live generation\n"},
		{"after_staged_to_active", "backup generation\n"},
		{"after_all_swaps_before_runtime", "backup generation\n"},
		{"after_runtime_restart", "backup generation\n"},
		{"after_restore_commit", "backup generation\n"},
	} {
		t.Run(test.point, func(t *testing.T) {
			fixture := newDurableRestoreFixture(t)
			request, token := fixture.begin(t)
			oldHook := restoreFaultHook
			restoreFaultHook = func(point, _ string) error {
				if point == test.point {
					return errRestoreInterrupted
				}
				return nil
			}
			t.Cleanup(func() { restoreFaultHook = oldHook })
			if _, err := restoreApplicationBackupWithFence(context.Background(), fixture.db, request, token); !errors.Is(err, errRestoreInterrupted) {
				t.Fatalf("restore interruption err=%v", err)
			}
			if blocked, err := restoreBlocksInstallation(context.Background(), fixture.db, fixture.instance, operations.NewID()); err != nil || !blocked {
				t.Fatalf("unresolved restore did not block lifecycle mutation: blocked=%v err=%v", blocked, err)
			}
			if _, err := fixture.coordinator.RecoverInterrupted(context.Background()); err != nil {
				t.Fatal(err)
			}
			if recovered, err := recoverInterruptedRestores(context.Background(), fixture.db, fixture.coordinator); err != nil || recovered != 1 {
				var phase, detail string
				_ = fixture.db.QueryRow(`SELECT phase,error_detail FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&phase, &detail)
				t.Fatalf("recovered=%d err=%v phase=%q detail=%q", recovered, err, phase, detail)
			}
			contents, err := os.ReadFile(fixture.statePath)
			gotChecksum, wantChecksum := sha256.Sum256(contents), sha256.Sum256([]byte(test.want))
			if err != nil || gotChecksum != wantChecksum {
				t.Fatalf("persistent checksum=%x err=%v want=%x", gotChecksum, err, wantChecksum)
			}
			if !fixture.runtime.running["fixture-runtime"] {
				t.Fatal("recovery did not return runtime to its intended running state")
			}
			var leaseState, journalPhase string
			if err = fixture.db.QueryRow(`SELECT state FROM installation_leases WHERE installation_id=?`, fixture.instance).Scan(&leaseState); err != nil {
				t.Fatal(err)
			}
			if err = fixture.db.QueryRow(`SELECT phase FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&journalPhase); err != nil {
				t.Fatal(err)
			}
			if leaseState != "released" || (journalPhase != "completed" && journalPhase != "rolled_back") {
				t.Fatalf("lease=%q journal=%q", leaseState, journalPhase)
			}
		})
	}
}

func TestRestoreJournalRequiresManualActionForAmbiguousFilesystemIdentity(t *testing.T) {
	fixture := newDurableRestoreFixture(t)
	request, token := fixture.begin(t)
	oldHook := restoreFaultHook
	restoreFaultHook = func(point, _ string) error {
		if point == "after_original_to_rollback" {
			return errRestoreInterrupted
		}
		return nil
	}
	t.Cleanup(func() { restoreFaultHook = oldHook })
	if _, err := restoreApplicationBackupWithFence(context.Background(), fixture.db, request, token); !errors.Is(err, errRestoreInterrupted) {
		t.Fatalf("restore interruption err=%v", err)
	}
	var rollbackPath string
	if err := fixture.db.QueryRow(`SELECT rollback_path FROM restore_storage_steps WHERE operation_id=?`, request.OperationID).Scan(&rollbackPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rollbackPath, rollbackPath+".tampered-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rollbackPath, 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recovered, err := recoverInterruptedRestores(context.Background(), fixture.db, fixture.coordinator); err != nil || recovered != 0 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	var operationPhase, stepPhase, leaseState string
	if err := fixture.db.QueryRow(`SELECT phase FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&operationPhase); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT phase FROM restore_storage_steps WHERE operation_id=?`, request.OperationID).Scan(&stepPhase); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT state FROM installation_leases WHERE installation_id=?`, fixture.instance).Scan(&leaseState); err != nil {
		t.Fatal(err)
	}
	if operationPhase != "action_required" || stepPhase != "action_required" || leaseState != "recovery_required" {
		t.Fatalf("operation=%q step=%q lease=%q", operationPhase, stepPhase, leaseState)
	}
	if fixture.runtime.running["fixture-runtime"] {
		t.Fatal("ambiguous restore recovery restarted the runtime")
	}
}

func TestInterruptedRestoreResponseRetainsRecoveryFence(t *testing.T) {
	fixture := newDurableRestoreFixture(t)
	request, token := fixture.begin(t)
	oldHook := restoreFaultHook
	restoreFaultHook = func(point, _ string) error {
		if point == "after_original_to_rollback" {
			return errRestoreInterrupted
		}
		return nil
	}
	t.Cleanup(func() { restoreFaultHook = oldHook })
	var resultFrame bytes.Buffer
	executeMutation(&resultFrame, fixture.db, request, fixture.coordinator, token)
	response, err := protocol.ReadResponse(&resultFrame)
	if err != nil {
		t.Fatal(err)
	}
	if response.ErrorCode != "RecoveryRequired" {
		t.Fatalf("response=%#v", response)
	}
	if _, err = fixture.coordinator.Complete(context.Background(), request.OperationID, token, response); err != nil {
		t.Fatal(err)
	}
	var operationState, leaseState string
	if err = fixture.db.QueryRow(`SELECT state FROM helper_operations WHERE operation_id=?`, request.OperationID).Scan(&operationState); err != nil {
		t.Fatal(err)
	}
	if err = fixture.db.QueryRow(`SELECT state FROM installation_leases WHERE installation_id=?`, fixture.instance).Scan(&leaseState); err != nil {
		t.Fatal(err)
	}
	if operationState != helperops.StateActionRequired || leaseState != "recovery_required" {
		t.Fatalf("operation=%q lease=%q", operationState, leaseState)
	}
}

func TestRestoreCleanupFailureBecomesCleanupDebt(t *testing.T) {
	fixture := newDurableRestoreFixture(t)
	request, token := fixture.begin(t)
	oldHook := restoreFaultHook
	restoreFaultHook = func(point, _ string) error {
		if point == "during_cleanup" {
			return errors.New("injected cleanup failure")
		}
		return nil
	}
	t.Cleanup(func() { restoreFaultHook = oldHook })
	result, err := restoreApplicationBackupWithFence(context.Background(), fixture.db, request, token)
	if err != nil || result["cleanup_deferred"] != true {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	contents, err := os.ReadFile(fixture.statePath)
	if err != nil || string(contents) != "backup generation\n" || !fixture.runtime.running["fixture-runtime"] {
		t.Fatalf("state=%q running=%v err=%v", contents, fixture.runtime.running, err)
	}
	var phase, cleanupError string
	if err = fixture.db.QueryRow(`SELECT phase,cleanup_error FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&phase, &cleanupError); err != nil {
		t.Fatal(err)
	}
	if phase != "cleanup_pending" || cleanupError == "" {
		t.Fatalf("phase=%q cleanup_error=%q", phase, cleanupError)
	}
	restoreFaultHook = nil
	journal, err := loadRestoreJournal(context.Background(), fixture.db, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = finishRestoreCleanupOnly(context.Background(), fixture.db, journal); err != nil {
		t.Fatal(err)
	}
	var stepError string
	if err = fixture.db.QueryRow(`SELECT phase,cleanup_error FROM restore_operations WHERE operation_id=?`, request.OperationID).Scan(&phase, &cleanupError); err != nil {
		t.Fatal(err)
	}
	if err = fixture.db.QueryRow(`SELECT error_detail FROM restore_storage_steps WHERE operation_id=?`, request.OperationID).Scan(&stepError); err != nil {
		t.Fatal(err)
	}
	if phase != "completed" || cleanupError != "" || stepError != "" {
		t.Fatalf("phase=%q cleanup_error=%q step_error=%q", phase, cleanupError, stepError)
	}
}

func TestMultiStorageRestoreNeverStartsMixedGeneration(t *testing.T) {
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
	instance := "inst-papermixed"
	base := filepath.Join(managedApplicationRoot, "paperless-ngx", instance)
	for _, relative := range []string{"broker/data", "web/data", "web/media", "web/consume", "web/export"} {
		if err := os.MkdirAll(filepath.Join(base, relative), 0750); err != nil {
			t.Fatal(err)
		}
	}
	databasePath := filepath.Join(base, "web/data/db.sqlite3")
	mediaPath := filepath.Join(base, "web/media/document.pdf")
	writeApplicationRecord(t, databasePath, "backup database")
	if err := os.WriteFile(mediaPath, []byte("backup media"), 0640); err != nil {
		t.Fatal(err)
	}
	components := []struct{ id, runtimeID, image string }{
		{"broker", "broker-runtime", "docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7"},
		{"web", "web-runtime", "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f"},
	}
	for _, component := range components {
		if _, err := db.Exec(`INSERT INTO component_ownership(installation_id,component_id,container_id,container_name,network_name,image_digest,runtime_generation,created_at) VALUES(?,?,?,?,?,?,1,'now')`, instance, component.id, component.runtimeID, component.id, "network", component.image); err != nil {
			t.Fatal(err)
		}
	}
	backupRequest := protocol.Request{Version: 1, ID: "op-82828282828282828282828282828282", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "paperless-ngx", ReleaseID: "2.20.15", RuntimeGeneration: 1}
	if _, err := createApplicationBackup(context.Background(), db, backupRequest); err != nil {
		t.Fatal(err)
	}
	writeApplicationRecord(t, databasePath, "live database")
	if err := os.WriteFile(mediaPath, []byte("live media"), 0640); err != nil {
		t.Fatal(err)
	}
	liveDatabaseChecksum, err := fileSHA256(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	liveMediaChecksum, err := fileSHA256(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := helperops.Coordinator{DB: db}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RestoreApplicationBackup", OperationRevision: 1, InstanceID: instance, RuntimeGeneration: 1, BackupID: backupRequest.ID}
	decision, err := coordinator.Begin(context.Background(), request, 0, instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	oldHook := restoreFaultHook
	interrupted := false
	restoreFaultHook = func(point, _ string) error {
		if point == "between_storage_slots" && !interrupted {
			interrupted = true
			return errRestoreInterrupted
		}
		return nil
	}
	t.Cleanup(func() { restoreFaultHook = oldHook })
	if _, err = restoreApplicationBackupWithFence(context.Background(), db, request, decision.FencingToken); !errors.Is(err, errRestoreInterrupted) {
		t.Fatalf("restore interruption err=%v", err)
	}
	runtime.beforeStart = func() error {
		if got := readApplicationRecord(t, databasePath); got != "live database" {
			return fmt.Errorf("mixed database generation %q", got)
		}
		contents, readErr := os.ReadFile(mediaPath)
		if readErr != nil || string(contents) != "live media" {
			return fmt.Errorf("mixed media generation %q: %v", contents, readErr)
		}
		return nil
	}
	if _, err = coordinator.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recovered, recoverErr := recoverInterruptedRestores(context.Background(), db, coordinator); recoverErr != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, recoverErr)
	}
	if !runtime.running["broker-runtime"] || !runtime.running["web-runtime"] {
		t.Fatalf("runtime state=%v", runtime.running)
	}
	databaseChecksum, databaseErr := fileSHA256(databasePath)
	mediaChecksum, mediaErr := fileSHA256(mediaPath)
	if databaseErr != nil || databaseChecksum != liveDatabaseChecksum {
		t.Fatalf("database checksum mismatch: %s err=%v want=%s", databaseChecksum, databaseErr, liveDatabaseChecksum)
	}
	if mediaErr != nil || mediaChecksum != liveMediaChecksum {
		t.Fatalf("media checksum mismatch: %s err=%v want=%s", mediaChecksum, mediaErr, liveMediaChecksum)
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
