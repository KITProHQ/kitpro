package exposure

import "testing"

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
