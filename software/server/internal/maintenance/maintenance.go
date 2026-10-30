package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/backup"
)

var safeVersion = regexp.MustCompile(`^[0-9A-Za-z.+:~_-]{1,64}$`)

type Result struct {
	BackupPath    string
	SchemaVersion int
}

func PrepareUpgrade(ctx context.Context, db *sql.DB, backupDir, component, targetVersion string, now time.Time) (Result, error) {
	if !safeVersion.MatchString(component) || !safeVersion.MatchString(targetVersion) {
		return Result{}, fmt.Errorf("invalid upgrade metadata")
	}
	var schema int
	if err := db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&schema); err != nil {
		return Result{}, fmt.Errorf("read schema version: %w", err)
	}
	name := fmt.Sprintf("pre-upgrade-%s-to-%s-%s.db", component, targetVersion, now.UTC().Format("20060102T150405.000000000Z"))
	path, err := backup.Vacuum(ctx, db, backupDir, name)
	if err != nil {
		return Result{}, fmt.Errorf("create upgrade backup: %w", err)
	}
	return Result{BackupPath: path, SchemaVersion: schema}, nil
}
