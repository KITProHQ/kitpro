package appbackup

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCopyTreeAndSQLiteDiscovery(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(source, "state.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO parent VALUES(7); INSERT INTO child VALUES(7)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "upload.txt"), []byte("content"), 0640); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "copy")
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	size, err := TreeSize(destination)
	if err != nil || size <= int64(len("content")) {
		t.Fatalf("tree size: %d %v", size, err)
	}
	databases, err := DiscoverAndVerifySQLite(context.Background(), []ManagedSource{{Component: "app", StorageID: "data", Path: destination}})
	if err != nil || len(databases) != 1 || databases[0].RelativePath != "state.db" {
		t.Fatalf("SQLite discovery: %#v %v", databases, err)
	}
}

func TestSQLiteDiscoveryRecoversWALInPrivateWritableSnapshot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(source, "state.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE records(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO records(value) VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "staged")
	if err = CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	if err = filepath.Walk(destination, func(name string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return os.Chmod(name, 0555)
		}
		return os.Chmod(name, 0444)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(destination, func(name string, info os.FileInfo, walkErr error) error {
			if walkErr == nil && info.IsDir() {
				_ = os.Chmod(name, 0755)
			}
			return nil
		})
	})
	databases, err := DiscoverAndVerifySQLite(context.Background(), []ManagedSource{{Component: "app", StorageID: "data", Path: destination}})
	if err != nil || len(databases) != 1 || databases[0].RelativePath != "state.db" {
		t.Fatalf("WAL snapshot verification: %#v %v", databases, err)
	}
}

func TestRequireAvailableSpaceRejectsImpossibleRequest(t *testing.T) {
	if err := RequireAvailableSpace(t.TempDir(), math.MaxInt64); err == nil {
		t.Fatal("accepted impossible backup capacity request")
	}
}

func TestCopyTreeRejectsSymlinkAndSpecialFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, filepath.Join(root, "copy")); err == nil {
		t.Fatal("accepted symbolic link")
	}
}

func TestCopyTreePopulatesReadOnlyDirectoryBeforeApplyingMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "state"), []byte("preserved"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0555); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "copy")
	t.Cleanup(func() {
		_ = os.Chmod(nested, 0755)
		_ = os.Chmod(filepath.Join(destination, "nested"), 0755)
	})
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "nested", "state"))
	if err != nil || string(contents) != "preserved" {
		t.Fatalf("copied read-only tree: %q %v", contents, err)
	}
	info, err := os.Stat(filepath.Join(destination, "nested"))
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("nested mode: %v %v", info, err)
	}
}

func TestSQLiteForeignKeyFailureIsExplicit(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "bad.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=OFF; CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO child VALUES(9)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverAndVerifySQLite(context.Background(), []ManagedSource{{Component: "app", StorageID: "data", Path: root}}); err == nil {
		t.Fatal("accepted SQLite foreign-key violation")
	}
}
