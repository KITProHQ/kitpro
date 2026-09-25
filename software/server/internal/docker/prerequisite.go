package docker

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

// MinimumAvailableNetworkSubnets reserves enough allocator capacity for one
// active, one retained, and one candidate network for each alpha.13 catalog
// application, plus a small amount of operational headroom.
const MinimumAvailableNetworkSubnets uint64 = 64

const addressPoolGuidance = `configure an operator-selected, non-overlapping Docker "default-address-pools" entry with at least 64 available IPv4 subnets of /24 or larger, restart Docker, and run kitpro-helper --verify-host-prerequisites again; Docker applies address-pool changes only to newly created networks`

type DefaultAddressPool struct {
	Base string
	Size int
}

type ExistingNetwork struct {
	Name      string
	Interface string
	Subnet    netip.Prefix
}

type HostRoute struct {
	Interface string
	Prefix    netip.Prefix
}

// VerifyNetworkPrerequisites checks the daemon's explicit allocator policy and
// obvious current host-route collisions. It does not modify Docker settings.
func (c *Client) VerifyNetworkPrerequisites(ctx context.Context) error {
	info, err := c.Info()
	if err != nil {
		return prerequisiteError("inspect Docker daemon configuration: %v", err)
	}
	pools, err := parseDefaultAddressPools(info["DefaultAddressPools"])
	if err != nil {
		return prerequisiteError("read Docker default address pools: %v", err)
	}
	networks, err := c.existingNetworks(ctx)
	if err != nil {
		return prerequisiteError("inspect existing Docker networks: %v", err)
	}
	routes, err := readIPv4Routes("/proc/net/route")
	if err != nil {
		return prerequisiteError("inspect host IPv4 routes: %v", err)
	}
	if err = ValidateNetworkPrerequisites(pools, networks, routes); err != nil {
		return prerequisiteError("%v", err)
	}
	return nil
}

func prerequisiteError(format string, args ...any) error {
	return fmt.Errorf("docker address-pool prerequisite failed: %s; %s", fmt.Sprintf(format, args...), addressPoolGuidance)
}

func parseDefaultAddressPools(value any) ([]DefaultAddressPool, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, errors.New("unexpected DefaultAddressPools value")
	}
	pools := make([]DefaultAddressPool, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("unexpected default address-pool entry")
		}
		base, _ := entry["Base"].(string)
		sizeValue, ok := entry["Size"].(float64)
		if !ok || sizeValue != float64(int(sizeValue)) {
			return nil, fmt.Errorf("address pool %q has an invalid subnet size", base)
		}
		pools = append(pools, DefaultAddressPool{Base: base, Size: int(sizeValue)})
	}
	return pools, nil
}

func (c *Client) existingNetworks(ctx context.Context) ([]ExistingNetwork, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/networks", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("docker network list: HTTP %s", response.Status)
	}
	var raw []struct {
		ID      string            `json:"Id"`
		Name    string            `json:"Name"`
		Driver  string            `json:"Driver"`
		Options map[string]string `json:"Options"`
		IPAM    struct {
			Config []struct {
				Subnet string `json:"Subnet"`
			} `json:"Config"`
		} `json:"IPAM"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	var networks []ExistingNetwork
	for _, network := range raw {
		bridgeInterface := network.Options["com.docker.network.bridge.name"]
		if network.Driver == "bridge" && bridgeInterface == "" {
			if network.Name == "bridge" {
				bridgeInterface = "docker0"
			} else if len(network.ID) >= 12 {
				bridgeInterface = "br-" + network.ID[:12]
			}
		}
		for _, config := range network.IPAM.Config {
			if strings.TrimSpace(config.Subnet) == "" {
				continue
			}
			subnet, parseErr := netip.ParsePrefix(config.Subnet)
			if parseErr != nil {
				return nil, fmt.Errorf("network %q has invalid subnet metadata", network.Name)
			}
			if subnet.Addr().Is4() {
				networks = append(networks, ExistingNetwork{Name: network.Name, Interface: bridgeInterface, Subnet: subnet.Masked()})
			}
		}
	}
	return networks, nil
}

// ValidateNetworkPrerequisites is pure so package and runtime gates share the
// same policy and regression tests can exercise it without a Docker daemon.
func ValidateNetworkPrerequisites(pools []DefaultAddressPool, networks []ExistingNetwork, routes []HostRoute) error {
	if len(pools) == 0 {
		return errors.New("Docker has no explicit default address pool")
	}
	type validatedPool struct {
		prefix netip.Prefix
		size   int
	}
	validated := make([]validatedPool, 0, len(pools))
	for _, pool := range pools {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(pool.Base))
		if err != nil || !prefix.Addr().Is4() {
			return fmt.Errorf("Docker address-pool base %q is not a valid IPv4 CIDR", pool.Base)
		}
		prefix = prefix.Masked()
		if !privateIPv4Prefix(prefix) {
			return fmt.Errorf("Docker address pool %s is not contained in RFC 1918 private address space", prefix)
		}
		if pool.Size <= prefix.Bits() || pool.Size > 24 {
			return fmt.Errorf("Docker address pool %s must allocate subnets no smaller than /24 and narrower than its base", prefix)
		}
		validated = append(validated, validatedPool{prefix: prefix, size: pool.Size})
	}

	for i, pool := range validated {
		for j := i + 1; j < len(validated); j++ {
			if prefixesOverlap(pool.prefix, validated[j].prefix) {
				return fmt.Errorf("Docker default address pools %s and %s overlap", pool.prefix, validated[j].prefix)
			}
		}
	}

	for _, route := range routes {
		route.Prefix = route.Prefix.Masked()
		if !route.Prefix.Addr().Is4() || route.Prefix.Bits() == 0 || routeIsDockerNetwork(route, networks) {
			continue
		}
		for _, pool := range validated {
			if prefixesOverlap(route.Prefix, pool.prefix) {
				return fmt.Errorf("Docker address pool %s overlaps existing host route %s on %s", pool.prefix, route.Prefix, route.Interface)
			}
		}
	}

	var available uint64
	for _, pool := range validated {
		total := uint64(1) << uint(pool.size-pool.prefix.Bits())
		occupied := occupiedChildSubnets(pool.prefix, pool.size, networks)
		if occupied < total {
			available += total - occupied
		}
	}
	if available < MinimumAvailableNetworkSubnets {
		return fmt.Errorf("Docker default address pools provide %d available network subnets; KITPro alpha.13 requires at least %d", available, MinimumAvailableNetworkSubnets)
	}
	return nil
}

func privateIPv4Prefix(prefix netip.Prefix) bool {
	for _, private := range []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	} {
		if private.Bits() <= prefix.Bits() && private.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func routeIsDockerNetwork(route HostRoute, networks []ExistingNetwork) bool {
	for _, network := range networks {
		if network.Interface != "" && route.Interface == network.Interface && route.Prefix == network.Subnet.Masked() {
			return true
		}
	}
	return false
}

func prefixesOverlap(a, b netip.Prefix) bool {
	if a.Addr().BitLen() != b.Addr().BitLen() {
		return false
	}
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

type indexRange struct{ first, last uint64 }

func occupiedChildSubnets(pool netip.Prefix, childBits int, networks []ExistingNetwork) uint64 {
	poolStart := uint64(binary.BigEndian.Uint32(pool.Addr().AsSlice()))
	poolAddresses := uint64(1) << uint(32-pool.Bits())
	poolEnd := poolStart + poolAddresses - 1
	childAddresses := uint64(1) << uint(32-childBits)
	ranges := make([]indexRange, 0, len(networks))
	for _, network := range networks {
		subnet := network.Subnet.Masked()
		if !subnet.Addr().Is4() || !prefixesOverlap(pool, subnet) {
			continue
		}
		start := uint64(binary.BigEndian.Uint32(subnet.Addr().AsSlice()))
		addresses := uint64(1) << uint(32-subnet.Bits())
		end := start + addresses - 1
		if start < poolStart {
			start = poolStart
		}
		if end > poolEnd {
			end = poolEnd
		}
		ranges = append(ranges, indexRange{first: (start - poolStart) / childAddresses, last: (end - poolStart) / childAddresses})
	}
	if len(ranges) == 0 {
		return 0
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].first < ranges[j].first })
	occupied := uint64(0)
	current := ranges[0]
	for _, item := range ranges[1:] {
		if item.first <= current.last+1 {
			if item.last > current.last {
				current.last = item.last
			}
			continue
		}
		occupied += current.last - current.first + 1
		current = item
	}
	return occupied + current.last - current.first + 1
}

func readIPv4Routes(path string) ([]HostRoute, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return nil, errors.New("route table is empty")
	}
	var routes []HostRoute
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		flags, parseErr := strconv.ParseUint(fields[3], 16, 32)
		if parseErr != nil || flags&1 == 0 {
			continue
		}
		destination, parseErr := decodeRouteIPv4(fields[1])
		if parseErr != nil {
			return nil, parseErr
		}
		mask, parseErr := decodeRouteIPv4(fields[7])
		if parseErr != nil {
			return nil, parseErr
		}
		maskValue := binary.BigEndian.Uint32(mask.AsSlice())
		bits := 0
		seenZero := false
		for bit := 31; bit >= 0; bit-- {
			set := maskValue&(uint32(1)<<uint(bit)) != 0
			if seenZero && set {
				return nil, errors.New("host route has a non-contiguous IPv4 mask")
			}
			if set {
				bits++
			} else {
				seenZero = true
			}
		}
		routes = append(routes, HostRoute{Interface: fields[0], Prefix: netip.PrefixFrom(destination, bits).Masked()})
	}
	return routes, nil
}

func decodeRouteIPv4(value string) (netip.Addr, error) {
	bytes, err := hex.DecodeString(value)
	if err != nil || len(bytes) != 4 {
		return netip.Addr{}, fmt.Errorf("invalid IPv4 route value %q", value)
	}
	return netip.AddrFrom4([4]byte{bytes[3], bytes[2], bytes[1], bytes[0]}), nil
}
