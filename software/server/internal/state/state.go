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
	target := 14
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
	if n < 6 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS hardware_assignments (installation_id TEXT NOT NULL, component_id TEXT NOT NULL DEFAULT '', device_class TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('device','cpu')), vendor TEXT NOT NULL DEFAULT '', stable_id TEXT NOT NULL DEFAULT '', resolved_devices TEXT NOT NULL DEFAULT '[]', runtime_generation INTEGER NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(installation_id,component_id,device_class))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=6"); err != nil {
			return err
		}
	}
	if n < 7 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS trusted_storage_roots (root_id TEXT PRIMARY KEY, display_name TEXT NOT NULL, canonical_path TEXT NOT NULL UNIQUE, allowed_mode TEXT NOT NULL CHECK(allowed_mode IN ('read-only','read-write')), device_major INTEGER NOT NULL, device_minor INTEGER NOT NULL, inode INTEGER NOT NULL, filesystem_type TEXT NOT NULL, mount_source TEXT NOT NULL, mount_point TEXT NOT NULL, network_backed INTEGER NOT NULL CHECK(network_backed IN (0,1)), created_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS external_storage_bindings (installation_id TEXT NOT NULL, component_id TEXT NOT NULL DEFAULT '', slot_id TEXT NOT NULL, root_id TEXT NOT NULL, access_mode TEXT NOT NULL CHECK(access_mode IN ('read-only','read-write')), container_path TEXT NOT NULL, runtime_generation INTEGER NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(installation_id,component_id,slot_id), FOREIGN KEY(root_id) REFERENCES trusted_storage_roots(root_id) ON DELETE RESTRICT)`)
		} else {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS installation_storage_selections (installation_id TEXT NOT NULL, component_id TEXT NOT NULL DEFAULT '', slot_id TEXT NOT NULL, root_id TEXT NOT NULL, PRIMARY KEY(installation_id,component_id,slot_id))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=7"); err != nil {
			return err
		}
	}
	if n < 8 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS application_backups (backup_id TEXT PRIMARY KEY, installation_id TEXT NOT NULL, application_id TEXT NOT NULL, release_id TEXT NOT NULL, runtime_generation INTEGER NOT NULL, archive_path TEXT NOT NULL UNIQUE, archive_sha256 TEXT NOT NULL, created_at TEXT NOT NULL, status TEXT NOT NULL CHECK(status = 'complete'))`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=8"); err != nil {
			return err
		}
	}
	if n < 9 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS helper_operations (
				operation_id TEXT PRIMARY KEY,
				request_hash TEXT NOT NULL,
				request_json TEXT NOT NULL,
				protocol_version INTEGER NOT NULL,
				operation_revision INTEGER NOT NULL,
				operation_kind TEXT NOT NULL,
				installation_id TEXT NOT NULL,
				caller_uid INTEGER NOT NULL,
				state TEXT NOT NULL CHECK(state IN ('accepted','executing','reconciling','succeeded','failed','action_required','cancelled','superseded')),
				current_phase TEXT NOT NULL,
				outcome_confidence TEXT NOT NULL CHECK(outcome_confidence IN ('not_dispatched','confirmed_absent','confirmed_applied','unknown','mixed')),
				recovery_disposition TEXT NOT NULL CHECK(recovery_disposition IN ('completed','retry_after_validation','reconcile_first','administrator_action','retry_forbidden')),
				expected_generation INTEGER,
				fencing_token INTEGER,
				accepted_at TEXT NOT NULL,
				started_at TEXT,
				updated_at TEXT NOT NULL,
				completed_at TEXT,
				result_json TEXT NOT NULL DEFAULT '',
				error_code TEXT NOT NULL DEFAULT '',
				error_detail TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX IF NOT EXISTS helper_operations_installation_state ON helper_operations(installation_id,state);
			CREATE INDEX IF NOT EXISTS helper_operations_state_updated ON helper_operations(state,updated_at);
			CREATE TABLE IF NOT EXISTS helper_operation_events (
				event_id INTEGER PRIMARY KEY AUTOINCREMENT,
				operation_id TEXT NOT NULL,
				installation_id TEXT NOT NULL,
				fencing_token INTEGER,
				event_kind TEXT NOT NULL,
				phase TEXT NOT NULL,
				facts_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				FOREIGN KEY(operation_id) REFERENCES helper_operations(operation_id) ON DELETE RESTRICT
			);
			CREATE INDEX IF NOT EXISTS helper_operation_events_operation ON helper_operation_events(operation_id,event_id);
			CREATE TABLE IF NOT EXISTS installation_leases (
				installation_id TEXT PRIMARY KEY,
				operation_id TEXT NOT NULL,
				fencing_token INTEGER NOT NULL,
				state TEXT NOT NULL CHECK(state IN ('held','recovery_required','released')),
				acquired_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				recovery_required INTEGER NOT NULL CHECK(recovery_required IN (0,1)),
				FOREIGN KEY(operation_id) REFERENCES helper_operations(operation_id) ON DELETE RESTRICT
			)`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=9"); err != nil {
			return err
		}
	}
	if n < 10 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS runtime_generations (
				installation_id TEXT NOT NULL,
				runtime_generation INTEGER NOT NULL,
				creating_operation_id TEXT NOT NULL,
				application_id TEXT NOT NULL,
				release_id TEXT NOT NULL,
				status TEXT NOT NULL CHECK(status IN ('verification_required','prepared','candidate','active','retained','failed','cleanup_pending','removed')),
				network_name TEXT NOT NULL,
				observed_network_id TEXT NOT NULL DEFAULT '',
				plan_hash TEXT NOT NULL DEFAULT '',
				data_path TEXT NOT NULL DEFAULT '',
				exposure_mode TEXT NOT NULL DEFAULT 'internal',
				service_id TEXT NOT NULL DEFAULT '',
				host_address TEXT NOT NULL DEFAULT '',
				host_port INTEGER NOT NULL DEFAULT 0,
				container_port INTEGER NOT NULL DEFAULT 0,
				service_protocol TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				verified_at TEXT,
				committed_at TEXT,
				retired_at TEXT,
				cleanup_state TEXT NOT NULL CHECK(cleanup_state IN ('not_required','clean','pending')),
				PRIMARY KEY(installation_id,runtime_generation)
			);
			CREATE UNIQUE INDEX IF NOT EXISTS runtime_generations_one_active ON runtime_generations(installation_id) WHERE status='active';
			CREATE INDEX IF NOT EXISTS runtime_generations_cleanup ON runtime_generations(cleanup_state,status);
			CREATE TABLE IF NOT EXISTS runtime_components (
				installation_id TEXT NOT NULL,
				runtime_generation INTEGER NOT NULL,
				component_id TEXT NOT NULL,
				container_name TEXT NOT NULL,
				observed_container_id TEXT NOT NULL DEFAULT '',
				image_digest TEXT NOT NULL,
				observed_image_id TEXT NOT NULL DEFAULT '',
				configuration_hash TEXT NOT NULL DEFAULT '',
				state TEXT NOT NULL CHECK(state IN ('unknown','created','stopped','running','missing','failed','removed')),
				created_at TEXT NOT NULL,
				started_at TEXT,
				verified_at TEXT,
				PRIMARY KEY(installation_id,runtime_generation,component_id),
				FOREIGN KEY(installation_id,runtime_generation) REFERENCES runtime_generations(installation_id,runtime_generation) ON DELETE RESTRICT
			);
			INSERT OR IGNORE INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state)
				SELECT instance_id,runtime_generation,'legacy-migration',application_id,release_id,'verification_required',network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,'not_required'
				FROM ownership WHERE runtime_generation > 0 AND container_id <> '';
			INSERT OR IGNORE INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,state,created_at)
				SELECT instance_id,runtime_generation,'app',container_name,container_id,image_digest,'unknown',created_at
				FROM ownership WHERE runtime_generation > 0 AND container_id <> '';
			INSERT OR IGNORE INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state)
				SELECT instance_id,runtime_generation,'legacy-migration',application_id,release_id,'removed',network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,'clean'
				FROM ownership WHERE runtime_generation > 0 AND container_id = '';
			INSERT OR IGNORE INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,state,created_at)
				SELECT instance_id,runtime_generation,'app',container_name,container_id,image_digest,'removed',created_at
				FROM ownership WHERE runtime_generation > 0 AND container_id = '';`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=10"); err != nil {
			return err
		}
	}
	if n < 11 {
		if helper {
			for _, statement := range []string{
				`ALTER TABLE runtime_generations ADD COLUMN topology_hash TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE runtime_components ADD COLUMN dependencies_json TEXT NOT NULL DEFAULT '[]'`,
				`ALTER TABLE runtime_components ADD COLUMN start_ordinal INTEGER NOT NULL DEFAULT 0`,
				`CREATE UNIQUE INDEX IF NOT EXISTS runtime_components_generation_ordinal ON runtime_components(installation_id,runtime_generation,start_ordinal)`,
				`CREATE TABLE IF NOT EXISTS lifecycle_component_steps (
					operation_id TEXT NOT NULL,
					installation_id TEXT NOT NULL,
					runtime_generation INTEGER NOT NULL,
					component_id TEXT NOT NULL,
					action TEXT NOT NULL,
					attempt INTEGER NOT NULL,
					expected_runtime_id TEXT NOT NULL DEFAULT '',
					observed_runtime_id TEXT NOT NULL DEFAULT '',
					intent_at TEXT NOT NULL,
					dispatched_at TEXT,
					verified_at TEXT,
					outcome_confidence TEXT NOT NULL CHECK(outcome_confidence IN ('not_dispatched','confirmed_absent','confirmed_applied','unknown')),
					error_detail TEXT NOT NULL DEFAULT '',
					PRIMARY KEY(operation_id,runtime_generation,component_id,action,attempt),
					FOREIGN KEY(operation_id) REFERENCES helper_operations(operation_id) ON DELETE RESTRICT,
					FOREIGN KEY(installation_id,runtime_generation,component_id) REFERENCES runtime_components(installation_id,runtime_generation,component_id) ON DELETE RESTRICT
				)`,
				`CREATE INDEX IF NOT EXISTS lifecycle_component_steps_operation ON lifecycle_component_steps(operation_id,runtime_generation,component_id,action,attempt)`,
			} {
				if _, err = tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=11"); err != nil {
			return err
		}
	}
	if n < 12 {
		if helper {
			if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS reconciliation (instance_id TEXT PRIMARY KEY, classification TEXT NOT NULL, observed_at TEXT NOT NULL, summary TEXT NOT NULL)`); err != nil {
				return err
			}
			for _, statement := range []string{
				`ALTER TABLE reconciliation ADD COLUMN checked_generation INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE reconciliation ADD COLUMN runtime_state TEXT NOT NULL DEFAULT 'unknown'`,
				`ALTER TABLE reconciliation ADD COLUMN runtime_identity TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE reconciliation ADD COLUMN mismatch_codes_json TEXT NOT NULL DEFAULT '[]'`,
				`ALTER TABLE reconciliation ADD COLUMN recommended_action TEXT NOT NULL DEFAULT 'none'`,
				`ALTER TABLE reconciliation ADD COLUMN originating_operation_id TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE reconciliation ADD COLUMN evidence_json TEXT NOT NULL DEFAULT '{}'`,
				`CREATE INDEX IF NOT EXISTS reconciliation_classification ON reconciliation(classification,observed_at)`,
			} {
				if _, err = tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		} else {
			for _, statement := range []string{
				`ALTER TABLE installations ADD COLUMN runtime_state TEXT NOT NULL DEFAULT 'unknown'`,
				`ALTER TABLE installations ADD COLUMN reconciliation_state TEXT NOT NULL DEFAULT 'runtime_unknown'`,
				`ALTER TABLE installations ADD COLUMN reconciliation_codes_json TEXT NOT NULL DEFAULT '[]'`,
				`ALTER TABLE installations ADD COLUMN reconciled_at TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE installations ADD COLUMN projection_repaired_at TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE installations ADD COLUMN projection_source_operation_id TEXT NOT NULL DEFAULT ''`,
			} {
				if _, err = tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=12"); err != nil {
			return err
		}
	}
	if n < 13 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS restore_operations (
				operation_id TEXT PRIMARY KEY,
				installation_id TEXT NOT NULL,
				backup_id TEXT NOT NULL,
				fencing_token INTEGER NOT NULL,
				phase TEXT NOT NULL CHECK(phase IN ('validated','staged','swapping','runtime_restore_pending','committed','cleanup_pending','completed','rolled_back','action_required')),
				running_components_json TEXT NOT NULL DEFAULT '[]',
				original_secrets_json TEXT NOT NULL DEFAULT '{}',
				restored_secrets_json TEXT NOT NULL DEFAULT '{}',
				started_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				completed_at TEXT,
				cleanup_error TEXT NOT NULL DEFAULT '',
				error_detail TEXT NOT NULL DEFAULT '',
				evidence_json TEXT NOT NULL DEFAULT '{}',
				FOREIGN KEY(operation_id) REFERENCES helper_operations(operation_id) ON DELETE RESTRICT
			);
			CREATE INDEX IF NOT EXISTS restore_operations_installation_phase ON restore_operations(installation_id,phase);
			CREATE TABLE IF NOT EXISTS restore_storage_steps (
				operation_id TEXT NOT NULL,
				storage_key TEXT NOT NULL,
				original_path TEXT NOT NULL,
				staged_path TEXT NOT NULL,
				rollback_path TEXT NOT NULL,
				original_device INTEGER NOT NULL,
				original_inode INTEGER NOT NULL,
				staged_device INTEGER NOT NULL,
				staged_inode INTEGER NOT NULL,
				phase TEXT NOT NULL CHECK(phase IN ('staged','original_to_rollback_prepared','original_to_rollback_dispatched','original_to_rollback_confirmed','staged_to_active_prepared','staged_to_active_dispatched','staged_to_active_confirmed','cleanup_pending','completed','rolled_back','action_required')),
				original_to_rollback_dispatched INTEGER NOT NULL DEFAULT 0 CHECK(original_to_rollback_dispatched IN (0,1)),
				original_to_rollback_confirmed INTEGER NOT NULL DEFAULT 0 CHECK(original_to_rollback_confirmed IN (0,1)),
				staged_to_active_dispatched INTEGER NOT NULL DEFAULT 0 CHECK(staged_to_active_dispatched IN (0,1)),
				staged_to_active_confirmed INTEGER NOT NULL DEFAULT 0 CHECK(staged_to_active_confirmed IN (0,1)),
				cleanup_dispatched INTEGER NOT NULL DEFAULT 0 CHECK(cleanup_dispatched IN (0,1)),
				cleanup_confirmed INTEGER NOT NULL DEFAULT 0 CHECK(cleanup_confirmed IN (0,1)),
				error_detail TEXT NOT NULL DEFAULT '',
				evidence_json TEXT NOT NULL DEFAULT '{}',
				updated_at TEXT NOT NULL,
				PRIMARY KEY(operation_id,storage_key),
				FOREIGN KEY(operation_id) REFERENCES restore_operations(operation_id) ON DELETE RESTRICT
			);
			CREATE INDEX IF NOT EXISTS restore_storage_steps_phase ON restore_storage_steps(operation_id,phase);`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=13"); err != nil {
			return err
		}
	}
	if n < 14 {
		if helper {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS runtime_generation_bindings (
				installation_id TEXT NOT NULL,
				runtime_generation INTEGER NOT NULL,
				service_id TEXT NOT NULL,
				transport TEXT NOT NULL CHECK(transport IN ('tcp','udp')),
				container_port INTEGER NOT NULL CHECK(container_port BETWEEN 1 AND 65535),
				mode TEXT NOT NULL CHECK(mode IN ('internal','loopback','lan')),
				host_address TEXT NOT NULL DEFAULT '',
				host_port INTEGER NOT NULL DEFAULT 0 CHECK(host_port BETWEEN 0 AND 65535),
				PRIMARY KEY(installation_id,runtime_generation,service_id),
				FOREIGN KEY(installation_id,runtime_generation) REFERENCES runtime_generations(installation_id,runtime_generation) ON DELETE RESTRICT
			);
			INSERT OR IGNORE INTO runtime_generation_bindings(installation_id,runtime_generation,service_id,transport,container_port,mode,host_address,host_port)
				SELECT installation_id,runtime_generation,service_id,CASE WHEN service_protocol='udp' THEN 'udp' ELSE 'tcp' END,container_port,exposure_mode,host_address,host_port
				FROM runtime_generations WHERE service_id<>'' AND container_port BETWEEN 1 AND 65535;`)
		} else {
			_, err = tx.ExecContext(ctx, `DROP INDEX IF EXISTS installation_service_exposure_binding_unique;
			ALTER TABLE installation_service_exposure RENAME TO installation_service_exposure_v13;
			CREATE TABLE installation_service_exposure (
				installation_id TEXT NOT NULL,
				service_id TEXT NOT NULL,
				transport TEXT NOT NULL DEFAULT 'tcp' CHECK(transport IN ('tcp','udp')),
				mode TEXT NOT NULL CHECK(mode IN ('internal','loopback','lan')),
				host_address TEXT NOT NULL DEFAULT '',
				host_port INTEGER NOT NULL DEFAULT 0 CHECK(host_port BETWEEN 0 AND 65535),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				PRIMARY KEY(installation_id,service_id)
			);
			INSERT INTO installation_service_exposure(installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at)
				SELECT installation_id,service_id,'tcp',mode,host_address,host_port,created_at,updated_at FROM installation_service_exposure_v13;
			DROP TABLE installation_service_exposure_v13;
			CREATE UNIQUE INDEX installation_service_exposure_binding_unique ON installation_service_exposure(host_address,host_port,transport) WHERE mode <> 'internal';`)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE schema_version SET version=14"); err != nil {
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
