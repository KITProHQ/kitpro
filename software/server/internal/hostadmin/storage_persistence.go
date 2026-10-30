package hostadmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/coreos/go-systemd/v22/unit"
	"golang.org/x/sys/unix"
)

const persistentUnitMarker = "# Managed by KITPro Storage Management. Do not edit.\n"

type PersistentMountStatus struct {
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"`
	Health    string `json:"health"`
	Unit      string `json:"unit,omitempty"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type PersistentMountResult struct {
	ResourceID string                `json:"resource_id"`
	Action     StorageAction         `json:"action"`
	Previous   PersistentMountStatus `json:"previous"`
	Result     PersistentMountStatus `json:"result"`
	Status     string                `json:"status"`
	Rollback   string                `json:"rollback,omitempty"`
}

type PersistenceController interface {
	Status(StoragePolicyResource) (PersistentMountStatus, error)
	Enable(context.Context, StoragePolicyResource) (PersistentMountResult, error)
	Disable(context.Context, StoragePolicyResource) (PersistentMountResult, error)
	Recover(context.Context, StoragePolicyResource) (PersistentMountResult, error)
}

type persistenceRecord struct {
	Version    int    `json:"version"`
	ResourceID string `json:"resource_id"`
	Unit       string `json:"unit"`
	UnitSHA256 string `json:"unit_sha256"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Filesystem string `json:"filesystem"`
	State      string `json:"state"`
	LastError  string `json:"last_error,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

type SystemdPersistenceController struct {
	UnitDir            string
	StateDir           string
	allowTestOwnership bool
	Reload             func(context.Context) error
	Start              func(context.Context, string) error
	Stop               func(context.Context, string) error
}

func NewSystemdPersistenceController() *SystemdPersistenceController {
	c := &SystemdPersistenceController{UnitDir: "/etc/systemd/system", StateDir: "/var/lib/kitpro-helper/storage-persistence"}
	c.Reload = func(ctx context.Context) error {
		conn, err := systemdbus.NewSystemConnectionContext(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		return conn.ReloadContext(ctx)
	}
	c.Start = func(ctx context.Context, name string) error { return persistentSystemdJob(ctx, name, true) }
	c.Stop = func(ctx context.Context, name string) error { return persistentSystemdJob(ctx, name, false) }
	return c
}

func NewReadOnlySystemdPersistenceController() *SystemdPersistenceController {
	return &SystemdPersistenceController{UnitDir: "/etc/systemd/system"}
}

func persistentSystemdJob(ctx context.Context, name string, start bool) error {
	conn, err := systemdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	result := make(chan string, 1)
	if start {
		_, err = conn.StartUnitContext(ctx, name, "fail", result)
	} else {
		_, err = conn.StopUnitContext(ctx, name, "fail", result)
	}
	if err != nil {
		return err
	}
	select {
	case outcome := <-result:
		if outcome != "done" {
			return fmt.Errorf("systemd job %s", outcome)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func persistentUnitContent(r StoragePolicyResource) []byte {
	options := append([]string(nil), r.Options...)
	return []byte(persistentUnitMarker + "[Unit]\nDescription=KITPro managed optional storage " + r.ID + "\nDefaultDependencies=no\nAfter=local-fs-pre.target\nJobRunningTimeoutSec=15s\nJobTimeoutAction=none\n\n[Mount]\nWhat=" + r.Source + "\nWhere=" + r.Target + "\nType=" + r.Filesystem + "\nOptions=" + strings.Join(options, ",") + "\nTimeoutSec=15s\n\n[Install]\nWantedBy=multi-user.target\n")
}

func validatePersistentUnit(r StoragePolicyResource, content []byte) error {
	if !strings.HasSuffix(mountUnitName(r.Target), ".mount") {
		return ErrInvalid
	}
	opts, err := unit.Deserialize(bytes.NewReader(content))
	if err != nil {
		return ErrInvalid
	}
	expected := map[string]string{
		"Unit.Description":          "KITPro managed optional storage " + r.ID,
		"Unit.DefaultDependencies":  "no",
		"Unit.After":                "local-fs-pre.target",
		"Unit.JobRunningTimeoutSec": "15s",
		"Unit.JobTimeoutAction":     "none",
		"Mount.What":                r.Source,
		"Mount.Where":               r.Target,
		"Mount.Type":                r.Filesystem,
		"Mount.Options":             strings.Join(r.Options, ","),
		"Mount.TimeoutSec":          "15s",
		"Install.WantedBy":          "multi-user.target",
	}
	seen := make(map[string]bool, len(expected))
	for _, opt := range opts {
		key := opt.Section + "." + opt.Name
		value, ok := expected[key]
		if !ok || seen[key] || opt.Value != value {
			return ErrInvalid
		}
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return ErrInvalid
	}
	return nil
}

func digestBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func (c *SystemdPersistenceController) paths(r StoragePolicyResource) (string, string, string) {
	unit := mountUnitName(r.Target)
	return unit, filepath.Join(c.UnitDir, unit), filepath.Join(c.UnitDir, "multi-user.target.wants", unit)
}
func (c *SystemdPersistenceController) recordPath(id string) string {
	return filepath.Join(c.StateDir, id+".json")
}

func readBoundedRegular(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) != info.Size() || int64(len(data)) > limit {
		return nil, ErrInvalid
	}
	return data, nil
}
func (c *SystemdPersistenceController) readRecord(id string) (persistenceRecord, error) {
	raw, err := readBoundedRegular(c.recordPath(id), 16*1024)
	if err != nil {
		return persistenceRecord{}, err
	}
	var record persistenceRecord
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&record) != nil || dec.Decode(&struct{}{}) == nil || record.Version != 1 || record.ResourceID != id {
		return persistenceRecord{}, ErrInvalid
	}
	return record, nil
}
func ensurePersistenceDirectory(path string, mode os.FileMode, private, allowTestOwnership bool) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || (private && info.Mode().Perm()&0077 != 0) {
		return ErrPermission
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || (!allowTestOwnership && st.Uid != 0) || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrPermission
	}
	return nil
}
func atomicRootFile(path string, data []byte, mode os.FileMode, replace, allowTestOwnership bool) error {
	dir := filepath.Dir(path)
	if err := ensurePersistenceDirectory(dir, 0755, false, allowTestOwnership); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".kitpro-"+filepath.Base(path)+fmt.Sprintf("-%d", time.Now().UnixNano()))
	fd, err := unix.Open(tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode))
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if replace {
		err = os.Rename(tmp, path)
	} else {
		err = unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
	}
	if err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}
func atomicSymlink(path, target string, allowTestOwnership bool) error {
	dir := filepath.Dir(path)
	if err := ensurePersistenceDirectory(dir, 0755, false, allowTestOwnership); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".kitpro-"+filepath.Base(path)+fmt.Sprintf("-%d", time.Now().UnixNano()))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}
func removeAndSync(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (c *SystemdPersistenceController) writeRecord(record persistenceRecord) error {
	if err := ensurePersistenceDirectory(c.StateDir, 0700, true, c.allowTestOwnership); err != nil {
		return err
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicRootFile(c.recordPath(record.ResourceID), raw, 0600, true, c.allowTestOwnership)
}
func (c *SystemdPersistenceController) validateManagedFiles(r StoragePolicyResource) (persistenceRecord, error) {
	unit, unitPath, wants := c.paths(r)
	destination, err := os.Readlink(wants)
	if err != nil || destination != "../"+unit {
		return persistenceRecord{}, ErrPermission
	}
	raw, err := readBoundedRegular(unitPath, 16*1024)
	if err != nil {
		return persistenceRecord{}, ErrPermission
	}
	record, err := c.readRecord(r.ID)
	if err != nil || digestBytes(raw) != record.UnitSHA256 || string(raw) != string(persistentUnitContent(r)) {
		return persistenceRecord{}, ErrPermission
	}
	return record, nil
}
func persistentMountActive(r StoragePolicyResource) (bool, error) {
	raw, err := readStorageFile("/proc/1/mountinfo", maxStorageOutput)
	if err != nil {
		return false, ErrUnavailable
	}
	records, err := parseMountInfo(raw)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Point != r.Target {
			continue
		}
		if !storageSourcesMatch(r.Source, record.Source) || record.FSType != r.Filesystem {
			return false, ErrPermission
		}
		return true, nil
	}
	return false, nil
}
func (c *SystemdPersistenceController) Status(r StoragePolicyResource) (PersistentMountStatus, error) {
	unit, unitPath, wants := c.paths(r)
	status := PersistentMountStatus{State: "disabled", Health: "not_configured", Unit: unit, Source: r.Source, Target: r.Target}
	if c.StateDir == "" {
		raw, unitErr := readBoundedRegular(unitPath, 16*1024)
		destination, linkErr := os.Readlink(wants)
		if errors.Is(unitErr, os.ErrNotExist) && errors.Is(linkErr, os.ErrNotExist) {
			return status, nil
		}
		unitExact := unitErr == nil && string(raw) == string(persistentUnitContent(r))
		linkExact := linkErr == nil && destination == "../"+unit
		if unitExact && errors.Is(linkErr, os.ErrNotExist) {
			status.State = "needs_recovery"
			status.Health = "activation_missing"
			return status, nil
		}
		if linkExact && errors.Is(unitErr, os.ErrNotExist) {
			status.State = "needs_recovery"
			status.Health = "missing_unit"
			return status, nil
		}
		if !unitExact || !linkExact {
			status.State = "conflict"
			status.Health = "unmanaged_or_modified"
			return status, nil
		}
		status.Enabled = true
		status.State = "enabled"
		status.Health = "configured"
		return status, nil
	}
	record, err := c.readRecord(r.ID)
	if errors.Is(err, os.ErrNotExist) {
		_, unitErr := os.Lstat(unitPath)
		_, linkErr := os.Lstat(wants)
		if unitErr == nil || linkErr == nil || (!errors.Is(unitErr, os.ErrNotExist) && unitErr != nil) || (!errors.Is(linkErr, os.ErrNotExist) && linkErr != nil) {
			status.State = "conflict"
			status.Health = "unmanaged"
			return status, nil
		}
		return status, nil
	}
	if err != nil {
		return status, err
	}
	if record.State == "disabled" {
		_, unitErr := os.Lstat(unitPath)
		_, linkErr := os.Lstat(wants)
		if errors.Is(unitErr, os.ErrNotExist) && errors.Is(linkErr, os.ErrNotExist) {
			return status, nil
		}
		status.State = "needs_recovery"
		status.Health = "unexpected_configuration"
		return status, nil
	}
	raw, err := readBoundedRegular(unitPath, 16*1024)
	if err != nil {
		status.State = "needs_recovery"
		status.Health = "missing_unit"
		status.LastError = record.LastError
		return status, nil
	}
	if digestBytes(raw) != record.UnitSHA256 || string(raw) != string(persistentUnitContent(r)) {
		status.State = "needs_recovery"
		status.Health = "modified"
		return status, nil
	}
	destination, err := os.Readlink(wants)
	if err != nil || destination != "../"+unit {
		status.State = "needs_recovery"
		status.Health = "activation_missing"
		return status, nil
	}
	if record.State != "enabled" {
		status.Enabled = true
		status.State = "needs_recovery"
		status.Health = "incomplete_transaction"
		status.LastError = record.LastError
		return status, nil
	}
	status.Enabled = true
	status.State = "enabled"
	status.Health = "configured"
	status.LastError = record.LastError
	return status, nil
}
func (c *SystemdPersistenceController) Enable(ctx context.Context, r StoragePolicyResource) (PersistentMountResult, error) {
	if c.StateDir == "" {
		return PersistentMountResult{}, ErrPermission
	}
	previous, _ := c.Status(r)
	result := PersistentMountResult{ResourceID: r.ID, Action: StorageActionPersistEnable, Previous: previous, Status: "failed"}
	if previous.Enabled {
		result.Result = previous
		result.Status = "completed"
		return result, nil
	}
	if previous.State != "disabled" {
		return result, fmt.Errorf("persistent configuration conflict requires recovery")
	}
	unit, unitPath, wants := c.paths(r)
	content := persistentUnitContent(r)
	if err := validatePersistentUnit(r, content); err != nil {
		return result, err
	}
	record := persistenceRecord{Version: 1, ResourceID: r.ID, Unit: unit, UnitSHA256: digestBytes(content), Source: r.Source, Target: r.Target, Filesystem: r.Filesystem, State: "preparing"}
	if err := c.writeRecord(record); err != nil {
		return result, err
	}
	unitCreated, linkCreated := false, false
	rollback := func(reason string) error {
		if linkCreated {
			if destination, err := os.Readlink(wants); err == nil && destination == "../"+unit {
				_ = removeAndSync(wants)
			}
		}
		if unitCreated {
			if raw, err := readBoundedRegular(unitPath, 16*1024); err == nil && digestBytes(raw) == record.UnitSHA256 {
				_ = removeAndSync(unitPath)
			}
		}
		record.State = "disabled"
		record.LastError = reason
		_ = c.writeRecord(record)
		if unitCreated || linkCreated {
			return c.Reload(ctx)
		}
		return nil
	}
	if err := atomicRootFile(unitPath, content, 0644, false, c.allowTestOwnership); err != nil {
		if raw, readErr := readBoundedRegular(unitPath, 16*1024); readErr == nil && digestBytes(raw) == record.UnitSHA256 {
			unitCreated = true
		}
		rollbackErr := rollback("unit publication failed")
		result.Result, _ = c.Status(r)
		if rollbackErr != nil {
			result.Status = "outcome_unknown"
			return result, fmt.Errorf("unit publication failed and rollback could not be verified: %w", rollbackErr)
		}
		return result, err
	}
	unitCreated = true
	if err := atomicSymlink(wants, "../"+unit, c.allowTestOwnership); err != nil {
		if destination, linkErr := os.Readlink(wants); linkErr == nil && destination == "../"+unit {
			linkCreated = true
		}
		rollbackErr := rollback("activation publication failed")
		result.Result, _ = c.Status(r)
		if rollbackErr != nil {
			result.Status = "outcome_unknown"
			return result, fmt.Errorf("activation publication failed and rollback could not be verified: %w", rollbackErr)
		}
		return result, err
	}
	linkCreated = true
	if err := c.Reload(ctx); err != nil {
		rollbackErr := rollback("systemd reload failed")
		result.Result, _ = c.Status(r)
		if rollbackErr != nil {
			result.Status = "outcome_unknown"
			return result, fmt.Errorf("systemd reload failed and rollback could not be verified: %w", rollbackErr)
		}
		return result, err
	}
	record.State = "published"
	_ = c.writeRecord(record)
	if err := c.Start(ctx, unit); err != nil {
		record.State = "needs_recovery"
		record.LastError = "mount activation failed"
		_ = c.writeRecord(record)
		result.Result, _ = c.Status(r)
		result.Status = "outcome_unknown"
		return result, err
	}
	record.State = "enabled"
	record.LastError = ""
	if err := c.writeRecord(record); err != nil {
		result.Status = "outcome_unknown"
		return result, err
	}
	result.Result, _ = c.Status(r)
	result.Status = "completed"
	return result, nil
}
func (c *SystemdPersistenceController) Disable(ctx context.Context, r StoragePolicyResource) (PersistentMountResult, error) {
	if c.StateDir == "" {
		return PersistentMountResult{}, ErrPermission
	}
	previous, err := c.Status(r)
	result := PersistentMountResult{ResourceID: r.ID, Action: StorageActionPersistDisable, Previous: previous, Status: "failed"}
	if err != nil {
		return result, err
	}
	if !previous.Enabled {
		return result, ErrPermission
	}
	unit, unitPath, wants := c.paths(r)
	record, err := c.validateManagedFiles(r)
	if err != nil {
		return result, ErrPermission
	}
	mounted, err := persistentMountActive(r)
	if err != nil {
		return result, err
	}
	if mounted {
		if err = c.Stop(ctx, unit); err != nil {
			record.State = "needs_recovery"
			record.LastError = "mount deactivation failed"
			_ = c.writeRecord(record)
			result.Result, _ = c.Status(r)
			result.Status = "outcome_unknown"
			return result, err
		}
		if mounted, err = persistentMountActive(r); err != nil || mounted {
			record.State = "needs_recovery"
			record.LastError = "mount deactivation could not be verified"
			_ = c.writeRecord(record)
			result.Result, _ = c.Status(r)
			result.Status = "outcome_unknown"
			if err != nil {
				return result, err
			}
			return result, ErrUnavailable
		}
	}
	if _, err = c.validateManagedFiles(r); err != nil {
		record.State = "needs_recovery"
		record.LastError = "configuration changed during deactivation"
		_ = c.writeRecord(record)
		result.Status = "outcome_unknown"
		return result, ErrPermission
	}
	if err = removeAndSync(wants); err != nil {
		result.Result, _ = c.Status(r)
		return result, err
	}
	if err = removeAndSync(unitPath); err != nil {
		record.State = "needs_recovery"
		record.LastError = "unit removal failed after activation removal"
		_ = c.writeRecord(record)
		result.Result, _ = c.Status(r)
		result.Status = "outcome_unknown"
		return result, err
	}
	if err = c.Reload(ctx); err != nil {
		record.State = "needs_recovery"
		record.LastError = "systemd reload failed after removal"
		_ = c.writeRecord(record)
		result.Result, _ = c.Status(r)
		result.Status = "outcome_unknown"
		return result, err
	}
	record.State = "disabled"
	record.LastError = ""
	if err = c.writeRecord(record); err != nil {
		result.Result, _ = c.Status(r)
		result.Status = "outcome_unknown"
		return result, err
	}
	result.Result, _ = c.Status(r)
	result.Status = "completed"
	return result, nil
}
func (c *SystemdPersistenceController) Recover(ctx context.Context, r StoragePolicyResource) (PersistentMountResult, error) {
	if c.StateDir == "" {
		return PersistentMountResult{}, ErrPermission
	}
	previous, err := c.Status(r)
	result := PersistentMountResult{ResourceID: r.ID, Action: StorageActionPersistRecover, Previous: previous, Status: "failed"}
	if err != nil {
		return result, err
	}
	if previous.State != "needs_recovery" {
		return result, fmt.Errorf("no recoverable KITPro persistence transaction")
	}
	record, err := c.readRecord(r.ID)
	if err != nil {
		return result, err
	}
	unit, unitPath, wants := c.paths(r)
	unitPresent := false
	if raw, readErr := readBoundedRegular(unitPath, 16*1024); readErr == nil {
		if digestBytes(raw) != record.UnitSHA256 {
			return result, ErrPermission
		}
		unitPresent = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return result, readErr
	}
	linkRemoved := false
	if destination, linkErr := os.Readlink(wants); linkErr == nil {
		if destination != "../"+unit {
			return result, ErrPermission
		}
		if err = removeAndSync(wants); err != nil {
			return result, err
		}
		linkRemoved = true
	} else if !errors.Is(linkErr, os.ErrNotExist) {
		return result, linkErr
	}
	if unitPresent {
		raw, readErr := readBoundedRegular(unitPath, 16*1024)
		if readErr != nil || digestBytes(raw) != record.UnitSHA256 {
			if linkRemoved {
				result.Status = "outcome_unknown"
			}
			result.Result, _ = c.Status(r)
			return result, ErrPermission
		}
		if err = removeAndSync(unitPath); err != nil {
			result.Status = "outcome_unknown"
			result.Result, _ = c.Status(r)
			return result, err
		}
	}
	if err = c.Reload(ctx); err != nil {
		result.Status = "outcome_unknown"
		result.Result, _ = c.Status(r)
		return result, err
	}
	record.State = "disabled"
	record.LastError = ""
	if err = c.writeRecord(record); err != nil {
		result.Status = "outcome_unknown"
		result.Result, _ = c.Status(r)
		return result, err
	}
	result.Result, _ = c.Status(r)
	result.Status = "completed"
	result.Rollback = "restored_absent_configuration"
	return result, nil
}
