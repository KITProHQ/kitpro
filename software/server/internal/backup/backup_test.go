package backup

import (
	"context"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"path/filepath"
	"testing"
)

func TestVacuum(t *testing.T) {
	d := t.TempDir()
	db, e := state.Open(filepath.Join(d, "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = Vacuum(context.Background(), db, d, "b.db"); e != nil {
		t.Fatal(e)
	}
	if e = Verify(context.Background(), filepath.Join(d, "b.db")); e != nil {
		t.Fatal(e)
	}
	if _, e = Vacuum(context.Background(), db, d, "b.db"); e == nil {
		t.Fatal("backup overwrite accepted")
	}
}
