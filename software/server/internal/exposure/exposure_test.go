package exposure

import (
	"strings"
	"testing"
)

func TestValidateModes(t *testing.T) {
	if err := Validate(Assignment{Mode: Internal}, "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Assignment{Mode: Internal, Port: 20000}, "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Assignment{Mode: Loopback, Address: "127.0.0.1", Port: 20000}, "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Assignment{Mode: LAN, Address: "10.0.0.2", Port: 20001}, "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Assignment{{Mode: LAN, Address: "0.0.0.0", Port: 20001}, {Mode: Loopback, Address: "127.0.0.1", Port: 80}} {
		if Validate(a, "10.0.0.2") == nil {
			t.Fatalf("accepted unsafe assignment %#v", a)
		}
	}
}

func TestObservedBindingsExact(t *testing.T) {
	loopback := Assignment{Mode: Loopback, Address: "127.0.0.1", Port: 20000}
	exact := map[string]any{"80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20000"}}}
	if !ObservedBindingsExact(loopback, 80, "http", exact) {
		t.Fatal("exact loopback binding reported as drift")
	}
	for name, bindings := range map[string]map[string]any{
		"missing":  {},
		"wildcard": {"80/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "20000"}}},
		"port":     {"80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20001"}}},
		"extra":    {"80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20000"}}, "81/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20001"}}},
	} {
		if ObservedBindingsExact(loopback, 80, "http", bindings) {
			t.Fatalf("accepted %s binding drift", name)
		}
	}
	if !ObservedBindingsExact(Assignment{Mode: Internal, Port: 20000}, 80, "http", map[string]any{}) {
		t.Fatal("internal mode without publication reported as drift")
	}
	if ObservedBindingsExact(Assignment{Mode: Internal, Port: 20000}, 80, "http", exact) {
		t.Fatal("internal mode accepted unexpected publication")
	}
}

func TestAllocate(t *testing.T) {
	p, err := Allocate(map[int]bool{FirstPort: true})
	if err != nil || p != FirstPort+1 {
		t.Fatalf("allocation %d %v", p, err)
	}
}

func TestVerifyLocalAddressRejectsUnsafeAddresses(t *testing.T) {
	for _, address := range []string{"", "not-an-ip", "0.0.0.0", "::", "127.0.0.1", "169.254.1.1", "224.0.0.1"} {
		if err := VerifyLocalAddress(address); err == nil {
			t.Fatalf("accepted unsafe LAN address %q", address)
		}
	}
}

func TestVerifyLocalAddressRejectsUnassignedAddress(t *testing.T) {
	if err := VerifyLocalAddress("192.0.2.1"); err == nil {
		t.Fatal("accepted documentation-only address as locally assigned")
	}
}

func TestTransportAwareConflicts(t *testing.T) {
	tcpWildcard := ServiceBinding{Transport: TCP, Mode: LAN, HostAddress: "0.0.0.0", HostPort: 53}
	tcpExact := ServiceBinding{Transport: TCP, Mode: LAN, HostAddress: "192.168.1.10", HostPort: 53}
	udpExact := ServiceBinding{Transport: UDP, Mode: LAN, HostAddress: "192.168.1.10", HostPort: 53}
	if !BindingsConflict(tcpWildcard, tcpExact) {
		t.Fatal("IPv4 wildcard did not conflict with exact TCP binding")
	}
	if BindingsConflict(tcpWildcard, udpExact) {
		t.Fatal("TCP and UDP numeric ports conflicted")
	}
	if BindingsConflict(tcpExact, ServiceBinding{Transport: TCP, Mode: LAN, HostAddress: "192.168.1.11", HostPort: 53}) {
		t.Fatal("distinct exact IPv4 addresses conflicted")
	}
	if !BindingsConflict(ServiceBinding{Transport: UDP, Mode: LAN, HostAddress: "::", HostPort: 53}, ServiceBinding{Transport: UDP, Mode: LAN, HostAddress: "2001:db8::1", HostPort: 53}) {
		t.Fatal("IPv6 wildcard did not conflict")
	}
	if ValidateConflicts([]ServiceBinding{tcpExact, tcpExact}) == nil {
		t.Fatal("duplicate requested binding accepted")
	}
}

func TestFixedAndDynamicPolicy(t *testing.T) {
	fixed := ServiceBinding{ServiceID: "dns", Transport: UDP, ContainerPort: 53, Mode: LAN, HostAddress: "10.0.0.2", HostPort: 53}
	if err := ValidateBinding(fixed, "10.0.0.2", 53); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinding(fixed, "10.0.0.2", 54); err == nil {
		t.Fatal("wrong fixed port accepted")
	}
	dynamic := ServiceBinding{ServiceID: "web", Transport: TCP, ContainerPort: 80, Mode: Loopback, HostAddress: "127.0.0.1", HostPort: FirstPort}
	if err := ValidateBinding(dynamic, "", 0); err != nil {
		t.Fatal(err)
	}
	dynamic.HostPort = 53
	if err := ValidateBinding(dynamic, "", 0); err == nil {
		t.Fatal("untrusted fixed port accepted")
	}
}

func TestObservedBindingSetSupportsMultipleTransports(t *testing.T) {
	expected := []ServiceBinding{{ServiceID: "dns-tcp", Transport: TCP, ContainerPort: 53, Mode: LAN, HostAddress: "10.0.0.2", HostPort: 53}, {ServiceID: "dns-udp", Transport: UDP, ContainerPort: 53, Mode: LAN, HostAddress: "10.0.0.2", HostPort: 53}, {ServiceID: "admin", Transport: TCP, ContainerPort: 80, Mode: Loopback, HostAddress: "127.0.0.1", HostPort: 20000}}
	observed := map[string]any{"53/tcp": []any{map[string]any{"HostIp": "10.0.0.2", "HostPort": "53"}}, "53/udp": []any{map[string]any{"HostIp": "10.0.0.2", "HostPort": "53"}}, "80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20000"}}}
	if !ObservedBindingsExactSet(expected, observed) {
		t.Fatal("exact multi-binding set reported as drift")
	}
	delete(observed, "53/udp")
	if ObservedBindingsExactSet(expected, observed) {
		t.Fatal("missing UDP binding accepted")
	}
}

func TestParseProcNetListeners(t *testing.T) {
	tcp := "sl local_address rem_address st\n 0: 0100007F:4E20 00000000:0000 0A\n 1: 00000000:0035 00000000:0000 01\n"
	listeners, err := parseProcNet(strings.NewReader(tcp), TCP, false)
	if err != nil || len(listeners) != 1 || listeners[0].HostAddress != "127.0.0.1" || listeners[0].HostPort != 20000 {
		t.Fatalf("TCP listeners=%#v err=%v", listeners, err)
	}
	udp := "sl local_address rem_address st\n 0: 00000000:0035 00000000:0000 07\n"
	listeners, err = parseProcNet(strings.NewReader(udp), UDP, false)
	if err != nil || len(listeners) != 1 || listeners[0].Transport != UDP || listeners[0].HostAddress != "0.0.0.0" {
		t.Fatalf("UDP listeners=%#v err=%v", listeners, err)
	}
}
