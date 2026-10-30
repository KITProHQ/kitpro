package hostadmin

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type managementNetworkRunner struct{ defaultRoute bool }

func (r managementNetworkRunner) Run(_ context.Context, operation string) ([]byte, error) {
	switch operation {
	case "links":
		return []byte(`[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP"],"mtu":65536,"operstate":"UNKNOWN","linkinfo":{"info_kind":"loopback"}},{"ifindex":9,"ifname":"kitpro-test0","flags":["UP"],"mtu":1500,"operstate":"UP","linkinfo":{"info_kind":"dummy"}}]`), nil
	case "addresses":
		return []byte(`[{"ifindex":1,"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},{"ifindex":9,"ifname":"kitpro-test0","addr_info":[]}]`), nil
	case "routes4":
		if r.defaultRoute {
			return []byte(`[{"dst":"default","dev":"kitpro-test0","gateway":"192.0.2.1"}]`), nil
		}
		return []byte(`[]`), nil
	case "routes6", "rules4", "rules6":
		return []byte(`[]`), nil
	}
	return nil, ErrInvalid
}

func managementProvider(defaultRoute bool) NetworkProvider {
	return NetworkProvider{Runner: managementNetworkRunner{defaultRoute}, ReadFile: func(path string, _ int64) ([]byte, error) {
		if path == "/run/systemd/netif/state" {
			return []byte("OPER_STATE=routable\n"), nil
		}
		return nil, ErrUnavailable
	}, ReadLink: func(string) (string, error) { return "", ErrUnavailable }, Stat: func(path string) (os.FileInfo, error) {
		if path == "/run/systemd/netif" {
			return os.Stat(".")
		}
		return nil, ErrUnavailable
	}, Now: time.Now, mu: &sync.Mutex{}, previous: map[string]counterSample{}}
}

func approvedNetworkResource() NetworkPolicyResource {
	return NetworkPolicyResource{ID: "qualification", Interface: "kitpro-test0", Backend: NetworkBackendNetworkd, Profile: "90-kitpro-qualification.network", AllowedModes: []string{NetworkModeDHCP, NetworkModeStatic}, QualificationOnly: true}
}

func TestNetworkCandidateValidationRejectsUnsafeValues(t *testing.T) {
	r := approvedNetworkResource()
	valid := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, Gateway: "192.0.2.1", DNSServers: []string{"192.0.2.53"}, SearchDomains: []string{"test.example"}}
	if err := ValidateNetworkCandidate(r, valid); err != nil {
		t.Fatalf("valid candidate: %v", err)
	}
	tests := []NetworkCandidate{
		{Mode: NetworkModeDHCP, Addresses: []string{"192.0.2.20/24"}},
		{Mode: NetworkModeStatic, Addresses: []string{"not-an-address"}},
		{Mode: NetworkModeStatic, Addresses: []string{"224.0.0.1/24"}},
		{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, Gateway: "bad"},
		{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, Gateway: "198.51.100.1"},
		{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, DNSServers: []string{"0.0.0.0"}},
		{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, SearchDomains: []string{"bad domain"}},
	}
	for i, candidate := range tests {
		if ValidateNetworkCandidate(r, candidate) == nil {
			t.Errorf("unsafe candidate %d accepted", i)
		}
	}
}

func TestNetworkdProfileContainsOnlyTypedConfiguration(t *testing.T) {
	r := approvedNetworkResource()
	candidate := NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}, Gateway: "192.0.2.1", DNSServers: []string{"192.0.2.53"}, SearchDomains: []string{"test.example."}}
	profile, err := NetworkdProfile(r, candidate)
	if err != nil {
		t.Fatal(err)
	}
	text := string(profile)
	for _, want := range []string{"Name=kitpro-test0", "Address=192.0.2.20/24", "Gateway=192.0.2.1", "DNS=192.0.2.53", "Domains=test.example", "IPv6AcceptRA=no"} {
		if !strings.Contains(text, want) {
			t.Errorf("profile missing %q: %s", want, text)
		}
	}
}

func TestNetworkPreviewRequiresRecoveryAndProtectsDefaultRoute(t *testing.T) {
	policy := NetworkPolicy{Version: 1, Resources: []NetworkPolicyResource{approvedNetworkResource()}}
	manager := NewNetworkManager(policy, managementProvider(false))
	manager.ReadCurrent = func(context.Context, NetworkPolicyResource) (NetworkCandidate, error) {
		return NetworkCandidate{Mode: NetworkModeDHCP}, nil
	}
	manager.RecoveryAvailable = func() bool { return false }
	preview, err := manager.Preview(context.Background(), "qualification", NetworkCandidate{Mode: NetworkModeDHCP})
	if err != nil || preview.Eligibility.State != "recovery_unavailable" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	manager = NewNetworkManager(policy, managementProvider(true))
	manager.ReadCurrent = func(context.Context, NetworkPolicyResource) (NetworkCandidate, error) {
		return NetworkCandidate{Mode: NetworkModeDHCP}, nil
	}
	preview, err = manager.Preview(context.Background(), "qualification", NetworkCandidate{Mode: NetworkModeDHCP})
	if err != nil || preview.Eligibility.State != "protected" {
		t.Fatalf("default route preview=%#v err=%v", preview, err)
	}
}

func TestNetworkPreviewAllowsExactDisposableInterface(t *testing.T) {
	policy := NetworkPolicy{Version: 1, Resources: []NetworkPolicyResource{approvedNetworkResource()}}
	manager := NewNetworkManager(policy, managementProvider(false))
	manager.ReadCurrent = func(context.Context, NetworkPolicyResource) (NetworkCandidate, error) {
		return NetworkCandidate{Mode: NetworkModeDHCP}, nil
	}
	preview, err := manager.Preview(context.Background(), "qualification", NetworkCandidate{Mode: NetworkModeStatic, Addresses: []string{"192.0.2.20/24"}})
	if err != nil || !preview.Eligibility.Allowed || preview.Proposed.Digest == "" || preview.Confirmation != "kitpro-test0" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
}

func TestKernelNetworkSafetyFailsClosedForLoopbackAndMissingLinks(t *testing.T) {
	for _, resource := range []NetworkPolicyResource{
		{ID: "loopback", Interface: "lo", Backend: NetworkBackendNetworkd, Profile: "90-kitpro-loopback.network", AllowedModes: []string{NetworkModeStatic}},
		{ID: "missing", Interface: "kitpro-missing0", Backend: NetworkBackendNetworkd, Profile: "90-kitpro-missing.network", AllowedModes: []string{NetworkModeStatic}},
	} {
		if err := VerifyNetworkResourceSafeKernel(resource); err == nil {
			t.Fatalf("resource %s unexpectedly eligible", resource.Interface)
		}
	}
}
