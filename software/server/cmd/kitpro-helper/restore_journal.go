package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

var errRestoreInterrupted = errors.New("simulated restore process interruption")
var restoreFaultHook func(point, storageKey string) error

type restorePathIdentity struct {
	Device uint64
	Inode  uint64
}

type restoreStorageJournal struct {
	Key, OriginalPath, StagedPath, RollbackPath string
	Original, Staged                            restorePathIdentity
	Phase                                       string
}

type restoreJournal struct {
	OperationID, InstallationID, BackupID, Phase string
	FencingToken                                 int64
	Running                                      map[string]bool
	OriginalSecrets, RestoredSecrets             secretValues
	Storage                                      []restoreStorageJournal
}

func pathIdentity(name string) (restorePathIdentity, bool, error) {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return restorePathIdentity{}, false, nil
	}
	if err != nil {
		return restorePathIdentity{}, false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return restorePathIdentity{}, false, errors.New("filesystem identity unavailable")
	}
	return restorePathIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, true, nil
}

func exactPathIdentity(name string, want restorePathIdentity) (bool, error) {
	got, exists, err := pathIdentity(name)
	return exists && got == want, err
}

func validateRestoreJournalPath(name, suffix string) error {
	clean := filepath.Clean(name)
	root := filepath.Clean(managedApplicationRoot) + string(os.PathSeparator)
	if !strings.HasPrefix(clean, root) || (suffix != "" && !strings.Contains(filepath.Base(clean), suffix)) {
		return errors.New("restore journal path is outside exact managed storage scope")
	}
	return nil
}

func verifyRestoreFence(ctx context.Context, tx *sql.Tx, installation, operation string, token int64, allowRecovery bool) error {
	states := "'held'"
	if allowRecovery {
		states = "'held','recovery_required'"
	}
	var holder string
	var leaseToken, operationToken int64
	query := `SELECT l.operation_id,l.fencing_token,o.fencing_token FROM installation_leases l JOIN helper_operations o ON o.operation_id=l.operation_id WHERE l.installation_id=? AND l.state IN (` + states + `)`
	if err := tx.QueryRowContext(ctx, query, installation).Scan(&holder, &leaseToken, &operationToken); err != nil {
		return err
	}
	if holder != operation || leaseToken != token || operationToken != token {
		return errors.New("stale restore fencing token")
	}
	return nil
}

func beginRestoreJournal(ctx context.Context, db *sql.DB, request protocol.Request, token int64, plan applicationBackupPlan, restored secretValues, newPaths, rollbackPaths map[string]string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	originalSecrets, err := json.Marshal(plan.Secrets)
	if err != nil {
		return err
	}
	restoredSecrets, err := json.Marshal(restored)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyRestoreFence(ctx, tx, request.InstanceID, request.OperationID, token, false); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO restore_operations(operation_id,installation_id,backup_id,fencing_token,phase,original_secrets_json,restored_secrets_json,started_at,updated_at) VALUES(?,?,?,?, 'validated',?,?,?,?)`, request.OperationID, request.InstanceID, request.BackupID, token, string(originalSecrets), string(restoredSecrets), now, now); err != nil {
		return err
	}
	for _, storage := range plan.Storage {
		if storage.Manifest.Disposition != "include" {
			continue
		}
		key := storageKey(storage.Manifest.Component, storage.Manifest.ID)
		originalPath, stagedPath, rollbackPath := storage.HostPath, newPaths[key], rollbackPaths[key]
		if err = validateRestoreJournalPath(originalPath, ""); err != nil {
			return err
		}
		if err = validateRestoreJournalPath(stagedPath, ".kitpro-restore-new-"); err != nil {
			return err
		}
		if err = validateRestoreJournalPath(rollbackPath, ".kitpro-restore-rollback-"); err != nil {
			return err
		}
		originalIdentity, originalExists, identityErr := pathIdentity(originalPath)
		if identityErr != nil || !originalExists {
			return errors.New("original restore storage identity unavailable")
		}
		stagedIdentity, stagedExists, identityErr := pathIdentity(stagedPath)
		if identityErr != nil || !stagedExists {
			return errors.New("staged restore storage identity unavailable")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO restore_storage_steps(operation_id,storage_key,original_path,staged_path,rollback_path,original_device,original_inode,staged_device,staged_inode,phase,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'staged',?)`, request.OperationID, key, originalPath, stagedPath, rollbackPath, originalIdentity.Device, originalIdentity.Inode, stagedIdentity.Device, stagedIdentity.Inode, now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE restore_operations SET phase='staged',updated_at=? WHERE operation_id=?`, now, request.OperationID); err != nil {
		return err
	}
	return tx.Commit()
}

func updateRestoreRuntimeIntent(ctx context.Context, db *sql.DB, operationID string, token int64, running map[string]bool) error {
	ids := make([]string, 0, len(running))
	for id, wasRunning := range running {
		if wasRunning {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	encoded, _ := json.Marshal(ids)
	result, err := db.ExecContext(ctx, `UPDATE restore_operations SET running_components_json=?,phase='swapping',updated_at=? WHERE operation_id=? AND fencing_token=? AND phase='staged'`, string(encoded), time.Now().UTC().Format(time.RFC3339Nano), operationID, token)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("restore runtime intent transition lost")
	}
	return nil
}

func updateRestoreStep(ctx context.Context, db *sql.DB, operationID, key, phase string, fields string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := `UPDATE restore_storage_steps SET phase=?,updated_at=?`
	if fields != "" {
		query += `,` + fields
	}
	query += ` WHERE operation_id=? AND storage_key=?`
	result, err := db.ExecContext(ctx, query, phase, now, operationID, key)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("restore storage phase transition lost")
	}
	return nil
}

func updateRestoreOperation(ctx context.Context, db *sql.DB, operationID, phase, detail string) error {
	completed := any(nil)
	if phase == "completed" || phase == "rolled_back" {
		completed = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := db.ExecContext(ctx, `UPDATE restore_operations SET phase=?,error_detail=?,cleanup_error=CASE WHEN ?='completed' THEN '' ELSE cleanup_error END,updated_at=?,completed_at=COALESCE(?,completed_at) WHERE operation_id=?`, phase, boundedRestoreError(detail), phase, time.Now().UTC().Format(time.RFC3339Nano), completed, operationID)
	return err
}

func markRestoreStepsActionRequired(ctx context.Context, db *sql.DB, operationID, detail string) error {
	result, err := db.ExecContext(ctx, `UPDATE restore_storage_steps SET phase='action_required',error_detail=?,updated_at=? WHERE operation_id=? AND phase NOT IN ('completed','rolled_back')`, boundedRestoreError(detail), time.Now().UTC().Format(time.RFC3339Nano), operationID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return errors.New("restore recovery has no unresolved storage steps")
	}
	return nil
}

func boundedRestoreError(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > 512 {
		return detail[:512]
	}
	return detail
}

func loadRestoreJournal(ctx context.Context, db *sql.DB, operationID string) (restoreJournal, error) {
	var journal restoreJournal
	var runningJSON, originalJSON, restoredJSON string
	if err := db.QueryRowContext(ctx, `SELECT operation_id,installation_id,backup_id,fencing_token,phase,running_components_json,original_secrets_json,restored_secrets_json FROM restore_operations WHERE operation_id=?`, operationID).Scan(&journal.OperationID, &journal.InstallationID, &journal.BackupID, &journal.FencingToken, &journal.Phase, &runningJSON, &originalJSON, &restoredJSON); err != nil {
		return journal, err
	}
	journal.Running = map[string]bool{}
	var running []string
	if json.Unmarshal([]byte(runningJSON), &running) != nil || json.Unmarshal([]byte(originalJSON), &journal.OriginalSecrets) != nil || json.Unmarshal([]byte(restoredJSON), &journal.RestoredSecrets) != nil {
		return journal, errors.New("restore journal payload is invalid")
	}
	for _, component := range running {
		journal.Running[component] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT storage_key,original_path,staged_path,rollback_path,original_device,original_inode,staged_device,staged_inode,phase FROM restore_storage_steps WHERE operation_id=? ORDER BY storage_key`, operationID)
	if err != nil {
		return journal, err
	}
	defer rows.Close()
	for rows.Next() {
		var item restoreStorageJournal
		if err = rows.Scan(&item.Key, &item.OriginalPath, &item.StagedPath, &item.RollbackPath, &item.Original.Device, &item.Original.Inode, &item.Staged.Device, &item.Staged.Inode, &item.Phase); err != nil {
			return journal, err
		}
		if validateRestoreJournalPath(item.OriginalPath, "") != nil || validateRestoreJournalPath(item.StagedPath, ".kitpro-restore-new-") != nil || validateRestoreJournalPath(item.RollbackPath, ".kitpro-restore-rollback-") != nil {
			return journal, errors.New("restore journal contains an invalid path")
		}
		journal.Storage = append(journal.Storage, item)
	}
	return journal, rows.Err()
}

func restoreBlocksInstallation(ctx context.Context, db *sql.DB, installation, operationID string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM restore_operations WHERE installation_id=? AND operation_id<>? AND phase IN ('validated','staged','swapping','runtime_restore_pending','committed','action_required')`, installation, operationID).Scan(&count)
	return count != 0, err
}

func restoreOperationNeedsRecovery(ctx context.Context, db *sql.DB, operationID string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM restore_operations WHERE operation_id=? AND phase IN ('validated','staged','swapping','runtime_restore_pending','committed','action_required')`, operationID).Scan(&count)
	return count != 0, err
}

func classifyRestorePaths(item restoreStorageJournal) (active string, originalAvailable bool, err error) {
	originalIsOriginal, err := exactPathIdentity(item.OriginalPath, item.Original)
	if err != nil {
		return "", false, err
	}
	originalIsStaged, err := exactPathIdentity(item.OriginalPath, item.Staged)
	if err != nil {
		return "", false, err
	}
	rollbackIsOriginal, err := exactPathIdentity(item.RollbackPath, item.Original)
	if err != nil {
		return "", false, err
	}
	stagedIsStaged, err := exactPathIdentity(item.StagedPath, item.Staged)
	if err != nil {
		return "", false, err
	}
	for _, path := range []struct {
		name  string
		valid bool
	}{{item.OriginalPath, originalIsOriginal || originalIsStaged}, {item.RollbackPath, rollbackIsOriginal}, {item.StagedPath, stagedIsStaged}} {
		_, exists, statErr := pathIdentity(path.name)
		if statErr != nil {
			return "", false, statErr
		}
		if exists && !path.valid {
			return "", false, errors.New("restore path identity is ambiguous")
		}
	}
	switch {
	case originalIsStaged:
		active = "restored"
	case originalIsOriginal:
		active = "original"
	case !originalIsOriginal && !originalIsStaged:
		active = "missing"
	}
	return active, originalIsOriginal || rollbackIsOriginal, nil
}

func recoverInterruptedRestores(ctx context.Context, db *sql.DB, coordinator helperops.Coordinator) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT operation_id FROM restore_operations WHERE phase IN ('validated','staged','swapping','runtime_restore_pending','committed','action_required') ORDER BY started_at`)
	if err != nil {
		return 0, err
	}
	var operations []string
	for rows.Next() {
		var operation string
		if err = rows.Scan(&operation); err != nil {
			rows.Close()
			return 0, err
		}
		operations = append(operations, operation)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	recovered := 0
	for _, operation := range operations {
		journal, loadErr := loadRestoreJournal(ctx, db, operation)
		if loadErr != nil {
			return recovered, loadErr
		}
		var token int64
		if err = db.QueryRowContext(ctx, `SELECT fencing_token FROM installation_leases WHERE installation_id=? AND operation_id=? AND state='recovery_required'`, journal.InstallationID, operation).Scan(&token); err != nil {
			_ = updateRestoreOperation(ctx, db, operation, "action_required", "restore recovery lease unavailable")
			continue
		}
		if _, err = db.ExecContext(ctx, `UPDATE restore_operations SET fencing_token=?,updated_at=? WHERE operation_id=?`, token, time.Now().UTC().Format(time.RFC3339Nano), operation); err != nil {
			return recovered, err
		}
		journal.FencingToken = token
		resolved := false
		allRestored, canRollback := len(journal.Storage) > 0, true
		for _, item := range journal.Storage {
			active, originalAvailable, classifyErr := classifyRestorePaths(item)
			if classifyErr != nil {
				allRestored, canRollback = false, false
				break
			}
			if active == "missing" {
				allRestored = false
				canRollback = canRollback && originalAvailable
				continue
			}
			allRestored = allRestored && active == "restored"
			canRollback = canRollback && originalAvailable
		}
		if allRestored {
			if err = finishRestoreForward(ctx, db, journal); err == nil {
				_, err = coordinator.CompleteRecovery(ctx, operation, token, protocol.Response{OK: true, Result: map[string]any{"backup_id": journal.BackupID, "status": "restored"}})
				resolved = err == nil
			}
		} else if canRollback {
			err = finishRestoreRollback(ctx, db, journal, errors.New("restore interrupted; original data restored"))
			if err == nil {
				_, err = coordinator.CompleteRecovery(ctx, operation, token, protocol.Response{ErrorCode: "RestoreInterrupted", Error: "restore interrupted; original data restored"})
				resolved = err == nil
			}
		} else {
			detail := "restore storage generations are mixed or ambiguous; runtime was not started"
			if stepErr := markRestoreStepsActionRequired(ctx, db, operation, detail); stepErr != nil {
				err = stepErr
			} else {
				err = updateRestoreOperation(ctx, db, operation, "action_required", detail)
			}
		}
		if err != nil {
			_ = updateRestoreOperation(ctx, db, operation, "action_required", err.Error())
			continue
		}
		if resolved {
			recovered++
		}
	}
	return recovered, nil
}

func finishRestoreRollback(ctx context.Context, db *sql.DB, journal restoreJournal, cause error) error {
	quiesced := journal.Phase != "validated" && journal.Phase != "staged"
	var err error
	for index := len(journal.Storage) - 1; index >= 0; index-- {
		item := journal.Storage[index]
		active, originalAvailable, classifyErr := classifyRestorePaths(item)
		if classifyErr != nil || !originalAvailable {
			return errors.New("restore rollback identity became ambiguous")
		}
		if active == "restored" {
			failedPath := item.OriginalPath + ".kitpro-restore-failed-" + journal.OperationID
			_ = removeBackupTree(failedPath)
			if err = os.Rename(item.OriginalPath, failedPath); err != nil {
				return err
			}
		}
		rollbackExact, identityErr := exactPathIdentity(item.RollbackPath, item.Original)
		if identityErr != nil {
			return identityErr
		}
		if rollbackExact {
			if err = os.Rename(item.RollbackPath, item.OriginalPath); err != nil {
				return err
			}
		}
		_ = removeBackupTree(item.StagedPath)
		_ = updateRestoreStep(ctx, db, journal.OperationID, item.Key, "rolled_back", "")
	}
	_, plan, err := restoreRecoveryPlan(db, journal)
	if err != nil {
		return err
	}
	runtime := newContainerRuntime()
	if err = replaceSecrets(ctx, db, journal.InstallationID, journal.OriginalSecrets); err != nil {
		return err
	}
	if quiesced {
		err = resumeBackupRuntime(runtime, plan, journal.Running)
	}
	if err != nil {
		return err
	}
	return updateRestoreOperation(ctx, db, journal.OperationID, "rolled_back", cause.Error())
}

func finishRestoreForward(ctx context.Context, db *sql.DB, journal restoreJournal) error {
	var err error
	if journal.Phase != "committed" {
		_, plan, planErr := restoreRecoveryPlan(db, journal)
		if planErr != nil {
			return planErr
		}
		if err = updateRestoreOperation(ctx, db, journal.OperationID, "runtime_restore_pending", ""); err != nil {
			return err
		}
		if err = replaceSecrets(ctx, db, journal.InstallationID, journal.RestoredSecrets); err != nil {
			return err
		}
		if err = resumeBackupRuntime(newContainerRuntime(), plan, journal.Running); err != nil {
			return err
		}
		if err = updateRestoreOperation(ctx, db, journal.OperationID, "committed", ""); err != nil {
			return err
		}
	}
	cleanupDeferred := false
	for _, item := range journal.Storage {
		_ = updateRestoreStep(ctx, db, journal.OperationID, item.Key, "cleanup_pending", "cleanup_dispatched=1")
		if cleanupErr := removeBackupTree(item.RollbackPath); cleanupErr != nil {
			cleanupDeferred = true
			_, _ = db.ExecContext(ctx, `UPDATE restore_storage_steps SET error_detail=?,updated_at=? WHERE operation_id=? AND storage_key=?`, boundedRestoreError(cleanupErr.Error()), time.Now().UTC().Format(time.RFC3339Nano), journal.OperationID, item.Key)
			continue
		}
		_ = updateRestoreStep(ctx, db, journal.OperationID, item.Key, "completed", "cleanup_confirmed=1")
	}
	if cleanupDeferred {
		_, err = db.ExecContext(ctx, `UPDATE restore_operations SET phase='cleanup_pending',cleanup_error='rollback cleanup deferred',updated_at=? WHERE operation_id=?`, time.Now().UTC().Format(time.RFC3339Nano), journal.OperationID)
		return err
	}
	return updateRestoreOperation(ctx, db, journal.OperationID, "completed", "")
}

func restoreRecoveryPlan(db *sql.DB, journal restoreJournal) (protocol.Request, applicationBackupPlan, error) {
	request := protocol.Request{Version: 2, ID: "recovery", OperationID: journal.OperationID, Operation: "RestoreApplicationBackup", OperationRevision: 1, InstanceID: journal.InstallationID, BackupID: journal.BackupID}
	var applicationID, releaseID string
	var generation int
	if err := db.QueryRow(`SELECT application_id,release_id,runtime_generation FROM application_backups WHERE backup_id=? AND installation_id=? AND status='complete'`, journal.BackupID, journal.InstallationID).Scan(&applicationID, &releaseID, &generation); err != nil {
		return request, applicationBackupPlan{}, err
	}
	request.ApplicationID, request.ReleaseID, request.RuntimeGeneration = applicationID, releaseID, generation
	plan, err := loadApplicationBackupPlan(db, request)
	return request, plan, err
}

func runRestoreFault(point, key string) error {
	if restoreFaultHook == nil {
		return nil
	}
	return restoreFaultHook(point, key)
}

func cleanupCommittedRestores(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `SELECT operation_id FROM restore_operations WHERE phase IN ('committed','cleanup_pending') ORDER BY updated_at`)
	if err != nil {
		return
	}
	var operations []string
	for rows.Next() {
		var operation string
		if rows.Scan(&operation) == nil {
			operations = append(operations, operation)
		}
	}
	_ = rows.Close()
	for _, operation := range operations {
		journal, loadErr := loadRestoreJournal(ctx, db, operation)
		if loadErr == nil {
			_ = finishRestoreCleanupOnly(ctx, db, journal)
		}
	}
}

func finishRestoreCleanupOnly(ctx context.Context, db *sql.DB, journal restoreJournal) error {
	deferred := false
	for _, item := range journal.Storage {
		_ = updateRestoreStep(ctx, db, journal.OperationID, item.Key, "cleanup_pending", "cleanup_dispatched=1")
		if err := removeBackupTree(item.RollbackPath); err != nil {
			deferred = true
			_, _ = db.ExecContext(ctx, `UPDATE restore_storage_steps SET error_detail=?,updated_at=? WHERE operation_id=? AND storage_key=?`, boundedRestoreError(err.Error()), time.Now().UTC().Format(time.RFC3339Nano), journal.OperationID, item.Key)
			continue
		}
		_ = updateRestoreStep(ctx, db, journal.OperationID, item.Key, "completed", "cleanup_dispatched=1,cleanup_confirmed=1,error_detail=''")
	}
	if deferred {
		_, _ = db.ExecContext(ctx, `UPDATE restore_operations SET phase='cleanup_pending',cleanup_error='rollback cleanup deferred',updated_at=? WHERE operation_id=?`, time.Now().UTC().Format(time.RFC3339Nano), journal.OperationID)
		return errors.New("restore cleanup remains pending")
	}
	return updateRestoreOperation(ctx, db, journal.OperationID, "completed", "")
}
