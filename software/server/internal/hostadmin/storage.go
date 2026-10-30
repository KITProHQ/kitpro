package hostadmin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	MaxStorageDevices      = 4096
	MaxStorageFilesystems  = 4096
	maxStorageOutput       = 4 * 1024 * 1024
	storageWarningPercent  = 85
	storageCriticalPercent = 95
)

type StorageSummary struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	MountedCount   int    `json:"mounted_filesystem_count"`
	DeviceCount    int    `json:"device_count"`
	WarningCount   int    `json:"warning_count"`
	CapacityScope  string `json:"capacity_scope"`
}

type BlockDevice struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Category       string   `json:"category"`
	DeviceType     string   `json:"device_type"`
	ParentID       string   `json:"parent_id,omitempty"`
	Model          string   `json:"model,omitempty"`
	Vendor         string   `json:"vendor,omitempty"`
	Transport      string   `json:"transport,omitempty"`
	SizeBytes      uint64   `json:"size_bytes"`
	Rotational     *bool    `json:"rotational,omitempty"`
	ReadOnly       bool     `json:"read_only"`
	Removable      bool     `json:"removable"`
	PartitionCount int      `json:"partition_count"`
	FilesystemType string   `json:"filesystem_type,omitempty"`
	UUID           string   `json:"uuid,omitempty"`
	Label          string   `json:"label,omitempty"`
	MountPoints    []string `json:"mount_points"`
	Mounted        bool     `json:"mounted"`
	Protection     []string `json:"protection,omitempty"`
	Status         Status   `json:"status"`
}

type Filesystem struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	Source         string   `json:"source"`
	DeviceID       string   `json:"device_id,omitempty"`
	UUID           string   `json:"uuid,omitempty"`
	Label          string   `json:"label,omitempty"`
	MountPoint     string   `json:"mount_point"`
	MountRoot      string   `json:"mount_root,omitempty"`
	TotalBytes     *uint64  `json:"total_bytes,omitempty"`
	UsedBytes      *uint64  `json:"used_bytes,omitempty"`
	AvailableBytes *uint64  `json:"available_bytes,omitempty"`
	UsagePercent   *float64 `json:"usage_percent,omitempty"`
	ReadOnly       bool     `json:"read_only"`
	Bind           bool     `json:"bind"`
	Network        bool     `json:"network"`
	MemoryBacked   bool     `json:"memory_backed"`
	Virtual        bool     `json:"virtual"`
	Protection     []string `json:"protection,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	Status         Status   `json:"status"`
}

type LVMVolume struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	Path      string `json:"path,omitempty"`
	SizeBytes uint64 `json:"size_bytes"`
	Kind      string `json:"kind,omitempty"`
}

type ZFSPool struct {
	Name           string `json:"name"`
	SizeBytes      uint64 `json:"size_bytes"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	Health         string `json:"health,omitempty"`
}

type ZFSDataset struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	MountPoint     string `json:"mount_point,omitempty"`
}

type PersistentMount struct {
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Filesystem string   `json:"filesystem_type"`
	Options    []string `json:"options"`
}

type StorageInventory struct {
	Summary          StorageSummary         `json:"summary"`
	Capabilities     map[string]Status      `json:"capabilities"`
	Devices          []BlockDevice          `json:"devices"`
	Filesystems      []Filesystem           `json:"filesystems"`
	LVMVolumes       []LVMVolume            `json:"lvm_volumes,omitempty"`
	ZFSPools         []ZFSPool              `json:"zfs_pools,omitempty"`
	ZFSDatasets      []ZFSDataset           `json:"zfs_datasets,omitempty"`
	PersistentMounts []PersistentMount      `json:"persistent_mounts,omitempty"`
	Management       []StorageActionPreview `json:"management,omitempty"`
}

type StorageStat struct {
	TotalBytes, FreeBytes, AvailableBytes uint64
}

type StorageRunner interface {
	Run(context.Context, string) ([]byte, error)
}

type StorageProvider struct {
	Runner        StorageRunner
	ReadMountInfo func() ([]byte, error)
	ReadSwaps     func() ([]byte, error)
	ReadFSTab     func() ([]byte, error)
	StatFS        func(string) (StorageStat, error)
}

func NewStorageProvider() StorageProvider {
	return StorageProvider{
		Runner:        fixedStorageRunner{},
		ReadMountInfo: func() ([]byte, error) { return readStorageFile("/proc/1/mountinfo", maxStorageOutput) },
		ReadSwaps:     func() ([]byte, error) { return readStorageFile("/proc/swaps", 256*1024) },
		ReadFSTab:     func() ([]byte, error) { return readStorageFile("/etc/fstab", 256*1024) },
		StatFS:        storageStatFS,
	}
}

func readStorageFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrUnavailable
	}
	return b, nil
}

func storageStatFS(path string) (StorageStat, error) {
	var value unix.Statfs_t
	if err := unix.Statfs(path, &value); err != nil {
		return StorageStat{}, err
	}
	block := uint64(value.Bsize)
	return StorageStat{TotalBytes: block * value.Blocks, FreeBytes: block * value.Bfree, AvailableBytes: block * value.Bavail}, nil
}

type fixedStorageRunner struct{}

var storageCommands = map[string]struct {
	paths []string
	args  []string
}{
	"lsblk": {[]string{"/usr/bin/lsblk"}, []string{"--json", "--bytes", "--paths", "--output", "NAME,KNAME,PATH,TYPE,PKNAME,MAJ:MIN,SIZE,ROTA,RO,RM,MODEL,VENDOR,TRAN,FSTYPE,UUID,LABEL,MOUNTPOINTS"}},
	"lvm":   {[]string{"/usr/bin/lvs", "/usr/sbin/lvs"}, []string{"--reportformat", "json", "--units", "b", "--nosuffix", "--options", "lv_name,vg_name,lv_path,lv_size,lv_attr,pool_lv,origin"}},
	"zpool": {[]string{"/usr/bin/zpool", "/usr/sbin/zpool"}, []string{"list", "-H", "-p", "-o", "name,size,alloc,free,health"}},
	"zfs":   {[]string{"/usr/bin/zfs", "/usr/sbin/zfs"}, []string{"list", "-H", "-p", "-o", "name,type,used,available,mountpoint"}},
}

func (fixedStorageRunner) Run(ctx context.Context, name string) ([]byte, error) {
	spec, ok := storageCommands[name]
	if !ok {
		return nil, ErrInvalid
	}
	path := ""
	for _, candidate := range spec.paths {
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		} else if errors.Is(err, os.ErrPermission) {
			return nil, ErrPermission
		}
	}
	if path == "" {
		return nil, ErrUnsupported
	}
	cmd := exec.CommandContext(ctx, path, spec.args...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/usr/sbin"}
	out := &limitedBuffer{Max: maxStorageOutput}
	errOut := &limitedBuffer{Max: 2048}
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Run(); err != nil {
		message := strings.ToLower(errOut.String())
		if strings.Contains(message, "permission") || strings.Contains(message, "access denied") || strings.Contains(message, "not permitted") {
			return nil, ErrPermission
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrUnavailable
		}
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

type lsblkDocument struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}
type lsblkDevice struct {
	Name        string        `json:"name"`
	KName       string        `json:"kname"`
	Path        string        `json:"path"`
	Type        string        `json:"type"`
	PKName      string        `json:"pkname"`
	MajorMinor  string        `json:"maj:min"`
	Size        uint64        `json:"size"`
	Rota        *bool         `json:"rota"`
	RO          bool          `json:"ro"`
	RM          bool          `json:"rm"`
	Model       string        `json:"model"`
	Vendor      string        `json:"vendor"`
	Tran        string        `json:"tran"`
	FSType      string        `json:"fstype"`
	UUID        string        `json:"uuid"`
	Label       string        `json:"label"`
	MountPoints []*string     `json:"mountpoints"`
	Children    []lsblkDevice `json:"children"`
}

type mountRecord struct {
	MajorMinor, Root, Point, Options, FSType, Source, SuperOptions string
}

func parseMountInfo(raw []byte) ([]mountRecord, error) {
	if len(raw) > maxStorageOutput {
		return nil, ErrUnavailable
	}
	var records []mountRecord
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || sep+3 >= len(fields) {
			continue
		}
		point, root, source := mountUnescape(fields[4]), mountUnescape(fields[3]), mountUnescape(fields[sep+2])
		if point == "" || len(point) > 512 || len(root) > 512 || len(source) > 512 || !filepath.IsAbs(point) || !utf8.ValidString(point+root+source) {
			continue
		}
		records = append(records, mountRecord{MajorMinor: fields[2], Root: root, Point: point, Options: fields[5], FSType: boundedText(fields[sep+1], 64), Source: boundedText(source, 512), SuperOptions: fields[sep+3]})
		if len(records) > MaxStorageFilesystems {
			return nil, ErrUnavailable
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, ErrUnavailable
	}
	return records, nil
}

func mountUnescape(value string) string {
	for code, replacement := range map[string]string{"040": " ", "011": "\t", "012": "\n", "134": "\\"} {
		value = strings.ReplaceAll(value, "\\"+code, replacement)
	}
	return value
}

func safeStorageText(value string, max int) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return ""
	}
	return boundedText(value, max)
}

func storageCategory(device lsblkDevice) string {
	switch device.Type {
	case "part":
		return "partition"
	case "loop":
		return "loop"
	case "lvm", "dm", "crypt", "md", "mpath":
		return "logical"
	case "disk":
		combined := strings.ToLower(device.Name + " " + device.KName + " " + device.Model + " " + device.Vendor + " " + device.Tran)
		base := filepath.Base(device.Path)
		if strings.Contains(combined, "virtio") || strings.Contains(combined, "qemu") || strings.HasPrefix(base, "vd") || strings.HasPrefix(base, "xvd") || strings.HasPrefix(base, "zram") {
			return "virtual_disk"
		}
		return "physical_disk"
	default:
		return "other"
	}
}

func stableDeviceID(device lsblkDevice) string {
	if value := safeStorageText(device.UUID, 128); value != "" {
		return "uuid:" + value
	}
	if value := safeStorageText(device.MajorMinor, 32); value != "" {
		return "dev:" + value
	}
	return "path:" + safeStorageText(device.Path, 256)
}

func flattenDevices(nodes []lsblkDevice, parent string, out *[]BlockDevice, byMajor map[string]string) error {
	for _, node := range nodes {
		if len(*out) >= MaxStorageDevices {
			return ErrUnavailable
		}
		id := stableDeviceID(node)
		mounts := []string{}
		for _, point := range node.MountPoints {
			if point != nil && filepath.IsAbs(*point) {
				mounts = append(mounts, safeStorageText(*point, 512))
			}
		}
		item := BlockDevice{ID: id, Name: safeStorageText(filepath.Base(node.Path), 128), Path: safeStorageText(node.Path, 256), Category: storageCategory(node), DeviceType: safeStorageText(node.Type, 32), ParentID: parent, Model: safeStorageText(node.Model, 128), Vendor: safeStorageText(node.Vendor, 128), Transport: safeStorageText(node.Tran, 64), SizeBytes: node.Size, Rotational: node.Rota, ReadOnly: node.RO, Removable: node.RM, FilesystemType: safeStorageText(node.FSType, 64), UUID: safeStorageText(node.UUID, 128), Label: safeStorageText(node.Label, 128), MountPoints: mounts, Mounted: len(mounts) > 0, Status: Status{State: Available}}
		for _, child := range node.Children {
			if child.Type == "part" {
				item.PartitionCount++
			}
		}
		*out = append(*out, item)
		if node.MajorMinor != "" {
			byMajor[node.MajorMinor] = id
		}
		if err := flattenDevices(node.Children, id, out, byMajor); err != nil {
			return err
		}
	}
	return nil
}

func fsKinds(kind string) (network, memory, virtual bool) {
	kind = strings.ToLower(kind)
	for _, prefix := range []string{"nfs", "cifs", "smb", "sshfs", "9p", "ceph", "gluster"} {
		if strings.HasPrefix(kind, prefix) {
			return true, false, false
		}
	}
	switch kind {
	case "tmpfs", "ramfs", "devtmpfs":
		return false, true, false
	case "overlay", "squashfs", "proc", "sysfs", "cgroup", "cgroup2", "debugfs", "tracefs", "securityfs", "pstore", "autofs", "mqueue", "hugetlbfs", "fusectl", "configfs", "efivarfs", "nsfs":
		return false, false, true
	}
	return false, false, false
}

func protectionFor(point, source string, swap map[string]bool) []string {
	values := []string{}
	add := func(value string) {
		for _, current := range values {
			if current == value {
				return
			}
		}
		values = append(values, value)
	}
	if point == "/" {
		add("root_filesystem")
	}
	if point == "/boot" || strings.HasPrefix(point, "/boot/") {
		add("boot")
	}
	for _, path := range []struct{ path, label string }{{"/var/lib/kitpro-api", "kitpro_state"}, {"/var/lib/kitpro-helper", "kitpro_state"}, {"/srv/kitpro", "kitpro_state"}, {"/var/lib/docker", "container_storage"}, {"/var/lib/containerd", "container_storage"}} {
		prefix := strings.TrimSuffix(point, "/") + "/"
		if point == "/" || path.path == point || strings.HasPrefix(path.path, prefix) {
			add(path.label)
		}
	}
	if swap[source] {
		add("swap")
	}
	return values
}

func parseSwaps(raw []byte) map[string]bool {
	result := map[string]bool{}
	for i, line := range strings.Split(string(raw), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			result[mountUnescape(fields[0])] = true
		}
	}
	return result
}

func parseFSTab(raw []byte) ([]PersistentMount, error) {
	if len(raw) > 256*1024 {
		return nil, ErrUnavailable
	}
	result := []PersistentMount{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		source, target, filesystem := mountUnescape(fields[0]), mountUnescape(fields[1]), boundedText(fields[2], 64)
		if len(source) > 512 || len(target) > 512 || !filepath.IsAbs(target) || strings.ContainsAny(source+target, "\x00\r\n") {
			continue
		}
		options := []string{}
		for _, option := range strings.Split(fields[3], ",") {
			if value := boundedText(option, 64); value != "" && len(options) < 32 {
				options = append(options, value)
			}
		}
		result = append(result, PersistentMount{Source: source, Target: target, Filesystem: filesystem, Options: options})
		if len(result) > 1024 {
			return nil, ErrUnavailable
		}
	}
	if scanner.Err() != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

func (p StorageProvider) Collect(ctx context.Context) (StorageInventory, error) {
	result := StorageInventory{Devices: []BlockDevice{}, Filesystems: []Filesystem{}, Capabilities: map[string]Status{}, Summary: StorageSummary{CapacityScope: "unique mounted local persistent filesystems"}}
	rawDevices, err := p.Runner.Run(ctx, "lsblk")
	if err != nil {
		return result, err
	}
	if len(rawDevices) > maxStorageOutput {
		return result, ErrUnavailable
	}
	var document lsblkDocument
	if json.Unmarshal(rawDevices, &document) != nil {
		return result, ErrUnavailable
	}
	byMajor := map[string]string{}
	if err = flattenDevices(document.BlockDevices, "", &result.Devices, byMajor); err != nil {
		return result, err
	}
	result.Capabilities["block_devices"] = Status{State: Available}
	rawMounts, err := p.ReadMountInfo()
	if err != nil {
		return result, ErrUnavailable
	}
	mounts, err := parseMountInfo(rawMounts)
	if err != nil {
		return result, err
	}
	rawSwaps, _ := p.ReadSwaps()
	swaps := parseSwaps(rawSwaps)
	deviceIndex := map[string]int{}
	devicePathIndex := map[string]int{}
	for i := range result.Devices {
		deviceIndex[result.Devices[i].ID] = i
		devicePathIndex[result.Devices[i].Path] = i
	}
	for source := range swaps {
		if index, ok := devicePathIndex[source]; ok {
			result.Devices[index].Protection = appendUnique(result.Devices[index].Protection, "swap")
		}
	}
	seenSummary := map[string]bool{}
	for i, mount := range mounts {
		network, memory, virtual := fsKinds(mount.FSType)
		id := fmt.Sprintf("mount:%d", i)
		deviceID := byMajor[mount.MajorMinor]
		if deviceID != "" {
			id = deviceID + "@" + mount.Point
		}
		readOnly := optionSet(mount.Options, "ro") || optionSet(mount.SuperOptions, "ro")
		bind := isBindMount(mount)
		fs := Filesystem{ID: id, Type: mount.FSType, Source: mount.Source, DeviceID: deviceID, MountPoint: mount.Point, MountRoot: mount.Root, ReadOnly: readOnly, Bind: bind, Network: network, MemoryBacked: memory, Virtual: virtual, Protection: protectionFor(mount.Point, mount.Source, swaps), Status: Status{State: Available}}
		if index, ok := deviceIndex[deviceID]; ok {
			fs.UUID = result.Devices[index].UUID
			fs.Label = result.Devices[index].Label
			result.Devices[index].Mounted = true
			result.Devices[index].MountPoints = appendUnique(result.Devices[index].MountPoints, mount.Point)
			result.Devices[index].Protection = appendUnique(result.Devices[index].Protection, fs.Protection...)
		}
		stat, statErr := p.StatFS(mount.Point)
		if statErr != nil {
			fs.Status = Status{State: Unavailable, Detail: "filesystem capacity unavailable"}
			if !memory && !virtual {
				fs.Warnings = append(fs.Warnings, "capacity_unavailable")
			}
		} else {
			used := uint64(0)
			if stat.TotalBytes >= stat.FreeBytes {
				used = stat.TotalBytes - stat.FreeBytes
			}
			usage := float64(0)
			if stat.TotalBytes > 0 {
				usage = 100 * float64(used) / float64(stat.TotalBytes)
			}
			fs.TotalBytes = &stat.TotalBytes
			fs.UsedBytes = &used
			fs.AvailableBytes = &stat.AvailableBytes
			fs.UsagePercent = &usage
			if !virtual && usage >= storageCriticalPercent {
				fs.Warnings = append(fs.Warnings, "capacity_critical")
			} else if !virtual && usage >= storageWarningPercent {
				fs.Warnings = append(fs.Warnings, "capacity_warning")
			}
		}
		if readOnly && !virtual && !memory {
			fs.Warnings = append(fs.Warnings, "mounted_read_only")
		}
		result.Filesystems = append(result.Filesystems, fs)
		result.Summary.MountedCount++
		result.Summary.WarningCount += len(fs.Warnings)
		key := mount.MajorMinor
		if key == "0:0" || key == "" {
			key = mount.FSType + "|" + mount.Source
		}
		if !network && !memory && !virtual && !fs.Bind && !seenSummary[key] && fs.TotalBytes != nil {
			seenSummary[key] = true
			result.Summary.TotalBytes += *fs.TotalBytes
			result.Summary.UsedBytes += *fs.UsedBytes
			result.Summary.AvailableBytes += *fs.AvailableBytes
		}
	}
	result.Summary.DeviceCount = len(result.Devices)
	result.Capabilities["filesystems"] = Status{State: Available}
	if p.ReadFSTab == nil {
		result.Capabilities["persistent_mounts"] = Status{State: Unsupported, Detail: "persistent mount configuration reader unavailable"}
	} else if raw, readErr := p.ReadFSTab(); readErr != nil {
		result.Capabilities["persistent_mounts"] = Status{State: Unavailable, Detail: "persistent mount configuration unavailable"}
	} else if mounts, parseErr := parseFSTab(raw); parseErr != nil {
		result.Capabilities["persistent_mounts"] = Status{State: Failed, Detail: "persistent mount configuration is malformed or exceeds limits"}
	} else {
		result.PersistentMounts = mounts
		result.Capabilities["persistent_mounts"] = Status{State: Available, Detail: fmt.Sprintf("%d configured entries", len(mounts))}
	}
	result.Capabilities["device_health"] = Status{State: Unsupported, Detail: "device health requires a separately authorized provider; presence is not reported as health"}
	result.LVMVolumes, result.Capabilities["lvm"] = collectLVM(ctx, p.Runner)
	result.ZFSPools, result.ZFSDatasets, result.Capabilities["zfs"] = collectZFS(ctx, p.Runner)
	sort.Slice(result.Devices, func(i, j int) bool { return result.Devices[i].Path < result.Devices[j].Path })
	sort.Slice(result.Filesystems, func(i, j int) bool { return result.Filesystems[i].MountPoint < result.Filesystems[j].MountPoint })
	return result, nil
}

func isBindMount(mount mountRecord) bool {
	return mount.Root != "/" && mount.FSType != "btrfs" && mount.FSType != "zfs"
}

func optionSet(options, want string) bool {
	for _, value := range strings.Split(options, ",") {
		if value == want {
			return true
		}
	}
	return false
}
func appendUnique(values []string, additions ...string) []string {
	for _, add := range additions {
		found := false
		for _, value := range values {
			if value == add {
				found = true
				break
			}
		}
		if !found && add != "" {
			values = append(values, add)
		}
	}
	return values
}

func capabilityError(err error, label string) Status {
	switch {
	case errors.Is(err, ErrUnsupported):
		return Status{State: Unsupported, Detail: label + " tools are not installed"}
	case errors.Is(err, ErrPermission):
		return Status{State: PermissionDenied, Detail: label + " inventory permission denied"}
	default:
		return Status{State: Unavailable, Detail: label + " inventory unavailable"}
	}
}

func collectLVM(ctx context.Context, runner StorageRunner) ([]LVMVolume, Status) {
	raw, err := runner.Run(ctx, "lvm")
	if err != nil {
		return nil, capabilityError(err, "LVM")
	}
	var report struct {
		Report []struct {
			LV []map[string]string `json:"lv"`
		} `json:"report"`
	}
	if json.Unmarshal(raw, &report) != nil {
		return nil, Status{State: Failed, Detail: "LVM returned malformed data"}
	}
	items := []LVMVolume{}
	for _, group := range report.Report {
		for _, row := range group.LV {
			if len(items) >= MaxStorageDevices {
				return nil, Status{State: Unavailable, Detail: "LVM inventory exceeds limit"}
			}
			size, _ := strconv.ParseUint(strings.TrimSpace(row["lv_size"]), 10, 64)
			kind := "volume"
			if row["pool_lv"] != "" {
				kind = "thin_volume"
			}
			if strings.HasPrefix(row["lv_attr"], "t") {
				kind = "thin_pool"
			}
			items = append(items, LVMVolume{Name: safeStorageText(row["lv_name"], 128), Group: safeStorageText(row["vg_name"], 128), Path: safeStorageText(row["lv_path"], 256), SizeBytes: size, Kind: kind})
		}
	}
	return items, Status{State: Available, Detail: fmt.Sprintf("%d logical volumes", len(items))}
}

func collectZFS(ctx context.Context, runner StorageRunner) ([]ZFSPool, []ZFSDataset, Status) {
	poolsRaw, err := runner.Run(ctx, "zpool")
	if err != nil {
		return nil, nil, capabilityError(err, "ZFS")
	}
	datasetsRaw, err := runner.Run(ctx, "zfs")
	if err != nil {
		return nil, nil, capabilityError(err, "ZFS")
	}
	pools := []ZFSPool{}
	for _, line := range strings.Split(strings.TrimSpace(string(poolsRaw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, nil, Status{State: Failed, Detail: "ZFS returned malformed pool data"}
		}
		size, _ := strconv.ParseUint(fields[1], 10, 64)
		allocated, _ := strconv.ParseUint(fields[2], 10, 64)
		free, _ := strconv.ParseUint(fields[3], 10, 64)
		pools = append(pools, ZFSPool{Name: safeStorageText(fields[0], 128), SizeBytes: size, AllocatedBytes: allocated, FreeBytes: free, Health: safeStorageText(fields[4], 32)})
	}
	datasets := []ZFSDataset{}
	for _, line := range strings.Split(strings.TrimSpace(string(datasetsRaw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, nil, Status{State: Failed, Detail: "ZFS returned malformed dataset data"}
		}
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		available, _ := strconv.ParseUint(fields[3], 10, 64)
		datasets = append(datasets, ZFSDataset{Name: safeStorageText(fields[0], 256), Type: safeStorageText(fields[1], 32), UsedBytes: used, AvailableBytes: available, MountPoint: safeStorageText(fields[4], 512)})
	}
	if len(pools) > MaxStorageDevices || len(datasets) > MaxStorageFilesystems {
		return nil, nil, Status{State: Unavailable, Detail: "ZFS inventory exceeds limit"}
	}
	return pools, datasets, Status{State: Available, Detail: fmt.Sprintf("%d pools and %d datasets", len(pools), len(datasets))}
}
