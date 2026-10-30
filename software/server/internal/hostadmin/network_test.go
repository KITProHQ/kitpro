package hostadmin

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureNetworkRunner struct {
	output map[string]string
	err    map[string]error
}

func (f fixtureNetworkRunner) Run(_ context.Context, name string) ([]byte, error) {
	if e := f.err[name]; e != nil {
		return nil, e
	}
	return []byte(f.output[name]), nil
}

type fakeInfo struct{}

func (fakeInfo) Name() string       { return "x" }
func (fakeInfo) Size() int64        { return 0 }
func (fakeInfo) Mode() os.FileMode  { return 0 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

func networkFixture(now *time.Time, counters map[string]string) NetworkProvider {
	outputs := map[string]string{
		"links": `[
   {"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP"],"mtu":65536,"operstate":"UNKNOWN","address":"00:00:00:00:00:00","linkinfo":{"info_kind":"loopback"}},
   {"ifindex":2,"ifname":"ens18","flags":["BROADCAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","address":"52:54:00:11:22:33"},
   {"ifindex":3,"ifname":"docker0","flags":["BROADCAST","UP"],"mtu":1500,"operstate":"DOWN","address":"02:42:00:00:00:01","linkinfo":{"info_kind":"bridge"}},
   {"ifindex":4,"ifname":"veth123","flags":["BROADCAST","UP"],"mtu":1500,"operstate":"UP","address":"02:42:00:00:00:02","link":"docker0","master":"docker0","linkinfo":{"info_kind":"veth"}}
  ]`,
		"addresses": `[
   {"ifindex":1,"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"},{"family":"inet6","local":"::1","prefixlen":128,"scope":"host"}]},
   {"ifindex":2,"ifname":"ens18","addr_info":[{"family":"inet","local":"10.20.30.40","prefixlen":24,"scope":"global"},{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]}
  ]`,
		"routes4": `[
   {"dst":"default","gateway":"10.20.30.1","dev":"ens18","metric":100,"table":"main","protocol":"dhcp"},
   {"dst":"default","gateway":"10.20.30.2","dev":"ens18","metric":200,"table":100,"protocol":"static"},
   {"dst":"172.17.0.0/16","dev":"docker0","table":"main","protocol":"kernel","scope":"link"}
  ]`,
		"routes6": `[{"dst":"default","gateway":"2001:db8::1","dev":"ens18","metric":100,"table":"main","protocol":"ra"}]`,
		"rules4":  `[{"priority":0}]`, "rules6": `[]`,
	}
	read := func(path string, _ int64) ([]byte, error) {
		if path == "/etc/resolv.conf" {
			return []byte("nameserver 10.20.30.53\nsearch home.example test.example\n"), nil
		}
		for suffix, value := range counters {
			if strings.HasSuffix(path, suffix) {
				return []byte(value), nil
			}
		}
		return []byte("0"), nil
	}
	return NetworkProvider{Runner: fixtureNetworkRunner{output: outputs, err: map[string]error{}}, ReadFile: read, ReadLink: func(path string) (string, error) {
		if path == "/etc/resolv.conf" {
			return "../run/systemd/resolve/stub-resolv.conf", nil
		}
		if strings.Contains(path, "/ens18/") && strings.HasSuffix(path, "device/driver") {
			return "../../../../virtio_net", nil
		}
		return "", os.ErrNotExist
	}, Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, Now: func() time.Time { return *now }, mu: &sync.Mutex{}, previous: map[string]counterSample{}}
}

func TestNetworkInventoryClassifiesLinksRoutesDNSAndBackends(t *testing.T) {
	now := time.Unix(100, 0)
	counters := map[string]string{"rx_bytes": "1024", "tx_bytes": "2048", "rx_packets": "10", "tx_packets": "20"}
	p := networkFixture(&now, counters)
	inventory, err := p.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Interfaces) != 4 || inventory.Interfaces[1].Name != "ens18" || inventory.Interfaces[1].Classification != "virtual" || inventory.Interfaces[1].Type != "virtio" || inventory.Interfaces[2].Type != "docker_bridge" || inventory.Interfaces[3].Classification != "container" {
		t.Fatalf("interfaces %#v", inventory.Interfaces)
	}
	if got := len(inventory.Interfaces[1].Addresses); got != 2 {
		t.Fatalf("addresses %d", got)
	}
	if inventory.Summary.DefaultRouteCount != 3 || len(inventory.Routes) != 4 || inventory.Routes[2].Container != true || inventory.Routes[1].Table != "100" {
		t.Fatalf("routes %#v summary %#v", inventory.Routes, inventory.Summary)
	}
	if inventory.DNS.Source != "systemd-resolved" || len(inventory.DNS.Servers) != 1 || len(inventory.DNS.SearchDomains) != 2 {
		t.Fatalf("dns %#v", inventory.DNS)
	}
	if inventory.Summary.Health != "healthy" || inventory.Capabilities["configuration_mutation"].State != Unsupported {
		t.Fatalf("health %#v", inventory.Summary)
	}
}

func TestNetworkRatesAndCounterReset(t *testing.T) {
	now := time.Unix(100, 0)
	counters := map[string]string{"rx_bytes": "1000", "tx_bytes": "2000"}
	p := networkFixture(&now, counters)
	first, _ := p.Collect(context.Background())
	if first.Interfaces[1].Stats.RXBytesPerSecond != nil {
		t.Fatal("first sample has a rate")
	}
	now = now.Add(2 * time.Second)
	counters["rx_bytes"] = "3000"
	counters["tx_bytes"] = "6000"
	second, _ := p.Collect(context.Background())
	if second.Interfaces[1].Stats.RXBytesPerSecond == nil || *second.Interfaces[1].Stats.RXBytesPerSecond != 1000 {
		t.Fatalf("rate %#v", second.Interfaces[1].Stats)
	}
	now = now.Add(time.Second)
	counters["rx_bytes"] = "1"
	counters["tx_bytes"] = "1"
	third, _ := p.Collect(context.Background())
	if third.Interfaces[1].Stats.RXBytesPerSecond != nil || third.Interfaces[1].Stats.TXBytesPerSecond != nil {
		t.Fatal("counter reset produced negative rate")
	}
}

func TestNetworkDNSPermissionAndMalformedOutput(t *testing.T) {
	now := time.Now()
	p := networkFixture(&now, map[string]string{})
	p.ReadFile = func(path string, _ int64) ([]byte, error) {
		if path == "/etc/resolv.conf" {
			return nil, ErrPermission
		}
		return []byte("0"), nil
	}
	inventory, err := p.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.DNS.Status.State != PermissionDenied {
		t.Fatalf("dns status %#v", inventory.DNS.Status)
	}
	p.Runner = fixtureNetworkRunner{output: map[string]string{"links": "not-json"}, err: map[string]error{}}
	if _, err = p.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("malformed err %v", err)
	}
}

type pipeConn struct{ net.Conn }

func TestDiagnosticsRestrictTargetsPortsAndRate(t *testing.T) {
	now := time.Unix(100, 0)
	network := networkFixture(&now, map[string]string{})
	policy := DefaultDiagnosticPolicy("203.0.113.8", "443")
	policy.MaxPerMinute = 2
	d := NewDiagnosticProvider(network, policy)
	d.Now = func() time.Time { return now }
	d.Dial = func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go b.Close()
		return a, nil
	}
	result, err := d.Run(context.Background(), DiagnosticRequest{Operation: "tcp", Target: "203.0.113.8", Port: 443})
	if err != nil || !result.Success {
		t.Fatalf("allowed %#v %v", result, err)
	}
	if _, err = d.Run(context.Background(), DiagnosticRequest{Operation: "tcp", Target: "169.254.169.254", Port: 443}); !errors.Is(err, ErrPermission) {
		t.Fatalf("metadata target err %v", err)
	}
	if _, err = d.Run(context.Background(), DiagnosticRequest{Operation: "tcp", Target: "203.0.113.8", Port: 22}); err == nil {
		t.Fatal("unapproved port allowed")
	}
	if _, err = d.Run(context.Background(), DiagnosticRequest{Operation: "dns", Target: "bad host"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid host err %v", err)
	}
}
