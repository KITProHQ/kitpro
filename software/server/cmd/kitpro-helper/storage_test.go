package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/externalstorage"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func storageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func allowStorageTestRoot(t *testing.T, root string) {
	t.Helper()
	oldInspect, oldValidate := inspectExternalStorage, validateExternalStorage
	parents := []string{filepath.Dir(root)}
	inspectExternalStorage = func(path string) (externalstorage.Identity, error) {
		return externalstorage.InspectWithin(path, parents)
	}
	validateExternalStorage = func(identity externalstorage.Identity, path string) (externalstorage.Identity, error) {
		return externalstorage.ValidateWithin(identity, path, parents)
	}
	t.Cleanup(func() {
		inspectExternalStorage, validateExternalStorage = oldInspect, oldValidate
	})
}

func TestTrustedRootBindingAndDeletionPolicy(t *testing.T) {
	db := storageTestDB(t)
	root := filepath.Join(t.TempDir(), "media")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	allowStorageTestRoot(t, root)
	registered, err := registerStorageRoot(db, protocol.Request{RootName: "Test media", RootPath: root, RootMode: externalstorage.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	id := registered["id"].(string)
	declaration := []manifest.ExternalStorage{{ID: "media", ContainerPath: "/media", Mode: externalstorage.ReadOnly, Required: true, Purpose: "Media library"}}
	mounts, err := resolveExternalMounts(db, "inst-testmedia01", "", 1, declaration, []protocol.ExternalStorageBinding{{SlotID: "media", RootID: id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || !mounts[0].ReadOnly || mounts[0].HostPath != root {
		t.Fatalf("unsafe mount: %#v", mounts)
	}
	if err = persistExternalBindings(db, "inst-testmedia01", "", 1, declaration, []protocol.ExternalStorageBinding{{SlotID: "media", RootID: id}}); err != nil {
		t.Fatal(err)
	}
	if err = removeStorageRoot(db, id); err == nil {
		t.Fatal("removed an in-use trusted root")
	}
}

func TestReadWriteRequiresRootPermission(t *testing.T) {
	db := storageTestDB(t)
	root := filepath.Join(t.TempDir(), "sync")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	allowStorageTestRoot(t, root)
	registered, err := registerStorageRoot(db, protocol.Request{RootName: "Read only", RootPath: root, RootMode: externalstorage.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	declaration := []manifest.ExternalStorage{{ID: "files", ContainerPath: "/files", Mode: externalstorage.ReadWrite, Required: true, Purpose: "Files"}}
	if _, err = resolveExternalMounts(db, "inst-testsync01", "", 1, declaration, []protocol.ExternalStorageBinding{{SlotID: "files", RootID: registered["id"].(string)}}); err == nil {
		t.Fatal("read-write escalation accepted")
	}
}

func TestReadWriteRootResolvesOnlyItsExactDirectory(t *testing.T) {
	db := storageTestDB(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "sync")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	allowStorageTestRoot(t, root)
	registered, err := registerStorageRoot(db, protocol.Request{RootName: "Writable files", RootPath: root, RootMode: externalstorage.ReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	declaration := []manifest.ExternalStorage{{ID: "files", ContainerPath: "/files", Mode: externalstorage.ReadWrite, Required: true, Purpose: "Files"}}
	mounts, err := resolveExternalMounts(db, "inst-testsync01", "", 1, declaration, []protocol.ExternalStorageBinding{{SlotID: "files", RootID: registered["id"].(string)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || mounts[0].HostPath != root || mounts[0].ContainerPath != "/files" || mounts[0].ReadOnly {
		t.Fatalf("read-write binding escaped its exact trusted root: %#v", mounts)
	}
}

func TestStorageWriterIsExclusiveButReadersMayShare(t *testing.T) {
	db := storageTestDB(t)
	root := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	allowStorageTestRoot(t, root)
	registered, err := registerStorageRoot(db, protocol.Request{RootName: "Shared", RootPath: root, RootMode: externalstorage.ReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	id := registered["id"].(string)
	read := []manifest.ExternalStorage{{ID: "media", ContainerPath: "/media", Mode: externalstorage.ReadOnly, Required: true, Purpose: "Media"}}
	write := []manifest.ExternalStorage{{ID: "files", ContainerPath: "/files", Mode: externalstorage.ReadWrite, Required: true, Purpose: "Files"}}
	selection := func(slot string) []protocol.ExternalStorageBinding {
		return []protocol.ExternalStorageBinding{{SlotID: slot, RootID: id}}
	}
	if err = persistExternalBindings(db, "inst-readerone1", "", 1, read, selection("media")); err != nil {
		t.Fatal(err)
	}
	if _, err = resolveExternalMounts(db, "inst-readertwo2", "", 1, read, selection("media")); err != nil {
		t.Fatalf("second reader rejected: %v", err)
	}
	if _, err = resolveExternalMounts(db, "inst-writerone1", "", 1, write, selection("files")); err == nil {
		t.Fatal("writer admitted beside reader")
	}
	if _, err = db.Exec(`DELETE FROM external_storage_bindings`); err != nil {
		t.Fatal(err)
	}
	if err = persistExternalBindings(db, "inst-writerone1", "", 1, write, selection("files")); err != nil {
		t.Fatal(err)
	}
	if _, err = resolveExternalMounts(db, "inst-readerone1", "", 1, read, selection("media")); err == nil {
		t.Fatal("reader admitted beside writer")
	}
	if _, err = resolveExternalMounts(db, "inst-writertwo2", "", 1, write, selection("files")); err == nil {
		t.Fatal("second writer admitted")
	}
}

func TestStorageReconciliationRejectsUnexpectedBind(t *testing.T) {
	db := storageTestDB(t)
	host := map[string]any{"Binds": []any{"/etc:/host:ro"}}
	if err := validateStorageObservation(db, "inst-testtools01", "", "it-tools", host); err == nil {
		t.Fatal("unexpected host bind was not classified as storage drift")
	}
}
