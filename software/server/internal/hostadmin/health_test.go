package hostadmin

import (
	"testing"
	"time"
)

func healthyInput() HealthInput {
	cpu := 20.0
	uptime := uint64(86400)
	total := uint64(16 << 30)
	available := uint64(10 << 30)
	rootTotal := uint64(100 << 30)
	rootUsed := uint64(40 << 30)
	rootAvailable := uint64(60 << 30)
	usage := 40.0
	carrier := true
	return HealthInput{
		ObservedAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), CPUCount: 4,
		Overview: Overview{CPUUtilization: &cpu, UptimeSeconds: &uptime, LoadAverage: []float64{1, .8, .5}, Memory: &Memory{TotalBytes: total, AvailableBytes: available, SwapTotalBytes: 2 << 30, SwapFreeBytes: 2 << 30}, Security: Status{State: Available, Detail: "AppArmor enabled"}},
		Services: ServiceHealthInput{Capability: Status{State: Available}, Essential: map[string]Service{"kitpro-api.service": {Name: "kitpro-api.service", Active: "active", Sub: "running"}, "docker.service": {Name: "docker.service", Active: "active", Sub: "running"}}},
		Storage:  &StorageInventory{Capabilities: map[string]Status{"device_health": {State: Unsupported, Detail: "not configured"}}, Filesystems: []Filesystem{{ID: "root", Type: "ext4", MountPoint: "/", TotalBytes: &rootTotal, UsedBytes: &rootUsed, AvailableBytes: &rootAvailable, UsagePercent: &usage, Protection: []string{"root"}, Status: Status{State: Available}}}},
		Network:  &NetworkInventory{Summary: NetworkSummary{Health: "healthy", HealthDetail: "Configured", ActiveCount: 1, DefaultRouteCount: 1, ResolverCount: 1}, Interfaces: []NetworkInterface{{Name: "eth0", Classification: "physical", AdministrativeState: "up", Carrier: &carrier, Status: Status{State: Available}}}},
		Platform: PlatformHealthInput{Management: Status{State: Available, Detail: "available"}, Helper: Status{State: Available, Detail: "responding"}, Runtime: Status{State: Available, Detail: "responding"}, Authentication: Status{State: Available, Detail: "available"}, TimeSynchronization: Status{State: Available, Detail: "synchronized"}, Confinement: Status{State: Available, Detail: "NoNewPrivileges enabled"}, InstalledApps: 1, RunningApps: 1, IngressRoutes: 1, Certificates: 1, Backups: 1},
		Journal:  JournalHealthInput{Capability: Status{State: NotConfigured, Detail: "Journal access disabled"}},
	}
}
func byID(t *testing.T, s HealthSnapshot, id string) HealthCheck {
	t.Helper()
	for _, c := range s.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("missing %s", id)
	return HealthCheck{}
}
func TestHealthHealthyOptionalStates(t *testing.T) {
	s := EvaluateHealth(healthyInput(), DefaultHealthThresholds())
	if s.Summary.State != HealthHealthy {
		t.Fatalf("overall=%s", s.Summary.State)
	}
	if byID(t, s, "journal.critical").State != HealthNotConfigured {
		t.Fatal("journal gate hidden")
	}
	if byID(t, s, "storage.device_health").State != HealthUnsupported {
		t.Fatal("SMART capability invented")
	}
	if s.EvaluatedAt == "" || s.FreshUntil == "" {
		t.Fatal("freshness missing")
	}
}
func TestHealthThresholdBoundaries(t *testing.T) {
	in := healthyInput()
	cpu := 95.0
	in.Overview.CPUUtilization = &cpu
	in.Overview.Memory.AvailableBytes = in.Overview.Memory.TotalBytes / 20
	in.Overview.Memory.SwapFreeBytes = 0
	s := EvaluateHealth(in, DefaultHealthThresholds())
	for _, id := range []string{"resources.cpu", "resources.memory", "resources.swap"} {
		if byID(t, s, id).State != HealthCritical {
			t.Fatalf("%s not critical", id)
		}
	}
}

func TestHealthCapacityWarningAndStaleReadiness(t *testing.T) {
	in := healthyInput()
	usage := 85.0
	in.Storage.Filesystems[0].UsagePercent = &usage
	in.Storage.Filesystems[0].Warnings = []string{"capacity_warning"}
	in.Platform.StaleReadiness = 1
	s := EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "storage.root").State; got != HealthWarning {
		t.Fatalf("storage.root=%s", got)
	}
	if got := byID(t, s, "applications.summary").State; got != HealthDegraded {
		t.Fatalf("applications.summary=%s", got)
	}
	if s.Summary.State != HealthDegraded {
		t.Fatalf("aggregate=%s", s.Summary.State)
	}
}

func TestHealthPersistentOptionalMountStates(t *testing.T) {
	in := healthyInput()
	in.Persistence = []PersistentStorageHealthInput{{ResourceID: "archive", Target: "/mnt/archive", State: "enabled", Health: "healthy", Mounted: true, SourceAvailable: true}}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "storage.persistence.archive").State; got != HealthHealthy {
		t.Fatalf("mounted persistence=%s", got)
	}

	in.Persistence[0].Mounted = false
	in.Persistence[0].SourceAvailable = false
	s = EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "storage.persistence.archive").State; got != HealthWarning {
		t.Fatalf("missing optional mount=%s", got)
	}
	if s.Summary.State == HealthCritical {
		t.Fatalf("optional mount made platform critical: %#v", s.Summary)
	}

	in.Persistence[0] = PersistentStorageHealthInput{ResourceID: "archive", Target: "/mnt/archive", State: "needs_recovery", Health: "needs_recovery"}
	s = EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "storage.persistence.archive").State; got != HealthDegraded {
		t.Fatalf("recovery state=%s", got)
	}
}

func TestHealthMissingRuntimeIsCritical(t *testing.T) {
	in := healthyInput()
	in.Platform.Runtime = Status{State: Unavailable, Detail: "container runtime unavailable"}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "platform.runtime").State; got != HealthCritical {
		t.Fatalf("platform.runtime=%s", got)
	}
	if s.Summary.State != HealthCritical {
		t.Fatalf("aggregate=%s", s.Summary.State)
	}
}
func TestHealthServiceStorageApplicationFailures(t *testing.T) {
	in := healthyInput()
	in.Services.FailedCount = 2
	in.Services.Essential["kitpro-api.service"] = Service{Name: "kitpro-api.service", Active: "failed", Sub: "failed"}
	in.Storage.Filesystems[0].Warnings = []string{"mounted_read_only"}
	in.Platform.FailedApps = 1
	in.Platform.ReadinessFailures = 1
	s := EvaluateHealth(in, DefaultHealthThresholds())
	for _, id := range []string{"services.failed", "services.kitpro-api", "storage.root", "applications.summary"} {
		if byID(t, s, id).State != HealthCritical {
			t.Fatalf("%s not critical", id)
		}
	}
}
func TestHealthIsolatedNetworkAndOptionalService(t *testing.T) {
	in := healthyInput()
	in.Network.Summary.Health = "not_configured"
	in.Network.Summary.HealthDetail = "No default route"
	in.Network.Summary.DefaultRouteCount = 0
	in.Services.Essential["containerd.service"] = Service{Name: "containerd.service", Active: "inactive", Sub: "dead"}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	if byID(t, s, "network.configuration").State != HealthNotConfigured {
		t.Fatal("isolated network marked unhealthy")
	}
	if byID(t, s, "services.containerd").State != HealthDegraded {
		t.Fatal("containerd distinction missing")
	}
}
func TestHealthPartialFailuresAndSecurityModes(t *testing.T) {
	in := healthyInput()
	in.Storage = nil
	in.StorageError = Status{State: Unavailable, Detail: "timeout"}
	in.Network = nil
	in.NetworkError = Status{State: Unsupported, Detail: "iproute absent"}
	in.Overview.Security = Status{State: Available, Detail: "SELinux permissive"}
	in.Platform.TimeSynchronization = Status{State: Unavailable, Detail: "unknown"}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	cases := map[string]HealthState{"storage.inventory": HealthUnavailable, "network.inventory": HealthUnsupported, "security.enforcement": HealthWarning, "security.time": HealthUnavailable}
	for id, want := range cases {
		if got := byID(t, s, id).State; got != want {
			t.Fatalf("%s=%s", id, got)
		}
	}
}
func TestHealthJournalSensitiveContentExcluded(t *testing.T) {
	in := healthyInput()
	in.Journal = JournalHealthInput{Capability: Status{State: Available, Detail: "bounded query"}, CriticalCount: 3}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	c := byID(t, s, "journal.critical")
	if c.State != HealthWarning || c.Evidence != "recent critical entry count: 3" {
		t.Fatalf("journal=%#v", c)
	}
}
func TestHealthUnavailableCollectorsStayUnavailable(t *testing.T) {
	in := healthyInput()
	in.Overview.CPUUtilization = nil
	in.Overview.Memory = nil
	in.CPUCount = 0
	in.Services = ServiceHealthInput{Capability: Status{State: Unavailable, Detail: "timeout"}}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	for _, id := range []string{"resources.cpu", "resources.load", "resources.memory", "services.inventory"} {
		if byID(t, s, id).State != HealthUnavailable {
			t.Fatalf("%s not unavailable", id)
		}
	}
}

func TestHealthUnavailableManagementDoesNotBecomeHealthyZeros(t *testing.T) {
	in := healthyInput()
	in.Platform.Management = Status{State: Unavailable, Detail: "control database query failed"}
	s := EvaluateHealth(in, DefaultHealthThresholds())
	if got := byID(t, s, "applications.inventory").State; got != HealthUnavailable {
		t.Fatalf("applications.inventory=%s", got)
	}
}

func TestHealthCheckCollectionIsBounded(t *testing.T) {
	b := healthBuilder{now: time.Unix(1, 0)}
	for i := 0; i < MaxHealthChecks+10; i++ {
		b.add("check", "fixture", HealthHealthy, "ok", "ok", "", "", "", Status{State: Available})
	}
	if len(b.checks) != MaxHealthChecks || !b.truncated {
		t.Fatalf("checks=%d truncated=%v", len(b.checks), b.truncated)
	}
}
