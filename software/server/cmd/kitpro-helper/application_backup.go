package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/appbackup"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/multicontainer"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

var backupIDPattern = regexp.MustCompile(`^op-[a-f0-9]{32}$`)
var managedApplicationRoot = "/srv/kitpro/apps"
var requireApplicationBackupSpace = appbackup.RequireAvailableSpace

type managedBackupStorage struct {
	Manifest appbackup.Storage
	HostPath string
}

type ownedBackupComponent struct {
	Manifest appbackup.Component
	Runtime  string
	Running  bool
}

type applicationBackupPlan struct {
	Application manifest.Manifest
	ReleaseID   string
	Generation  int
	Components  []ownedBackupComponent
	Storage     []managedBackupStorage
	Imported    []appbackup.ImportedStorage
	Secrets     secretValues
	StartOrder  []string
}

type secretValue struct {
	Component string `json:"component"`
	Name      string `json:"name"`
	Value     string `json:"value"`
}

type secretValues struct {
	Version int           `json:"version"`
	Values  []secretValue `json:"values"`
}

type installationMetadata struct {
	Version           int      `json:"version"`
	RuntimeGeneration int      `json:"runtime_generation"`
	RunningComponents []string `json:"running_components"`
}

func createApplicationBackup(ctx context.Context, db *sql.DB, request protocol.Request) (result map[string]any, retErr error) {
	plan, err := loadApplicationBackupPlan(db, request)
	if err != nil {
		return nil, err
	}
	backupDir := env("KITPRO_APPLICATION_BACKUP_DIR", "/var/lib/kitpro-helper/application-backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, fmt.Errorf("prepare application backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0700); err != nil {
		return nil, fmt.Errorf("secure application backup directory: %w", err)
	}

	var sourceBytes int64
	for _, storage := range plan.Storage {
		if storage.Manifest.Disposition != "include" {
			continue
		}
		size, err := appbackup.TreeSize(storage.HostPath)
		if err != nil {
			return nil, fmt.Errorf("inspect managed storage: %w", err)
		}
		if size < 0 || sourceBytes > math.MaxInt64-size {
			return nil, fmt.Errorf("managed storage is too large")
		}
		sourceBytes += size
	}
	const workspaceOverhead = int64(64 << 20)
	if sourceBytes > (math.MaxInt64-workspaceOverhead)/2 {
		return nil, fmt.Errorf("managed storage is too large")
	}
	required := sourceBytes*2 + workspaceOverhead
	if err := requireApplicationBackupSpace(backupDir, required); err != nil {
		return nil, err
	}
	workspace, err := os.MkdirTemp(backupDir, ".backup-work-")
	if err != nil {
		return nil, fmt.Errorf("create backup workspace: %w", err)
	}
	defer removeBackupTree(workspace)

	runtime := newContainerRuntime()
	stopForBackup := plan.Application.Backup.Strategy != "metadata-only"
	running, err := snapshotBackupRuntime(runtime, &plan, stopForBackup)
	if err != nil {
		return nil, err
	}
	resumeNeeded := stopForBackup
	defer func() {
		if resumeNeeded {
			if resumeErr := resumeBackupRuntime(runtime, plan, running); resumeErr != nil && retErr == nil {
				retErr = fmt.Errorf("resume application after backup: %w", resumeErr)
				result = nil
			}
		}
	}()

	staged := make([]appbackup.ManagedSource, 0)
	archiveSources := make([]appbackup.Source, 0)
	for _, storage := range plan.Storage {
		if storage.Manifest.Disposition != "include" {
			continue
		}
		destination := filepath.Join(workspace, "application", storage.Manifest.Component, storage.Manifest.ID)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return nil, err
		}
		if err := appbackup.CopyTree(storage.HostPath, destination); err != nil {
			return nil, fmt.Errorf("stage managed storage: %w", err)
		}
		staged = append(staged, appbackup.ManagedSource{Component: storage.Manifest.Component, StorageID: storage.Manifest.ID, Path: destination, OwnerUID: storage.Manifest.OwnerUID, OwnerGID: storage.Manifest.OwnerGID})
		archiveSources = append(archiveSources, appbackup.Source{ArchivePath: storage.Manifest.ArchivePath, HostPath: destination})
	}
	if resumeNeeded {
		if err := resumeBackupRuntime(runtime, plan, running); err != nil {
			resumeNeeded = false
			return nil, fmt.Errorf("resume application after backup: %w", err)
		}
		resumeNeeded = false
	}

	var databases []appbackup.Database
	if plan.Application.Backup.Strategy == "cold-sqlite-filesystem" {
		databases, err = appbackup.DiscoverAndVerifySQLite(ctx, staged)
		if err != nil {
			return nil, err
		}
	}
	secretBytes, err := json.Marshal(plan.Secrets)
	if err != nil {
		return nil, err
	}
	secretBytes = append(secretBytes, '\n')
	runningIDs := make([]string, 0, len(running))
	for component, wasRunning := range running {
		if wasRunning {
			runningIDs = append(runningIDs, component)
		}
	}
	sort.Strings(runningIDs)
	metadataBytes, _ := json.Marshal(installationMetadata{Version: 1, RuntimeGeneration: plan.Generation, RunningComponents: runningIDs})
	metadataBytes = append(metadataBytes, '\n')

	backupManifest := appbackup.Manifest{
		Format:            appbackup.FormatName,
		FormatVersion:     appbackup.FormatVersion,
		KITProVersion:     buildinfo.Version,
		CreatedAt:         time.Now().UTC(),
		ApplicationID:     plan.Application.ID,
		InstallationID:    request.InstanceID,
		ReleaseID:         plan.ReleaseID,
		RuntimeGeneration: plan.Generation,
		Strategy:          plan.Application.Backup.Strategy,
		ImportedStorage:   plan.Imported,
		Databases:         databases,
	}
	for _, component := range plan.Components {
		backupManifest.Components = append(backupManifest.Components, component.Manifest)
	}
	for _, storage := range plan.Storage {
		backupManifest.Storage = append(backupManifest.Storage, storage.Manifest)
	}
	for _, secret := range plan.Secrets.Values {
		backupManifest.Secrets = append(backupManifest.Secrets, appbackup.Secret{Component: secret.Component, Name: secret.Name, Included: true})
	}

	stamp := backupManifest.CreatedAt.Format("20060102T150405Z")
	operationID := request.SemanticOperationID()
	filename := plan.Application.ID + "--" + request.InstanceID + "--" + stamp + "--" + operationID + ".kitpro-backup.tar.gz"
	archivePath := filepath.Join(backupDir, filename)
	if _, err := appbackup.Create(archivePath, backupManifest, archiveSources, []appbackup.InlineFile{
		{ArchivePath: "metadata/installation.json", Contents: metadataBytes, Mode: 0600},
		{ArchivePath: "secrets/generated.json", Contents: secretBytes, Mode: 0600},
	}); err != nil {
		return nil, err
	}
	archiveHash, err := fileSHA256(archivePath)
	if err != nil {
		_ = os.Remove(archivePath)
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO application_backups(backup_id,installation_id,application_id,release_id,runtime_generation,archive_path,archive_sha256,created_at,status) VALUES(?,?,?,?,?,?,?,?, 'complete')`, operationID, request.InstanceID, plan.Application.ID, plan.ReleaseID, plan.Generation, archivePath, archiveHash, backupManifest.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		_ = os.Remove(archivePath)
		return nil, fmt.Errorf("record application backup: %w", err)
	}
	return map[string]any{"backup_id": operationID, "filename": filename, "sha256": archiveHash, "created_at": backupManifest.CreatedAt.Format(time.RFC3339Nano), "databases": len(databases)}, nil
}

func restoreApplicationBackup(ctx context.Context, db *sql.DB, request protocol.Request) (map[string]any, error) {
	return restoreApplicationBackupWithFence(ctx, db, request, 0)
}

func restoreApplicationBackupWithFence(ctx context.Context, db *sql.DB, request protocol.Request, fencingToken int64) (map[string]any, error) {
	if !backupIDPattern.MatchString(request.BackupID) {
		return nil, fmt.Errorf("invalid backup identity")
	}
	var archivePath, expectedHash, applicationID, releaseID string
	var generation int
	if err := db.QueryRowContext(ctx, `SELECT archive_path,archive_sha256,application_id,release_id,runtime_generation FROM application_backups WHERE backup_id=? AND installation_id=? AND status='complete'`, request.BackupID, request.InstanceID).Scan(&archivePath, &expectedHash, &applicationID, &releaseID, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("backup not found for installation")
		}
		return nil, err
	}
	if filepath.Dir(archivePath) != filepath.Clean(env("KITPRO_APPLICATION_BACKUP_DIR", "/var/lib/kitpro-helper/application-backups")) {
		return nil, fmt.Errorf("backup path is outside managed destination")
	}
	actualHash, err := fileSHA256(archivePath)
	if err != nil || actualHash != expectedHash {
		return nil, fmt.Errorf("backup archive hash mismatch")
	}
	extractedBytes, _, err := appbackup.Measure(archivePath, appbackup.Limits{})
	if err != nil {
		return nil, err
	}
	const restoreOverhead = int64(64 << 20)
	if extractedBytes > (math.MaxInt64-restoreOverhead)/2 {
		return nil, fmt.Errorf("backup archive is too large")
	}
	if err := requireApplicationBackupSpace(filepath.Dir(archivePath), extractedBytes*2+restoreOverhead); err != nil {
		return nil, err
	}
	request.ApplicationID, request.ReleaseID, request.RuntimeGeneration = applicationID, releaseID, generation
	plan, err := loadApplicationBackupPlan(db, request)
	if err != nil {
		return nil, err
	}
	workspace, err := os.MkdirTemp(filepath.Dir(archivePath), ".restore-work-")
	if err != nil {
		return nil, err
	}
	defer removeBackupTree(workspace)
	extractRoot := filepath.Join(workspace, "extracted")
	extracted, err := appbackup.Extract(archivePath, extractRoot, appbackup.ExtractOptions{PreserveOwnership: os.Geteuid() == 0})
	if err != nil {
		return nil, err
	}
	if err := validateRestoreManifest(extracted.Manifest, request, plan); err != nil {
		return nil, err
	}
	secrets, err := readRestoreSecrets(filepath.Join(extractRoot, "secrets", "generated.json"), plan.Application)
	if err != nil {
		return nil, err
	}
	stagedDatabases := make([]appbackup.ManagedSource, 0)
	newPaths := map[string]string{}
	rollbackPaths := map[string]string{}
	for _, storage := range plan.Storage {
		if storage.Manifest.Disposition != "include" {
			continue
		}
		extractedPath := filepath.Join(extractRoot, filepath.FromSlash(storage.Manifest.ArchivePath))
		newPath := storage.HostPath + ".kitpro-restore-new-" + request.SemanticOperationID()
		rollbackPath := storage.HostPath + ".kitpro-restore-rollback-" + request.SemanticOperationID()
		if _, err := os.Lstat(storage.HostPath); err != nil {
			return nil, fmt.Errorf("managed restore target unavailable")
		}
		if _, err := os.Lstat(newPath); !os.IsNotExist(err) {
			return nil, fmt.Errorf("restore staging path already exists")
		}
		if _, err := os.Lstat(rollbackPath); !os.IsNotExist(err) {
			return nil, fmt.Errorf("restore rollback path already exists")
		}
		if err := appbackup.CopyTree(extractedPath, newPath); err != nil {
			return nil, fmt.Errorf("prepare restored managed storage: %w", err)
		}
		newPaths[storageKey(storage.Manifest.Component, storage.Manifest.ID)] = newPath
		rollbackPaths[storageKey(storage.Manifest.Component, storage.Manifest.ID)] = rollbackPath
		stagedDatabases = append(stagedDatabases, appbackup.ManagedSource{Component: storage.Manifest.Component, StorageID: storage.Manifest.ID, Path: newPath})
	}
	preserveStaging := false
	defer func() {
		if preserveStaging {
			return
		}
		for _, name := range newPaths {
			_ = removeBackupTree(name)
		}
	}()
	if plan.Application.Backup.Strategy == "cold-sqlite-filesystem" {
		databases, err := appbackup.DiscoverAndVerifySQLite(ctx, stagedDatabases)
		if err != nil {
			return nil, err
		}
		if !sameDatabaseInventory(databases, extracted.Manifest.Databases) {
			return nil, fmt.Errorf("restored SQLite inventory does not match manifest")
		}
	}
	journaled := fencingToken > 0 && request.OperationID != ""
	if journaled {
		if err := beginRestoreJournal(ctx, db, request, fencingToken, plan, secrets, newPaths, rollbackPaths); err != nil {
			return nil, fmt.Errorf("record restore journal: %w", err)
		}
		if err := runRestoreFault("before_first_rename", ""); err != nil {
			preserveStaging = errors.Is(err, errRestoreInterrupted)
			return nil, err
		}
	}

	runtime := newContainerRuntime()
	running, err := snapshotBackupRuntime(runtime, &plan, false)
	if err != nil {
		return nil, err
	}
	if journaled {
		if err = updateRestoreRuntimeIntent(ctx, db, request.OperationID, fencingToken, running); err != nil {
			return nil, err
		}
	}
	if err = quiesceRecordedBackupRuntime(runtime, plan, running); err != nil {
		return nil, err
	}
	swapped := make([]managedBackupStorage, 0)
	rollback := func(cause error) error {
		_ = stopBackupRuntime(runtime, plan)
		for i := len(swapped) - 1; i >= 0; i-- {
			storage := swapped[i]
			key := storageKey(storage.Manifest.Component, storage.Manifest.ID)
			failedPath := storage.HostPath + ".kitpro-restore-failed-" + request.SemanticOperationID()
			_ = os.Rename(storage.HostPath, failedPath)
			_ = os.Rename(rollbackPaths[key], storage.HostPath)
			if journaled {
				_ = updateRestoreStep(ctx, db, request.OperationID, key, "rolled_back", "")
			}
		}
		if secretErr := replaceSecrets(ctx, db, request.InstanceID, plan.Secrets); secretErr != nil {
			cause = fmt.Errorf("%v; secret rollback failed", cause)
		}
		if resumeErr := resumeBackupRuntime(runtime, plan, running); resumeErr != nil {
			cause = fmt.Errorf("%v; application rollback restart failed", cause)
		}
		if journaled {
			_ = updateRestoreOperation(ctx, db, request.OperationID, "rolled_back", cause.Error())
		}
		return cause
	}
	for _, storage := range plan.Storage {
		if storage.Manifest.Disposition != "include" {
			continue
		}
		key := storageKey(storage.Manifest.Component, storage.Manifest.ID)
		if journaled {
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "original_to_rollback_prepared", ""); err != nil {
				return nil, rollback(err)
			}
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "original_to_rollback_dispatched", "original_to_rollback_dispatched=1"); err != nil {
				return nil, rollback(err)
			}
		}
		if err := os.Rename(storage.HostPath, rollbackPaths[key]); err != nil {
			return nil, rollback(fmt.Errorf("stage current managed storage for rollback: %w", err))
		}
		if journaled {
			if err := runRestoreFault("after_original_to_rollback", key); err != nil {
				if errors.Is(err, errRestoreInterrupted) {
					preserveStaging = true
					return nil, err
				}
				return nil, rollback(err)
			}
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "original_to_rollback_confirmed", "original_to_rollback_confirmed=1"); err != nil {
				preserveStaging = true
				return nil, err
			}
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "staged_to_active_prepared", ""); err != nil {
				preserveStaging = true
				return nil, err
			}
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "staged_to_active_dispatched", "staged_to_active_dispatched=1"); err != nil {
				preserveStaging = true
				return nil, err
			}
		}
		if err := os.Rename(newPaths[key], storage.HostPath); err != nil {
			_ = os.Rename(rollbackPaths[key], storage.HostPath)
			return nil, rollback(fmt.Errorf("activate restored managed storage: %w", err))
		}
		if journaled {
			if err := runRestoreFault("after_staged_to_active", key); err != nil {
				if errors.Is(err, errRestoreInterrupted) {
					preserveStaging = true
					return nil, err
				}
				return nil, rollback(err)
			}
			if err := updateRestoreStep(ctx, db, request.OperationID, key, "staged_to_active_confirmed", "staged_to_active_confirmed=1"); err != nil {
				preserveStaging = true
				return nil, err
			}
		}
		swapped = append(swapped, storage)
		delete(newPaths, key)
		if journaled {
			if err := runRestoreFault("between_storage_slots", key); err != nil {
				if errors.Is(err, errRestoreInterrupted) {
					preserveStaging = true
					return nil, err
				}
				return nil, rollback(err)
			}
		}
	}
	if journaled {
		if err := updateRestoreOperation(ctx, db, request.OperationID, "runtime_restore_pending", ""); err != nil {
			preserveStaging = true
			return nil, err
		}
		if err := runRestoreFault("after_all_swaps_before_runtime", ""); err != nil {
			if errors.Is(err, errRestoreInterrupted) {
				preserveStaging = true
				return nil, err
			}
			return nil, rollback(err)
		}
	}
	if err := replaceSecrets(ctx, db, request.InstanceID, secrets); err != nil {
		return nil, rollback(fmt.Errorf("restore generated secrets: %w", err))
	}
	if err := resumeBackupRuntime(runtime, plan, running); err != nil {
		return nil, rollback(fmt.Errorf("start restored application: %w", err))
	}
	if journaled {
		if err := runRestoreFault("after_runtime_restart", ""); err != nil {
			if errors.Is(err, errRestoreInterrupted) {
				preserveStaging = true
				return nil, err
			}
			return nil, rollback(err)
		}
		if err := updateRestoreOperation(ctx, db, request.OperationID, "committed", ""); err != nil {
			preserveStaging = true
			return nil, err
		}
		if err := runRestoreFault("after_restore_commit", ""); err != nil {
			if errors.Is(err, errRestoreInterrupted) {
				preserveStaging = true
				return nil, err
			}
			return nil, err
		}
	}
	cleanupDeferred := false
	for _, storage := range swapped {
		key := storageKey(storage.Manifest.Component, storage.Manifest.ID)
		if journaled {
			_ = updateRestoreStep(ctx, db, request.OperationID, key, "cleanup_pending", "cleanup_dispatched=1")
			if err := runRestoreFault("during_cleanup", key); err != nil {
				cleanupDeferred = true
				_, _ = db.ExecContext(ctx, `UPDATE restore_storage_steps SET error_detail=?,updated_at=? WHERE operation_id=? AND storage_key=?`, boundedRestoreError(err.Error()), time.Now().UTC().Format(time.RFC3339Nano), request.OperationID, key)
				continue
			}
		}
		if err := removeBackupTree(rollbackPaths[key]); err != nil {
			if !journaled {
				return nil, fmt.Errorf("restore succeeded but rollback cleanup failed: %w", err)
			}
			cleanupDeferred = true
			_, _ = db.ExecContext(ctx, `UPDATE restore_storage_steps SET error_detail=?,updated_at=? WHERE operation_id=? AND storage_key=?`, boundedRestoreError(err.Error()), time.Now().UTC().Format(time.RFC3339Nano), request.OperationID, key)
			continue
		}
		if journaled {
			_ = updateRestoreStep(ctx, db, request.OperationID, key, "completed", "cleanup_confirmed=1")
		}
	}
	if journaled {
		phase := "completed"
		if cleanupDeferred {
			phase = "cleanup_pending"
			_, _ = db.ExecContext(ctx, `UPDATE restore_operations SET phase=?,cleanup_error='rollback cleanup deferred',updated_at=? WHERE operation_id=?`, phase, time.Now().UTC().Format(time.RFC3339Nano), request.OperationID)
		} else if err := updateRestoreOperation(ctx, db, request.OperationID, phase, ""); err != nil {
			return nil, err
		}
	}
	return map[string]any{"backup_id": request.BackupID, "status": "restored", "application_id": applicationID, "release_id": releaseID, "cleanup_deferred": cleanupDeferred}, nil
}

// removeBackupTree reclaims only a caller-selected backup workspace, staging
// tree, or rollback tree before removing it. Preserved application ownership
// can otherwise prevent the least-privileged helper from unlinking children;
// CAP_CHOWN is sufficient and avoids granting CAP_DAC_OVERRIDE or CAP_FOWNER.
func removeBackupTree(name string) error {
	err := filepath.WalkDir(name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if err := os.Chown(path, os.Geteuid(), os.Getegid()); err != nil {
			return err
		}
		return os.Chmod(path, 0700)
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(name)
}

func loadApplicationBackupPlan(db *sql.DB, request protocol.Request) (applicationBackupPlan, error) {
	if !backupIDPattern.MatchString(request.SemanticOperationID()) || request.InstanceID == "" || request.ApplicationID == "" || request.ReleaseID == "" || request.RuntimeGeneration < 1 {
		return applicationBackupPlan{}, fmt.Errorf("invalid application backup request")
	}
	entries, err := catalog.Load()
	if err != nil {
		return applicationBackupPlan{}, fmt.Errorf("trusted catalog unavailable")
	}
	entry, ok := entries[request.ApplicationID]
	if !ok || entry.Manifest.Backup == nil {
		return applicationBackupPlan{}, fmt.Errorf("application backup policy unavailable")
	}
	plan := applicationBackupPlan{Application: entry.Manifest, ReleaseID: request.ReleaseID, Generation: request.RuntimeGeneration}
	if len(entry.Manifest.Components) == 0 {
		owned, ownershipErr := loadSingleRuntime(db, request.InstanceID)
		if ownershipErr != nil {
			return applicationBackupPlan{}, fmt.Errorf("trusted application ownership unavailable")
		}
		expectedImage, err := releaseImage(entry.Manifest, request.ReleaseID)
		if err != nil || owned.ApplicationID != request.ApplicationID || owned.ReleaseID != request.ReleaseID || owned.Generation != request.RuntimeGeneration || owned.Image != expectedImage {
			return applicationBackupPlan{}, fmt.Errorf("trusted application backup identity mismatch")
		}
		plan.Components = []ownedBackupComponent{{Manifest: appbackup.Component{ID: "app", ImageDigest: expectedImage}, Runtime: owned.ContainerID}}
		plan.StartOrder = []string{"app"}
	} else {
		if request.ReleaseID != entry.Manifest.Releases[0].Version {
			return applicationBackupPlan{}, fmt.Errorf("trusted application release mismatch")
		}
		rows, err := db.Query(`SELECT c.component_id,c.observed_container_id,c.image_digest,g.runtime_generation,c.dependencies_json,c.start_ordinal,g.application_id,g.release_id FROM runtime_components c JOIN runtime_generations g USING(installation_id,runtime_generation) WHERE c.installation_id=? AND g.status='active' ORDER BY c.start_ordinal,c.component_id`, request.InstanceID)
		if err != nil {
			return applicationBackupPlan{}, err
		}
		owned := map[string]ownedBackupComponent{}
		persistedOrder := []string{}
		for rows.Next() {
			var componentID, runtimeID, image, dependenciesJSON, applicationID, releaseID string
			var generation, ordinal int
			if err := rows.Scan(&componentID, &runtimeID, &image, &generation, &dependenciesJSON, &ordinal, &applicationID, &releaseID); err != nil {
				_ = rows.Close()
				return applicationBackupPlan{}, err
			}
			var dependencies []string
			if json.Unmarshal([]byte(dependenciesJSON), &dependencies) != nil || ordinal != len(persistedOrder) || applicationID != request.ApplicationID || releaseID != request.ReleaseID || generation != request.RuntimeGeneration {
				_ = rows.Close()
				return applicationBackupPlan{}, fmt.Errorf("trusted component generation or topology mismatch")
			}
			owned[componentID] = ownedBackupComponent{Manifest: appbackup.Component{ID: componentID, ImageDigest: image, DependsOn: dependencies}, Runtime: runtimeID}
			persistedOrder = append(persistedOrder, componentID)
		}
		_ = rows.Close()
		if len(owned) == 0 {
			// Pre-slice installations retain their original evidence until a
			// trusted topology migration can run. Backups remain available, but
			// no new lifecycle mutation uses this legacy ownership path.
			legacyRows, legacyErr := db.Query(`SELECT component_id,container_id,image_digest,runtime_generation FROM component_ownership WHERE installation_id=? ORDER BY component_id`, request.InstanceID)
			if legacyErr != nil {
				return applicationBackupPlan{}, legacyErr
			}
			for legacyRows.Next() {
				var componentID, runtimeID, image string
				var generation int
				if legacyErr = legacyRows.Scan(&componentID, &runtimeID, &image, &generation); legacyErr != nil {
					legacyRows.Close()
					return applicationBackupPlan{}, legacyErr
				}
				if generation != request.RuntimeGeneration {
					legacyRows.Close()
					return applicationBackupPlan{}, fmt.Errorf("trusted component generation mismatch")
				}
				owned[componentID] = ownedBackupComponent{Manifest: appbackup.Component{ID: componentID, ImageDigest: image}, Runtime: runtimeID}
			}
			_ = legacyRows.Close()
		}
		componentGraph := make([]manifest.Component, 0, len(entry.Manifest.Components))
		for _, component := range entry.Manifest.Components {
			ownedComponent, present := owned[component.ID]
			expectedImage, imageErr := componentImage(entry.Manifest, component)
			if !present || imageErr != nil || ownedComponent.Manifest.ImageDigest != expectedImage {
				return applicationBackupPlan{}, fmt.Errorf("trusted component backup identity mismatch")
			}
			if len(ownedComponent.Manifest.DependsOn) == 0 && len(component.DependsOn) > 0 {
				ownedComponent.Manifest.DependsOn = append([]string(nil), component.DependsOn...)
			}
			if !sameStringSet(ownedComponent.Manifest.DependsOn, component.DependsOn) {
				return applicationBackupPlan{}, fmt.Errorf("trusted component topology mismatch")
			}
			plan.Components = append(plan.Components, ownedComponent)
			componentGraph = append(componentGraph, manifest.Component{ID: component.ID, DependsOn: component.DependsOn})
		}
		if len(owned) != len(entry.Manifest.Components) {
			return applicationBackupPlan{}, fmt.Errorf("trusted component count mismatch")
		}
		if len(persistedOrder) > 0 {
			plan.StartOrder = persistedOrder
		} else {
			plan.StartOrder, err = multicontainer.StartOrder(componentGraph)
			if err != nil {
				return applicationBackupPlan{}, err
			}
		}
	}
	plan.Storage, err = backupStoragePlan(entry.Manifest, request.InstanceID)
	if err != nil {
		return applicationBackupPlan{}, err
	}
	plan.Imported, err = loadImportedBackupBindings(db, request.InstanceID, entry.Manifest)
	if err != nil {
		return applicationBackupPlan{}, err
	}
	plan.Secrets, err = loadBackupSecrets(db, request.InstanceID, entry.Manifest)
	if err != nil {
		return applicationBackupPlan{}, err
	}
	return plan, nil
}

func backupStoragePlan(application manifest.Manifest, installationID string) ([]managedBackupStorage, error) {
	declared := map[string]manifest.Storage{}
	for _, item := range application.Storage {
		declared[storageKey("app", item.ID)] = item
	}
	for _, component := range application.Components {
		for _, item := range component.Storage {
			declared[storageKey(component.ID, item.ID)] = item
		}
	}
	result := make([]managedBackupStorage, 0, len(application.Backup.Storage))
	for _, policy := range application.Backup.Storage {
		item, ok := declared[storageKey(policy.Component, policy.ID)]
		if !ok {
			return nil, fmt.Errorf("trusted backup storage unavailable")
		}
		hostPath := filepath.Join(managedApplicationRoot, application.ID, installationID)
		if policy.Component != "app" {
			hostPath = filepath.Join(hostPath, policy.Component)
		}
		hostPath = filepath.Join(hostPath, policy.ID)
		info, err := os.Lstat(hostPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("managed backup storage unavailable")
		}
		archivePath := ""
		if policy.Disposition == "include" {
			archivePath = pathJoin("application", policy.Component, policy.ID)
		}
		result = append(result, managedBackupStorage{Manifest: appbackup.Storage{Component: policy.Component, ID: policy.ID, Disposition: policy.Disposition, ArchivePath: archivePath, OwnerUID: item.OwnerUID, OwnerGID: item.OwnerGID}, HostPath: hostPath})
	}
	return result, nil
}

func loadImportedBackupBindings(db *sql.DB, installationID string, application manifest.Manifest) ([]appbackup.ImportedStorage, error) {
	expected := map[string]manifest.ExternalStorage{}
	for _, item := range application.ExternalStorage {
		expected[storageKey("app", item.ID)] = item
	}
	for _, component := range application.Components {
		for _, item := range component.ExternalStorage {
			expected[storageKey(component.ID, item.ID)] = item
		}
	}
	rows, err := db.Query(`SELECT b.component_id,b.slot_id,b.root_id,b.access_mode,r.filesystem_type,r.device_major,r.device_minor,r.inode,r.network_backed FROM external_storage_bindings b JOIN trusted_storage_roots r ON r.root_id=b.root_id WHERE b.installation_id=? ORDER BY b.component_id,b.slot_id`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []appbackup.ImportedStorage
	for rows.Next() {
		var component, slot, rootID, mode, filesystem string
		var major, minor, inode uint64
		var network bool
		if err := rows.Scan(&component, &slot, &rootID, &mode, &filesystem, &major, &minor, &inode, &network); err != nil {
			return nil, err
		}
		archiveComponent := component
		if archiveComponent == "" {
			archiveComponent = "app"
		}
		declaration, ok := expected[storageKey(archiveComponent, slot)]
		if !ok || declaration.Mode != mode {
			return nil, fmt.Errorf("trusted imported storage binding mismatch")
		}
		delete(expected, storageKey(archiveComponent, slot))
		result = append(result, appbackup.ImportedStorage{Component: archiveComponent, SlotID: slot, RootID: rootID, AccessMode: mode, Filesystem: filesystem, DeviceMajor: major, DeviceMinor: minor, Inode: inode, NetworkBacked: network, ContentIncluded: false, ExclusionReason: "external-administrator-managed"})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, missing := range expected {
		if missing.Required {
			return nil, fmt.Errorf("required imported storage binding missing")
		}
	}
	return result, nil
}

func loadBackupSecrets(db *sql.DB, installationID string, application manifest.Manifest) (secretValues, error) {
	expected := map[string]bool{}
	for _, item := range application.Environment {
		if item.Secret && item.Generate != "" {
			expected[storageKey("app", item.Name)] = true
		}
	}
	for _, component := range application.Components {
		for _, item := range component.Environment {
			if item.Secret && item.Generate != "" {
				expected[storageKey(component.ID, item.Name)] = true
			}
		}
	}
	rows, err := db.Query(`SELECT component_id,name,value FROM installation_secrets WHERE installation_id=? ORDER BY component_id,name`, installationID)
	if err != nil {
		return secretValues{}, err
	}
	defer rows.Close()
	result := secretValues{Version: 1}
	for rows.Next() {
		var component, name, value string
		if err := rows.Scan(&component, &name, &value); err != nil {
			return secretValues{}, err
		}
		archiveComponent := component
		if archiveComponent == "" {
			archiveComponent = "app"
		}
		key := storageKey(archiveComponent, name)
		if !expected[key] || len(value) != 64 {
			return secretValues{}, fmt.Errorf("generated secret inventory mismatch")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return secretValues{}, fmt.Errorf("generated secret inventory invalid")
		}
		delete(expected, key)
		result.Values = append(result.Values, secretValue{Component: archiveComponent, Name: name, Value: value})
	}
	if err := rows.Err(); err != nil {
		return secretValues{}, err
	}
	if len(expected) != 0 {
		return secretValues{}, fmt.Errorf("required generated secret missing")
	}
	return result, nil
}

func readRestoreSecrets(name string, application manifest.Manifest) (secretValues, error) {
	contents, err := os.ReadFile(name)
	if err != nil {
		return secretValues{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var values secretValues
	if err := decoder.Decode(&values); err != nil || values.Version != 1 {
		return secretValues{}, fmt.Errorf("invalid generated secret payload")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return secretValues{}, fmt.Errorf("invalid generated secret payload")
	}
	expected := map[string]bool{}
	for _, item := range application.Environment {
		if item.Secret && item.Generate != "" {
			expected[storageKey("app", item.Name)] = true
		}
	}
	for _, component := range application.Components {
		for _, item := range component.Environment {
			if item.Secret && item.Generate != "" {
				expected[storageKey(component.ID, item.Name)] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, value := range values.Values {
		key := storageKey(value.Component, value.Name)
		if !expected[key] || seen[key] || len(value.Value) != 64 {
			return secretValues{}, fmt.Errorf("generated secret payload does not match catalog")
		}
		if _, err := hex.DecodeString(value.Value); err != nil {
			return secretValues{}, fmt.Errorf("invalid generated secret value")
		}
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return secretValues{}, fmt.Errorf("generated secret payload is incomplete")
	}
	return values, nil
}

func replaceSecrets(ctx context.Context, db *sql.DB, installationID string, values secretValues) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM installation_secrets WHERE installation_id=?`, installationID); err != nil {
		return err
	}
	for _, value := range values.Values {
		component := value.Component
		if component == "app" {
			component = ""
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO installation_secrets(installation_id,component_id,name,value,created_at) VALUES(?,?,?,?,?)`, installationID, component, value.Name, value.Value, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func quiesceBackupRuntime(runtime containers.Runtime, plan *applicationBackupPlan) (map[string]bool, error) {
	return snapshotBackupRuntime(runtime, plan, true)
}

// quiesceRecordedBackupRuntime stops only the components whose running intent
// was already persisted in the restore journal. Recovery can therefore resume
// them even if the helper exits during the first stop.
func quiesceRecordedBackupRuntime(runtime containers.Runtime, plan applicationBackupPlan, running map[string]bool) error {
	byID := map[string]ownedBackupComponent{}
	for _, component := range plan.Components {
		byID[component.Manifest.ID] = component
	}
	for i := len(plan.StartOrder) - 1; i >= 0; i-- {
		component := byID[plan.StartOrder[i]]
		if component.Runtime == "" || !running[component.Manifest.ID] {
			continue
		}
		if err := runtime.Stop(component.Runtime); err != nil {
			_ = resumeBackupRuntime(runtime, plan, running)
			return fmt.Errorf("stop application for backup: %w", err)
		}
		observed, err := runtime.Inspect(component.Runtime)
		if err != nil || runtimeRunning(observed) {
			_ = resumeBackupRuntime(runtime, plan, running)
			return fmt.Errorf("application did not quiesce")
		}
	}
	return nil
}

func snapshotBackupRuntime(runtime containers.Runtime, plan *applicationBackupPlan, stop bool) (map[string]bool, error) {
	running := map[string]bool{}
	byID := map[string]*ownedBackupComponent{}
	for i := range plan.Components {
		component := &plan.Components[i]
		byID[component.Manifest.ID] = component
		if component.Runtime == "" {
			continue
		}
		observed, err := runtime.Inspect(component.Runtime)
		if err != nil {
			return nil, fmt.Errorf("inspect application before backup: %w", err)
		}
		state, _ := observed["State"].(map[string]any)
		isRunning, ok := state["Running"].(bool)
		if !ok {
			return nil, fmt.Errorf("runtime did not report application state")
		}
		component.Running = isRunning
		running[component.Manifest.ID] = isRunning
	}
	if !stop {
		return running, nil
	}
	for i := len(plan.StartOrder) - 1; i >= 0; i-- {
		component := byID[plan.StartOrder[i]]
		if component != nil && component.Runtime != "" && component.Running {
			if err := runtime.Stop(component.Runtime); err != nil {
				_ = resumeBackupRuntime(runtime, *plan, running)
				return nil, fmt.Errorf("stop application for backup: %w", err)
			}
			observed, err := runtime.Inspect(component.Runtime)
			if err != nil || runtimeRunning(observed) {
				_ = resumeBackupRuntime(runtime, *plan, running)
				return nil, fmt.Errorf("application did not quiesce")
			}
		}
	}
	return running, nil
}

func stopBackupRuntime(runtime containers.Runtime, plan applicationBackupPlan) error {
	byID := map[string]ownedBackupComponent{}
	for _, component := range plan.Components {
		byID[component.Manifest.ID] = component
	}
	var result error
	for i := len(plan.StartOrder) - 1; i >= 0; i-- {
		component := byID[plan.StartOrder[i]]
		if component.Runtime == "" {
			continue
		}
		if observed, err := runtime.Inspect(component.Runtime); err == nil && runtimeRunning(observed) {
			result = errors.Join(result, runtime.Stop(component.Runtime))
		}
	}
	return result
}

func resumeBackupRuntime(runtime containers.Runtime, plan applicationBackupPlan, running map[string]bool) error {
	byID := map[string]ownedBackupComponent{}
	for _, component := range plan.Components {
		byID[component.Manifest.ID] = component
	}
	for _, componentID := range plan.StartOrder {
		component := byID[componentID]
		if component.Runtime == "" || !running[componentID] {
			continue
		}
		if err := runtime.Start(component.Runtime); err != nil {
			return err
		}
		observed, err := runtime.Inspect(component.Runtime)
		if err != nil || !runtimeRunning(observed) {
			return fmt.Errorf("application runtime did not return to the running state")
		}
	}
	return nil
}

func runtimeRunning(observed map[string]any) bool {
	state, _ := observed["State"].(map[string]any)
	running, _ := state["Running"].(bool)
	return running
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	first, second := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(first)
	sort.Strings(second)
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func validateRestoreManifest(backup appbackup.Manifest, request protocol.Request, plan applicationBackupPlan) error {
	if backup.ApplicationID != request.ApplicationID || backup.InstallationID != request.InstanceID || backup.ReleaseID != request.ReleaseID || backup.RuntimeGeneration != request.RuntimeGeneration || backup.Strategy != plan.Application.Backup.Strategy {
		return fmt.Errorf("backup is incompatible with target installation")
	}
	if len(backup.Components) != len(plan.Components) || len(backup.Storage) != len(plan.Storage) || len(backup.ImportedStorage) != len(plan.Imported) {
		return fmt.Errorf("backup topology does not match target installation")
	}
	for i := range plan.Components {
		if backup.Components[i].ID != plan.Components[i].Manifest.ID || backup.Components[i].ImageDigest != plan.Components[i].Manifest.ImageDigest {
			return fmt.Errorf("backup component image does not match target")
		}
	}
	for i := range plan.Storage {
		if backup.Storage[i] != plan.Storage[i].Manifest {
			return fmt.Errorf("backup storage mapping does not match target")
		}
	}
	for i := range plan.Imported {
		if backup.ImportedStorage[i] != plan.Imported[i] {
			return fmt.Errorf("imported storage must be reassociated before restore")
		}
	}
	return nil
}

func sameDatabaseInventory(got, want []appbackup.Database) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func releaseImage(application manifest.Manifest, releaseID string) (string, error) {
	for _, release := range application.Releases {
		if release.Version == releaseID {
			return release.Registry + "/" + release.Repository + "@" + release.Digest, nil
		}
	}
	return "", fmt.Errorf("trusted release unavailable")
}

func componentImage(application manifest.Manifest, component manifest.Component) (string, error) {
	return releaseImage(application, component.Release)
}

func fileSHA256(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func storageKey(component, id string) string { return component + "/" + id }

func pathJoin(parts ...string) string { return strings.Join(parts, "/") }
