package docker

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prefix(value string) netip.Prefix { return netip.MustParsePrefix(value) }

func TestNetworkPrerequisiteRequiresExplicitPool(t *testing.T) {
	err := ValidateNetworkPrerequisites(nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no explicit default address pool") {
		t.Fatalf("expected explicit pool failure, got %v", err)
	}
}

func TestNetworkPrerequisiteRequiresSixtyFourAvailableNetworks(t *testing.T) {
	pools := []DefaultAddressPool{{Base: "10.200.0.0/19", Size: 24}}
	err := ValidateNetworkPrerequisites(pools, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "provide 32 available") {
		t.Fatalf("expected capacity failure, got %v", err)
	}
}

func TestNetworkPrerequisiteRejectsPublicPool(t *testing.T) {
	err := ValidateNetworkPrerequisites([]DefaultAddressPool{{Base: "8.0.0.0/8", Size: 24}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not contained in RFC 1918") {
		t.Fatalf("expected public pool failure, got %v", err)
	}
}

func TestNetworkPrerequisiteAccountsForExistingNetworks(t *testing.T) {
	pools := []DefaultAddressPool{{Base: "10.200.0.0/18", Size: 24}}
	networks := []ExistingNetwork{{Name: "existing", Interface: "br-existing", Subnet: prefix("10.200.0.0/24")}}
	err := ValidateNetworkPrerequisites(pools, networks, []HostRoute{{Interface: "eth0", Prefix: prefix("0.0.0.0/0")}, {Interface: "br-existing", Prefix: prefix("10.200.0.0/24")}})
	if err == nil || !strings.Contains(err.Error(), "provide 63 available") {
		t.Fatalf("expected consumed capacity failure, got %v", err)
	}
}

func TestNetworkPrerequisiteRejectsObviousHostRouteOverlap(t *testing.T) {
	pools := []DefaultAddressPool{{Base: "10.128.0.0/9", Size: 24}}
	err := ValidateNetworkPrerequisites(pools, nil, []HostRoute{{Interface: "eth0", Prefix: prefix("0.0.0.0/0")}, {Interface: "wg0", Prefix: prefix("10.140.0.0/16")}})
	if err == nil || !strings.Contains(err.Error(), "overlaps existing host route 10.140.0.0/16") {
		t.Fatalf("expected route collision failure, got %v", err)
	}
}

func TestNetworkPrerequisiteDoesNotMistakeHostRouteForDockerBridge(t *testing.T) {
	pools := []DefaultAddressPool{{Base: "10.128.0.0/9", Size: 24}}
	networks := []ExistingNetwork{{Name: "kitpro", Interface: "br-kitpro", Subnet: prefix("10.140.0.0/16")}}
	routes := []HostRoute{{Interface: "eth0", Prefix: prefix("10.140.0.0/16")}}
	if err := ValidateNetworkPrerequisites(pools, networks, routes); err == nil || !strings.Contains(err.Error(), "on eth0") {
		t.Fatalf("expected non-Docker interface collision, got %v", err)
	}
}

func TestNetworkPrerequisiteAcceptsLargeNonOverlappingPool(t *testing.T) {
	pools := []DefaultAddressPool{{Base: "10.128.0.0/9", Size: 24}}
	networks := []ExistingNetwork{{Name: "kitpro", Interface: "br-kitpro", Subnet: prefix("10.128.1.0/24")}, {Name: "legacy", Interface: "br-legacy", Subnet: prefix("172.20.0.0/16")}}
	routes := []HostRoute{{Interface: "eth0", Prefix: prefix("0.0.0.0/0")}, {Interface: "eth0", Prefix: prefix("10.10.0.0/24")}, {Interface: "br-kitpro", Prefix: prefix("10.128.1.0/24")}, {Interface: "br-legacy", Prefix: prefix("172.20.0.0/16")}}
	if err := ValidateNetworkPrerequisites(pools, networks, routes); err != nil {
		t.Fatalf("expected suitable pool, got %v", err)
	}
}

func TestReadIPv4Routes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "route")
	contents := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\neth0 00000A0A 00000000 0001 0 0 0 00FFFFFF 0 0 0\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	routes, err := readIPv4Routes(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Interface != "eth0" || routes[0].Prefix.String() != "10.10.0.0/24" {
		t.Fatalf("unexpected routes: %v", routes)
	}
}
