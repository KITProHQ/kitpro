package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return nil, err
	}
	if _, err = db.ExecContext(context.Background(), "PRAGMA busy_timeout=250"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB, helper bool) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var n int
	err := db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&n)
	if err == sql.ErrNoRows {
		_, err = db.ExecContext(ctx, "INSERT INTO schema_version(version) VALUES (0)")
		n = 0
	}
	if err != nil {
		return err
	}
	target := 5
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if n > target {
		return fmt.Errorf("unsupported schema version %d", n)
	}
	if n < 1 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE ownership (instance_id TEXT PRIMARY KEY, container_id TEXT NOT NULL, container_name TEXT NOT NULL, network_name TEXT NOT NULL, image_digest TEXT NOT NULL, data_path TEXT NOT NULL, created_at TEXT NOT NULL); CREATE TABLE receipts (operation_id TEXT PRIMARY KEY, request_hash TEXT NOT NULL, operation_type TEXT NOT NULL, instance_id TEXT NOT NULL, phase TEXT NOT NULL, outcome TEXT NOT NULL, container_id TEXT, created_at TEXT NOT NULL); CREATE TABLE reconciliation (instance_id TEXT PRIMARY KEY, classification TEXT NOT NULL, observed_at TEXT NOT NULL, summary TEXT NOT NULL)`)
		} else {
			_, err = tx.ExecContext(ctx, `CREATE TABLE operations (id TEXT PRIMARY KEY, type TEXT NOT NULL, requested_at TEXT NOT NULL, status TEXT NOT NULL, instance_id TEXT NOT NULL, summary TEXT NOT NULL); CREATE TABLE IF NOT EXISTS installation_service_exposure (installation_id TEXT NOT NULL, service_id TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('internal','loopback','lan')), host_address TEXT NOT NULL DEFAULT '', host_port INTEGER NOT NULL DEFAULT 0 CHECK(host_port = 0 OR host_port BETWEEN 20000 AND 29999), created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(installation_id,service_id))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=1"); err != nil {
			return err
		}
	}
	if n < 2 {
		if helper {
			_, err = tx.ExecContext(ctx, `ALTER TABLE ownership ADD COLUMN runtime_generation INTEGER NOT NULL DEFAULT 1`)
		} else {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS installations (installation_id TEXT PRIMARY KEY, application_id TEXT NOT NULL, release_id TEXT NOT NULL, desired_state TEXT NOT NULL, runtime_generation INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=2"); err != nil {
			return err
		}
	}
	if n < 3 {
		if helper {
			for _, statement := range []string{
				`ALTER TABLE ownership ADD COLUMN application_id TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ownership ADD COLUMN release_id TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ownership ADD COLUMN exposure_mode TEXT NOT NULL DEFAULT 'internal'`,
				`ALTER TABLE ownership ADD COLUMN service_id TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ownership ADD COLUMN host_address TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ownership ADD COLUMN host_port INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE ownership ADD COLUMN container_port INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE ownership ADD COLUMN service_protocol TEXT NOT NULL DEFAULT ''`,
			} {
				if _, err = tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		} else {
			if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS installation_service_exposure_binding_unique ON installation_service_exposure(host_address,host_port) WHERE mode <> 'internal'`); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=3"); err != nil {
			return err
		}
	}
	if n < 4 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS component_ownership (installation_id TEXT NOT NULL, component_id TEXT NOT NULL, container_id TEXT NOT NULL, container_name TEXT NOT NULL, network_name TEXT NOT NULL, image_digest TEXT NOT NULL, runtime_generation INTEGER NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(installation_id,component_id))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=4"); err != nil {
			return err
		}
	}
	if n < 5 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS installation_secrets (installation_id TEXT NOT NULL, component_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, value TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(installation_id,component_id,name))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=5"); err != nil {
			return err
		}
	}
	if !helper {
		if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS installations (installation_id TEXT PRIMARY KEY, application_id TEXT NOT NULL, release_id TEXT NOT NULL, desired_state TEXT NOT NULL, runtime_generation INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS installation_service_exposure (installation_id TEXT NOT NULL, service_id TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('internal','loopback','lan')), host_address TEXT NOT NULL DEFAULT '', host_port INTEGER NOT NULL DEFAULT 0 CHECK(host_port = 0 OR host_port BETWEEN 20000 AND 29999), created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(installation_id,service_id))`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS installation_service_exposure_binding_unique ON installation_service_exposure(host_address,host_port) WHERE mode <> 'internal'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
