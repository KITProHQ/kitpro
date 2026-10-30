package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

const healthCacheTTL = 5 * time.Second

type hostHealthCache struct {
	mu        sync.Mutex
	collected time.Time
	bundle    hostHealthBundle
}

type hostHealthBundle struct {
	Health   hostadmin.HealthSnapshot
	Overview hostadmin.Overview
	Storage  *hostadmin.StorageInventory
	Network  *hostadmin.NetworkInventory
	Services hostadmin.ServiceHealthInput
	Platform hostadmin.PlatformHealthInput
}

type dashboardResponse struct {
	EvaluatedAt  string                               `json:"evaluated_at"`
	FreshUntil   string                               `json:"fresh_until"`
	Health       hostadmin.HealthSummary              `json:"health"`
	Components   map[string]hostadmin.ComponentHealth `json:"components"`
	Host         dashboardHost                        `json:"host"`
	Resources    dashboardResources                   `json:"resources"`
	Applications dashboardApplications                `json:"applications"`
	Services     dashboardServices                    `json:"services"`
	Storage      dashboardStorage                     `json:"storage"`
	Network      dashboardNetwork                     `json:"network"`
	Attention    []hostadmin.HealthCheck              `json:"attention"`
}

type dashboardHost struct {
	Hostname      string  `json:"hostname,omitempty"`
	OS            string  `json:"os,omitempty"`
	Kernel        string  `json:"kernel,omitempty"`
	Architecture  string  `json:"architecture"`
	ServerVersion string  `json:"server_version"`
	UptimeSeconds *uint64 `json:"uptime_seconds,omitempty"`
}
type dashboardResources struct {
	CPUUtilization   *float64          `json:"cpu_utilization_percent,omitempty"`
	LoadAverage      []float64         `json:"load_average,omitempty"`
	Memory           *hostadmin.Memory `json:"memory,omitempty"`
	RootDisk         *hostadmin.Disk   `json:"root_disk,omitempty"`
	RXBytesPerSecond *float64          `json:"rx_bytes_per_second,omitempty"`
	TXBytesPerSecond *float64          `json:"tx_bytes_per_second,omitempty"`
}
type dashboardApplications struct{ Installed, Running, Stopped, Failed, ReadinessFailures, RecentFailedOperations int }

func (d dashboardApplications) MarshalJSON() ([]byte, error) {
	type wire struct {
		Installed              int `json:"installed"`
		Running                int `json:"running"`
		Stopped                int `json:"stopped"`
		Failed                 int `json:"failed"`
		ReadinessFailures      int `json:"readiness_failures"`
		RecentFailedOperations int `json:"recent_failed_operations"`
	}
	return json.Marshal(wire(d))
}

type dashboardServices struct {
	Total, Failed int
	Essential     map[string]string
}

func (d dashboardServices) MarshalJSON() ([]byte, error) {
	type wire struct {
		Total     int               `json:"total"`
		Failed    int               `json:"failed"`
		Essential map[string]string `json:"essential"`
	}
	return json.Marshal(wire(d))
}

type dashboardStorage struct {
	AvailableBytes, UsedBytes uint64
	Mounted, Warnings         int
	RootUsagePercent          *float64
}

func (d dashboardStorage) MarshalJSON() ([]byte, error) {
	type wire struct {
		AvailableBytes   uint64   `json:"available_bytes"`
		UsedBytes        uint64   `json:"used_bytes"`
		Mounted          int      `json:"mounted_filesystems"`
		Warnings         int      `json:"warnings"`
		RootUsagePercent *float64 `json:"root_usage_percent,omitempty"`
	}
	return json.Marshal(wire(d))
}

type dashboardNetwork struct {
	ActiveInterfaces, DefaultRoutes, Resolvers int
	Health                                     string
	RXBytesPerSecond, TXBytesPerSecond         *float64
}

func (d dashboardNetwork) MarshalJSON() ([]byte, error) {
	type wire struct {
		ActiveInterfaces int      `json:"active_interfaces"`
		DefaultRoutes    int      `json:"default_routes"`
		Resolvers        int      `json:"resolvers"`
		Health           string   `json:"health"`
		RXBytesPerSecond *float64 `json:"rx_bytes_per_second,omitempty"`
		TXBytesPerSecond *float64 `json:"tx_bytes_per_second,omitempty"`
	}
	return json.Marshal(wire(d))
}

func hostStatusFromError(err error, detail string) hostadmin.Status {
	switch {
	case err == nil:
		return hostadmin.Status{State: hostadmin.Available}
	case errors.Is(err, hostadmin.ErrUnsupported):
		return hostadmin.Status{State: hostadmin.Unsupported, Detail: detail + " is unsupported"}
	case errors.Is(err, hostadmin.ErrPermission):
		return hostadmin.Status{State: hostadmin.PermissionDenied, Detail: detail + " permission denied"}
	default:
		return hostadmin.Status{State: hostadmin.Unavailable, Detail: detail + " unavailable"}
	}
}

func (a *app) healthCacheValue() *hostHealthCache {
	if a.healthCache == nil {
		a.healthCache = &hostHealthCache{}
	}
	return a.healthCache
}

func (a *app) collectHealth(ctx context.Context) hostHealthBundle {
	cache := a.healthCacheValue()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.collected.IsZero() && time.Since(cache.collected) < healthCacheTTL {
		return cache.bundle
	}
	now := time.Now().UTC()
	var overview hostadmin.Overview
	var storage hostadmin.StorageInventory
	var network hostadmin.NetworkInventory
	var storageErr, networkErr error
	var services hostadmin.ServiceHealthInput
	var platform hostadmin.PlatformHealthInput
	var journal hostadmin.JournalHealthInput
	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); overview = hostadmin.Collect() }()
	go func() { defer wg.Done(); storage, storageErr = a.hostStorageProvider.Collect(ctx) }()
	go func() { defer wg.Done(); network, networkErr = a.hostNetworkProvider.Collect(ctx) }()
	go func() { defer wg.Done(); services = a.collectServiceHealth(ctx) }()
	go func() { defer wg.Done(); platform = a.collectPlatformHealth(ctx) }()
	go func() { defer wg.Done(); journal = a.collectJournalHealth(ctx, now) }()
	wg.Wait()
	input := hostadmin.HealthInput{Overview: overview, CPUCount: runtime.NumCPU(), Services: services, Platform: platform, Journal: journal, ObservedAt: now}
	if a.hostStorageManagementEnabled && a.hostStorageManager != nil {
		input.Persistence = a.hostStorageManager.PersistentHealth(ctx)
	}
	if storageErr == nil {
		input.Storage = &storage
	} else {
		input.StorageError = hostStatusFromError(storageErr, "storage inventory")
	}
	if networkErr == nil {
		input.Network = &network
	} else {
		input.NetworkError = hostStatusFromError(networkErr, "network inventory")
	}
	bundle := hostHealthBundle{Overview: overview, Services: services, Platform: platform}
	if storageErr == nil {
		bundle.Storage = &storage
	}
	if networkErr == nil {
		bundle.Network = &network
	}
	bundle.Health = hostadmin.EvaluateHealth(input, hostadmin.DefaultHealthThresholds())
	cache.bundle, cache.collected = bundle, now
	return bundle
}

func (a *app) collectServiceHealth(ctx context.Context) hostadmin.ServiceHealthInput {
	result := hostadmin.ServiceHealthInput{Essential: map[string]hostadmin.Service{}}
	page, err := a.hostServicesProvider.List(ctx, hostadmin.ServiceQuery{State: "failed", Limit: hostadmin.MaxServicePage})
	result.Capability = hostStatusFromError(err, "systemd service inventory")
	if err != nil {
		return result
	}
	result.FailedCount = page.Total
	if all, allErr := a.hostServicesProvider.List(ctx, hostadmin.ServiceQuery{State: "all", Limit: 1}); allErr == nil {
		result.TotalCount = all.Total
	}
	for _, name := range []string{"kitpro-api.service", "kitpro-helper.service", "kitpro-ingress.service", "docker.service", "containerd.service"} {
		item, detailErr := a.hostServicesProvider.Detail(ctx, name)
		if detailErr == nil {
			result.Essential[name] = item
		} else if errors.Is(detailErr, hostadmin.ErrMissing) {
			result.Essential[name] = hostadmin.Service{Name: name, Active: "missing", Sub: "not-found", Failure: "unit is not loaded"}
		} else {
			result.Capability = hostStatusFromError(detailErr, "systemd service detail")
		}
	}
	return result
}

func (a *app) collectPlatformHealth(ctx context.Context) hostadmin.PlatformHealthInput {
	p := hostadmin.PlatformHealthInput{}
	managementOK := a.db != nil
	query := func(statement string, args ...any) int {
		n := 0
		if managementOK {
			if err := a.db.QueryRowContext(ctx, statement, args...).Scan(&n); err != nil {
				managementOK = false
			}
		}
		return n
	}
	p.InstalledApps = query("SELECT COUNT(*) FROM installations WHERE desired_state<>'runtime_removed'")
	p.RunningApps = query("SELECT COUNT(*) FROM installations WHERE desired_state='running'")
	p.StoppedApps = query("SELECT COUNT(*) FROM installations WHERE desired_state='stopped'")
	p.FailedApps = query("SELECT COUNT(*) FROM installations WHERE desired_state NOT IN ('running','stopped','runtime_removed')")
	p.ReadinessFailures = query("SELECT COUNT(*) FROM readiness_observations WHERE state='failed'")
	p.StaleReadiness = query("SELECT COUNT(*) FROM readiness_observations WHERE checked_at<?", time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano))
	p.RecentFailedOps = query("SELECT COUNT(*) FROM operations WHERE status IN ('failed','rejected') AND requested_at>=?", time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	p.IngressRoutes = query("SELECT COUNT(*) FROM ingress_routes r WHERE desired_state='enabled' AND NOT EXISTS (SELECT 1 FROM ingress_route_holds h WHERE h.route_id=r.route_id)")
	p.IngressHeld = query("SELECT COUNT(*) FROM ingress_route_holds")
	p.Certificates = query("SELECT COUNT(*) FROM certificates WHERE state IN ('issued','active','expiring','renewal-pending')")
	p.CertificateFailures = query("SELECT COUNT(*) FROM certificates WHERE state IN ('renewal-failed','invalid') OR (not_after<>'' AND not_after<=?)", time.Now().UTC().Format(time.RFC3339))
	p.CertificateExpiring = query("SELECT COUNT(*) FROM certificates WHERE state='expiring' OR (not_after>? AND not_after<=?)", time.Now().UTC().Format(time.RFC3339), time.Now().Add(30*24*time.Hour).UTC().Format(time.RFC3339))
	p.Backups = query("SELECT COUNT(*) FROM operations WHERE type='CreateApplicationBackup' AND status='succeeded'")
	count := query("SELECT COUNT(*) FROM administrator")
	if !managementOK {
		p.Management = hostadmin.Status{State: hostadmin.Unavailable, Detail: "KITPro control database health queries are unavailable"}
		p.Authentication = hostadmin.Status{State: hostadmin.Unavailable, Detail: "administrator configuration could not be evaluated"}
	} else if count > 0 {
		p.Management = hostadmin.Status{State: hostadmin.Available, Detail: "KITPro application health state is available"}
		p.Authentication = hostadmin.Status{State: hostadmin.Available, Detail: "administrator authentication is configured"}
	} else {
		p.Management = hostadmin.Status{State: hostadmin.Available, Detail: "KITPro application health state is available"}
		p.Authentication = hostadmin.Status{State: hostadmin.NotConfigured, Detail: "administrator authentication is not configured"}
	}
	if response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "Ping"}); err == nil && response.OK {
		p.Helper = hostadmin.Status{State: hostadmin.Available, Detail: "privileged helper is responding"}
	} else {
		p.Helper = hostadmin.Status{State: hostadmin.Unavailable, Detail: "privileged helper is unavailable"}
	}
	if response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"}); err == nil && response.OK {
		p.Runtime = hostadmin.Status{State: hostadmin.Available, Detail: "container runtime is responding"}
	} else {
		p.Runtime = hostadmin.Status{State: hostadmin.Unavailable, Detail: "container runtime is unavailable"}
	}
	p.TimeSynchronization, p.Confinement, p.MemoryPressure, p.MemoryPressureSome, p.MemoryPressureFull = hostadmin.CollectSystemSignals(ctx)
	return p
}

func (a *app) collectJournalHealth(ctx context.Context, now time.Time) hostadmin.JournalHealthInput {
	if !a.hostLogsEnabled {
		return hostadmin.JournalHealthInput{Capability: hostadmin.Status{State: hostadmin.NotConfigured, Detail: "journal health evidence is disabled by the Logs feature gate"}}
	}
	priority := 3
	page, err := a.hostJournalProvider.Query(ctx, hostadmin.JournalQuery{Priority: &priority, Since: now.Add(-15 * time.Minute), Until: now, Limit: 20})
	if err != nil {
		return hostadmin.JournalHealthInput{Capability: hostStatusFromError(err, "journal health evidence")}
	}
	return hostadmin.JournalHealthInput{Capability: hostadmin.Status{State: hostadmin.Available, Detail: "bounded critical journal query available"}, CriticalCount: len(page.Entries)}
}

func (a *app) hostHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	bundle := a.collectHealth(ctx)
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/host/health")
	switch {
	case path == "" || path == "/":
		writeNetworkJSON(w, bundle.Health)
	case path == "/checks":
		writeNetworkJSON(w, struct {
			Items       []hostadmin.HealthCheck `json:"items"`
			EvaluatedAt string                  `json:"evaluated_at"`
			FreshUntil  string                  `json:"fresh_until"`
		}{bundle.Health.Checks, bundle.Health.EvaluatedAt, bundle.Health.FreshUntil})
	case strings.HasPrefix(path, "/"):
		component := strings.TrimPrefix(path, "/")
		value, ok := bundle.Health.Components[component]
		if !ok || strings.Contains(component, "/") {
			http.NotFound(w, r)
			return
		}
		items := []hostadmin.HealthCheck{}
		for _, check := range bundle.Health.Checks {
			if check.Component == component {
				items = append(items, check)
			}
		}
		writeNetworkJSON(w, struct {
			Summary     hostadmin.ComponentHealth `json:"summary"`
			Checks      []hostadmin.HealthCheck   `json:"checks"`
			EvaluatedAt string                    `json:"evaluated_at"`
		}{value, items, bundle.Health.EvaluatedAt})
	default:
		http.NotFound(w, r)
	}
}

func (a *app) dashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	b := a.collectHealth(ctx)
	response := dashboardResponse{EvaluatedAt: b.Health.EvaluatedAt, FreshUntil: b.Health.FreshUntil, Health: b.Health.Summary, Components: b.Health.Components, Host: dashboardHost{Hostname: b.Overview.Hostname, OS: b.Overview.OS, Kernel: b.Overview.Kernel, Architecture: b.Overview.Architecture, ServerVersion: buildinfo.Version, UptimeSeconds: b.Overview.UptimeSeconds}, Resources: dashboardResources{CPUUtilization: b.Overview.CPUUtilization, LoadAverage: b.Overview.LoadAverage, Memory: b.Overview.Memory, RootDisk: b.Overview.RootDisk}, Applications: dashboardApplications{Installed: b.Platform.InstalledApps, Running: b.Platform.RunningApps, Stopped: b.Platform.StoppedApps, Failed: b.Platform.FailedApps, ReadinessFailures: b.Platform.ReadinessFailures, RecentFailedOperations: b.Platform.RecentFailedOps}, Services: dashboardServices{Total: b.Services.TotalCount, Failed: b.Services.FailedCount, Essential: map[string]string{}}, Attention: []hostadmin.HealthCheck{}}
	for name, item := range b.Services.Essential {
		response.Services.Essential[name] = item.Active
	}
	if b.Storage != nil {
		response.Storage = dashboardStorage{AvailableBytes: b.Storage.Summary.AvailableBytes, UsedBytes: b.Storage.Summary.UsedBytes, Mounted: b.Storage.Summary.MountedCount, Warnings: b.Storage.Summary.WarningCount}
		for _, fs := range b.Storage.Filesystems {
			if fs.MountPoint == "/" {
				response.Storage.RootUsagePercent = fs.UsagePercent
				break
			}
		}
	}
	if b.Network != nil {
		var rx, tx float64
		seen := false
		for _, iface := range b.Network.Interfaces {
			if iface.Classification == "loopback" || iface.Classification == "container" {
				continue
			}
			if iface.Stats.RXBytesPerSecond != nil {
				rx += *iface.Stats.RXBytesPerSecond
				seen = true
			}
			if iface.Stats.TXBytesPerSecond != nil {
				tx += *iface.Stats.TXBytesPerSecond
				seen = true
			}
		}
		response.Network = dashboardNetwork{ActiveInterfaces: b.Network.Summary.ActiveCount, DefaultRoutes: b.Network.Summary.DefaultRouteCount, Resolvers: b.Network.Summary.ResolverCount, Health: b.Network.Summary.Health}
		if seen {
			response.Network.RXBytesPerSecond = &rx
			response.Network.TXBytesPerSecond = &tx
			response.Resources.RXBytesPerSecond = &rx
			response.Resources.TXBytesPerSecond = &tx
		}
	}
	for _, check := range b.Health.Checks {
		if check.State == hostadmin.HealthCritical || check.State == hostadmin.HealthWarning || check.State == hostadmin.HealthDegraded || check.State == hostadmin.HealthUnavailable {
			response.Attention = append(response.Attention, check)
			if len(response.Attention) == 8 {
				break
			}
		}
	}
	writeNetworkJSON(w, response)
}
