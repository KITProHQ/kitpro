package hostadmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

type healthPersistenceController struct{ status PersistentMountStatus }

func (h *healthPersistenceController) Status(StoragePolicyResource) (PersistentMountStatus, error) {
	return h.status, nil
}
func (h *healthPersistenceController) Enable(context.Context, StoragePolicyResource) (PersistentMountResult, error) {
	return PersistentMountResult{}, errors.New("not used")
}
func (h *healthPersistenceController) Disable(context.Context, StoragePolicyResource) (PersistentMountResult, error) {
	return PersistentMountResult{}, errors.New("not used")
}
func (h *healthPersistenceController) Recover(context.Context, StoragePolicyResource) (PersistentMountResult, error) {
	return PersistentMountResult{}, errors.New("not used")
}

type fakeMountKernel struct {
	mu      sync.Mutex
	mounted bool
	owned   bool
	err     error
	data    string
}

func (f *fakeMountKernel) Mount(_, _, _ string, _ uintptr, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.mounted = true
	f.owned = true
	f.data = data
	return nil
}
func (f *fakeMountKernel) Owned(string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owned, f.err
}
func (f *fakeMountKernel) Unmount(string, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.mounted = false
	return nil
}
func (f *fakeMountKernel) state() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.mounted }

func testStorageManager(t *testing.T) (*StorageManager, *fakeMountKernel, string) {
	t.Helper()
	target := t.TempDir()
	kernel := &fakeMountKernel{}
	manager := NewStorageManager(StoragePolicyDocument{Version: 1, Resources: []StoragePolicyResource{{ID: "qualification-tmpfs", Source: "tmpfs", Target: target, Filesystem: "tmpfs", Options: []string{"nodev", "nosuid", "noexec"}, Actions: []StorageAction{StorageActionMount, StorageActionUnmount}}}})
	manager.Kernel = kernel
	manager.ValidateTarget = func(string) error { return nil }
	manager.ReadMountInfo = func() ([]byte, error) {
		if !kernel.state() {
			return []byte("1 0 0:1 / / rw - rootfs rootfs rw\n"), nil
		}
		return []byte(fmt.Sprintf("1 0 0:1 / / rw - rootfs rootfs rw\n2 1 0:2 / %s rw,nodev,nosuid,noexec - tmpfs tmpfs rw\n", target)), nil
	}
	return manager, kernel, target
}

func TestStorageManagerMountUnmountAndStateVerification(t *testing.T) {
	manager, kernel, _ := testStorageManager(t)
	ctx := context.Background()
	preview, err := manager.Preview(ctx, "qualification-tmpfs", StorageActionMount)
	if err != nil || !preview.Eligibility.Allowed || preview.Mounted {
		t.Fatalf("mount preview=%+v err=%v", preview, err)
	}
	result, err := manager.Mutate(ctx, "qualification-tmpfs", StorageActionMount)
	if err != nil || result.Status != "completed" || !result.Result.Mounted {
		t.Fatalf("mount result=%+v err=%v", result, err)
	}
	if kernel.data != "mode=0755" {
		t.Fatalf("tmpfs mount data=%q", kernel.data)
	}
	preview, err = manager.Preview(ctx, "qualification-tmpfs", StorageActionUnmount)
	if err != nil || !preview.Eligibility.Allowed || !preview.Mounted {
		t.Fatalf("unmount preview=%+v err=%v", preview, err)
	}
	result, err = manager.Mutate(ctx, "qualification-tmpfs", StorageActionUnmount)
	if err != nil || result.Status != "completed" || result.Result.Mounted {
		t.Fatalf("unmount result=%+v err=%v", result, err)
	}
}

func TestStorageManagerFailsClosedForProtectedNestedAndChangedState(t *testing.T) {
	manager, kernel, target := testStorageManager(t)
	manager.Policy.Resources[0].Target = "/"
	preview, _ := manager.Preview(context.Background(), "qualification-tmpfs", StorageActionMount)
	if preview.Eligibility.Allowed || !preview.Eligibility.Protected {
		t.Fatalf("root preview=%+v", preview)
	}
	manager.Policy.Resources[0].Target = target
	manager.ReadMountInfo = func() ([]byte, error) {
		return []byte(fmt.Sprintf("1 0 0:1 / / rw - rootfs rootfs rw\n2 1 0:2 / %s/child rw - tmpfs tmpfs rw\n", target)), nil
	}
	preview, _ = manager.Preview(context.Background(), "qualification-tmpfs", StorageActionMount)
	if preview.Eligibility.Allowed || preview.Eligibility.State != "dependent_mounts" {
		t.Fatalf("nested preview=%+v", preview)
	}
	manager.ReadMountInfo = func() ([]byte, error) { return nil, errors.New("changed") }
	if _, err := manager.Preview(context.Background(), "qualification-tmpfs", StorageActionMount); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("changed err=%v", err)
	}
	kernel.err = unix.EBUSY
	manager.ReadMountInfo = func() ([]byte, error) { return []byte("1 0 0:1 / / rw - rootfs rootfs rw\n"), nil }
	_, err := manager.Mutate(context.Background(), "qualification-tmpfs", StorageActionMount)
	var mutation *StorageMutationError
	if !errors.As(err, &mutation) || mutation.Code != "StorageBusy" {
		t.Fatalf("busy err=%v", err)
	}
}

func TestStorageManagerWillNotUnmountMountItDidNotCreate(t *testing.T) {
	manager, kernel, _ := testStorageManager(t)
	kernel.mounted = true
	kernel.owned = false
	preview, err := manager.Preview(context.Background(), "qualification-tmpfs", StorageActionUnmount)
	if err != nil || preview.Eligibility.Allowed || preview.Eligibility.State != "not_kitpro_managed" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestStoragePolicyRejectsPathsWildcardsAndUnstableBlockIdentity(t *testing.T) {
	cases := []StoragePolicyResource{
		{ID: "bad/path", Source: "tmpfs", Target: "/mnt/fixture", Filesystem: "tmpfs", Actions: []StorageAction{StorageActionMount}},
		{ID: "fixture", Source: "tmpfs", Target: "/mnt/../etc", Filesystem: "tmpfs", Actions: []StorageAction{StorageActionMount}},
		{ID: "fixture", Source: "/dev/sda1", Target: "/mnt/data", Filesystem: "ext4", UUID: "abc", Actions: []StorageAction{StorageActionMount}},
		{ID: "fixture", Source: "tmpfs", Target: "/mnt/data", Filesystem: "tmpfs", Options: []string{"unsafe"}, Actions: []StorageAction{StorageActionMount}},
		{ID: "fixture", Source: "tmpfs", Target: "/opt/data", Filesystem: "tmpfs", Actions: []StorageAction{StorageActionMount}},
	}
	for _, item := range cases {
		if validateStoragePolicyResource(item) == nil {
			t.Fatalf("accepted unsafe policy %+v", item)
		}
	}
	valid := StoragePolicyResource{ID: "data", Source: "/dev/disk/by-uuid/abc", UUID: "abc", Target: "/mnt/data", Filesystem: "ext4", Options: []string{"nodev"}, Actions: []StorageAction{StorageActionMount, StorageActionUnmount}}
	if err := validateStoragePolicyResource(valid); err != nil {
		t.Fatalf("valid policy: %v", err)
	}
}

func TestStorageManagerClassifiesTimeoutAsUncertain(t *testing.T) {
	manager, kernel, _ := testStorageManager(t)
	kernel.err = context.DeadlineExceeded
	_, err := manager.Mutate(context.Background(), "qualification-tmpfs", StorageActionMount)
	var mutation *StorageMutationError
	if !errors.As(err, &mutation) || mutation.Code != "StorageActionTimedOut" || mutation.Status != "timed_out" {
		t.Fatalf("timeout err=%v", err)
	}
}

func TestPersistentStorageRequiresValidatedBlockIdentityAndOwnsLifecycle(t *testing.T) {
	controller, resource, _ := testPersistenceController(t)
	resource.Target = t.TempDir()
	resource.Actions = []StorageAction{StorageActionMount, StorageActionUnmount, StorageActionPersistEnable, StorageActionPersistDisable, StorageActionPersistRecover}
	manager := NewStorageManager(StoragePolicyDocument{Version: 1, Resources: []StoragePolicyResource{resource}})
	manager.Persistence = controller
	manager.ValidateTarget = func(string) error { return nil }
	manager.ValidateSource = func(string) (string, error) { return "/dev/loop99", nil }
	manager.ReadMountInfo = func() ([]byte, error) { return []byte("1 0 0:1 / / rw - rootfs rootfs rw\n"), nil }
	preview, err := manager.Preview(context.Background(), resource.ID, StorageActionPersistEnable)
	if err != nil || !preview.Eligibility.Allowed {
		t.Fatalf("enable preview=%+v err=%v", preview, err)
	}
	result, err := manager.MutatePersistence(context.Background(), resource.ID, StorageActionPersistEnable)
	if err != nil || result.Status != "completed" {
		t.Fatalf("enable=%+v err=%v", result, err)
	}
	preview, err = manager.Preview(context.Background(), resource.ID, StorageActionMount)
	if err != nil || preview.Eligibility.Allowed || preview.Eligibility.State != "persistent_configuration" {
		t.Fatalf("transient preview=%+v err=%v", preview, err)
	}
	if _, err = manager.MutatePersistence(context.Background(), resource.ID, StorageActionPersistDisable); err != nil {
		t.Fatal(err)
	}
	manager.ValidateSource = func(string) (string, error) { return "", ErrInvalid }
	preview, err = manager.Preview(context.Background(), resource.ID, StorageActionPersistEnable)
	if err != nil || preview.Eligibility.Allowed || preview.Eligibility.State != "source_unavailable" {
		t.Fatalf("ambiguous source preview=%+v err=%v", preview, err)
	}
}

func TestPersistentSourceInventoryRequiresOneExactUUID(t *testing.T) {
	source := "/dev/disk/by-uuid/1111-2222"
	one := []byte(`{"blockdevices":[{"path":"/dev/loop7","uuid":"1111-2222","children":[]}]}`)
	resolved, err := validatePersistentStorageSourceDocument(source, one)
	if err != nil || resolved != "/dev/loop7" {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	duplicate := []byte(`{"blockdevices":[{"path":"/dev/loop7","uuid":"1111-2222"},{"path":"/dev/loop8","uuid":"1111-2222"}]}`)
	if _, err = validatePersistentStorageSourceDocument(source, duplicate); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate UUID err=%v", err)
	}
	if _, err = validatePersistentStorageSourceDocument(source, []byte(`{"blockdevices":[]}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing UUID err=%v", err)
	}
}

func TestPersistentSourceUdevValidationIsBoundedAndDetectsDuplicates(t *testing.T) {
	root := t.TempDir()
	sysRoot := filepath.Join(root, "sys")
	udevRoot := filepath.Join(root, "udev")
	for _, name := range []string{"loop7", "loop8"} {
		if err := os.MkdirAll(filepath.Join(sysRoot, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(udevRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysRoot, "loop7", "dev"), []byte("7:7\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysRoot, "loop8", "dev"), []byte("7:8\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(udevRoot, "b7:7"), []byte("E:ID_FS_UUID=1111-2222\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := "/dev/disk/by-uuid/1111-2222"
	resolved, err := validatePersistentStorageSourceFromUdevRoots(source, sysRoot, udevRoot)
	if err != nil || resolved != "/dev/loop7" {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	if err = os.WriteFile(filepath.Join(udevRoot, "b7:8"), []byte("E:ID_FS_UUID=1111-2222\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = validatePersistentStorageSourceFromUdevRoots(source, sysRoot, udevRoot); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate udev UUID err=%v", err)
	}
}

func TestStoragePolicyFileMustBeRootOwnedAndNonWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"resources":[]}`), 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStoragePolicy(path); !errors.Is(err, ErrPermission) {
		t.Fatalf("writable policy err=%v", err)
	}
}

func TestStorageIdentifiersAndUnitNamesAreBounded(t *testing.T) {
	for _, id := range []string{"ok", "data_1", "fixture-2"} {
		if !ValidStorageResourceID(id) {
			t.Fatalf("rejected %q", id)
		}
	}
	for _, id := range []string{"", "../data", "UPPER", "space here"} {
		if ValidStorageResourceID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
	if got := mountUnitName("/mnt/kitpro qualification"); got != "mnt-kitpro\\x20qualification.mount" {
		t.Fatalf("unit=%q", got)
	}
}

func TestPersistentMountConfigurationParsingIsBoundedAndReadOnly(t *testing.T) {
	items, err := parseFSTab([]byte("# comment\nUUID=abc /mnt/data ext4 defaults,nodev 0 2\nserver:/share /mnt/network nfs4 ro,_netdev 0 0\nmalformed\n"))
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Source != "UUID=abc" || items[0].Target != "/mnt/data" || items[1].Filesystem != "nfs4" {
		t.Fatalf("items=%+v", items)
	}
}

func TestPersistentHealthReportsOnlyConfiguredResources(t *testing.T) {
	resource := StoragePolicyResource{ID: "archive", Source: "/dev/disk/by-uuid/test-uuid", UUID: "test-uuid", Target: "/mnt/archive", Filesystem: "ext4", Actions: []StorageAction{StorageActionPersistEnable, StorageActionPersistDisable}}
	manager := NewStorageManager(StoragePolicyDocument{Version: 1, Resources: []StoragePolicyResource{resource}})
	manager.ReadMountInfo = func() ([]byte, error) { return []byte(""), nil }
	manager.ValidateSource = func(string) (string, error) { return "", ErrUnavailable }
	manager.Persistence = &healthPersistenceController{status: PersistentMountStatus{Enabled: true, State: "enabled", Health: "healthy"}}

	health := manager.PersistentHealth(context.Background())
	if len(health) != 1 || health[0].ResourceID != resource.ID || health[0].Mounted || health[0].SourceAvailable {
		t.Fatalf("unexpected persistent health: %#v", health)
	}

	manager.Persistence = &healthPersistenceController{status: PersistentMountStatus{State: "disabled", Health: "not_configured"}}
	if health = manager.PersistentHealth(context.Background()); len(health) != 0 {
		t.Fatalf("disabled persistence should be omitted: %#v", health)
	}
}
