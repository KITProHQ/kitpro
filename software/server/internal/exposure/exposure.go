package exposure

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
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

type Transport string

const (
	TCP         Transport = "tcp"
	UDP         Transport = "udp"
	MaxBindings           = 8
)

// ServiceBinding is the complete trusted runtime binding for one declared
// service. Internal bindings are retained so exposure changes and generation
// replacement cannot silently lose another service.
type ServiceBinding struct {
	ServiceID     string    `json:"service_id"`
	Transport     Transport `json:"transport"`
	ContainerPort int       `json:"container_port"`
	Mode          Mode      `json:"mode"`
	HostAddress   string    `json:"host_address,omitempty"`
	HostPort      int       `json:"host_port,omitempty"`
}

func Normalize(bindings []ServiceBinding) []ServiceBinding {
	out := append([]ServiceBinding(nil), bindings...)
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceID < out[j].ServiceID })
	return out
}

func ValidateBinding(b ServiceBinding, configuredLAN string, fixedHostPort int) error {
	if b.ServiceID == "" || b.ContainerPort < 1 || b.ContainerPort > 65535 || (b.Transport != TCP && b.Transport != UDP) {
		return fmt.Errorf("invalid service binding")
	}
	if fixedHostPort != 0 {
		if fixedHostPort < 1 || fixedHostPort > 65535 || b.HostPort != fixedHostPort {
			return fmt.Errorf("host port does not match trusted fixed-port policy")
		}
	} else if b.HostPort != 0 && (b.HostPort < FirstPort || b.HostPort > LastPort) {
		return fmt.Errorf("host port outside approved range")
	}
	return validateAddress(b.Mode, b.HostAddress, b.HostPort, configuredLAN)
}

func validateAddress(mode Mode, address string, port int, configuredLAN string) error {
	switch mode {
	case Internal:
		if address != "" {
			return fmt.Errorf("internal exposure cannot bind a host")
		}
	case Loopback:
		ip := net.ParseIP(address)
		if ip == nil || !ip.IsLoopback() || port == 0 {
			return fmt.Errorf("loopback address required")
		}
	case LAN:
		ip := net.ParseIP(address)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || address != configuredLAN || port == 0 {
			return fmt.Errorf("LAN address is not the configured host address")
		}
	default:
		return fmt.Errorf("unsupported exposure mode")
	}
	return nil
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

func ContainerProtocol(protocol string) (string, error) {
	switch protocol {
	case "http", "https", "tcp":
		return "tcp", nil
	case "udp":
		return "udp", nil
	default:
		return "", fmt.Errorf("unsupported service protocol")
	}
}

func TransportFor(protocol string) (Transport, error) {
	value, err := ContainerProtocol(protocol)
	return Transport(value), err
}

// BindingsConflict models Linux bind conflicts for the same transport and
// numeric port. Wildcards overlap every exact address in their IP family.
func BindingsConflict(a, b ServiceBinding) bool {
	if a.Transport != b.Transport || a.HostPort == 0 || a.HostPort != b.HostPort || a.Mode == Internal || b.Mode == Internal {
		return false
	}
	return addressesOverlap(a.HostAddress, b.HostAddress)
}

func addressesOverlap(a, b string) bool {
	aIP, bIP := net.ParseIP(a), net.ParseIP(b)
	if aIP == nil || bIP == nil {
		return a == b
	}
	a4, b4 := aIP.To4(), bIP.To4()
	if (a4 == nil) != (b4 == nil) {
		return false
	}
	return aIP.IsUnspecified() || bIP.IsUnspecified() || aIP.Equal(bIP)
}

func ValidateConflicts(bindings []ServiceBinding) error {
	for i := range bindings {
		if bindings[i].Mode == Internal {
			continue
		}
		for j := 0; j < i; j++ {
			if BindingsConflict(bindings[i], bindings[j]) {
				return fmt.Errorf("binding conflict: %s:%d/%s", bindings[i].HostAddress, bindings[i].HostPort, bindings[i].Transport)
			}
		}
	}
	return nil
}

// ObservedBindingsExactSet rejects missing, extra, wildcard, or changed
// runtime bindings. Internal services intentionally produce no runtime entry.
func ObservedBindingsExactSet(expected []ServiceBinding, observed map[string]any) bool {
	want := map[string][]ServiceBinding{}
	for _, binding := range expected {
		if binding.Mode == Internal {
			continue
		}
		key := fmt.Sprintf("%d/%s", binding.ContainerPort, binding.Transport)
		want[key] = append(want[key], binding)
	}
	if len(want) != len(observed) {
		return false
	}
	for key, expectedItems := range want {
		raw, ok := observed[key]
		if !ok {
			return false
		}
		items, ok := raw.([]any)
		if !ok || len(items) != len(expectedItems) {
			return false
		}
		matched := make([]bool, len(expectedItems))
		for _, item := range items {
			value, ok := item.(map[string]any)
			if !ok || len(value) != 2 {
				return false
			}
			ip, ipOK := value["HostIp"].(string)
			port, portOK := value["HostPort"].(string)
			found := false
			for i, expected := range expectedItems {
				if !matched[i] && ipOK && portOK && ip == expected.HostAddress && port == strconv.Itoa(expected.HostPort) {
					matched[i], found = true, true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// DockerProtocol is retained for source compatibility with older callers.
func DockerProtocol(protocol string) (string, error) { return ContainerProtocol(protocol) }

// ObservedBindingsExact compares a runtime's decoded HostConfig.PortBindings to
// the trusted installation-service assignment. Any missing, extra, wildcard,
// or changed binding is security drift.
func ObservedBindingsExact(a Assignment, containerPort int, protocol string, bindings map[string]any) bool {
	if a.Mode == Internal {
		return len(bindings) == 0
	}
	if Validate(a, a.Address) != nil || containerPort < 1 || containerPort > 65535 {
		return false
	}
	runtimeProtocol, err := ContainerProtocol(protocol)
	if err != nil || len(bindings) != 1 {
		return false
	}
	key := fmt.Sprintf("%d/%s", containerPort, runtimeProtocol)
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
	return AllocateAvailableTransport(used, address, TCP)
}

func AllocateAvailableTransport(used map[int]bool, address string, transport Transport) (int, error) {
	for p := FirstPort; p <= LastPort; p++ {
		if used[p] {
			continue
		}
		if transport == UDP {
			l, err := net.ListenPacket("udp", net.JoinHostPort(address, strconv.Itoa(p)))
			if err != nil {
				continue
			}
			_ = l.Close()
			return p, nil
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

type procSource struct {
	path      string
	transport Transport
	ipv6      bool
}

// HostListeners reads the kernel socket tables without shell execution or
// additional privilege. TCP entries must be LISTEN; UDP entries represent
// bound sockets. Runtime bind remains the final authority because this is a
// point-in-time preflight.
func HostListeners() ([]ServiceBinding, error) {
	sources := []procSource{{"/proc/net/tcp", TCP, false}, {"/proc/net/tcp6", TCP, true}, {"/proc/net/udp", UDP, false}, {"/proc/net/udp6", UDP, true}}
	var out []ServiceBinding
	for _, source := range sources {
		file, err := os.Open(source.path)
		if err != nil {
			return nil, err
		}
		parsed, parseErr := parseProcNet(file, source.transport, source.ipv6)
		_ = file.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, parsed...)
	}
	return out, nil
}

func parseProcNet(r io.Reader, transport Transport, ipv6 bool) ([]ServiceBinding, error) {
	scanner := bufio.NewScanner(r)
	first := true
	var out []ServiceBinding
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		if transport == TCP && fields[3] != "0A" {
			continue
		}
		parts := strings.Split(fields[1], ":")
		if len(parts) != 2 {
			continue
		}
		port64, err := strconv.ParseUint(parts[1], 16, 16)
		if err != nil {
			return nil, err
		}
		address, err := decodeProcAddress(parts[0], ipv6)
		if err != nil {
			return nil, err
		}
		out = append(out, ServiceBinding{Transport: transport, Mode: LAN, HostAddress: address, HostPort: int(port64)})
	}
	return out, scanner.Err()
}

func decodeProcAddress(raw string, ipv6 bool) (string, error) {
	want := 8
	if ipv6 {
		want = 32
	}
	if len(raw) != want {
		return "", fmt.Errorf("invalid proc address")
	}
	bytes := make([]byte, want/2)
	for word := 0; word < len(bytes); word += 4 {
		for i := 0; i < 4; i++ {
			value, err := strconv.ParseUint(raw[(word+3-i)*2:(word+4-i)*2], 16, 8)
			if err != nil {
				return "", err
			}
			bytes[word+i] = byte(value)
		}
	}
	return net.IP(bytes).String(), nil
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
