package hostadmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPersistenceController(t *testing.T) (*SystemdPersistenceController, StoragePolicyResource, *int) {
	t.Helper()
	root := t.TempDir()
	unitDir := filepath.Join(root, "systemd")
	stateDir := filepath.Join(root, "state")
	if err := os.Mkdir(unitDir, 0755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := &SystemdPersistenceController{UnitDir: unitDir, StateDir: stateDir, allowTestOwnership: true}
	c.Reload = func(context.Context) error { calls++; return nil }
	c.Start = func(context.Context, string) error { return nil }
	c.Stop = func(context.Context, string) error { return nil }
	resource := StoragePolicyResource{ID: "fixture", Source: "/dev/disk/by-uuid/1111-2222", UUID: "1111-2222", Target: "/mnt/fixture", Filesystem: "ext4", Options: []string{"nodev", "nosuid", "noexec"}, Actions: []StorageAction{StorageActionPersistEnable, StorageActionPersistDisable, StorageActionPersistRecover}}
	return c, resource, &calls
}

func TestPersistentMountTransactionEnableDisable(t *testing.T) {
	c, r, calls := testPersistenceController(t)
	before, err := c.Status(r)
	if err != nil || before.Enabled || before.State != "disabled" {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	result, err := c.Enable(context.Background(), r)
	if err != nil || result.Status != "completed" || !result.Result.Enabled {
		t.Fatalf("enable=%+v err=%v", result, err)
	}
	unit, unitPath, wants := c.paths(r)
	raw, err := os.ReadFile(unitPath)
	if err != nil || !strings.Contains(string(raw), "What="+r.Source) || !strings.Contains(string(raw), "JobRunningTimeoutSec=15s") {
		t.Fatalf("unit=%q err=%v", raw, err)
	}
	destination, err := os.Readlink(wants)
	if err != nil || destination != "../"+unit {
		t.Fatalf("wants=%q err=%v", destination, err)
	}
	result, err = c.Disable(context.Background(), r)
	if err != nil || result.Status != "completed" || result.Result.Enabled {
		t.Fatalf("disable=%+v err=%v", result, err)
	}
	if _, err = os.Lstat(unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit remained: %v", err)
	}
	if *calls < 2 {
		t.Fatalf("reload calls=%d", *calls)
	}
}

func TestPersistentMountDisableSkipsStopWhenOptionalMountAlreadyAbsent(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	r.Target = t.TempDir()
	if _, err := c.Enable(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	c.Stop = func(context.Context, string) error { return errors.New("inactive unit cannot be stopped") }
	result, err := c.Disable(context.Background(), r)
	if err != nil || result.Status != "completed" || result.Result.Enabled {
		t.Fatalf("disable=%+v err=%v", result, err)
	}
}

func TestPersistentMountPublicationFailureRollsBack(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	c.Reload = func(context.Context) error { return errors.New("reload failed") }
	result, err := c.Enable(context.Background(), r)
	if err == nil || result.Status != "outcome_unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, unitPath, wants := c.paths(r)
	if _, err = os.Lstat(unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit remained: %v", err)
	}
	if _, err = os.Lstat(wants); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("activation remained: %v", err)
	}
}

func TestPersistentMountDoesNotReplaceUnmanagedUnit(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	_, unitPath, _ := c.paths(r)
	unmanaged := []byte("[Mount]\nWhat=/dev/administrator-owned\n")
	if err := os.WriteFile(unitPath, unmanaged, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Enable(context.Background(), r); err == nil {
		t.Fatal("enable unexpectedly replaced an unmanaged unit")
	}
	raw, err := os.ReadFile(unitPath)
	if err != nil || string(raw) != string(unmanaged) {
		t.Fatalf("unmanaged unit changed: %q err=%v", raw, err)
	}
}

func TestPersistentMountDetectsExternalModificationAndRefusesRemoval(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	stopCalls := 0
	c.Stop = func(context.Context, string) error { stopCalls++; return nil }
	if _, err := c.Enable(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	_, unitPath, _ := c.paths(r)
	if err := os.WriteFile(unitPath, []byte("[Mount]\nWhat=/dev/other\n"), 0644); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(r)
	if err != nil || status.State != "needs_recovery" || status.Health != "modified" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err = c.Disable(context.Background(), r); !errors.Is(err, ErrPermission) {
		t.Fatalf("disable err=%v", err)
	}
	if stopCalls != 0 {
		t.Fatalf("modified unit was stopped before ownership validation: calls=%d", stopCalls)
	}
	if _, err = c.Recover(context.Background(), r); !errors.Is(err, ErrPermission) {
		t.Fatalf("recover err=%v", err)
	}
	if _, err = os.Stat(unitPath); err != nil {
		t.Fatalf("modified administrator unit removed: %v", err)
	}
}

func TestPersistentMountActivationFailureRequiresExplicitRecovery(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	c.Start = func(context.Context, string) error { return errors.New("job failed") }
	result, err := c.Enable(context.Background(), r)
	if err == nil || result.Status != "outcome_unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	status, err := c.Status(r)
	if err != nil || status.State != "needs_recovery" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	result, err = c.Recover(context.Background(), r)
	if err != nil || result.Status != "completed" || result.Rollback == "" {
		t.Fatalf("recover=%+v err=%v", result, err)
	}
	status, _ = c.Status(r)
	if status.Enabled || status.State != "disabled" {
		t.Fatalf("after=%+v", status)
	}
}

func TestReadOnlyPersistentStatusRecognizesRecoverablePartialPublication(t *testing.T) {
	c, r, _ := testPersistenceController(t)
	readOnly := &SystemdPersistenceController{UnitDir: c.UnitDir}
	unit, unitPath, wants := c.paths(r)
	if err := os.WriteFile(unitPath, persistentUnitContent(r), 0644); err != nil {
		t.Fatal(err)
	}
	status, err := readOnly.Status(r)
	if err != nil || status.State != "needs_recovery" || status.Health != "activation_missing" {
		t.Fatalf("unit-only status=%+v err=%v", status, err)
	}
	if err = os.Remove(unitPath); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(wants), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("../"+unit, wants); err != nil {
		t.Fatal(err)
	}
	status, err = readOnly.Status(r)
	if err != nil || status.State != "needs_recovery" || status.Health != "missing_unit" {
		t.Fatalf("link-only status=%+v err=%v", status, err)
	}
}
