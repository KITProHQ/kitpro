package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	VerificationFull            = "passed"
	VerificationStructuralPages = "structural-pages-passed"
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
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := VerifyDatabase(ctx, db); err != nil {
		return "", fmt.Errorf("source database integrity check failed: %w", err)
	}
	temporary, err := os.CreateTemp(dir, "."+name+".partial-")
	if err != nil {
		return "", fmt.Errorf("create temporary backup: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("close temporary backup: %w", err)
	}
	if err = os.Remove(temporaryPath); err != nil {
		return "", fmt.Errorf("prepare temporary backup: %w", err)
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", temporaryPath); err != nil {
		return "", err
	}
	if err = os.Chmod(temporaryPath, 0600); err != nil {
		return "", fmt.Errorf("secure backup permissions: %w", err)
	}
	if err = Verify(ctx, temporaryPath); err != nil {
		return "", err
	}
	backupFile, err := os.Open(temporaryPath)
	if err != nil {
		return "", fmt.Errorf("open completed backup: %w", err)
	}
	if err = backupFile.Sync(); err != nil {
		_ = backupFile.Close()
		return "", fmt.Errorf("sync completed backup: %w", err)
	}
	if err = backupFile.Close(); err != nil {
		return "", fmt.Errorf("close completed backup: %w", err)
	}
	// A hard link promotes the verified file without overwriting an existing
	// backup. The temporary and final names are always in the same directory.
	if err = os.Link(temporaryPath, path); err != nil {
		return "", fmt.Errorf("promote completed backup: %w", err)
	}
	if err = os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("remove temporary backup name: %w", err)
	}
	backupDir, err := os.Open(dir)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("open backup directory: %w", err)
	}
	if err = backupDir.Sync(); err != nil {
		_ = backupDir.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync backup directory: %w", err)
	}
	if err = backupDir.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close backup directory: %w", err)
	}
	return path, nil
}

func Verify(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return err
	}
	defer db.Close()
	return VerifyDatabase(ctx, db)
}

// VerifyWritableSnapshot verifies a private SQLite snapshot with write access
// so SQLite can recover copied WAL state without mutating the archived staging
// tree. The caller must provide an existing, disposable database copy.
func VerifyWritableSnapshot(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return err
	}
	defer db.Close()
	return VerifyDatabase(ctx, db)
}

// VerifyApplicationSnapshot verifies a private, disposable copy of an
// application SQLite database. Full SQLite integrity and foreign-key checks
// remain the primary gate. Some applications use collations, tokenizers, or
// virtual-table modules that are available only in the application's SQLite
// build. When that exact limitation prevents the full check, KITPro still
// requires extension-independent page accounting and a successful foreign-key
// check, and reports the narrower result to the backup manifest.
func VerifyApplicationSnapshot(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return "", err
	}
	defer db.Close()

	if err = VerifyDatabase(ctx, db); err == nil {
		return VerificationFull, nil
	} else if !isUnavailableApplicationExtension(err) {
		return "", err
	}
	if err = verifyPageAccounting(ctx, db); err != nil {
		return "", fmt.Errorf("database structural page check failed: %w", err)
	}
	if err = verifyForeignKeys(ctx, db); err != nil {
		return "", fmt.Errorf("database foreign-key check failed: %w", err)
	}
	return VerificationStructuralPages, nil
}

func VerifyDatabase(ctx context.Context, db *sql.DB) error {
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return fmt.Errorf("database integrity check failed: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", check)
	}
	return verifyForeignKeys(ctx, db)
}

func verifyForeignKeys(ctx context.Context, db *sql.DB) error {
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

func isUnavailableApplicationExtension(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"no such collation sequence",
		"unknown tokenizer",
		"no such module",
		"no such function",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func verifyPageAccounting(ctx context.Context, db *sql.DB) error {
	var pageCount, freelistCount int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return fmt.Errorf("read page count: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelistCount); err != nil {
		return fmt.Errorf("read freelist count: %w", err)
	}
	var rows, distinctPages, minimumPage, maximumPage int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT pageno),
		       COALESCE(MIN(pageno), 0), COALESCE(MAX(pageno), 0)
		FROM dbstat
	`).Scan(&rows, &distinctPages, &minimumPage, &maximumPage); err != nil {
		return fmt.Errorf("walk database pages: %w", err)
	}
	if pageCount < 1 || freelistCount < 0 || freelistCount > pageCount {
		return fmt.Errorf("invalid page counts")
	}
	if rows != distinctPages || minimumPage != 1 || maximumPage > pageCount {
		return fmt.Errorf("database page map is inconsistent")
	}
	if distinctPages+freelistCount != pageCount {
		return fmt.Errorf("database has unaccounted pages")
	}
	return nil
}
