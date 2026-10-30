package hostadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	NetworkModeDHCP         = "dhcp"
	NetworkModeStatic       = "static"
	NetworkBackendNetworkd  = "systemd-networkd"
	MaxNetworkDNS           = 6
	MaxNetworkSearchDomains = 6
)

var networkResourceIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type NetworkPolicy struct {
	Version   int                     `json:"version"`
	Resources []NetworkPolicyResource `json:"resources"`
}

type NetworkPolicyResource struct {
	ID                  string   `json:"id"`
	Interface           string   `json:"interface"`
	Backend             string   `json:"backend"`
	Profile             string   `json:"profile"`
	AllowedModes        []string `json:"allowed_modes"`
	ManagementProtected bool     `json:"management_protected"`
	QualificationOnly   bool     `json:"qualification_only,omitempty"`
}

type NetworkCandidate struct {
	Mode          string   `json:"mode"`
	Addresses     []string `json:"addresses,omitempty"`
	Gateway       string   `json:"gateway,omitempty"`
	DNSServers    []string `json:"dns_servers,omitempty"`
	SearchDomains []string `json:"search_domains,omitempty"`
}

type NetworkConfiguration struct {
	ResourceID string           `json:"resource_id"`
	Interface  string           `json:"interface"`
	Backend    string           `json:"backend"`
	Profile    string           `json:"profile"`
	Candidate  NetworkCandidate `json:"candidate"`
	Digest     string           `json:"digest"`
}

type NetworkEligibility struct {
	Allowed       bool     `json:"allowed"`
	State         string   `json:"state"`
	Reason        string   `json:"reason"`
	Protected     bool     `json:"protected"`
	RecoveryReady bool     `json:"recovery_ready"`
	Concerns      []string `json:"concerns,omitempty"`
}

type NetworkConfigurationPreview struct {
	Resource        NetworkPolicyResource `json:"resource"`
	Current         NetworkConfiguration  `json:"current"`
	Proposed        NetworkConfiguration  `json:"proposed"`
	Eligibility     NetworkEligibility    `json:"eligibility"`
	Confirmation    string                `json:"confirmation"`
	DeadlineSeconds int                   `json:"confirmation_deadline_seconds"`
}

type NetworkTransaction struct {
	ID            string               `json:"id"`
	ResourceID    string               `json:"resource_id"`
	Interface     string               `json:"interface"`
	Backend       string               `json:"backend"`
	Previous      NetworkConfiguration `json:"previous"`
	Candidate     NetworkConfiguration `json:"candidate"`
	State         string               `json:"state"`
	Deadline      string               `json:"deadline,omitempty"`
	RecoveryArmed bool                 `json:"recovery_armed"`
	Connectivity  string               `json:"connectivity"`
	Rollback      string               `json:"rollback,omitempty"`
	FailureReason string               `json:"failure_reason,omitempty"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

func ValidNetworkResourceID(value string) bool { return networkResourceIDRE.MatchString(value) }

func LoadNetworkPolicy(path string) (NetworkPolicy, error) {
	raw, err := readBoundedRegular(path, 64*1024)
	if err != nil {
		return NetworkPolicy{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return NetworkPolicy{}, ErrPermission
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Uid != 0 {
		return NetworkPolicy{}, ErrPermission
	}
	var policy NetworkPolicy
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&policy) != nil || dec.Decode(&struct{}{}) != io.EOF || policy.Version != 1 || len(policy.Resources) > 64 {
		return NetworkPolicy{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, resource := range policy.Resources {
		if !ValidNetworkResourceID(resource.ID) || seen[resource.ID] || !ValidInterfaceName(resource.Interface) || resource.Backend != NetworkBackendNetworkd || !validNetworkProfile(resource) || len(resource.AllowedModes) == 0 || len(resource.AllowedModes) > 2 {
			return NetworkPolicy{}, ErrInvalid
		}
		seen[resource.ID] = true
		modes := map[string]bool{}
		for _, mode := range resource.AllowedModes {
			if mode != NetworkModeDHCP && mode != NetworkModeStatic || modes[mode] {
				return NetworkPolicy{}, ErrInvalid
			}
			modes[mode] = true
		}
	}
	return policy, nil
}

func validNetworkProfile(resource NetworkPolicyResource) bool {
	want := "90-kitpro-" + resource.ID + ".network"
	return resource.Profile == want && filepath.Base(resource.Profile) == resource.Profile
}

func (p NetworkPolicy) Resource(id string) (NetworkPolicyResource, bool) {
	for _, resource := range p.Resources {
		if resource.ID == id {
			return resource, true
		}
	}
	return NetworkPolicyResource{}, false
}

func ValidateNetworkCandidate(resource NetworkPolicyResource, candidate NetworkCandidate) error {
	allowed := false
	for _, mode := range resource.AllowedModes {
		allowed = allowed || mode == candidate.Mode
	}
	if !allowed {
		return ErrUnsupported
	}
	if len(candidate.Addresses) > 8 || len(candidate.DNSServers) > MaxNetworkDNS || len(candidate.SearchDomains) > MaxNetworkSearchDomains {
		return ErrInvalid
	}
	if candidate.Mode == NetworkModeDHCP && (len(candidate.Addresses) != 0 || candidate.Gateway != "") {
		return ErrInvalid
	}
	if candidate.Mode == NetworkModeStatic && len(candidate.Addresses) == 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, raw := range candidate.Addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Bits() < 1 || seen[prefix.String()] {
			return ErrInvalid
		}
		seen[prefix.String()] = true
	}
	if candidate.Gateway != "" {
		gateway, err := netip.ParseAddr(candidate.Gateway)
		if err != nil || !gateway.Is4() || gateway.IsUnspecified() || gateway.IsMulticast() {
			return ErrInvalid
		}
		reachable := false
		for _, raw := range candidate.Addresses {
			prefix, _ := netip.ParsePrefix(raw)
			if prefix.Contains(gateway) {
				reachable = true
				break
			}
		}
		if !reachable {
			return ErrInvalid
		}
	}
	seen = map[string]bool{}
	for _, raw := range candidate.DNSServers {
		address, err := netip.ParseAddr(raw)
		if err != nil || address.IsUnspecified() || address.IsMulticast() || seen[address.String()] {
			return ErrInvalid
		}
		seen[address.String()] = true
	}
	seen = map[string]bool{}
	for _, domain := range candidate.SearchDomains {
		value := strings.TrimSuffix(strings.ToLower(domain), ".")
		if value == "" || len(value) > 253 || seen[value] || !validSearchDomain(value) {
			return ErrInvalid
		}
		seen[value] = true
	}
	return nil
}

func validSearchDomain(value string) bool {
	for _, label := range strings.Split(value, ".") {
		if !hostLabelRE.MatchString(label) || len(label) > 63 {
			return false
		}
	}
	return true
}

func CanonicalNetworkCandidate(candidate NetworkCandidate) NetworkCandidate {
	result := candidate
	result.Addresses = append([]string(nil), candidate.Addresses...)
	result.DNSServers = append([]string(nil), candidate.DNSServers...)
	result.SearchDomains = append([]string(nil), candidate.SearchDomains...)
	sort.Strings(result.Addresses)
	sort.Strings(result.DNSServers)
	sort.Strings(result.SearchDomains)
	return result
}

func NetworkConfigurationDigest(resource NetworkPolicyResource, candidate NetworkCandidate) string {
	value, _ := json.Marshal(struct {
		ID, Interface, Backend, Profile string
		Candidate                       NetworkCandidate
	}{resource.ID, resource.Interface, resource.Backend, resource.Profile, CanonicalNetworkCandidate(candidate)})
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func NetworkdProfile(resource NetworkPolicyResource, candidate NetworkCandidate) ([]byte, error) {
	if err := ValidateNetworkCandidate(resource, candidate); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("# Managed by KITPro Network Configuration. Do not edit.\n[Match]\nName=")
	b.WriteString(resource.Interface)
	b.WriteString("\n\n[Network]\n")
	if candidate.Mode == NetworkModeDHCP {
		b.WriteString("DHCP=ipv4\n")
	} else {
		b.WriteString("DHCP=no\n")
		for _, address := range candidate.Addresses {
			b.WriteString("Address=" + address + "\n")
		}
		if candidate.Gateway != "" {
			b.WriteString("Gateway=" + candidate.Gateway + "\n")
		}
	}
	for _, server := range candidate.DNSServers {
		b.WriteString("DNS=" + server + "\n")
	}
	for _, domain := range candidate.SearchDomains {
		b.WriteString("Domains=" + strings.TrimSuffix(strings.ToLower(domain), ".") + "\n")
	}
	b.WriteString("IPv6AcceptRA=no\nLinkLocalAddressing=no\n")
	return []byte(b.String()), nil
}

type NetworkManager struct {
	Policy            NetworkPolicy
	Inventory         NetworkProvider
	ReadCurrent       func(context.Context, NetworkPolicyResource) (NetworkCandidate, error)
	RecoveryAvailable func() bool
}

func NewNetworkManager(policy NetworkPolicy, provider NetworkProvider) *NetworkManager {
	return &NetworkManager{Policy: policy, Inventory: provider, RecoveryAvailable: func() bool { return true }}
}

func (m *NetworkManager) Preview(ctx context.Context, id string, candidate NetworkCandidate) (NetworkConfigurationPreview, error) {
	resource, ok := m.Policy.Resource(id)
	if !ok {
		return NetworkConfigurationPreview{}, ErrInvalid
	}
	preview := NetworkConfigurationPreview{Resource: resource, Proposed: NetworkConfiguration{ResourceID: id, Interface: resource.Interface, Backend: resource.Backend, Profile: resource.Profile, Candidate: CanonicalNetworkCandidate(candidate)}, Confirmation: resource.Interface, DeadlineSeconds: 90}
	preview.Proposed.Digest = NetworkConfigurationDigest(resource, candidate)
	if err := ValidateNetworkCandidate(resource, candidate); err != nil {
		preview.Eligibility = NetworkEligibility{State: "invalid", Reason: "The proposed network configuration is invalid."}
		return preview, nil
	}
	inventory, err := m.Inventory.Collect(ctx)
	if err != nil {
		return preview, err
	}
	if state, reason := NetworkResourceSafety(inventory, resource); state != "eligible" {
		preview.Eligibility = NetworkEligibility{State: state, Reason: reason, Protected: state == "protected"}
		return preview, nil
	}
	for _, requested := range candidate.Addresses {
		prefix, _ := netip.ParsePrefix(requested)
		for _, iface := range inventory.Interfaces {
			if iface.Name == resource.Interface {
				continue
			}
			for _, address := range iface.Addresses {
				observed, parseErr := netip.ParseAddr(address.Address)
				if parseErr == nil && observed == prefix.Addr() {
					preview.Eligibility = NetworkEligibility{State: "address_conflict", Reason: "The proposed address is already present on another interface."}
					return preview, nil
				}
			}
		}
	}
	if m.ReadCurrent == nil {
		preview.Eligibility = NetworkEligibility{State: "configuration_unavailable", Reason: "The current managed configuration cannot be verified."}
		return preview, nil
	}
	current, err := m.ReadCurrent(ctx, resource)
	if err != nil {
		preview.Eligibility = NetworkEligibility{State: "configuration_unavailable", Reason: "The current managed configuration cannot be verified."}
		return preview, nil
	}
	preview.Current = NetworkConfiguration{ResourceID: id, Interface: resource.Interface, Backend: resource.Backend, Profile: resource.Profile, Candidate: CanonicalNetworkCandidate(current), Digest: NetworkConfigurationDigest(resource, current)}
	recovery := m.RecoveryAvailable != nil && m.RecoveryAvailable()
	if !recovery {
		preview.Eligibility = NetworkEligibility{State: "recovery_unavailable", Reason: "Independent network rollback is unavailable."}
		return preview, nil
	}
	preview.Eligibility = NetworkEligibility{Allowed: true, State: "eligible", Reason: "The exact approved interface can be changed with independent rollback.", RecoveryReady: true}
	return preview, nil
}

func NetworkResourceSafety(inventory NetworkInventory, resource NetworkPolicyResource) (string, string) {
	var observed *NetworkInterface
	for i := range inventory.Interfaces {
		if inventory.Interfaces[i].Name == resource.Interface {
			observed = &inventory.Interfaces[i]
			break
		}
	}
	if observed == nil {
		return "unavailable", "The approved interface is not present."
	}
	if resource.ManagementProtected || observed.Classification == "loopback" || strings.HasPrefix(observed.Backend, "container") || strings.HasPrefix(observed.Name, "docker") || strings.HasPrefix(observed.Name, "veth") || strings.HasPrefix(observed.Name, "br-") || observed.Master != "" || hasDefaultRoute(inventory.Routes, resource.Interface) {
		return "protected", "This interface carries protected or management network state."
	}
	if observed.Backend != "" && !strings.HasPrefix(observed.Backend, resource.Backend) && observed.Backend != "unresolved" {
		return "ownership_conflict", "The interface is owned by a different network backend."
	}
	return "eligible", ""
}

func VerifyNetworkResourceSafe(ctx context.Context, provider NetworkProvider, resource NetworkPolicyResource) error {
	inventory, err := provider.Collect(ctx)
	if err != nil {
		return err
	}
	state, _ := NetworkResourceSafety(inventory, resource)
	if state != "eligible" {
		return ErrPermission
	}
	return nil
}

// VerifyNetworkResourceSafeKernel repeats the helper's final safety decision
// without executing an inventory adapter. The confined helper may read only
// fixed kernel paths and must fail closed when link or route identity is
// incomplete.
func VerifyNetworkResourceSafeKernel(resource NetworkPolicyResource) error {
	if !ValidInterfaceName(resource.Interface) || resource.ManagementProtected {
		return ErrPermission
	}
	name := resource.Interface
	base := filepath.Join("/sys/class/net", name)
	ifindexRaw, err := os.ReadFile(filepath.Join(base, "ifindex"))
	if err != nil {
		return ErrUnavailable
	}
	iflinkRaw, err := os.ReadFile(filepath.Join(base, "iflink"))
	if err != nil || strings.TrimSpace(string(iflinkRaw)) != strings.TrimSpace(string(ifindexRaw)) {
		return ErrPermission
	}
	typeRaw, err := os.ReadFile(filepath.Join(base, "type"))
	if err != nil || strings.TrimSpace(string(typeRaw)) == "772" {
		return ErrPermission
	}
	if _, err = os.Lstat(filepath.Join(base, "master")); err == nil || !os.IsNotExist(err) {
		return ErrPermission
	}
	lower := strings.ToLower(name)
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "podman", "tun", "tap", "wg"} {
		if strings.HasPrefix(lower, prefix) {
			return ErrPermission
		}
	}
	for _, routeFile := range []string{"/proc/net/route", "/proc/net/ipv6_route"} {
		raw, readErr := os.ReadFile(routeFile)
		if readErr != nil {
			return ErrUnavailable
		}
		for lineNumber, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || lineNumber == 0 && routeFile == "/proc/net/route" {
				continue
			}
			if routeFile == "/proc/net/route" {
				if len(fields) < 8 {
					return ErrUnavailable
				}
				if fields[0] == name && fields[1] == "00000000" && fields[7] == "00000000" {
					return ErrPermission
				}
				continue
			}
			if len(fields) < 10 {
				return ErrUnavailable
			}
			if fields[len(fields)-1] == name && fields[0] == strings.Repeat("0", 32) && fields[1] == "00" {
				return ErrPermission
			}
		}
	}
	return nil
}

func hasDefaultRoute(routes []NetworkRoute, name string) bool {
	for _, route := range routes {
		if route.Interface == name && route.Default && !route.Container {
			return true
		}
	}
	return false
}

var ErrNetworkConflict = errors.New("network configuration changed")

func NetworkCandidateEqual(a, b NetworkCandidate) bool {
	return NetworkConfigurationDigest(NetworkPolicyResource{ID: "compare", Interface: "compare0", Backend: NetworkBackendNetworkd, Profile: "90-kitpro-compare.network"}, a) == NetworkConfigurationDigest(NetworkPolicyResource{ID: "compare", Interface: "compare0", Backend: NetworkBackendNetworkd, Profile: "90-kitpro-compare.network"}, b)
}

func ValidateNetworkTransactionID(value string) error {
	if len(value) < 20 || len(value) > 128 {
		return ErrInvalid
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return ErrInvalid
		}
	}
	return nil
}

func ParseNetworkDeadline(value string, now time.Time) (time.Time, error) {
	deadline, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || deadline.Before(now.Add(15*time.Second)) || deadline.After(now.Add(5*time.Minute)) {
		return time.Time{}, fmt.Errorf("invalid confirmation deadline")
	}
	return deadline, nil
}
