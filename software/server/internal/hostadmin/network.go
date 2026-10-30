package hostadmin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxNetworkInterfaces = 512
	MaxNetworkRoutes     = 4096
	maxNetworkOutput     = 4 * 1024 * 1024
	maxDiagnosticResults = 32
)

type NetworkSummary struct {
	InterfaceCount    int    `json:"interface_count"`
	ActiveCount       int    `json:"active_interface_count"`
	PhysicalCount     int    `json:"physical_interface_count"`
	DefaultRouteCount int    `json:"default_route_count"`
	ResolverCount     int    `json:"resolver_count"`
	Health            string `json:"health"`
	HealthDetail      string `json:"health_detail"`
	TrafficScope      string `json:"traffic_scope"`
}
type NetworkAddress struct {
	Family       string `json:"family"`
	Address      string `json:"address"`
	PrefixLength int    `json:"prefix_length"`
	Scope        string `json:"scope"`
}
type NetworkStats struct {
	RXBytes          uint64   `json:"rx_bytes"`
	TXBytes          uint64   `json:"tx_bytes"`
	RXPackets        uint64   `json:"rx_packets"`
	TXPackets        uint64   `json:"tx_packets"`
	RXErrors         uint64   `json:"rx_errors"`
	TXErrors         uint64   `json:"tx_errors"`
	RXDropped        uint64   `json:"rx_dropped"`
	TXDropped        uint64   `json:"tx_dropped"`
	RXBytesPerSecond *float64 `json:"rx_bytes_per_second,omitempty"`
	TXBytesPerSecond *float64 `json:"tx_bytes_per_second,omitempty"`
	SampledAt        string   `json:"sampled_at"`
}
type NetworkInterface struct {
	Name                string           `json:"name"`
	Index               int              `json:"index"`
	Type                string           `json:"type"`
	Classification      string           `json:"classification"`
	MACAddress          string           `json:"mac_address,omitempty"`
	AdministrativeState string           `json:"administrative_state"`
	OperationalState    string           `json:"operational_state"`
	Carrier             *bool            `json:"carrier,omitempty"`
	MTU                 int              `json:"mtu"`
	SpeedMbps           *int64           `json:"speed_mbps,omitempty"`
	Duplex              string           `json:"duplex,omitempty"`
	Driver              string           `json:"driver,omitempty"`
	Flags               []string         `json:"flags"`
	Addresses           []NetworkAddress `json:"addresses"`
	Parent              string           `json:"parent,omitempty"`
	Master              string           `json:"master,omitempty"`
	Backend             string           `json:"backend,omitempty"`
	Stats               NetworkStats     `json:"stats"`
	Warnings            []string         `json:"warnings,omitempty"`
	Status              Status           `json:"status"`
}
type NetworkRoute struct {
	Family      string `json:"family"`
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Source      string `json:"source,omitempty"`
	Metric      *int64 `json:"metric,omitempty"`
	Table       string `json:"table,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Type        string `json:"type,omitempty"`
	Default     bool   `json:"default"`
	Container   bool   `json:"container"`
}
type DNSInterface struct {
	Interface string   `json:"interface"`
	Servers   []string `json:"servers"`
	Domains   []string `json:"domains"`
}
type DNSConfiguration struct {
	Servers          []string       `json:"servers"`
	SearchDomains    []string       `json:"search_domains"`
	Source           string         `json:"source"`
	ResolvConfPath   string         `json:"resolv_conf_path"`
	ResolvConfTarget string         `json:"resolv_conf_target,omitempty"`
	DNSSEC           string         `json:"dnssec,omitempty"`
	SplitDNS         Status         `json:"split_dns"`
	Status           Status         `json:"status"`
	Interfaces       []DNSInterface `json:"interfaces,omitempty"`
}
type NetworkInventory struct {
	Summary      NetworkSummary     `json:"summary"`
	Capabilities map[string]Status  `json:"capabilities"`
	Backends     map[string]Status  `json:"backends"`
	Interfaces   []NetworkInterface `json:"interfaces"`
	Routes       []NetworkRoute     `json:"routes"`
	DNS          DNSConfiguration   `json:"dns"`
}

type NetworkRunner interface {
	Run(context.Context, string) ([]byte, error)
}
type NetworkProvider struct {
	Runner   NetworkRunner
	ReadFile func(string, int64) ([]byte, error)
	ReadLink func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	Now      func() time.Time
	mu       *sync.Mutex
	previous map[string]counterSample
}
type counterSample struct {
	RX, TX uint64
	At     time.Time
}

func NewNetworkProvider() NetworkProvider {
	return NetworkProvider{Runner: fixedNetworkRunner{}, ReadFile: readNetworkFile, ReadLink: os.Readlink, Stat: os.Stat, Now: time.Now, mu: &sync.Mutex{}, previous: map[string]counterSample{}}
}
func readNetworkFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, ErrPermission
		}
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

type fixedNetworkRunner struct{}

var networkCommands = map[string][]string{
	"links":     {"-json", "-details", "link", "show"},
	"addresses": {"-json", "-details", "address", "show"},
	"routes4":   {"-json", "-details", "-4", "route", "show", "table", "all"},
	"routes6":   {"-json", "-details", "-6", "route", "show", "table", "all"},
	"rules4":    {"-json", "-4", "rule", "show"},
	"rules6":    {"-json", "-6", "rule", "show"},
}

func findIP() (string, error) {
	for _, path := range []string{"/usr/sbin/ip", "/usr/bin/ip"} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if errors.Is(err, os.ErrPermission) {
			return "", ErrPermission
		}
	}
	return "", ErrUnsupported
}
func (fixedNetworkRunner) Run(ctx context.Context, operation string) ([]byte, error) {
	args, ok := networkCommands[operation]
	if !ok {
		return nil, ErrInvalid
	}
	path, err := findIP()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/usr/sbin"}
	out := &limitedBuffer{Max: maxNetworkOutput}
	errOut := &limitedBuffer{Max: 2048}
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrUnavailable
		}
		if strings.Contains(strings.ToLower(errOut.String()), "permission") {
			return nil, ErrPermission
		}
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

type ipAddress struct {
	Family    string `json:"family"`
	Local     string `json:"local"`
	PrefixLen int    `json:"prefixlen"`
	Scope     string `json:"scope"`
}
type ipLink struct {
	IfIndex   int      `json:"ifindex"`
	IfName    string   `json:"ifname"`
	Flags     []string `json:"flags"`
	MTU       int      `json:"mtu"`
	OperState string   `json:"operstate"`
	Address   string   `json:"address"`
	Link      string   `json:"link"`
	Master    string   `json:"master"`
	LinkInfo  struct {
		InfoKind string `json:"info_kind"`
	} `json:"linkinfo"`
	AddrInfo []ipAddress `json:"addr_info"`
}
type ipRoute struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	PrefSrc  string `json:"prefsrc"`
	Metric   *int64 `json:"metric"`
	Table    any    `json:"table"`
	Protocol string `json:"protocol"`
	Scope    string `json:"scope"`
	Type     string `json:"type"`
}

var interfaceNameRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
var hostLabelRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

func ValidInterfaceName(value string) bool { return interfaceNameRE.MatchString(value) }
func safeNetworkText(value string, max int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if len(value) > max {
		value = value[:max]
	}
	return value
}
func fileText(p NetworkProvider, path string, limit int64) string {
	b, err := p.ReadFile(path, limit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func parseUint(value string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return n
}
func parseInt64Ptr(value string) *int64 {
	n, e := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if e != nil || n < 0 {
		return nil
	}
	return &n
}
func boolFile(value string) *bool {
	switch strings.TrimSpace(value) {
	case "1":
		v := true
		return &v
	case "0":
		v := false
		return &v
	}
	return nil
}
func routeTable(value any) string {
	switch v := value.(type) {
	case string:
		return safeNetworkText(v, 32)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return "main"
}
func flagState(flags []string, want string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}

func classifyInterface(link ipLink) (string, string) {
	kind := strings.ToLower(link.LinkInfo.InfoKind)
	name := link.IfName
	if name == "lo" || kind == "loopback" {
		return "loopback", "virtual"
	}
	if kind != "" {
		if kind == "bridge" && strings.HasPrefix(name, "docker") {
			return "docker_bridge", "container"
		}
		if kind == "veth" {
			return "veth", "container"
		}
		return kind, "virtual"
	}
	if strings.HasPrefix(name, "wl") {
		return "wifi", "physical"
	}
	if strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "docker") {
		return "docker_bridge", "container"
	}
	if strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "vnet") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "tun") {
		return "virtual", "virtual"
	}
	return "ethernet", "physical"
}
func (p NetworkProvider) readStats(name string, now time.Time) NetworkStats {
	base := filepath.Join("/sys/class/net", name, "statistics")
	s := NetworkStats{
		RXBytes: parseUint(fileText(p, filepath.Join(base, "rx_bytes"), 64)), TXBytes: parseUint(fileText(p, filepath.Join(base, "tx_bytes"), 64)),
		RXPackets: parseUint(fileText(p, filepath.Join(base, "rx_packets"), 64)), TXPackets: parseUint(fileText(p, filepath.Join(base, "tx_packets"), 64)),
		RXErrors: parseUint(fileText(p, filepath.Join(base, "rx_errors"), 64)), TXErrors: parseUint(fileText(p, filepath.Join(base, "tx_errors"), 64)),
		RXDropped: parseUint(fileText(p, filepath.Join(base, "rx_dropped"), 64)), TXDropped: parseUint(fileText(p, filepath.Join(base, "tx_dropped"), 64)),
		SampledAt: now.UTC().Format(time.RFC3339Nano),
	}
	if p.mu == nil {
		return s
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.previous == nil {
		p.previous = map[string]counterSample{}
	}
	if prev, ok := p.previous[name]; ok {
		seconds := now.Sub(prev.At).Seconds()
		if seconds >= 0.1 && s.RXBytes >= prev.RX && s.TXBytes >= prev.TX {
			rx := float64(s.RXBytes-prev.RX) / seconds
			tx := float64(s.TXBytes-prev.TX) / seconds
			s.RXBytesPerSecond = &rx
			s.TXBytesPerSecond = &tx
		}
	}
	p.previous[name] = counterSample{RX: s.RXBytes, TX: s.TXBytes, At: now}
	return s
}
func parseLinks(raw []byte) ([]ipLink, error) {
	var links []ipLink
	if json.Unmarshal(raw, &links) != nil {
		return nil, ErrUnavailable
	}
	if len(links) > MaxNetworkInterfaces {
		return nil, ErrUnavailable
	}
	return links, nil
}
func mergeAddresses(links []ipLink, raw []byte) error {
	var rows []ipLink
	if json.Unmarshal(raw, &rows) != nil {
		return ErrUnavailable
	}
	by := map[int][]ipAddress{}
	for _, x := range rows {
		by[x.IfIndex] = x.AddrInfo
	}
	for i := range links {
		links[i].AddrInfo = by[links[i].IfIndex]
	}
	return nil
}
func parseRoutes(raw []byte, family string) ([]NetworkRoute, error) {
	var rows []ipRoute
	if json.Unmarshal(raw, &rows) != nil {
		return nil, ErrUnavailable
	}
	if len(rows) > MaxNetworkRoutes {
		return nil, ErrUnavailable
	}
	out := make([]NetworkRoute, 0, len(rows))
	for _, r := range rows {
		dst := safeNetworkText(r.Dst, 128)
		def := dst == "" || dst == "default"
		if def {
			if family == "ipv6" {
				dst = "::/0"
			} else {
				dst = "0.0.0.0/0"
			}
		}
		container := strings.HasPrefix(r.Dev, "docker") || strings.HasPrefix(r.Dev, "br-") || strings.HasPrefix(r.Dev, "veth")
		out = append(out, NetworkRoute{Family: family, Destination: dst, Gateway: safeNetworkText(r.Gateway, 128), Interface: safeNetworkText(r.Dev, 64), Source: safeNetworkText(r.PrefSrc, 128), Metric: r.Metric, Table: routeTable(r.Table), Protocol: safeNetworkText(r.Protocol, 32), Scope: safeNetworkText(r.Scope, 32), Type: safeNetworkText(r.Type, 32), Default: def, Container: container})
	}
	return out, nil
}
func statusFromNetworkError(err error, detail string) Status {
	if errors.Is(err, ErrPermission) || errors.Is(err, os.ErrPermission) {
		return Status{State: PermissionDenied, Detail: detail}
	}
	if errors.Is(err, ErrUnsupported) {
		return Status{State: Unsupported, Detail: detail}
	}
	return Status{State: Unavailable, Detail: detail}
}
func (p NetworkProvider) collectDNS() DNSConfiguration {
	d := DNSConfiguration{Servers: []string{}, SearchDomains: []string{}, Source: "static or unknown", ResolvConfPath: "/etc/resolv.conf", SplitDNS: Status{State: Unsupported, Detail: "interface routing domains are not available from resolv.conf"}}
	if target, e := p.ReadLink("/etc/resolv.conf"); e == nil {
		d.ResolvConfTarget = safeNetworkText(target, 512)
		lower := strings.ToLower(target)
		if strings.Contains(lower, "systemd/resolve") {
			d.Source = "systemd-resolved"
		} else if strings.Contains(lower, "networkmanager") {
			d.Source = "NetworkManager"
		}
	}
	raw, e := p.ReadFile("/etc/resolv.conf", 256*1024)
	if e != nil {
		d.Status = statusFromNetworkError(e, "resolver configuration is unavailable")
		return d
	}
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		line := strings.TrimSpace(strings.SplitN(s.Text(), "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			if ip := net.ParseIP(fields[1]); ip != nil && len(d.Servers) < 16 {
				d.Servers = append(d.Servers, ip.String())
			}
		case "search", "domain":
			for _, v := range fields[1:] {
				if len(d.SearchDomains) < 32 {
					d.SearchDomains = append(d.SearchDomains, safeNetworkText(v, 253))
				}
			}
		}
	}
	if entries, readErr := os.ReadDir("/run/systemd/resolve/netif"); readErr == nil {
		for _, entry := range entries {
			if len(d.Interfaces) >= MaxNetworkInterfaces || entry.IsDir() {
				break
			}
			index, parseErr := strconv.Atoi(entry.Name())
			if parseErr != nil || index < 1 {
				continue
			}
			content, fileErr := p.ReadFile(filepath.Join("/run/systemd/resolve/netif", entry.Name()), 64*1024)
			if fileErr != nil {
				continue
			}
			item := DNSInterface{Interface: strconv.Itoa(index), Servers: []string{}, Domains: []string{}}
			if link, linkErr := net.InterfaceByIndex(index); linkErr == nil {
				item.Interface = safeNetworkText(link.Name, 64)
			}
			scanner := bufio.NewScanner(bytes.NewReader(content))
			for scanner.Scan() {
				fields := strings.SplitN(scanner.Text(), "=", 2)
				if len(fields) != 2 {
					continue
				}
				values := strings.Fields(fields[1])
				switch fields[0] {
				case "DNS":
					for _, value := range values {
						if ip := net.ParseIP(value); ip != nil && len(item.Servers) < 16 {
							item.Servers = append(item.Servers, ip.String())
						}
					}
				case "DOMAINS":
					for _, value := range values {
						if len(item.Domains) < 32 {
							item.Domains = append(item.Domains, safeNetworkText(value, 253))
						}
					}
				case "DNSSEC":
					d.DNSSEC = safeNetworkText(fields[1], 32)
				}
			}
			if len(item.Servers) > 0 || len(item.Domains) > 0 {
				d.Interfaces = append(d.Interfaces, item)
			}
		}
		if len(d.Interfaces) > 0 {
			d.SplitDNS = Status{State: Available, Detail: "systemd-resolved per-link DNS and routing domains are available"}
		}
	}
	if len(d.Servers) == 0 {
		d.Status = Status{State: NotConfigured, Detail: "no resolver servers are configured"}
	} else {
		d.Status = Status{State: Available, Detail: "resolver configuration is present; reachability is not implied"}
	}
	return d
}
func (p NetworkProvider) exists(path string) bool { _, e := p.Stat(path); return e == nil }
func (p NetworkProvider) backends() map[string]Status {
	out := map[string]Status{}
	if p.exists("/run/NetworkManager") {
		out["network_manager"] = Status{State: Available, Detail: "NetworkManager runtime state is present"}
	} else {
		out["network_manager"] = Status{State: NotConfigured, Detail: "NetworkManager runtime state is not present"}
	}
	if p.exists("/run/systemd/netif") {
		out["systemd_networkd"] = Status{State: Available, Detail: "systemd-networkd runtime state is present"}
	} else {
		out["systemd_networkd"] = Status{State: NotConfigured, Detail: "systemd-networkd runtime state is not present"}
	}
	if p.exists("/etc/netplan") {
		out["netplan"] = Status{State: Available, Detail: "Netplan configuration layer is installed; renderer ownership is reported separately"}
	} else {
		out["netplan"] = Status{State: NotConfigured, Detail: "Netplan is not installed"}
	}
	if p.exists("/etc/network/interfaces") {
		out["ifupdown"] = Status{State: Available, Detail: "traditional interfaces configuration is present"}
	} else {
		out["ifupdown"] = Status{State: NotConfigured, Detail: "traditional interfaces configuration is not present"}
	}
	return out
}
func backendFor(name string, b map[string]Status) string {
	if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
		return "container runtime"
	}
	if b["network_manager"].State == Available {
		return "NetworkManager (ownership not asserted)"
	}
	if b["systemd_networkd"].State == Available {
		return "systemd-networkd (ownership not asserted)"
	}
	return "kernel/static"
}

func (p NetworkProvider) Collect(ctx context.Context) (NetworkInventory, error) {
	result := NetworkInventory{Capabilities: map[string]Status{}, Backends: p.backends(), Interfaces: []NetworkInterface{}, Routes: []NetworkRoute{}}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	raw, err := p.Runner.Run(ctx, "links")
	if err != nil {
		return result, err
	}
	links, err := parseLinks(raw)
	if err != nil {
		return result, err
	}
	if addressRaw, e := p.Runner.Run(ctx, "addresses"); e == nil {
		if e = mergeAddresses(links, addressRaw); e != nil {
			return result, e
		}
		result.Capabilities["addresses"] = Status{State: Available, Detail: "kernel address inventory available"}
	} else {
		result.Capabilities["addresses"] = statusFromNetworkError(e, "address inventory unavailable")
	}
	now := p.Now()
	for _, link := range links {
		if !ValidInterfaceName(link.IfName) {
			continue
		}
		kind, class := classifyInterface(link)
		base := filepath.Join("/sys/class/net", link.IfName)
		speed := parseInt64Ptr(fileText(p, filepath.Join(base, "speed"), 64))
		carrier := boolFile(fileText(p, filepath.Join(base, "carrier"), 64))
		driver := ""
		if target, e := p.ReadLink(filepath.Join(base, "device/driver")); e == nil {
			driver = filepath.Base(target)
		}
		if driver == "virtio_net" {
			kind, class = "virtio", "virtual"
		}
		addresses := []NetworkAddress{}
		for _, a := range link.AddrInfo {
			if net.ParseIP(a.Local) != nil {
				addresses = append(addresses, NetworkAddress{Family: safeNetworkText(a.Family, 16), Address: safeNetworkText(a.Local, 128), PrefixLength: a.PrefixLen, Scope: safeNetworkText(a.Scope, 32)})
			}
		}
		admin := "down"
		if flagState(link.Flags, "UP") {
			admin = "up"
		}
		stats := p.readStats(link.IfName, now)
		warnings := []string{}
		if stats.RXErrors+stats.TXErrors > 0 {
			warnings = append(warnings, "packet errors observed")
		}
		if stats.RXDropped+stats.TXDropped > 0 {
			warnings = append(warnings, "packet drops observed")
		}
		result.Interfaces = append(result.Interfaces, NetworkInterface{Name: safeNetworkText(link.IfName, 64), Index: link.IfIndex, Type: kind, Classification: class, MACAddress: safeNetworkText(link.Address, 64), AdministrativeState: admin, OperationalState: safeNetworkText(strings.ToLower(link.OperState), 32), Carrier: carrier, MTU: link.MTU, SpeedMbps: speed, Duplex: safeNetworkText(fileText(p, filepath.Join(base, "duplex"), 64), 16), Driver: safeNetworkText(driver, 128), Flags: link.Flags, Addresses: addresses, Parent: safeNetworkText(link.Link, 64), Master: safeNetworkText(link.Master, 64), Backend: backendFor(link.IfName, result.Backends), Stats: stats, Warnings: warnings, Status: Status{State: Available}})
	}
	for _, spec := range []struct{ op, family string }{{"routes4", "ipv4"}, {"routes6", "ipv6"}} {
		routeRaw, e := p.Runner.Run(ctx, spec.op)
		if e != nil {
			result.Capabilities["routes_"+spec.family] = statusFromNetworkError(e, "route inventory unavailable")
			continue
		}
		routes, e := parseRoutes(routeRaw, spec.family)
		if e != nil {
			return result, e
		}
		result.Routes = append(result.Routes, routes...)
		result.Capabilities["routes_"+spec.family] = Status{State: Available, Detail: "kernel routes available"}
	}
	for _, op := range []string{"rules4", "rules6"} {
		if _, e := p.Runner.Run(ctx, op); e == nil {
			result.Capabilities["policy_routing"] = Status{State: Available, Detail: "policy rules can be inspected; routes retain table identifiers"}
			break
		}
	}
	result.DNS = p.collectDNS()
	result.Capabilities["dns"] = result.DNS.Status
	result.Capabilities["traffic"] = Status{State: Available, Detail: "kernel counters with request-to-request rate samples"}
	result.Capabilities["configuration_mutation"] = Status{State: Unsupported, Detail: "network configuration changes are disabled in this milestone"}
	result.Summary.InterfaceCount = len(result.Interfaces)
	result.Summary.ResolverCount = len(result.DNS.Servers)
	result.Summary.TrafficScope = "per-interface counters; virtual interfaces are not summed into host throughput"
	for _, item := range result.Interfaces {
		if item.AdministrativeState == "up" && item.OperationalState != "down" {
			result.Summary.ActiveCount++
		}
		if item.Classification == "physical" {
			result.Summary.PhysicalCount++
		}
	}
	for _, route := range result.Routes {
		if route.Default && !route.Container {
			result.Summary.DefaultRouteCount++
		}
	}
	if len(result.Interfaces) == 0 {
		result.Summary.Health = "unavailable"
		result.Summary.HealthDetail = "no interfaces were discovered"
	} else if result.Summary.DefaultRouteCount == 0 || result.DNS.Status.State != Available {
		result.Summary.Health = "not_configured"
		result.Summary.HealthDetail = "inventory is available; a default route or resolver configuration is absent"
	} else {
		result.Summary.Health = "healthy"
		result.Summary.HealthDetail = "interfaces, a non-container default route, and resolver configuration are present; external connectivity was not assumed"
	}
	sort.Slice(result.Interfaces, func(i, j int) bool { return result.Interfaces[i].Index < result.Interfaces[j].Index })
	return result, nil
}

type DiagnosticRequest struct {
	Operation string `json:"operation"`
	Target    string `json:"target,omitempty"`
	Port      int    `json:"port,omitempty"`
	Interface string `json:"interface,omitempty"`
}
type DiagnosticResult struct {
	Operation         string   `json:"operation"`
	Target            string   `json:"target,omitempty"`
	Success           bool     `json:"success"`
	DurationMS        int64    `json:"duration_ms"`
	Category          string   `json:"category"`
	Detail            string   `json:"detail"`
	Addresses         []string `json:"addresses,omitempty"`
	Interface         string   `json:"interface,omitempty"`
	Gateway           string   `json:"gateway,omitempty"`
	Source            string   `json:"source,omitempty"`
	PacketLossPercent *float64 `json:"packet_loss_percent,omitempty"`
	ResponseTimeMS    *float64 `json:"response_time_ms,omitempty"`
}
type DiagnosticPolicy struct {
	ExtraTargets  []string
	Ports         map[int]bool
	MaxPerMinute  int
	MaxConcurrent int
}
type DiagnosticProvider struct {
	Network  NetworkProvider
	Policy   DiagnosticPolicy
	Resolver *net.Resolver
	Dial     func(context.Context, string, string) (net.Conn, error)
	Now      func() time.Time
	mu       sync.Mutex
	recent   []time.Time
	active   int
}

func DefaultDiagnosticPolicy(targets, ports string) DiagnosticPolicy {
	p := DiagnosticPolicy{MaxPerMinute: 10, MaxConcurrent: 2, Ports: map[int]bool{53: true, 80: true, 443: true}}
	for _, v := range strings.Split(targets, ",") {
		if v = strings.TrimSpace(v); v != "" && len(v) <= 253 {
			p.ExtraTargets = append(p.ExtraTargets, v)
		}
	}
	if ports != "" {
		p.Ports = map[int]bool{}
		for _, v := range strings.Split(ports, ",") {
			if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 && n <= 65535 {
				p.Ports[n] = true
			}
		}
	}
	return p
}
func NewDiagnosticProvider(network NetworkProvider, policy DiagnosticPolicy) *DiagnosticProvider {
	return &DiagnosticProvider{Network: network, Policy: policy, Resolver: net.DefaultResolver, Now: time.Now, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
	}}
}
func validHost(value string) bool {
	if len(value) < 1 || len(value) > 253 {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	value = strings.TrimSuffix(value, ".")
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || !hostLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}
func (d *DiagnosticProvider) begin() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Now()
	cut := now.Add(-time.Minute)
	keep := d.recent[:0]
	for _, v := range d.recent {
		if v.After(cut) {
			keep = append(keep, v)
		}
	}
	d.recent = keep
	if len(d.recent) >= d.Policy.MaxPerMinute {
		return fmt.Errorf("rate limit: %w", ErrUnavailable)
	}
	if d.active >= d.Policy.MaxConcurrent {
		return fmt.Errorf("concurrency limit: %w", ErrUnavailable)
	}
	d.recent = append(d.recent, now)
	d.active++
	return nil
}
func (d *DiagnosticProvider) end() { d.mu.Lock(); d.active--; d.mu.Unlock() }
func (d *DiagnosticProvider) allowed(target string, inventory NetworkInventory) bool {
	for _, v := range d.Policy.ExtraTargets {
		if strings.EqualFold(v, target) {
			return true
		}
		if _, network, e := net.ParseCIDR(v); e == nil {
			if ip := net.ParseIP(target); ip != nil && network.Contains(ip) {
				return true
			}
		}
	}
	for _, v := range inventory.DNS.Servers {
		if v == target {
			return true
		}
	}
	for _, r := range inventory.Routes {
		if r.Default && r.Gateway == target && !r.Container {
			return true
		}
	}
	return false
}
func diagnosticFailure(op, target string, start time.Time, err error) DiagnosticResult {
	category, detail := "failed", "diagnostic failed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		category, detail = "timeout", "diagnostic timed out"
	} else if op == "dns" {
		detail = "name resolution failed"
	} else if op == "tcp" {
		detail = "TCP connection failed"
	} else if op == "route" {
		detail = "kernel route lookup failed"
	}
	return DiagnosticResult{Operation: op, Target: target, DurationMS: time.Since(start).Milliseconds(), Category: category, Detail: detail}
}

func (d *DiagnosticProvider) Run(ctx context.Context, request DiagnosticRequest) (DiagnosticResult, error) {
	request.Operation = strings.ToLower(strings.TrimSpace(request.Operation))
	request.Target = strings.TrimSpace(request.Target)
	if !map[string]bool{"dns": true, "tcp": true, "icmp": true, "route": true, "gateway": true}[request.Operation] {
		return DiagnosticResult{}, ErrInvalid
	}
	if request.Operation != "gateway" && !validHost(request.Target) {
		return DiagnosticResult{}, ErrInvalid
	}
	if request.Port < 0 || request.Port > 65535 {
		return DiagnosticResult{}, ErrInvalid
	}
	if request.Interface != "" && !ValidInterfaceName(request.Interface) {
		return DiagnosticResult{}, ErrInvalid
	}
	if e := d.begin(); e != nil {
		return DiagnosticResult{}, e
	}
	defer d.end()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	inventory, e := d.Network.Collect(ctx)
	if e != nil {
		return DiagnosticResult{}, e
	}
	gatewayCheck := request.Operation == "gateway"
	if gatewayCheck {
		for _, route := range inventory.Routes {
			if route.Default && !route.Container && (request.Interface == "" || route.Interface == request.Interface) && route.Gateway != "" {
				request.Target = route.Gateway
				request.Operation = "icmp"
				break
			}
		}
		if request.Target == "" {
			return DiagnosticResult{}, ErrUnavailable
		}
	}
	if request.Operation != "dns" && !d.allowed(request.Target, inventory) {
		return DiagnosticResult{}, ErrPermission
	}
	start := time.Now()
	switch request.Operation {
	case "dns":
		addresses, e := d.Resolver.LookupIPAddr(ctx, request.Target)
		if e != nil {
			return diagnosticFailure("dns", request.Target, start, e), nil
		}
		values := []string{}
		for _, address := range addresses {
			if len(values) < maxDiagnosticResults {
				values = append(values, address.IP.String())
			}
		}
		return DiagnosticResult{Operation: "dns", Target: request.Target, Success: true, DurationMS: time.Since(start).Milliseconds(), Category: "resolved", Detail: "hostname resolved", Addresses: values}, nil
	case "tcp":
		if request.Port == 0 || !d.Policy.Ports[request.Port] {
			return DiagnosticResult{}, ErrPermission
		}
		connection, e := d.Dial(ctx, "tcp", net.JoinHostPort(request.Target, strconv.Itoa(request.Port)))
		if e != nil {
			return diagnosticFailure("tcp", request.Target, start, e), nil
		}
		connection.Close()
		return DiagnosticResult{Operation: "tcp", Target: request.Target, Success: true, DurationMS: time.Since(start).Milliseconds(), Category: "connected", Detail: "TCP connection completed"}, nil
	case "route":
		return runRouteDiagnostic(ctx, request.Target, start)
	case "icmp":
		result, runErr := runPingDiagnostic(ctx, request.Target, start)
		if gatewayCheck {
			result.Operation = "gateway"
		}
		return result, runErr
	}
	return DiagnosticResult{}, ErrInvalid
}
func runRouteDiagnostic(ctx context.Context, target string, start time.Time) (DiagnosticResult, error) {
	path, e := findIP()
	if e != nil {
		return DiagnosticResult{}, e
	}
	cmd := exec.CommandContext(ctx, path, "-json", "route", "get", target)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/usr/sbin"}
	out := &limitedBuffer{Max: 64 * 1024}
	cmd.Stdout = out
	if e = cmd.Run(); e != nil {
		return diagnosticFailure("route", target, start, e), nil
	}
	var rows []ipRoute
	if json.Unmarshal(out.Bytes(), &rows) != nil || len(rows) != 1 {
		return DiagnosticResult{}, ErrUnavailable
	}
	r := rows[0]
	return DiagnosticResult{Operation: "route", Target: target, Success: true, DurationMS: time.Since(start).Milliseconds(), Category: "route_found", Detail: "kernel route selected", Interface: safeNetworkText(r.Dev, 64), Gateway: safeNetworkText(r.Gateway, 128), Source: safeNetworkText(r.PrefSrc, 128)}, nil
}

var pingTimeRE = regexp.MustCompile(`time[=<]([0-9]+(?:\.[0-9]+)?)\s*ms`)

func runPingDiagnostic(ctx context.Context, target string, start time.Time) (DiagnosticResult, error) {
	path := "/usr/bin/ping"
	if _, e := os.Stat(path); e != nil {
		return DiagnosticResult{}, ErrUnsupported
	}
	cmd := exec.CommandContext(ctx, path, "-n", "-c", "1", "-W", "2", "--", target)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/usr/sbin"}
	out := &limitedBuffer{Max: 64 * 1024}
	cmd.Stdout = out
	cmd.Stderr = out
	e := cmd.Run()
	loss := 100.0
	result := DiagnosticResult{Operation: "icmp", Target: target, DurationMS: time.Since(start).Milliseconds(), PacketLossPercent: &loss}
	if e != nil {
		message := strings.ToLower(out.String())
		if strings.Contains(message, "operation not permitted") || strings.Contains(message, "permission denied") {
			return DiagnosticResult{}, ErrPermission
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return diagnosticFailure("icmp", target, start, ctx.Err()), nil
		}
		result.Category = "unreachable"
		result.Detail = "no ICMP response was received"
		return result, nil
	}
	loss = 0
	result.Success = true
	result.Category = "reachable"
	result.Detail = "ICMP response received"
	if match := pingTimeRE.FindStringSubmatch(out.String()); len(match) == 2 {
		if value, parseErr := strconv.ParseFloat(match[1], 64); parseErr == nil {
			result.ResponseTimeMS = &value
		}
	}
	return result, nil
}
