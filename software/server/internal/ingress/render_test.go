package ingress

import (
	"strings"
	"testing"
)

func TestRenderNginxIsDeterministicAndConstrained(t *testing.T) {
	one := AppliedRoute{Route: baseRoute(), Backend: Backend{Address: "127.0.0.1", Port: 20000}}
	two := one
	two.ID, two.Hostname, two.Backend.Port = "route-fedcba9876543210", "b.example.internal", 20001
	one.Hostname = "a.example.internal"
	first, err := RenderNginx([]AppliedRoute{two, one}, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderNginx([]AppliedRoute{one, two}, RenderOptions{})
	if err != nil || string(first) != string(second) {
		t.Fatalf("render is not deterministic: %v", err)
	}
	text := string(first)
	for _, required := range []string{"server_name a.example.internal", "proxy_pass http://127.0.0.1:20000", "X-Forwarded-For $remote_addr", "ssl_reject_handshake on", "location ^~ /.well-known/acme-challenge/", "proxy_pass http://unix:/run/kitpro-ingress/acme.sock:", "proxy_set_header Host $host", "client_body_temp_path temp-client-body", "proxy_temp_path temp-proxy", "fastcgi_temp_path temp-fastcgi", "uwsgi_temp_path temp-uwsgi", "scgi_temp_path temp-scgi"} {
		if !strings.Contains(text, required) {
			t.Fatalf("generated config missing %q", required)
		}
	}
	for _, forbidden := range []string{"$http_x_forwarded_for", "proxy_pass $", "include ", "resolver "} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("generated config contains %q", forbidden)
		}
	}
}

func TestRenderNginxRejectsDuplicateHostAndInjection(t *testing.T) {
	one := AppliedRoute{Route: baseRoute(), Backend: Backend{Address: "127.0.0.1", Port: 20000}}
	two := one
	two.ID = "route-fedcba9876543210"
	if _, err := RenderNginx([]AppliedRoute{one, two}, RenderOptions{}); err == nil {
		t.Fatal("duplicate hostname accepted")
	}
	one.Hostname = "app.example.internal; include /etc/passwd"
	if _, err := RenderNginx([]AppliedRoute{one}, RenderOptions{}); err == nil {
		t.Fatal("hostname injection accepted")
	}
}

func TestRenderNginxAddsTLSAndCloudflareListeners(t *testing.T) {
	managed := baseRoute()
	managed.ExposureMode, managed.TLSPolicy, managed.CertificateID = ManagedHTTPS, TLSACME, "cert-0123456789abcdef"
	cloudflare := baseRoute()
	cloudflare.ID, cloudflare.Hostname = "route-fedcba9876543210", "tunnel.example.com"
	cloudflare.ExposureMode, cloudflare.TLSPolicy, cloudflare.ProviderID = CloudflareTunnel, TLSCloudflare, "provider-0123456789abcdef"
	config, err := RenderNginx([]AppliedRoute{
		{Route: managed, Backend: Backend{Address: "127.0.0.1", Port: 20000}},
		{Route: cloudflare, Backend: Backend{Address: "127.0.0.1", Port: 20001}},
	}, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, required := range []string{"listen 443 ssl", "return 308 https://$host$request_uri", "/cert-0123456789abcdef/privkey.pem", "listen 127.0.0.1:18080"} {
		if !strings.Contains(text, required) {
			t.Fatalf("generated config missing %q", required)
		}
	}
}
