package platform

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Support string

const (
	Supported    Support = "supported"
	Experimental Support = "experimental"
	Unsupported  Support = "unsupported"
)

type Platform struct {
	ID, Version, VersionMajor, PrettyName string
	Family, PackageManager                string
	ContainerRuntime, Orchestration, MAC  string
	Support                               Support
	Installable                           bool
}

func Detect() (Platform, error) { return DetectFile("/etc/os-release") }

func DetectFile(path string) (Platform, error) {
	f, err := os.Open(path)
	if err != nil {
		return Platform{}, fmt.Errorf("read os-release: %w", err)
	}
	defer f.Close()
	values, err := parse(f)
	if err != nil {
		return Platform{}, err
	}
	return classify(values), nil
}

func parse(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid os-release entry")
		}
		for _, ch := range key {
			if !(ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
				return nil, fmt.Errorf("invalid os-release key")
			}
		}
		if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, `'`) {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				if strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`) && len(value) >= 2 {
					decoded = value[1 : len(value)-1]
				} else {
					return nil, fmt.Errorf("invalid os-release value for %s", key)
				}
			}
			value = decoded
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read os-release: %w", err)
	}
	return values, nil
}

func classify(values map[string]string) Platform {
	id := strings.ToLower(values["ID"])
	version := values["VERSION_ID"]
	major := version
	if before, _, ok := strings.Cut(version, "."); ok {
		major = before
	}
	p := Platform{ID: id, Version: version, VersionMajor: major, PrettyName: values["PRETTY_NAME"], Support: Unsupported}
	if p.PrettyName == "" {
		p.PrettyName = strings.TrimSpace(id + " " + version)
	}
	switch id {
	case "debian":
		p.Family, p.PackageManager, p.ContainerRuntime, p.Orchestration, p.MAC = "debian", "apt/dpkg", "docker", "compose", "apparmor"
		if major == "13" {
			p.Support, p.Installable = Supported, true
		}
	case "ubuntu":
		p.Family, p.PackageManager, p.ContainerRuntime, p.Orchestration, p.MAC = "debian", "apt/dpkg", "docker", "compose", "apparmor"
		if version == "26.04" {
			p.Support, p.Installable = Supported, true
		}
	case "arch":
		p.Family, p.PackageManager, p.ContainerRuntime, p.Orchestration, p.MAC = "arch", "pacman", "docker", "compose", "apparmor"
		p.Support, p.Installable = Supported, true
	case "rocky":
		p.Family, p.PackageManager, p.ContainerRuntime, p.Orchestration, p.MAC = "rhel", "dnf/rpm", "podman", "quadlet", "selinux"
		if major == "10" {
			p.Support, p.Installable = Experimental, true
		}
	case "rhel", "almalinux":
		p.Family, p.PackageManager, p.ContainerRuntime, p.Orchestration, p.MAC = "rhel", "dnf/rpm", "podman", "quadlet", "selinux"
		if major == "10" {
			p.Support = Experimental
		}
	}
	return p
}
