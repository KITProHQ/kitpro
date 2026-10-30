package hostadmin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type ServiceCategory string

const (
	CategoryKITPro    ServiceCategory = "kitpro_protected"
	CategoryCritical  ServiceCategory = "os_critical"
	CategoryNetwork   ServiceCategory = "network_access"
	CategoryStorage   ServiceCategory = "storage_filesystem"
	CategoryContainer ServiceCategory = "container_runtime"
	CategoryApproved  ServiceCategory = "approved_noncritical"
	CategoryCustom    ServiceCategory = "administrator_reviewed_custom"
	CategoryUnknown   ServiceCategory = "unknown_unsupported"
)

type ServiceRelationships struct {
	Requires         []string `json:"requires,omitempty"`
	Wants            []string `json:"wants,omitempty"`
	Requisite        []string `json:"requisite,omitempty"`
	BindsTo          []string `json:"binds_to,omitempty"`
	PartOf           []string `json:"part_of,omitempty"`
	Conflicts        []string `json:"conflicts,omitempty"`
	RequiredBy       []string `json:"required_by,omitempty"`
	WantedBy         []string `json:"wanted_by,omitempty"`
	TriggeredBy      []string `json:"triggered_by,omitempty"`
	Triggers         []string `json:"triggers,omitempty"`
	StopWhenUnneeded bool     `json:"stop_when_unneeded"`
}

func serviceRelationships(props map[string]any) ServiceRelationships {
	return ServiceRelationships{
		Requires: boundedUnits(props["Requires"]), Wants: boundedUnits(props["Wants"]),
		Requisite: boundedUnits(props["Requisite"]), BindsTo: boundedUnits(props["BindsTo"]),
		PartOf: boundedUnits(props["PartOf"]), Conflicts: boundedUnits(props["Conflicts"]),
		RequiredBy: boundedUnits(props["RequiredBy"]), WantedBy: boundedUnits(props["WantedBy"]),
		TriggeredBy: boundedUnits(props["TriggeredBy"]), Triggers: boundedUnits(props["Triggers"]),
		StopWhenUnneeded: boolValue(props["StopWhenUnneeded"]),
	}
}

func boundedUnits(value any) []string {
	items, _ := value.([]string)
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if len(result) == 32 {
			break
		}
		if len(item) <= 200 && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

func boolValue(value any) bool { result, _ := value.(bool); return result }

func (r ServiceRelationships) all() []string {
	result := []string{}
	for _, group := range [][]string{r.Requires, r.Wants, r.Requisite, r.BindsTo, r.PartOf, r.Conflicts, r.RequiredBy, r.WantedBy, r.TriggeredBy, r.Triggers} {
		result = append(result, group...)
	}
	return result
}

func trustedUnitDefinition(path string) bool {
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return false
	}
	clean = resolved
	trustedRoot := false
	for _, root := range []string{"/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system"} {
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			trustedRoot = true
			break
		}
	}
	if !trustedRoot {
		return false
	}
	info, err := os.Stat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

func classifyService(name string) (ServiceCategory, string) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "kitpro-"):
		return CategoryKITPro, "KITPro infrastructure is protected from Host Administration mutations."
	case containsAny(lower, "ssh.service", "sshd.service", "networkmanager", "systemd-networkd", "systemd-resolved", "networking.service", "wicked", "connman", "iwd.service", "wpa_supplicant", "dhcpcd", "firewalld", "ufw.service", "nftables"):
		return CategoryNetwork, "Networking, remote access, and firewall services are protected."
	case containsAny(lower, "docker.service", "docker.socket", "containerd.service", "podman.service", "podman.socket", "nginx.service"):
		return CategoryContainer, "Container runtime and managed ingress services are protected."
	case containsAny(lower, "cryptsetup", "lvm2", "multipathd", "mdmonitor", "zfs", "iscsid", "iscsi.service", "nfs-server", "rpc-statd", "remote-fs", "local-fs", "udisks2"):
		return CategoryStorage, "Storage and filesystem infrastructure is protected."
	case containsAny(lower, "dbus.service", "systemd-", "apparmor.service"):
		return CategoryCritical, "Operating-system critical services are protected."
	default:
		return CategoryUnknown, "This service has not been approved for management."
	}
}

func containsAny(value string, fragments ...string) bool {
	for _, f := range fragments {
		if strings.Contains(value, f) {
			return true
		}
	}
	return false
}
