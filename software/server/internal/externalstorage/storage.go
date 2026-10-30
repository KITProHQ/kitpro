package externalstorage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	ReadOnly  = "read-only"
	ReadWrite = "read-write"
)

type Identity struct {
	CanonicalPath string `json:"canonical_path"`
	DeviceMajor   uint32 `json:"device_major"`
	DeviceMinor   uint32 `json:"device_minor"`
	Inode         uint64 `json:"inode"`
	Filesystem    string `json:"filesystem"`
	MountSource   string `json:"mount_source"`
	MountPoint    string `json:"mount_point"`
	NetworkBacked bool   `json:"network_backed"`
}

var forbidden = []string{"/", "/boot", "/dev", "/etc", "/home", "/proc", "/root", "/run", "/sys", "/usr", "/var/lib/docker", "/var/lib/containerd", "/var/lib/containers", "/var/lib/kitpro-api", "/var/lib/kitpro-helper", "/srv/kitpro"}
var allowedParents = []string{"/mnt", "/media", "/data", "/srv"}

func Inspect(path string) (Identity, error) {
	return InspectWithin(path, allowedParents)
}

// InspectWithin applies the same canonical and identity checks with an explicit
// parent policy. It exists so tests can use isolated temporary filesystems; the
// privileged helper always calls Inspect and therefore uses the product policy.
func InspectWithin(path string, parents []string) (Identity, error) {
	if path == "" || !filepath.IsAbs(path) {
		return Identity{}, fmt.Errorf("storage path must be absolute")
	}
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return Identity{}, fmt.Errorf("storage path traversal is not allowed")
		}
	}
	clean := filepath.Clean(path)
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return Identity{}, fmt.Errorf("storage path unavailable: %w", err)
	}
	if canonical != clean {
		return Identity{}, fmt.Errorf("storage root must not contain symbolic links")
	}
	if forbiddenPath(canonical) {
		return Identity{}, fmt.Errorf("storage path is protected by KITPro policy")
	}
	if !allowedPath(canonical, parents) {
		return Identity{}, fmt.Errorf("storage path must be inside /mnt, /media, /data, or /srv")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return Identity{}, fmt.Errorf("storage path must be an existing directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, fmt.Errorf("storage filesystem identity unavailable")
	}
	mount, err := mountFor(canonical, "/proc/self/mountinfo")
	if err != nil {
		return Identity{}, err
	}
	return Identity{CanonicalPath: canonical, DeviceMajor: unix.Major(uint64(stat.Dev)), DeviceMinor: unix.Minor(uint64(stat.Dev)), Inode: stat.Ino, Filesystem: mount.fs, MountSource: mount.source, MountPoint: mount.point, NetworkBacked: networkFS(mount.fs)}, nil
}

func allowedPath(path string, parents []string) bool {
	for _, parent := range parents {
		if strings.HasPrefix(path, parent+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func Validate(current Identity, path string) (Identity, error) {
	return ValidateWithin(current, path, allowedParents)
}

// ValidateWithin is the testable form of Validate. Production helper paths use
// Validate and cannot select a wider parent policy.
func ValidateWithin(current Identity, path string, parents []string) (Identity, error) {
	now, err := InspectWithin(path, parents)
	if err != nil {
		return Identity{}, err
	}
	if now.CanonicalPath != current.CanonicalPath || now.DeviceMajor != current.DeviceMajor || now.DeviceMinor != current.DeviceMinor || now.Inode != current.Inode || now.Filesystem != current.Filesystem || now.MountSource != current.MountSource || now.MountPoint != current.MountPoint {
		return Identity{}, fmt.Errorf("storage identity changed")
	}
	return now, nil
}

func ValidMode(mode string) bool { return mode == ReadOnly || mode == ReadWrite }

func forbiddenPath(path string) bool {
	for _, base := range forbidden {
		if path == base || (base != "/" && strings.HasPrefix(path, base+string(filepath.Separator))) {
			return true
		}
	}
	return strings.Contains(path, "docker.sock") || strings.Contains(path, "containerd.sock")
}

type mountRecord struct{ point, fs, source string }

func mountFor(path, mountInfo string) (mountRecord, error) {
	f, err := os.Open(mountInfo)
	if err != nil {
		return mountRecord{}, fmt.Errorf("mount identity unavailable: %w", err)
	}
	defer f.Close()
	best := mountRecord{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || sep+2 >= len(fields) {
			continue
		}
		point := unescape(fields[4])
		if path != point && !strings.HasPrefix(path, strings.TrimSuffix(point, "/")+"/") {
			continue
		}
		if len(point) > len(best.point) {
			best = mountRecord{point: point, fs: fields[sep+1], source: unescape(fields[sep+2])}
		}
	}
	if err := s.Err(); err != nil {
		return mountRecord{}, err
	}
	if best.point == "" {
		return mountRecord{}, fmt.Errorf("storage mount identity unavailable")
	}
	return best, nil
}

func unescape(value string) string {
	for code, replacement := range map[string]string{"040": " ", "011": "\t", "012": "\n", "134": "\\"} {
		value = strings.ReplaceAll(value, "\\"+code, replacement)
	}
	return value
}

func networkFS(fs string) bool {
	for _, prefix := range []string{"nfs", "cifs", "smb", "sshfs", "9p", "ceph", "gluster"} {
		if strings.HasPrefix(strings.ToLower(fs), prefix) {
			return true
		}
	}
	return false
}

func ParseUint(value string) uint64 { n, _ := strconv.ParseUint(value, 10, 64); return n }
