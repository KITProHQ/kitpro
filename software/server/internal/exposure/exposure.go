package exposure

import (
	"fmt"
	"net"
	"strconv"
)

type Mode string

const (
	Internal Mode = "internal"
	Loopback Mode = "loopback"
	LAN      Mode = "lan"
)

const (
	FirstPort = 20000
	LastPort  = 29999
)

type Assignment struct {
	Mode    Mode
	Address string
	Port    int
}

func Validate(a Assignment, configuredLAN string) error {
	switch a.Mode {
	case Internal:
		if a.Address != "" || (a.Port != 0 && (a.Port < FirstPort || a.Port > LastPort)) {
			return fmt.Errorf("internal exposure cannot bind a host")
		}
	case Loopback:
		ip := net.ParseIP(a.Address)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("loopback address required")
		}
		if a.Port < FirstPort || a.Port > LastPort {
			return fmt.Errorf("host port outside approved range")
		}
	case LAN:
		ip := net.ParseIP(a.Address)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || a.Address != configuredLAN {
			return fmt.Errorf("LAN address is not the configured host address")
		}
		if a.Port < FirstPort || a.Port > LastPort {
			return fmt.Errorf("host port outside approved range")
		}
	default:
		return fmt.Errorf("unsupported exposure mode")
	}
	return nil
}

func DockerProtocol(protocol string) (string, error) {
	switch protocol {
	case "http", "https", "tcp":
		return "tcp", nil
	default:
		return "", fmt.Errorf("unsupported service protocol")
	}
}

// ObservedBindingsExact compares Docker's decoded HostConfig.PortBindings to
// the trusted installation-service assignment. Any missing, extra, wildcard,
// or changed binding is security drift.
func ObservedBindingsExact(a Assignment, containerPort int, protocol string, bindings map[string]any) bool {
	if a.Mode == Internal {
		return len(bindings) == 0
	}
	if Validate(a, a.Address) != nil || containerPort < 1 || containerPort > 65535 {
		return false
	}
	dockerProtocol, err := DockerProtocol(protocol)
	if err != nil || len(bindings) != 1 {
		return false
	}
	key := fmt.Sprintf("%d/%s", containerPort, dockerProtocol)
	raw, ok := bindings[key]
	if !ok {
		return false
	}
	items, ok := raw.([]any)
	if !ok || len(items) != 1 {
		return false
	}
	binding, ok := items[0].(map[string]any)
	if !ok || len(binding) != 2 {
		return false
	}
	hostIP, ipOK := binding["HostIp"].(string)
	hostPort, portOK := binding["HostPort"].(string)
	return ipOK && portOK && hostIP == a.Address && hostPort == strconv.Itoa(a.Port)
}

func Allocate(used map[int]bool) (int, error) {
	for p := FirstPort; p <= LastPort; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("exposure port range exhausted")
}

// AllocateAvailable skips both persisted assignments and ports currently
// occupied on the requested bind address.
func AllocateAvailable(used map[int]bool, address string) (int, error) {
	for p := FirstPort; p <= LastPort; p++ {
		if used[p] {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(p)))
		if err != nil {
			continue
		}
		_ = l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no available exposure port")
}

// VerifyLocalAddress proves that address is assigned to the host by binding an
// ephemeral TCP listener to that exact address. This deliberately avoids
// interface enumeration, which requires AF_NETLINK and is unavailable inside
// the hardened API service sandbox.
func VerifyLocalAddress(address string) error {
	ip := net.ParseIP(address)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("invalid LAN bind address")
	}
	l, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
	if err != nil {
		return fmt.Errorf("LAN bind address is not assigned: %w", err)
	}
	return l.Close()
}
