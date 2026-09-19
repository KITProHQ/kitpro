package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
