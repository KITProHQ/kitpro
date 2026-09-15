package maintenance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func TestPrepareUpgradeCreatesValidatedBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	result, err := PrepareUpgrade(context.Background(), db, filepath.Join(dir, "backups"), "control", "0.1.0~alpha1", time.Unix(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 7 {
		t.Fatalf("schema version = %d", result.SchemaVersion)
	}
	if err = backup.Verify(context.Background(), result.BackupPath); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareUpgradeRejectsUnsafeVersion(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareUpgrade(context.Background(), db, filepath.Join(dir, "backups"), "control", "../../bad", time.Now()); err == nil {
		t.Fatal("unsafe version accepted")
	}
}
