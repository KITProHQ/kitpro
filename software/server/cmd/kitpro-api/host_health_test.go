package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

func cachedHealthApp(t *testing.T) (*app, *http.Cookie) {
	a, session, _ := newTestApp(t)
	cpu := 25.0
	uptime := uint64(3600)
	total := uint64(10 << 30)
	available := uint64(6 << 30)
	rootTotal := uint64(100 << 30)
	rootUsed := uint64(20 << 30)
	rootAvail := uint64(80 << 30)
	rootUsage := 20.0
	rx := float64(100)
	tx := float64(50)
	now := time.Now().UTC()
	checks := []hostadmin.HealthCheck{{ID: "services.failed", Component: "services", State: hostadmin.HealthHealthy, Severity: "information", Summary: "No failed services", LastAttempted: now.Format(time.RFC3339), Capability: hostadmin.Available}, {ID: "storage.root", Component: "storage", State: hostadmin.HealthWarning, Severity: "warning", Summary: "Storage warning", Explanation: "Fixture warning", Evidence: "root at 85%", LastAttempted: now.Format(time.RFC3339), Capability: hostadmin.Available, Destination: "#host-storage"}}
	summary := hostadmin.HealthSummary{State: hostadmin.HealthWarning, Healthy: 1, Warnings: 1}
	components := map[string]hostadmin.ComponentHealth{"services": {Component: "services", State: hostadmin.HealthHealthy, Healthy: 1}, "storage": {Component: "storage", State: hostadmin.HealthWarning, Warning: 1}}
	a.healthCache = &hostHealthCache{collected: now, bundle: hostHealthBundle{Health: hostadmin.HealthSnapshot{Summary: summary, Components: components, Checks: checks, EvaluatedAt: now.Format(time.RFC3339), FreshUntil: now.Add(time.Minute).Format(time.RFC3339)}, Overview: hostadmin.Overview{Hostname: "fixture-host", OS: "Fixture Linux", Kernel: "1.0", Architecture: "amd64", CPUUtilization: &cpu, UptimeSeconds: &uptime, LoadAverage: []float64{1, .5, .25}, Memory: &hostadmin.Memory{TotalBytes: total, AvailableBytes: available}, RootDisk: &hostadmin.Disk{TotalBytes: rootTotal, AvailableBytes: rootAvail}}, Storage: &hostadmin.StorageInventory{Summary: hostadmin.StorageSummary{AvailableBytes: rootAvail, UsedBytes: rootUsed, MountedCount: 2, WarningCount: 1}, Filesystems: []hostadmin.Filesystem{{MountPoint: "/", UsagePercent: &rootUsage}}}, Network: &hostadmin.NetworkInventory{Summary: hostadmin.NetworkSummary{ActiveCount: 2, DefaultRouteCount: 1, ResolverCount: 1, Health: "healthy"}, Interfaces: []hostadmin.NetworkInterface{{Name: "eth0", Classification: "physical", Stats: hostadmin.NetworkStats{RXBytesPerSecond: &rx, TXBytesPerSecond: &tx}}}}, Services: hostadmin.ServiceHealthInput{FailedCount: 0, Essential: map[string]hostadmin.Service{"kitpro-api.service": {Active: "active"}}}, Platform: hostadmin.PlatformHealthInput{InstalledApps: 3, RunningApps: 2, StoppedApps: 1}}}
	return a, session
}
func TestHealthEndpointsRequireAuthenticationAndReturnTypedData(t *testing.T) {
	a, session := cachedHealthApp(t)
	for _, path := range []string{"/api/v1/host/health", "/api/v1/host/health/checks", "/api/v1/host/health/storage"} {
		denied := httptest.NewRecorder()
		a.guard(a.hostHealth)(denied, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil))
		if denied.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous=%d", path, denied.Code)
		}
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		req.AddCookie(session)
		response := httptest.NewRecorder()
		a.guard(a.hostHealth)(response, req)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "cookie") {
			t.Fatal("health response exposed sensitive fixture")
		}
	}
}
func TestHealthComponentMissingAndInvalidMethods(t *testing.T) {
	a, session := cachedHealthApp(t)
	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/host/health/missing", nil)
	missingRequest.AddCookie(session)
	a.hostHealth(missing, missingRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing=%d", missing.Code)
	}
	method := httptest.NewRecorder()
	methodRequest := httptest.NewRequest(http.MethodPost, "/api/v1/host/health", nil)
	methodRequest.AddCookie(session)
	a.hostHealth(method, methodRequest)
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", method.Code)
	}
	query := httptest.NewRecorder()
	queryRequest := httptest.NewRequest(http.MethodGet, "/api/v1/host/health?path=/etc/passwd", nil)
	queryRequest.AddCookie(session)
	a.hostHealth(query, queryRequest)
	if query.Code != http.StatusBadRequest {
		t.Fatalf("query=%d", query.Code)
	}
}
func TestDashboardMatchesUnderlyingHealthAndProviders(t *testing.T) {
	a, session := cachedHealthApp(t)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/v1/dashboard", nil)
	req.AddCookie(session)
	response := httptest.NewRecorder()
	a.guard(a.dashboard)(response, req)
	body := response.Body.String()
	for _, want := range []string{`"state":"warning"`, `"hostname":"fixture-host"`, `"installed":3`, `"running":2`, `"failed":0`, `"active_interfaces":2`, `"root_usage_percent":20`, `"summary":"Storage warning"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %s: %s", want, body)
		}
	}
}
func TestDashboardRequiresAuthentication(t *testing.T) {
	a, _ := cachedHealthApp(t)
	response := httptest.NewRecorder()
	a.guard(a.dashboard)(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/v1/dashboard", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous=%d", response.Code)
	}
}

func TestPlatformHealthQueriesTheControlSchema(t *testing.T) {
	a, _, _ := newTestApp(t)
	result := a.collectPlatformHealth(context.Background())
	if result.Management.State != hostadmin.Available {
		t.Fatalf("management=%#v", result.Management)
	}
}
