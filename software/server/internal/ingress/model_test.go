package ingress

import "testing"

func baseRoute() Route {
	return Route{ID: "route-0123456789abcdef", InstallationID: "inst-0123456789abcdef", ServiceID: "web", Hostname: "app.example.internal", ExposureMode: LAN, UpstreamProtocol: "http", TLSPolicy: TLSNone, DesiredState: Enabled}
}

func TestNormalizeHostnameRejectsRoutingAndHeaderConfusion(t *testing.T) {
	for _, value := range []string{"app.example.internal", "APP.EXAMPLE.INTERNAL.", "xn--bcher-kva.example"} {
		if _, err := NormalizeHostname(value); err != nil {
			t.Fatalf("valid hostname %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"localhost", "127.0.0.1", "*.example.com", "app_example.com", "-app.example.com", "app-.example.com", "app.example.com:443", "app.example.com\nproxy_pass http://evil", "bücher.example"} {
		if _, err := NormalizeHostname(value); err == nil {
			t.Fatalf("unsafe hostname %q accepted", value)
		}
	}
}

func TestValidateRouteEnforcesCatalogAndTLSPolicy(t *testing.T) {
	route := baseRoute()
	if err := ValidateRoute(route, []string{"lan"}, false); err != nil {
		t.Fatal(err)
	}
	managed := route
	managed.ExposureMode, managed.TLSPolicy, managed.CertificateID = ManagedHTTPS, TLSACME, "cert-0123456789abcdef"
	if err := ValidateRoute(managed, []string{"managed-https"}, false); err != nil {
		t.Fatal(err)
	}
	cloudflare := route
	cloudflare.ExposureMode, cloudflare.TLSPolicy, cloudflare.ProviderID = CloudflareTunnel, TLSCloudflare, "provider-0123456789abcdef"
	if err := ValidateRoute(cloudflare, []string{"cloudflare-tunnel"}, true); err != nil {
		t.Fatal(err)
	}
	invalid := []Route{
		func() Route { r := route; r.Hostname = "Other.Example"; return r }(),
		func() Route { r := route; r.ExposureMode = ManagedHTTPS; return r }(),
		func() Route { r := route; r.ProviderID = "provider-0123456789abcdef"; return r }(),
		func() Route { r := cloudflare; r.CertificateID = "cert-0123456789abcdef"; return r }(),
	}
	for i, candidate := range invalid {
		allowed := []string{"lan"}
		external := false
		if i == 3 {
			allowed, external = []string{"cloudflare-tunnel"}, true
		}
		if err := ValidateRoute(candidate, allowed, external); err == nil {
			t.Fatalf("invalid route %d accepted: %#v", i, candidate)
		}
	}
}

func TestValidateBackendRequiresExactLoopbackBinding(t *testing.T) {
	for _, backend := range []Backend{{Address: "0.0.0.0", Port: 20000}, {Address: "10.0.0.2", Port: 20000}, {Address: "localhost", Port: 20000}, {Address: "127.0.0.1", Port: 0}} {
		if ValidateBackend(backend) == nil {
			t.Fatalf("unsafe backend accepted: %#v", backend)
		}
	}
	if err := ValidateBackend(Backend{Address: "127.0.0.1", Port: 20000}); err != nil {
		t.Fatal(err)
	}
}
