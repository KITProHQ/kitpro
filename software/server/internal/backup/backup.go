package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Vacuum(ctx context.Context, db *sql.DB, dir, name string) (string, error) {
	if filepath.Base(name) != name {
		return "", fmt.Errorf("invalid backup name")
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("backup already exists")
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("secure backup permissions: %w", err)
	}
	if err := Verify(ctx, path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func Verify(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", check)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("backup foreign-key check failed")
	}
	return rows.Err()
}
