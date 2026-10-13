package appbackup

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
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
	if databases[0].IntegrityCheck != "passed" {
		t.Fatalf("integrity check = %q", databases[0].IntegrityCheck)
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
	if databases[0].IntegrityCheck != "passed" {
		t.Fatalf("integrity check = %q", databases[0].IntegrityCheck)
	}
}

func TestSQLiteDiscoveryUsesStructuralCheckForUnavailableApplicationExtension(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "vendor.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		CREATE TABLE records(value TEXT COLLATE BINARY);
		CREATE INDEX records_value ON records(value);
		INSERT INTO records VALUES ('alpha'), ('beta');
		PRAGMA writable_schema=ON;
		UPDATE sqlite_schema
		SET sql=replace(sql, 'COLLATE BINARY', 'COLLATE icu_root')
		WHERE name='records';
		PRAGMA writable_schema=OFF;
	`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	databases, err := DiscoverAndVerifySQLite(context.Background(), []ManagedSource{{Component: "app", StorageID: "config", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(databases) != 1 || databases[0].IntegrityCheck != "structural-pages-passed" {
		t.Fatalf("SQLite extension fallback: %#v", databases)
	}
}

func TestSQLiteDiscoveryStillRejectsCorruptDatabase(t *testing.T) {
	root := t.TempDir()
	contents := append([]byte("SQLite format 3\x00"), make([]byte, 4096-16)...)
	if err := os.WriteFile(filepath.Join(root, "corrupt.db"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := DiscoverAndVerifySQLite(context.Background(), []ManagedSource{{Component: "app", StorageID: "data", Path: root}})
	if err == nil || !strings.Contains(err.Error(), "SQLite verification failed") {
		t.Fatalf("corrupt database error = %v", err)
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

func TestCopyTreeNormalizesInternalRelativeFileSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "links"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(source, "payload")
	if err := os.WriteFile(target, []byte("preserved"), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source, "links", "payload-link")
	if err := os.Symlink("../payload", link); err != nil {
		t.Fatal(err)
	}
	size, err := TreeSize(source)
	if err != nil || size != int64(2*len("preserved")) {
		t.Fatalf("tree size = %d, %v", size, err)
	}
	destination := filepath.Join(root, "copy")
	if err = CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(destination, "links", "payload-link")
	info, err := os.Lstat(copyPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("normalized link = %#v, %v", info, err)
	}
	contents, err := os.ReadFile(copyPath)
	if err != nil || string(contents) != "preserved" {
		t.Fatalf("normalized contents = %q, %v", contents, err)
	}
}

func TestCopyTreeRejectsEscapingRelativeSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, filepath.Join(root, "copy")); err == nil || !strings.Contains(err.Error(), "escapes source") {
		t.Fatalf("escaping symlink error = %v", err)
	}
}

func TestCopyTreeRejectsInternalDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(source, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, filepath.Join(root, "copy")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory symlink error = %v", err)
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
