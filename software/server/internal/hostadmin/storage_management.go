package hostadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/coreos/go-systemd/v22/unit"
	godbus "github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const MaxStoragePolicyResources = 64

type StorageAction string

const (
	StorageActionMount          StorageAction = "mount"
	StorageActionUnmount        StorageAction = "unmount"
	StorageActionPersistEnable  StorageAction = "enable_persistence"
	StorageActionPersistDisable StorageAction = "disable_persistence"
	StorageActionPersistRecover StorageAction = "recover_persistence"
)

type StoragePolicyDocument struct {
	Version   int                     `json:"version"`
	Resources []StoragePolicyResource `json:"resources"`
}

type StoragePolicyResource struct {
	ID         string          `json:"id"`
	Source     string          `json:"source"`
	UUID       string          `json:"uuid,omitempty"`
	Target     string          `json:"target"`
	Filesystem string          `json:"filesystem"`
	Options    []string        `json:"options,omitempty"`
	Actions    []StorageAction `json:"actions"`
}

type StorageEligibility struct {
	Allowed      bool     `json:"allowed"`
	State        string   `json:"state"`
	Reason       string   `json:"reason"`
	Protected    bool     `json:"protected"`
	Dependencies []string `json:"dependencies,omitempty"`
	Risk         string   `json:"risk"`
}

type StorageActionPreview struct {
	ResourceID     string                 `json:"resource_id"`
	Source         string                 `json:"source"`
	Target         string                 `json:"target"`
	Filesystem     string                 `json:"filesystem_type"`
	Options        []string               `json:"options,omitempty"`
	Action         StorageAction          `json:"action"`
	Mounted        bool                   `json:"mounted"`
	ObservedSource string                 `json:"observed_source,omitempty"`
	Eligibility    StorageEligibility     `json:"eligibility"`
	ExpectedResult string                 `json:"expected_result"`
	Confirmation   string                 `json:"confirmation"`
	Persistence    *PersistentMountStatus `json:"persistence,omitempty"`
}

type StorageMountState struct {
	Mounted    bool   `json:"mounted"`
	Source     string `json:"source,omitempty"`
	Target     string `json:"target"`
	Filesystem string `json:"filesystem_type,omitempty"`
	Options    string `json:"options,omitempty"`
}

type StorageMutationResult struct {
	ResourceID string            `json:"resource_id"`
	Action     StorageAction     `json:"action"`
	Previous   StorageMountState `json:"previous"`
	Result     StorageMountState `json:"result"`
	Status     string            `json:"status"`
}

type StorageMutationError struct {
	Code, Status, Detail string
}

func (e *StorageMutationError) Error() string { return e.Detail }

func ValidStorageResourceID(id string) bool {
	if len(id) < 1 || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func ValidStorageAction(action StorageAction) bool {
	switch action {
	case StorageActionMount, StorageActionUnmount, StorageActionPersistEnable, StorageActionPersistDisable, StorageActionPersistRecover:
		return true
	default:
		return false
	}
}

func LoadStoragePolicy(path string) (StoragePolicyDocument, error) {
	if path == "" {
		return StoragePolicyDocument{}, ErrUnsupported
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EACCES) {
			return StoragePolicyDocument{}, ErrPermission
		}
		return StoragePolicyDocument{}, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return StoragePolicyDocument{}, ErrPermission
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return StoragePolicyDocument{}, ErrPermission
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return StoragePolicyDocument{}, ErrUnavailable
	}
	var doc StoragePolicyDocument
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || decoder.Decode(&struct{}{}) != io.EOF || doc.Version != 1 || len(doc.Resources) > MaxStoragePolicyResources {
		return StoragePolicyDocument{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, resource := range doc.Resources {
		if err := validateStoragePolicyResource(resource); err != nil || seen[resource.ID] {
			return StoragePolicyDocument{}, ErrInvalid
		}
		seen[resource.ID] = true
	}
	return doc, nil
}

func validateStoragePolicyResource(r StoragePolicyResource) error {
	if !ValidStorageResourceID(r.ID) || !filepath.IsAbs(r.Target) || filepath.Clean(r.Target) != r.Target || len(r.Target) > 512 || strings.ContainsAny(r.Target, "\\%\x00\r\n") {
		return ErrInvalid
	}
	if r.Source == "" || len(r.Source) > 512 || strings.ContainsAny(r.Source, "%\x00\r\n") {
		return ErrInvalid
	}
	allowedBase := false
	for _, base := range []string{"/mnt", "/media", "/data", "/srv"} {
		if strings.HasPrefix(r.Target, base+string(filepath.Separator)) {
			allowedBase = true
			break
		}
	}
	if !allowedBase {
		return ErrInvalid
	}
	if r.UUID != "" && (len(r.UUID) > 128 || strings.ContainsAny(r.UUID, "/\\%\x00\r\n")) {
		return ErrInvalid
	}
	switch r.Filesystem {
	case "tmpfs", "ext4", "xfs", "btrfs":
	default:
		return ErrInvalid
	}
	if r.Filesystem == "tmpfs" {
		if r.Source != "tmpfs" || r.UUID != "" {
			return ErrInvalid
		}
	} else if r.UUID == "" || r.Source != "/dev/disk/by-uuid/"+r.UUID {
		return ErrInvalid
	}
	if len(r.Actions) == 0 || len(r.Actions) > 5 || len(r.Options) > 8 {
		return ErrInvalid
	}
	actions := map[StorageAction]bool{}
	for _, action := range r.Actions {
		if !ValidStorageAction(action) || actions[action] {
			return ErrInvalid
		}
		actions[action] = true
	}
	options := map[string]bool{}
	for _, option := range r.Options {
		switch option {
		case "ro", "nodev", "nosuid", "noexec":
		default:
			return ErrInvalid
		}
		if options[option] {
			return ErrInvalid
		}
		options[option] = true
	}
	return nil
}

type MountKernel interface {
	Mount(source, target, filesystem string, flags uintptr, data string) error
	Unmount(target string, flags int) error
	Owned(target string) (bool, error)
}

type SystemdMountKernel struct{}

func mountUnitName(target string) string { return unit.UnitNamePathEscape(target) + ".mount" }
func (SystemdMountKernel) Mount(source, target, filesystem string, flags uintptr, data string) error {
	if data != "" && data != "mode=0755" {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := systemdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	options := []string{}
	if flags&unix.MS_RDONLY != 0 {
		options = append(options, "ro")
	}
	if flags&unix.MS_NODEV != 0 {
		options = append(options, "nodev")
	}
	if flags&unix.MS_NOSUID != 0 {
		options = append(options, "nosuid")
	}
	if flags&unix.MS_NOEXEC != 0 {
		options = append(options, "noexec")
	}
	if data != "" {
		options = append(options, data)
	}
	props := []systemdbus.Property{{Name: "Description", Value: godbus.MakeVariant("KITPro approved temporary mount")}, {Name: "What", Value: godbus.MakeVariant(source)}, {Name: "Where", Value: godbus.MakeVariant(target)}, {Name: "Type", Value: godbus.MakeVariant(filesystem)}, {Name: "Options", Value: godbus.MakeVariant(strings.Join(options, ","))}}
	result := make(chan string, 1)
	if _, err = conn.StartTransientUnitContext(ctx, mountUnitName(target), "fail", props, result); err != nil {
		return err
	}
	select {
	case outcome := <-result:
		if outcome != "done" {
			return fmt.Errorf("systemd mount job %s", outcome)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (SystemdMountKernel) Unmount(target string, flags int) error {
	if flags != 0 {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := systemdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	result := make(chan string, 1)
	if _, err = conn.StopUnitContext(ctx, mountUnitName(target), "fail", result); err != nil {
		return err
	}
	select {
	case outcome := <-result:
		if outcome != "done" {
			return fmt.Errorf("systemd unmount job %s", outcome)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (SystemdMountKernel) Owned(target string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := systemdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	properties, err := conn.GetUnitPropertiesContext(ctx, mountUnitName(target))
	if err != nil {
		return false, err
	}
	transient, transientOK := properties["Transient"].(bool)
	description, descriptionOK := properties["Description"].(string)
	return transientOK && transient && descriptionOK && description == "KITPro approved temporary mount", nil
}

type StorageManager struct {
	Policy         StoragePolicyDocument
	ReadMountInfo  func() ([]byte, error)
	ValidateTarget func(string) error
	ResolveSource  func(string) (string, error)
	ValidateSource func(string) (string, error)
	Kernel         MountKernel
	Persistence    PersistenceController
	locks          sync.Map
}

func NewStorageManager(policy StoragePolicyDocument) *StorageManager {
	return &StorageManager{Policy: policy, ReadMountInfo: func() ([]byte, error) { return readStorageFile("/proc/1/mountinfo", maxStorageOutput) }, ValidateTarget: validateTrustedStorageTarget, ResolveSource: filepath.EvalSymlinks, ValidateSource: ValidatePersistentStorageSourceFromUdev, Kernel: SystemdMountKernel{}, Persistence: NewSystemdPersistenceController()}
}

func validatePersistentStorageSource(source string) (string, error) {
	if !strings.HasPrefix(source, "/dev/disk/by-uuid/") {
		return "", ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !strings.HasPrefix(resolved, "/dev/") {
		return "", ErrUnavailable
	}
	var stat unix.Stat_t
	if unix.Stat(resolved, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFBLK {
		return "", ErrInvalid
	}
	return resolved, nil
}

func validatePersistentStorageSourceDocument(source string, raw []byte) (string, error) {
	if !strings.HasPrefix(source, "/dev/disk/by-uuid/") {
		return "", ErrInvalid
	}
	uuid := strings.TrimPrefix(source, "/dev/disk/by-uuid/")
	var document lsblkDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", ErrInvalid
	}
	matches := []string{}
	var visit func([]lsblkDevice)
	visit = func(nodes []lsblkDevice) {
		for _, node := range nodes {
			if node.UUID == uuid && strings.HasPrefix(node.Path, "/dev/") {
				matches = append(matches, node.Path)
			}
			visit(node.Children)
		}
	}
	visit(document.BlockDevices)
	if len(matches) != 1 {
		return "", ErrUnavailable
	}
	return matches[0], nil
}

func ValidatePersistentStorageSourceFromInventory(source string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := (fixedStorageRunner{}).Run(ctx, "lsblk")
	if err != nil {
		return "", ErrUnavailable
	}
	return validatePersistentStorageSourceDocument(source, raw)
}

func validatePersistentStorageSourceFromUdevRoots(source, sysBlockRoot, udevDataRoot string) (string, error) {
	if !strings.HasPrefix(source, "/dev/disk/by-uuid/") {
		return "", ErrInvalid
	}
	uuid := strings.TrimPrefix(source, "/dev/disk/by-uuid/")
	entries, err := os.ReadDir(sysBlockRoot)
	if err != nil || len(entries) > 1024 {
		return "", ErrUnavailable
	}
	matches := []string{}
	for _, entry := range entries {
		if len(entry.Name()) == 0 || len(entry.Name()) > 128 || strings.ContainsAny(entry.Name(), "/\\\x00\r\n") {
			continue
		}
		devRaw, readErr := os.ReadFile(filepath.Join(sysBlockRoot, entry.Name(), "dev"))
		if readErr != nil || len(devRaw) > 32 {
			continue
		}
		majorMinor := strings.TrimSpace(string(devRaw))
		parts := strings.Split(majorMinor, ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		validDigits := true
		for _, part := range parts {
			for _, r := range part {
				if r < '0' || r > '9' {
					validDigits = false
				}
			}
		}
		if !validDigits {
			continue
		}
		data, readErr := readBoundedRegular(filepath.Join(udevDataRoot, "b"+majorMinor), 64*1024)
		if readErr != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "E:ID_FS_UUID="+uuid {
				matches = append(matches, "/dev/"+entry.Name())
				break
			}
		}
	}
	if len(matches) != 1 {
		return "", ErrUnavailable
	}
	return matches[0], nil
}

func ValidatePersistentStorageSourceFromUdev(source string) (string, error) {
	return validatePersistentStorageSourceFromUdevRoots(source, "/sys/class/block", "/run/udev/data")
}

func (m *StorageManager) Resources() []StoragePolicyResource {
	result := append([]StoragePolicyResource(nil), m.Policy.Resources...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func storagePolicyHasAction(resource StoragePolicyResource, action StorageAction) bool {
	for _, candidate := range resource.Actions {
		if candidate == action {
			return true
		}
	}
	return false
}

// PersistentHealth returns bounded observations for configured or incomplete
// KITPro-managed persistent mounts. Disabled resources are intentionally omitted.
func (m *StorageManager) PersistentHealth(ctx context.Context) []PersistentStorageHealthInput {
	if m == nil || m.Persistence == nil {
		return nil
	}
	raw, err := m.ReadMountInfo()
	if err != nil {
		return nil
	}
	records, err := parseMountInfo(raw)
	if err != nil {
		return nil
	}
	result := make([]PersistentStorageHealthInput, 0, len(m.Policy.Resources))
	for _, resource := range m.Resources() {
		if !storagePolicyHasAction(resource, StorageActionPersistEnable) && !storagePolicyHasAction(resource, StorageActionPersistDisable) && !storagePolicyHasAction(resource, StorageActionPersistRecover) {
			continue
		}
		select {
		case <-ctx.Done():
			return result
		default:
		}
		status, statusErr := m.Persistence.Status(resource)
		if statusErr != nil {
			result = append(result, PersistentStorageHealthInput{ResourceID: resource.ID, Target: resource.Target, State: "unknown", Health: "unavailable", Detail: "persistent configuration status unavailable"})
			continue
		}
		if status.State == "disabled" && !status.Enabled {
			continue
		}
		mounted := false
		for _, record := range records {
			if record.Point == resource.Target && record.FSType == resource.Filesystem && storageSourcesMatch(resource.Source, record.Source) {
				mounted = true
				break
			}
		}
		sourceAvailable := false
		validateSource := m.ValidateSource
		if validateSource == nil {
			validateSource = m.ResolveSource
		}
		if validateSource != nil {
			resolved, sourceErr := validateSource(resource.Source)
			sourceAvailable = sourceErr == nil && strings.HasPrefix(resolved, "/dev/")
		}
		result = append(result, PersistentStorageHealthInput{ResourceID: resource.ID, Target: resource.Target, State: status.State, Health: status.Health, Mounted: mounted, SourceAvailable: sourceAvailable})
	}
	return result
}

func (m *StorageManager) resource(id string) (StoragePolicyResource, bool) {
	for _, item := range m.Policy.Resources {
		if item.ID == id {
			return item, true
		}
	}
	return StoragePolicyResource{}, false
}

var protectedMountPaths = []string{"/", "/boot", "/boot/efi", "/efi", "/etc", "/usr", "/srv/kitpro", "/var/lib/kitpro-api", "/var/lib/kitpro-helper", "/var/lib/docker", "/var/lib/containerd", "/run", "/proc", "/sys", "/dev"}

func storageSourcesMatch(expected, observed string) bool {
	if expected == observed {
		return true
	}
	if !filepath.IsAbs(expected) || !filepath.IsAbs(observed) {
		return false
	}
	expectedResolved, expectedErr := filepath.EvalSymlinks(expected)
	observedResolved, observedErr := filepath.EvalSymlinks(observed)
	if expectedErr == nil && observedErr == nil && expectedResolved == observedResolved {
		return true
	}
	if strings.HasPrefix(expected, "/dev/disk/by-uuid/") {
		if destination, err := os.Readlink(expected); err == nil {
			return filepath.Clean(filepath.Join(filepath.Dir(expected), destination)) == filepath.Clean(observed)
		}
	}
	return false
}

func validateTrustedStorageTarget(target string) error {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return ErrInvalid
	}
	current := "/"
	for _, component := range strings.Split(strings.TrimPrefix(target, "/"), "/") {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnavailable
		}
		var stat unix.Stat_t
		if unix.Lstat(current, &stat) != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode().Perm()&0022 != 0 {
			return ErrPermission
		}
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func (m *StorageManager) Preview(ctx context.Context, id string, action StorageAction) (StorageActionPreview, error) {
	if !ValidStorageResourceID(id) || !ValidStorageAction(action) {
		return StorageActionPreview{}, ErrInvalid
	}
	resource, ok := m.resource(id)
	if !ok {
		return StorageActionPreview{}, ErrMissing
	}
	expected := map[StorageAction]string{StorageActionMount: "filesystem mounted at approved target", StorageActionUnmount: "approved mount absent", StorageActionPersistEnable: "KITPro-managed persistent mount enabled", StorageActionPersistDisable: "KITPro-managed persistent mount disabled", StorageActionPersistRecover: "incomplete KITPro persistence transaction removed"}
	preview := StorageActionPreview{ResourceID: id, Source: resource.Source, Target: resource.Target, Filesystem: resource.Filesystem, Options: append([]string(nil), resource.Options...), Action: action, Confirmation: id, ExpectedResult: expected[action]}
	deny := func(state, reason string, protected bool) (StorageActionPreview, error) {
		preview.Eligibility = StorageEligibility{State: state, Reason: reason, Protected: protected, Risk: "denied"}
		return preview, nil
	}
	allowedAction := false
	for _, candidate := range resource.Actions {
		if candidate == action {
			allowedAction = true
		}
	}
	if !allowedAction {
		return deny("not_approved", "The policy does not approve this action.", false)
	}
	for _, protected := range protectedMountPaths {
		if (protected == "/" && resource.Target == "/") || (protected != "/" && pathsOverlap(resource.Target, protected)) {
			return deny("protected", "The target overlaps protected system or application storage.", true)
		}
	}
	select {
	case <-ctx.Done():
		return StorageActionPreview{}, ErrUnavailable
	default:
	}
	resolved, err := filepath.EvalSymlinks(resource.Target)
	if err != nil || resolved != resource.Target || m.ValidateTarget == nil || m.ValidateTarget(resource.Target) != nil {
		return deny("unsafe_target", "The approved target is unavailable or resolves through a symbolic link.", false)
	}
	info, err := os.Stat(resource.Target)
	if err != nil || !info.IsDir() {
		return deny("unsafe_target", "The approved target is not a directory.", false)
	}
	raw, err := m.ReadMountInfo()
	if err != nil {
		return StorageActionPreview{}, ErrUnavailable
	}
	records, err := parseMountInfo(raw)
	if err != nil {
		return StorageActionPreview{}, err
	}
	var current *mountRecord
	for i := range records {
		record := &records[i]
		if record.Point == resource.Target {
			current = record
		}
		if record.Point != resource.Target && strings.HasPrefix(record.Point, resource.Target+string(os.PathSeparator)) {
			preview.Eligibility.Dependencies = append(preview.Eligibility.Dependencies, record.Point)
		}
	}
	preview.Mounted = current != nil
	if current != nil {
		preview.ObservedSource = current.Source
	}
	if action == StorageActionPersistEnable || action == StorageActionPersistDisable || action == StorageActionPersistRecover {
		if resource.Filesystem == "tmpfs" || resource.UUID == "" || resource.Source != "/dev/disk/by-uuid/"+resource.UUID {
			return deny("unstable_identity", "Persistent mounts require an exact UUID-backed block filesystem.", true)
		}
		validateSource := m.ValidateSource
		if validateSource == nil {
			validateSource = m.ResolveSource
		}
		resolvedSource, sourceErr := validateSource(resource.Source)
		if action == StorageActionPersistEnable && (sourceErr != nil || !strings.HasPrefix(resolvedSource, "/dev/")) {
			return deny("source_unavailable", "The approved UUID-backed source is unavailable or ambiguous.", false)
		}
		if m.Persistence == nil {
			return deny("persistence_unavailable", "Persistent storage management is unavailable.", false)
		}
		status, statusErr := m.Persistence.Status(resource)
		if statusErr != nil {
			return StorageActionPreview{}, ErrUnavailable
		}
		preview.Persistence = &status
		switch action {
		case StorageActionPersistEnable:
			if status.Enabled {
				return deny("already_persistent", "KITPro persistence is already enabled.", false)
			}
			if status.State != "disabled" {
				return deny("recovery_required", "Existing configuration must be recovered before enabling persistence.", true)
			}
			if current != nil && (!storageSourcesMatch(resource.Source, current.Source) || current.FSType != resource.Filesystem) {
				return deny("identity_mismatch", "The active mount does not match the approved identity.", true)
			}
			if current == nil {
				entries, readErr := os.ReadDir(resource.Target)
				if readErr != nil || len(entries) != 0 {
					return deny("target_not_empty", "The approved target must be empty before enabling persistence.", true)
				}
			}
		case StorageActionPersistDisable:
			if len(preview.Eligibility.Dependencies) > 0 {
				return deny("dependent_mounts", "The target has descendant mounts.", true)
			}
			if !status.Enabled {
				return deny("not_persistent", "No healthy KITPro-managed persistent configuration exists.", false)
			}
		case StorageActionPersistRecover:
			if len(preview.Eligibility.Dependencies) > 0 {
				return deny("dependent_mounts", "The target has descendant mounts.", true)
			}
			if status.State != "needs_recovery" {
				return deny("recovery_not_required", "No incomplete KITPro persistence transaction requires recovery.", false)
			}
			if current != nil {
				return deny("mounted_recovery_denied", "Recovery cannot remove configuration while the filesystem is mounted.", true)
			}
		}
		preview.Eligibility = StorageEligibility{Allowed: true, State: "eligible", Reason: "The exact persistence policy passed current safety checks.", Risk: "controlled"}
		return preview, nil
	}
	if m.Persistence != nil && resource.Filesystem != "tmpfs" {
		status, statusErr := m.Persistence.Status(resource)
		if statusErr != nil {
			return StorageActionPreview{}, ErrUnavailable
		}
		preview.Persistence = &status
		if status.Enabled || status.State != "disabled" {
			return deny("persistent_configuration", "Use the persistent storage actions while a persistent configuration exists or requires recovery.", status.State != "disabled")
		}
	}
	if len(preview.Eligibility.Dependencies) > 0 {
		return deny("dependent_mounts", "The target has ancestor or descendant mounts.", true)
	}
	if action == StorageActionMount {
		if current != nil {
			if storageSourcesMatch(resource.Source, current.Source) && current.FSType == resource.Filesystem {
				return deny("already_mounted", "The approved filesystem is already mounted.", false)
			}
			return deny("mount_conflict", "A different filesystem is mounted at the approved target.", true)
		}
		entries, readErr := os.ReadDir(resource.Target)
		if readErr != nil || len(entries) != 0 {
			return deny("target_not_empty", "The approved target must be empty before mounting.", true)
		}
	} else {
		if current == nil {
			return deny("not_mounted", "The approved filesystem is not mounted.", false)
		}
		if !storageSourcesMatch(resource.Source, current.Source) || current.FSType != resource.Filesystem {
			return deny("identity_mismatch", "The active mount does not match the approved identity.", true)
		}
		if m.Kernel == nil {
			return deny("ownership_unknown", "KITPro cannot establish ownership of the active mount.", true)
		}
		owned, ownershipErr := m.Kernel.Owned(resource.Target)
		if ownershipErr != nil || !owned {
			return deny("not_kitpro_managed", "Only a temporary mount created by KITPro can be unmounted.", true)
		}
	}
	preview.Eligibility = StorageEligibility{Allowed: true, State: "eligible", Reason: "The exact policy resource passed current safety checks.", Risk: "controlled"}
	return preview, nil
}

func storageMountFlags(options []string) uintptr {
	var flags uintptr
	for _, option := range options {
		switch option {
		case "ro":
			flags |= unix.MS_RDONLY
		case "nodev":
			flags |= unix.MS_NODEV
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "noexec":
			flags |= unix.MS_NOEXEC
		}
	}
	return flags
}

func (m *StorageManager) Mutate(ctx context.Context, id string, action StorageAction) (StorageMutationResult, error) {
	lockValue, _ := m.locks.LoadOrStore(id, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	preview, err := m.Preview(ctx, id, action)
	if err != nil {
		return StorageMutationResult{}, err
	}
	if !preview.Eligibility.Allowed {
		return StorageMutationResult{}, &StorageMutationError{Code: "StorageActionDenied", Status: "rejected", Detail: preview.Eligibility.Reason}
	}
	resource, _ := m.resource(id)
	previous := StorageMountState{Mounted: preview.Mounted, Source: preview.ObservedSource, Target: resource.Target, Filesystem: resource.Filesystem}
	if action == StorageActionMount {
		data := ""
		if resource.Filesystem == "tmpfs" {
			data = "mode=0755"
		}
		err = m.Kernel.Mount(resource.Source, resource.Target, resource.Filesystem, storageMountFlags(resource.Options), data)
	} else {
		err = m.Kernel.Unmount(resource.Target, 0)
	}
	if err != nil {
		code := "StorageActionFailed"
		status := "failed"
		detail := "storage operation failed"
		if errors.Is(err, unix.EBUSY) {
			code = "StorageBusy"
			detail = "filesystem is busy"
		}
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			code = "StoragePermissionDenied"
			detail = "storage operation permission denied"
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "StorageActionTimedOut"
			status = "timed_out"
			detail = "storage operation timed out; the resulting state is uncertain"
		}
		return StorageMutationResult{ResourceID: id, Action: action, Previous: previous, Status: status}, &StorageMutationError{Code: code, Status: status, Detail: detail}
	}
	post, postErr := m.Preview(ctx, id, action)
	result := StorageMountState{Mounted: post.Mounted, Source: post.ObservedSource, Target: resource.Target, Filesystem: resource.Filesystem}
	expected := action == StorageActionMount
	if postErr != nil || result.Mounted != expected {
		return StorageMutationResult{ResourceID: id, Action: action, Previous: previous, Result: result, Status: "outcome_unknown"}, &StorageMutationError{Code: "StorageActionOutcomeUnknown", Status: "outcome_unknown", Detail: "the resulting kernel mount state could not be confirmed"}
	}
	return StorageMutationResult{ResourceID: id, Action: action, Previous: previous, Result: result, Status: "completed"}, nil
}

func (m *StorageManager) MutatePersistence(ctx context.Context, id string, action StorageAction) (PersistentMountResult, error) {
	lockValue, _ := m.locks.LoadOrStore(id, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	preview, err := m.Preview(ctx, id, action)
	if err != nil {
		return PersistentMountResult{}, err
	}
	if !preview.Eligibility.Allowed {
		return PersistentMountResult{}, &StorageMutationError{Code: "StorageActionDenied", Status: "rejected", Detail: preview.Eligibility.Reason}
	}
	resource, _ := m.resource(id)
	var result PersistentMountResult
	switch action {
	case StorageActionPersistEnable:
		result, err = m.Persistence.Enable(ctx, resource)
	case StorageActionPersistDisable:
		result, err = m.Persistence.Disable(ctx, resource)
	case StorageActionPersistRecover:
		result, err = m.Persistence.Recover(ctx, resource)
	default:
		return PersistentMountResult{}, ErrInvalid
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, &StorageMutationError{Code: "StorageActionTimedOut", Status: "timed_out", Detail: "the persistent storage operation timed out with an uncertain outcome"}
	}
	if result.Status == "outcome_unknown" {
		return result, &StorageMutationError{Code: "StorageActionOutcomeUnknown", Status: "outcome_unknown", Detail: "the persistent storage operation outcome could not be confirmed"}
	}
	return result, err
}

func (r StoragePolicyResource) String() string { return fmt.Sprintf("%s:%s", r.ID, r.Target) }
