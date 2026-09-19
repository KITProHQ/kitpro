package backup

import (
	"context"
	"os"
	"path/filepath"
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
}
