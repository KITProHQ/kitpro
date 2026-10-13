package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/externalstorage"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

// TestDockerApplicationBackupRestore is an opt-in live parity test. It uses a
// real Docker container but keeps application and backup data in t.TempDir.
// Run it only on a disposable Docker host with the catalog-pinned SFTPGo image
// already present:
//
//	KITPRO_TEST_DOCKER=1 go test ./cmd/kitpro-helper -run TestDockerApplicationBackupRestore -v
func TestDockerApplicationBackupRestore(t *testing.T) {
	if os.Getenv("KITPRO_TEST_DOCKER") != "1" {
		t.Skip("set KITPRO_TEST_DOCKER=1 to use the local Docker daemon")
	}

	root := t.TempDir()
	oldRoot := managedApplicationRoot
	managedApplicationRoot = filepath.Join(root, "apps")
	t.Cleanup(func() { managedApplicationRoot = oldRoot })
	t.Setenv("KITPRO_APPLICATION_BACKUP_DIR", filepath.Join(root, "backups"))

	runtime := docker.New()
	oldRuntime := newContainerRuntime
	newContainerRuntime = func() containers.Runtime { return runtime }
	t.Cleanup(func() { newContainerRuntime = oldRuntime })

	instance := "inst-dockerbackup1"
	image := "ghcr.io/drakkan/sftpgo@sha256:d819bcea946470940416b63604f820aee965a02127b07126785e279fa311258e"
	name := fmt.Sprintf("kitpro-docker-backup-%d", time.Now().UnixNano())
	network := name + "-network"
	if _, err := runtime.CreateNetwork(network, map[string]string{"kitpro.test": "application-backup"}); err != nil {
		t.Fatal(err)
	}
	var containerID string
	t.Cleanup(func() {
		if containerID != "" {
			_ = runtime.Stop(containerID)
			_ = runtime.Remove(containerID)
		}
		_ = runtime.RemoveNetwork(network)
	})

	configPath := filepath.Join(managedApplicationRoot, "sftpgo", instance, "config")
	externalPath := filepath.Join(root, "imported-files")
	if err := os.MkdirAll(configPath, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(externalPath, 0750); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(configPath, "kitpro-acceptance.db")
	writeApplicationRecord(t, databasePath, "docker-before")
	externalMarker := filepath.Join(externalPath, "external.txt")
	if err := os.WriteFile(externalMarker, []byte("external-before\n"), 0640); err != nil {
		t.Fatal(err)
	}

	created, err := runtime.CreateContainerPlan(containers.ContainerPlan{
		Image:         image,
		Name:          name,
		Network:       network,
		User:          "1000:1000",
		RestartPolicy: "no",
		Storage: []containers.StorageMount{
			{HostPath: configPath, ContainerPath: "/var/lib/sftpgo"},
			{HostPath: externalPath, ContainerPath: "/srv/sftpgo/data"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	containerID = created
	if err := runtime.Start(containerID); err != nil {
		t.Fatal(err)
	}

	db := openBackupTestDB(t, root)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, instance, containerID, name, network, image, configPath, "now", 1, "sftpgo", "2.7.5", "internal", "", "", 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	allowStorageTestRoot(t, externalPath)
	registered, err := registerStorageRoot(db, protocol.Request{RootName: "Docker parity files", RootPath: externalPath, RootMode: externalstorage.ReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	declaration := []manifest.ExternalStorage{{ID: "files", ContainerPath: "/srv/sftpgo/data", Mode: externalstorage.ReadWrite, Required: true, Purpose: "Managed files"}}
	if err := persistExternalBindings(db, instance, "", 1, declaration, []protocol.ExternalStorageBinding{{SlotID: "files", RootID: registered["id"].(string)}}); err != nil {
		t.Fatal(err)
	}

	backupRequest := protocol.Request{Version: 1, ID: "op-12121212121212121212121212121212", Operation: "CreateApplicationBackup", InstanceID: instance, ApplicationID: "sftpgo", ReleaseID: "2.7.5", RuntimeGeneration: 1}
	result, err := createApplicationBackup(context.Background(), db, backupRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result["databases"].(int) < 1 {
		t.Fatalf("database inventory: %#v", result)
	}
	if err := runtime.Stop(containerID); err != nil {
		t.Fatal(err)
	}
	writeApplicationRecord(t, databasePath, "docker-mutated")
	if err := os.WriteFile(externalMarker, []byte("external-mutated\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(containerID); err != nil {
		t.Fatal(err)
	}

	restoreRequest := protocol.Request{Version: 1, ID: "op-34343434343434343434343434343434", Operation: "RestoreApplicationBackup", InstanceID: instance, BackupID: backupRequest.ID}
	if _, err := restoreApplicationBackup(context.Background(), db, restoreRequest); err != nil {
		t.Fatal(err)
	}
	if got := readApplicationRecord(t, databasePath); got != "docker-before" {
		t.Fatalf("restored Docker SQLite record = %q", got)
	}
	external, err := os.ReadFile(externalMarker)
	if err != nil || string(external) != "external-mutated\n" {
		t.Fatalf("imported storage was copied or lost: %q %v", external, err)
	}
	inspection, err := runtime.Inspect(containerID)
	if err != nil || !runtimeRunning(inspection) {
		t.Fatalf("Docker runtime not running after restore: %#v %v", inspection, err)
	}
}
