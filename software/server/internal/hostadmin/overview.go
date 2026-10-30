package hostadmin

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// State distinguishes a failed collector from a genuine zero measurement.
type State string

const (
	Available        State = "available"
	Unavailable      State = "unavailable"
	Unsupported      State = "unsupported"
	NotConfigured    State = "not_configured"
	PermissionDenied State = "permission_denied"
	Failed           State = "failed"
)

type Status struct {
	State  State  `json:"state"`
	Detail string `json:"detail,omitempty"`
}
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapFreeBytes  uint64 `json:"swap_free_bytes"`
}
type Disk struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}
type Interface struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
}
type LinuxRelease struct {
	ID            string `json:"id"`
	Release       string `json:"release"`
	Generation    string `json:"generation"`
	SupportStatus string `json:"support_status,omitempty"`
}

type linuxReleaseMetadata struct {
	LinuxRelease
	GenerationPolicy string `json:"generation_policy"`
}

func validReleaseValue(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

// Production metadata keeps the release identity in the package and the
// verified installed generation in /etc. Older development metadata embeds
// its generation in the package file.
func parseLinuxRelease(packageRaw, installedRaw string) (LinuxRelease, error) {
	var pkg linuxReleaseMetadata
	if json.Unmarshal([]byte(packageRaw), &pkg) != nil || pkg.ID != "kitpro-linux" || !validReleaseValue(pkg.Release) {
		return LinuxRelease{}, errors.New("invalid KITPro Linux package identity")
	}
	switch pkg.GenerationPolicy {
	case "current":
		var installed linuxReleaseMetadata
		if json.Unmarshal([]byte(installedRaw), &installed) != nil || installed.ID != pkg.ID || installed.Release != pkg.Release || !validReleaseValue(installed.Generation) {
			return LinuxRelease{}, errors.New("KITPro Linux installed generation unavailable")
		}
		pkg.Generation = installed.Generation
	case "":
		if pkg.Generation == "" {
			return LinuxRelease{}, errors.New("KITPro Linux generation missing")
		}
	default:
		return LinuxRelease{}, errors.New("unsupported KITPro Linux generation policy")
	}
	if !validReleaseValue(pkg.Generation) || (pkg.SupportStatus != "" && !validReleaseValue(pkg.SupportStatus)) {
		return LinuxRelease{}, errors.New("invalid KITPro Linux release metadata")
	}
	return pkg.LinuxRelease, nil
}

type Overview struct {
	Hostname       string            `json:"hostname,omitempty"`
	OS             string            `json:"os,omitempty"`
	Kernel         string            `json:"kernel,omitempty"`
	Architecture   string            `json:"architecture"`
	CPUModel       string            `json:"cpu_model,omitempty"`
	CPUUtilization *float64          `json:"cpu_utilization_percent,omitempty"`
	UptimeSeconds  *uint64           `json:"uptime_seconds,omitempty"`
	LoadAverage    []float64         `json:"load_average,omitempty"`
	Memory         *Memory           `json:"memory,omitempty"`
	RootDisk       *Disk             `json:"root_disk,omitempty"`
	Interfaces     []Interface       `json:"interfaces,omitempty"`
	Systemd        Status            `json:"systemd"`
	Security       Status            `json:"security"`
	KITProLinux    *LinuxRelease     `json:"kitpro_linux,omitempty"`
	Collectors     map[string]Status `json:"collectors"`
}

func readBounded(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return "", err
	}
	if len(b) > 64*1024 {
		return "", errors.New("host data exceeds limit")
	}
	return string(b), nil
}
func firstValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimPrefix(line, key+"="), "\"'")
		}
	}
	return ""
}
func parseMemory(text string) (*Memory, error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := strings.TrimSuffix(f[0], ":")
		if name != "MemTotal" && name != "MemAvailable" && name != "SwapTotal" && name != "SwapFree" {
			continue
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil {
			return nil, e
		}
		values[name] = n * 1024
	}
	if values["MemTotal"] == 0 || len(values) != 4 {
		return nil, errors.New("MemTotal unavailable")
	}
	return &Memory{values["MemTotal"], values["MemAvailable"], values["SwapTotal"], values["SwapFree"]}, nil
}
func cpuTicks(text string) (uint64, uint64, error) {
	line := strings.SplitN(text, "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, errors.New("CPU aggregate unavailable")
	}
	var total, idle uint64
	for i, field := range fields[1:] {
		n, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return total, idle, nil
}
func cpuUtilization() (*float64, error) {
	first, err := readBounded("/proc/stat")
	if err != nil {
		return nil, err
	}
	total1, idle1, err := cpuTicks(first)
	if err != nil {
		return nil, err
	}
	time.Sleep(100 * time.Millisecond)
	second, err := readBounded("/proc/stat")
	if err != nil {
		return nil, err
	}
	total2, idle2, err := cpuTicks(second)
	if err != nil {
		return nil, err
	}
	if total2 <= total1 || idle2 < idle1 || idle2-idle1 > total2-total1 {
		return nil, errors.New("CPU sample unavailable")
	}
	value := 100 * float64((total2-total1)-(idle2-idle1)) / float64(total2-total1)
	return &value, nil
}
func Collect() Overview {
	o := Overview{Architecture: runtime.GOARCH, Collectors: map[string]Status{}, Systemd: Status{Unsupported, "systemd status inspection is not implemented"}, Security: Status{Unsupported, "security enforcement inspection is not implemented"}}
	if h, e := os.Hostname(); e == nil {
		o.Hostname = h
		o.Collectors["hostname"] = Status{Available, ""}
	} else {
		o.Collectors["hostname"] = Status{Unavailable, "hostname read failed"}
	}
	if s, e := readBounded("/etc/os-release"); e == nil {
		o.OS = firstValue(s, "PRETTY_NAME")
		if o.OS == "" {
			o.OS = firstValue(s, "NAME")
		}
		if o.OS != "" {
			o.Collectors["os"] = Status{Available, ""}
		} else {
			o.Collectors["os"] = Status{Unavailable, "OS identity missing"}
		}
	} else {
		o.Collectors["os"] = Status{Unavailable, "OS identity unreadable"}
	}
	if s, e := readBounded("/proc/sys/kernel/osrelease"); e == nil {
		o.Kernel = strings.TrimSpace(s)
		o.Collectors["kernel"] = Status{Available, ""}
	} else {
		o.Collectors["kernel"] = Status{Unavailable, "kernel release unreadable"}
	}
	if s, e := readBounded("/proc/cpuinfo"); e == nil {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					o.CPUModel = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}
	if o.CPUModel != "" {
		o.Collectors["cpu_model"] = Status{Available, ""}
	} else {
		o.Collectors["cpu_model"] = Status{Unavailable, "CPU model unavailable"}
	}
	if n, e := cpuUtilization(); e == nil {
		o.CPUUtilization = n
		o.Collectors["cpu_utilization"] = Status{Available, ""}
	} else {
		o.Collectors["cpu_utilization"] = Status{Unavailable, "CPU sample unavailable"}
	}
	if s, e := readBounded("/proc/uptime"); e == nil {
		f := strings.Fields(s)
		if len(f) > 0 {
			if n, err := strconv.ParseFloat(f[0], 64); err == nil && n >= 0 {
				v := uint64(n)
				o.UptimeSeconds = &v
				o.Collectors["uptime"] = Status{Available, ""}
			}
		}
	}
	if _, ok := o.Collectors["uptime"]; !ok {
		o.Collectors["uptime"] = Status{Unavailable, "uptime unreadable"}
	}
	if s, e := readBounded("/proc/loadavg"); e == nil {
		f := strings.Fields(s)
		if len(f) >= 3 {
			for _, v := range f[:3] {
				n, err := strconv.ParseFloat(v, 64)
				if err != nil {
					o.LoadAverage = nil
					break
				}
				o.LoadAverage = append(o.LoadAverage, n)
			}
		}
	}
	if len(o.LoadAverage) == 3 {
		o.Collectors["load"] = Status{Available, ""}
	} else {
		o.Collectors["load"] = Status{Unavailable, "load average unreadable"}
	}
	if s, e := readBounded("/proc/meminfo"); e == nil {
		if m, err := parseMemory(s); err == nil {
			o.Memory = m
			o.Collectors["memory"] = Status{Available, ""}
		}
	}
	if o.Memory == nil {
		o.Collectors["memory"] = Status{Unavailable, "memory data unreadable"}
	}
	var fs unix.Statfs_t
	if e := unix.Statfs("/", &fs); e == nil {
		o.RootDisk = &Disk{uint64(fs.Blocks) * uint64(fs.Bsize), uint64(fs.Bavail) * uint64(fs.Bsize)}
		o.Collectors["root_disk"] = Status{Available, ""}
	} else {
		o.Collectors["root_disk"] = Status{Unavailable, "root filesystem unavailable"}
	}
	if list, e := net.Interfaces(); e == nil {
		for _, item := range list {
			v := Interface{Name: item.Name, Up: item.Flags&net.FlagUp != 0, Addresses: []string{}}
			if addrs, err := item.Addrs(); err == nil {
				for _, a := range addrs {
					v.Addresses = append(v.Addresses, a.String())
				}
			}
			o.Interfaces = append(o.Interfaces, v)
		}
		o.Collectors["interfaces"] = Status{Available, ""}
	} else {
		o.Collectors["interfaces"] = Status{Unavailable, "network interfaces unavailable"}
	}
	if _, e := os.Stat("/run/systemd/system"); e == nil {
		o.Systemd = Status{Available, "systemd runtime detected; service health inspection is pending"}
	} else {
		o.Systemd = Status{Unsupported, "systemd runtime not detected"}
	}
	if s, e := readBounded("/sys/fs/selinux/enforce"); e == nil {
		if strings.TrimSpace(s) == "1" {
			o.Security = Status{Available, "SELinux enforcing"}
		} else {
			o.Security = Status{Available, "SELinux permissive"}
		}
	} else if s, e := readBounded("/sys/module/apparmor/parameters/enabled"); e == nil {
		if strings.HasPrefix(strings.TrimSpace(s), "Y") {
			o.Security = Status{Available, "AppArmor enabled"}
		} else {
			o.Security = Status{Available, "AppArmor disabled"}
		}
	}
	if raw, e := readBounded("/usr/lib/kitpro-linux/release.json"); e == nil {
		installedRaw, _ := readBounded("/etc/kitpro-linux-release.json")
		if release, err := parseLinuxRelease(raw, installedRaw); err == nil {
			o.KITProLinux = &release
			o.Collectors["kitpro_linux"] = Status{Available, ""}
		} else {
			o.Collectors["kitpro_linux"] = Status{Unavailable, "KITPro Linux installed generation unavailable or metadata invalid"}
		}
	} else if os.IsNotExist(e) {
		o.Collectors["kitpro_linux"] = Status{NotConfigured, "KITPro Linux release package not installed"}
	} else {
		o.Collectors["kitpro_linux"] = Status{Unavailable, "KITPro Linux release metadata unreadable"}
	}
	return o
}
