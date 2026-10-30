package hostadmin

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type HealthState string

const (
	HealthHealthy       HealthState = "healthy"
	HealthWarning       HealthState = "warning"
	HealthCritical      HealthState = "critical"
	HealthDegraded      HealthState = "degraded"
	HealthUnavailable   HealthState = "unavailable"
	HealthUnsupported   HealthState = "unsupported"
	HealthNotConfigured HealthState = "not_configured"
)

type HealthThresholds struct {
	CPUWarningPercent       float64
	CPUCriticalPercent      float64
	LoadWarningPerCPU       float64
	LoadCriticalPerCPU      float64
	MemoryWarningAvailable  float64
	MemoryCriticalAvailable float64
	SwapWarningUsed         float64
	SwapCriticalUsed        float64
	CertificateWarningDays  int
	StaleAfter              time.Duration
}

func DefaultHealthThresholds() HealthThresholds {
	return HealthThresholds{
		CPUWarningPercent: 85, CPUCriticalPercent: 95,
		LoadWarningPerCPU: 1.5, LoadCriticalPerCPU: 2.5,
		MemoryWarningAvailable: 10, MemoryCriticalAvailable: 5,
		SwapWarningUsed: 50, SwapCriticalUsed: 80,
		CertificateWarningDays: 30, StaleAfter: 2 * time.Minute,
	}
}

type HealthCheck struct {
	ID                string      `json:"id"`
	Component         string      `json:"component"`
	State             HealthState `json:"state"`
	Severity          string      `json:"severity"`
	Summary           string      `json:"summary"`
	Explanation       string      `json:"explanation"`
	Evidence          string      `json:"evidence,omitempty"`
	LastSuccessful    string      `json:"last_successful_observation,omitempty"`
	LastAttempted     string      `json:"last_attempted_observation"`
	RecommendedAction string      `json:"recommended_action,omitempty"`
	Destination       string      `json:"destination,omitempty"`
	Capability        State       `json:"capability"`
}

type ComponentHealth struct {
	Component     string      `json:"component"`
	State         HealthState `json:"state"`
	Healthy       int         `json:"healthy"`
	Warning       int         `json:"warning"`
	Critical      int         `json:"critical"`
	Degraded      int         `json:"degraded"`
	Unavailable   int         `json:"unavailable"`
	Unsupported   int         `json:"unsupported"`
	NotConfigured int         `json:"not_configured"`
}

type HealthSummary struct {
	State         HealthState `json:"state"`
	Critical      int         `json:"critical"`
	Warnings      int         `json:"warnings"`
	Degraded      int         `json:"degraded"`
	Healthy       int         `json:"healthy"`
	Unavailable   int         `json:"unavailable"`
	Unsupported   int         `json:"unsupported"`
	NotConfigured int         `json:"not_configured"`
}

type HealthSnapshot struct {
	Summary     HealthSummary              `json:"summary"`
	Components  map[string]ComponentHealth `json:"components"`
	Checks      []HealthCheck              `json:"checks"`
	EvaluatedAt string                     `json:"evaluated_at"`
	FreshUntil  string                     `json:"fresh_until"`
	Truncated   bool                       `json:"truncated"`
}

const MaxHealthChecks = 512

type ServiceHealthInput struct {
	FailedCount int
	TotalCount  int
	Capability  Status
	Essential   map[string]Service
}

type PlatformHealthInput struct {
	Management          Status
	Helper              Status
	Runtime             Status
	Authentication      Status
	TimeSynchronization Status
	Confinement         Status
	MemoryPressure      Status
	MemoryPressureSome  float64
	MemoryPressureFull  float64
	InstalledApps       int
	RunningApps         int
	StoppedApps         int
	FailedApps          int
	ReadinessFailures   int
	StaleReadiness      int
	RecentFailedOps     int
	IngressRoutes       int
	IngressHeld         int
	Certificates        int
	CertificateFailures int
	CertificateExpiring int
	Backups             int
}

type JournalHealthInput struct {
	Capability    Status
	CriticalCount int
}

type PersistentStorageHealthInput struct {
	ResourceID      string
	Target          string
	State           string
	Health          string
	Mounted         bool
	SourceAvailable bool
	Detail          string
}

type HealthInput struct {
	Overview     Overview
	CPUCount     int
	Services     ServiceHealthInput
	Storage      *StorageInventory
	StorageError Status
	Persistence  []PersistentStorageHealthInput
	Network      *NetworkInventory
	NetworkError Status
	Platform     PlatformHealthInput
	Journal      JournalHealthInput
	ObservedAt   time.Time
}

type healthBuilder struct {
	now       time.Time
	checks    []HealthCheck
	truncated bool
}

func capabilityForState(state HealthState) Status {
	switch state {
	case HealthUnsupported:
		return Status{State: Unsupported}
	case HealthUnavailable:
		return Status{State: Unavailable}
	case HealthNotConfigured:
		return Status{State: NotConfigured}
	default:
		return Status{State: Available}
	}
}

func severityFor(state HealthState) string {
	switch state {
	case HealthCritical:
		return "critical"
	case HealthWarning, HealthDegraded:
		return "warning"
	default:
		return "information"
	}
}

func (b *healthBuilder) add(id, component string, state HealthState, summary, explanation, evidence, action, destination string, capability Status) {
	if len(b.checks) >= MaxHealthChecks {
		b.truncated = true
		return
	}
	if capability.State == "" {
		capability = capabilityForState(state)
	}
	at := b.now.UTC().Format(time.RFC3339)
	check := HealthCheck{ID: id, Component: component, State: state, Severity: severityFor(state), Summary: boundedText(summary, 200), Explanation: boundedText(explanation, 500), Evidence: boundedText(evidence, 500), LastAttempted: at, RecommendedAction: boundedText(action, 300), Destination: destination, Capability: capability.State}
	if state != HealthUnavailable {
		check.LastSuccessful = at
	}
	b.checks = append(b.checks, check)
}

func percent(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

func thresholdState(value, warning, critical float64) HealthState {
	if value >= critical {
		return HealthCritical
	}
	if value >= warning {
		return HealthWarning
	}
	return HealthHealthy
}

func EvaluateHealth(input HealthInput, thresholds HealthThresholds) HealthSnapshot {
	now := input.ObservedAt
	if now.IsZero() {
		now = time.Now()
	}
	b := healthBuilder{now: now}
	evaluateResources(&b, input, thresholds)
	evaluateServices(&b, input.Services)
	evaluateStorage(&b, input.Storage, input.StorageError, input.Persistence)
	evaluateNetwork(&b, input.Network, input.NetworkError)
	evaluateApplications(&b, input.Platform, thresholds)
	evaluateSecurity(&b, input.Overview.Security, input.Platform)
	evaluateJournal(&b, input.Journal)
	sort.SliceStable(b.checks, func(i, j int) bool {
		ri, rj := healthRank(b.checks[i].State), healthRank(b.checks[j].State)
		if ri != rj {
			return ri > rj
		}
		if b.checks[i].Component != b.checks[j].Component {
			return b.checks[i].Component < b.checks[j].Component
		}
		return b.checks[i].ID < b.checks[j].ID
	})
	snapshot := HealthSnapshot{Checks: b.checks, Components: map[string]ComponentHealth{}, EvaluatedAt: now.UTC().Format(time.RFC3339), FreshUntil: now.Add(thresholds.StaleAfter).UTC().Format(time.RFC3339), Truncated: b.truncated}
	for _, check := range b.checks {
		component := snapshot.Components[check.Component]
		component.Component = check.Component
		addStateCounts(&component, check.State)
		snapshot.Components[check.Component] = component
		addSummaryCounts(&snapshot.Summary, check.State)
	}
	for name, component := range snapshot.Components {
		component.State = aggregateCounts(component.Critical, component.Degraded, component.Warning, component.Unavailable, component.Healthy)
		snapshot.Components[name] = component
	}
	snapshot.Summary.State = aggregateCounts(snapshot.Summary.Critical, snapshot.Summary.Degraded, snapshot.Summary.Warnings, snapshot.Summary.Unavailable, snapshot.Summary.Healthy)
	return snapshot
}

func healthRank(state HealthState) int {
	switch state {
	case HealthCritical:
		return 7
	case HealthDegraded:
		return 6
	case HealthWarning:
		return 5
	case HealthUnavailable:
		return 4
	case HealthNotConfigured:
		return 3
	case HealthUnsupported:
		return 2
	default:
		return 1
	}
}

func aggregateCounts(critical, degraded, warning, unavailable, healthy int) HealthState {
	if critical > 0 {
		return HealthCritical
	}
	if degraded > 0 {
		return HealthDegraded
	}
	if warning > 0 {
		return HealthWarning
	}
	if unavailable > 0 {
		return HealthUnavailable
	}
	if healthy > 0 {
		return HealthHealthy
	}
	return HealthUnavailable
}

func addStateCounts(c *ComponentHealth, state HealthState) {
	switch state {
	case HealthHealthy:
		c.Healthy++
	case HealthWarning:
		c.Warning++
	case HealthCritical:
		c.Critical++
	case HealthDegraded:
		c.Degraded++
	case HealthUnavailable:
		c.Unavailable++
	case HealthUnsupported:
		c.Unsupported++
	case HealthNotConfigured:
		c.NotConfigured++
	}
}
func addSummaryCounts(s *HealthSummary, state HealthState) {
	switch state {
	case HealthHealthy:
		s.Healthy++
	case HealthWarning:
		s.Warnings++
	case HealthCritical:
		s.Critical++
	case HealthDegraded:
		s.Degraded++
	case HealthUnavailable:
		s.Unavailable++
	case HealthUnsupported:
		s.Unsupported++
	case HealthNotConfigured:
		s.NotConfigured++
	}
}

func evaluateResources(b *healthBuilder, input HealthInput, t HealthThresholds) {
	o := input.Overview
	if status, ok := o.Collectors["os"]; ok && status.State != Available {
		b.add("platform.os", "platform", HealthUnavailable, "Operating-system identity unavailable", status.Detail, "", "Review Host Overview collector access.", "#host-overview", status)
	} else {
		b.add("platform.os", "platform", HealthHealthy, "Operating-system identity", o.OS, o.Kernel, "", "#host-overview", Status{State: Available})
	}
	if o.UptimeSeconds == nil {
		b.add("resources.uptime", "resources", HealthUnavailable, "System uptime unavailable", "System uptime could not be collected.", "", "Review Host Overview collector access.", "#host-overview", Status{State: Unavailable})
	} else {
		b.add("resources.uptime", "resources", HealthHealthy, "System uptime", fmt.Sprintf("The host has been running for %d seconds.", *o.UptimeSeconds), fmt.Sprintf("%d seconds", *o.UptimeSeconds), "", "#host-overview", Status{State: Available})
	}
	if o.CPUUtilization == nil || !finite(*o.CPUUtilization) {
		b.add("resources.cpu", "resources", HealthUnavailable, "CPU utilization unavailable", "The bounded CPU sample could not be collected.", "", "Review Host Overview collector status.", "#host-overview", Status{State: Unavailable})
	} else {
		state := thresholdState(*o.CPUUtilization, t.CPUWarningPercent, t.CPUCriticalPercent)
		b.add("resources.cpu", "resources", state, "CPU utilization", fmt.Sprintf("CPU utilization is %.1f%%.", *o.CPUUtilization), fmt.Sprintf("%.1f%% over the bounded sample", *o.CPUUtilization), "Review active applications and services if utilization remains elevated.", "#host-overview", Status{State: Available})
	}
	if len(o.LoadAverage) < 1 || input.CPUCount < 1 || !finite(o.LoadAverage[0]) {
		b.add("resources.load", "resources", HealthUnavailable, "Load context unavailable", "Load average or CPU capacity could not be collected.", "", "Review Host Overview collector status.", "#host-overview", Status{State: Unavailable})
	} else {
		perCPU := o.LoadAverage[0] / float64(input.CPUCount)
		state := thresholdState(perCPU, t.LoadWarningPerCPU, t.LoadCriticalPerCPU)
		b.add("resources.load", "resources", state, "System load", fmt.Sprintf("One-minute load is %.2f across %d logical CPUs.", o.LoadAverage[0], input.CPUCount), fmt.Sprintf("%.2f load per logical CPU", perCPU), "Inspect sustained CPU and runnable work when elevated.", "#host-overview", Status{State: Available})
	}
	if input.Platform.MemoryPressure.State == Available {
		state := HealthHealthy
		if input.Platform.MemoryPressureFull >= 10 {
			state = HealthCritical
		} else if input.Platform.MemoryPressureSome >= 20 || input.Platform.MemoryPressureFull >= 2 {
			state = HealthWarning
		}
		b.add("resources.memory_pressure", "resources", state, "Memory pressure", fmt.Sprintf("Memory pressure over 10 seconds: some %.2f%%, full %.2f%%.", input.Platform.MemoryPressureSome, input.Platform.MemoryPressureFull), input.Platform.MemoryPressure.Detail, "Review sustained memory contention when pressure remains elevated.", "#host-health", input.Platform.MemoryPressure)
	} else {
		state := HealthUnsupported
		if input.Platform.MemoryPressure.State == Unavailable {
			state = HealthUnavailable
		}
		b.add("resources.memory_pressure", "resources", state, "Memory pressure unavailable", input.Platform.MemoryPressure.Detail, "", "", "#host-health", input.Platform.MemoryPressure)
	}
	if o.Memory == nil || o.Memory.TotalBytes == 0 {
		b.add("resources.memory", "resources", HealthUnavailable, "Memory availability unavailable", "Linux available-memory data could not be collected.", "", "Review Host Overview collector status.", "#host-overview", Status{State: Unavailable})
	} else {
		available := percent(o.Memory.AvailableBytes, o.Memory.TotalBytes)
		state := HealthHealthy
		if available <= t.MemoryCriticalAvailable {
			state = HealthCritical
		} else if available <= t.MemoryWarningAvailable {
			state = HealthWarning
		}
		b.add("resources.memory", "resources", state, "Available memory", fmt.Sprintf("%.1f%% of memory is available.", available), fmt.Sprintf("%d of %d bytes available", o.Memory.AvailableBytes, o.Memory.TotalBytes), "Review application memory use when available memory remains low.", "#host-overview", Status{State: Available})
		if o.Memory.SwapTotalBytes == 0 {
			b.add("resources.swap", "resources", HealthNotConfigured, "Swap is not configured", "This host has no configured swap. This may be intentional.", "", "Configure swap only when required by the host workload.", "#host-overview", Status{State: NotConfigured})
		} else {
			used := o.Memory.SwapTotalBytes - o.Memory.SwapFreeBytes
			usedPct := percent(used, o.Memory.SwapTotalBytes)
			state := thresholdState(usedPct, t.SwapWarningUsed, t.SwapCriticalUsed)
			if state == HealthCritical && available > t.MemoryWarningAvailable {
				state = HealthWarning
			}
			b.add("resources.swap", "resources", state, "Swap use", fmt.Sprintf("%.1f%% of swap is in use.", usedPct), fmt.Sprintf("%d of %d bytes used", used, o.Memory.SwapTotalBytes), "Review memory pressure and application working sets if swap use grows.", "#host-overview", Status{State: Available})
		}
	}
}

func CollectSystemSignals(ctx context.Context) (timeSync, confinement, pressure Status, pressureSome, pressureFull float64) {
	timeSync = Status{State: Unsupported, Detail: "time synchronization status is unsupported"}
	if _, err := os.Stat("/usr/bin/timedatectl"); err == nil {
		cmd := exec.CommandContext(ctx, "/usr/bin/timedatectl", "show", "--property=NTPSynchronized", "--value")
		cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/usr/sbin"}
		out := &limitedBuffer{Max: 64}
		cmd.Stdout, cmd.Stderr = out, &limitedBuffer{Max: 256}
		if err := cmd.Run(); err == nil {
			switch strings.TrimSpace(strings.ToLower(out.String())) {
			case "yes":
				timeSync = Status{State: Available, Detail: "system time is synchronized"}
			case "no":
				timeSync = Status{State: NotConfigured, Detail: "system time is not synchronized"}
			default:
				timeSync = Status{State: Unavailable, Detail: "time synchronization status is unavailable"}
			}
		} else {
			timeSync = Status{State: Unavailable, Detail: "time synchronization status is unavailable"}
		}
	}
	confinement = Status{State: Unavailable, Detail: "process confinement status is unavailable"}
	if raw, err := readBounded("/proc/self/status"); err == nil {
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "NoNewPrivs:") {
				if strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:")) == "1" {
					confinement = Status{State: Available, Detail: "NoNewPrivileges is enabled for the API process"}
				} else {
					confinement = Status{State: Failed, Detail: "NoNewPrivileges is disabled for the API process"}
				}
				break
			}
		}
	}
	pressure = Status{State: Unsupported, Detail: "memory pressure information is unsupported"}
	if raw, err := readBounded("/proc/pressure/memory"); err == nil {
		pressure = Status{State: Available, Detail: "Linux memory pressure information is available"}
		for _, line := range strings.Split(raw, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			value := 0.0
			for _, field := range fields[1:] {
				if strings.HasPrefix(field, "avg10=") {
					value, _ = strconv.ParseFloat(strings.TrimPrefix(field, "avg10="), 64)
				}
			}
			if fields[0] == "some" {
				pressureSome = value
			}
			if fields[0] == "full" {
				pressureFull = value
			}
		}
	} else if !os.IsNotExist(err) {
		pressure = Status{State: Unavailable, Detail: "memory pressure information is unavailable"}
	}
	return
}

func evaluateServices(b *healthBuilder, input ServiceHealthInput) {
	if input.Capability.State != Available {
		state := HealthUnavailable
		if input.Capability.State == Unsupported {
			state = HealthUnsupported
		}
		b.add("services.inventory", "services", state, "Service health unavailable", "The systemd service inventory could not be evaluated.", input.Capability.Detail, "Review systemd availability and API permissions.", "#host-services", input.Capability)
		return
	}
	if input.FailedCount > 0 {
		b.add("services.failed", "services", HealthCritical, "Failed system services", fmt.Sprintf("%d systemd service units are failed.", input.FailedCount), fmt.Sprintf("failed service count: %d", input.FailedCount), "Inspect failed services and their authorized logs.", "#host-services", Status{State: Available})
	} else {
		b.add("services.failed", "services", HealthHealthy, "No failed system services", "No failed systemd service units were observed.", "failed service count: 0", "", "#host-services", Status{State: Available})
	}
	for _, name := range []string{"kitpro-api.service", "kitpro-helper.service", "kitpro-helper.socket", "kitpro-ingress.service", "docker.service", "containerd.service"} {
		service, ok := input.Essential[name]
		if !ok {
			continue
		}
		state := HealthHealthy
		if service.Active != "active" {
			state = HealthCritical
			if name == "containerd.service" {
				state = HealthDegraded
			}
		}
		b.add("services."+strings.TrimSuffix(name, ".service"), "services", state, name, fmt.Sprintf("%s is %s (%s).", name, service.Active, service.Sub), service.Failure, "Inspect the service details and logs.", "#host-services", Status{State: Available})
	}
}

func evaluateStorage(b *healthBuilder, inventory *StorageInventory, capability Status, persistence []PersistentStorageHealthInput) {
	if inventory == nil {
		state := HealthUnavailable
		if capability.State == Unsupported {
			state = HealthUnsupported
		}
		b.add("storage.inventory", "storage", state, "Storage inventory unavailable", "Mounted filesystems and capacity could not be evaluated.", capability.Detail, "Review Storage inventory availability.", "#host-storage", capability)
		return
	}
	foundRoot := false
	for _, fs := range inventory.Filesystems {
		if fs.MountPoint == "/" {
			foundRoot = true
		}
		state := HealthHealthy
		summary := "Filesystem capacity is within configured thresholds"
		action := ""
		for _, warning := range fs.Warnings {
			switch warning {
			case "capacity_critical":
				state = HealthCritical
				summary = "Filesystem capacity is critical"
				action = "Free space or expand the filesystem before writes fail."
			case "mounted_read_only":
				if state != HealthCritical {
					state = HealthCritical
				}
				summary = "Critical filesystem is mounted read-only"
				action = "Inspect the filesystem and kernel logs before attempting recovery."
			case "capacity_warning":
				if state == HealthHealthy {
					state = HealthWarning
				}
				summary = "Filesystem capacity is nearing its limit"
				action = "Review storage use and available capacity."
			case "capacity_unavailable":
				if state == HealthHealthy {
					state = HealthUnavailable
				}
				summary = "Filesystem capacity is unavailable"
			}
		}
		if fs.MountPoint == "/" || len(fs.Warnings) > 0 || healthContains(fs.Protection, "kitpro_state") || healthContains(fs.Protection, "container_storage") {
			evidence := fs.MountPoint
			if fs.UsagePercent != nil {
				evidence = fmt.Sprintf("%s at %.1f%% used", fs.MountPoint, *fs.UsagePercent)
			}
			b.add("storage."+safeID(fs.ID), "storage", state, summary, fmt.Sprintf("Filesystem %s (%s).", fs.MountPoint, fs.Type), evidence, action, "#host-storage", fs.Status)
		}
	}
	if !foundRoot {
		b.add("storage.root", "storage", HealthUnavailable, "Root filesystem unavailable", "The storage inventory did not include the root filesystem.", "", "Review mount visibility for the packaged API service.", "#host-storage", Status{State: Unavailable})
	}
	deviceHealth := inventory.Capabilities["device_health"]
	state := HealthUnsupported
	if deviceHealth.State == PermissionDenied || deviceHealth.State == Unavailable {
		state = HealthUnavailable
	}
	b.add("storage.device_health", "storage", state, "Physical device health not asserted", "Block-device presence and capacity do not establish physical media health.", deviceHealth.Detail, "Use a separately authorized device-health provider when required.", "#host-storage", deviceHealth)
	for _, configured := range persistence {
		state := HealthHealthy
		summary := "Persistent optional mount is available"
		explanation := fmt.Sprintf("The KITPro-managed optional mount at %s is configured and mounted.", configured.Target)
		action := ""
		if configured.State == "needs_recovery" || configured.Health == "needs_recovery" {
			state = HealthDegraded
			summary = "Persistent mount configuration needs recovery"
			explanation = fmt.Sprintf("The KITPro-managed configuration for %s is incomplete.", configured.Target)
			action = "Review the Storage configuration and run the explicit recovery action."
		} else if configured.State == "enabled" && (!configured.Mounted || !configured.SourceAvailable) {
			state = HealthWarning
			summary = "Optional persistent mount is unavailable"
			explanation = fmt.Sprintf("The optional KITPro-managed filesystem at %s is configured but is not currently available.", configured.Target)
			action = "Restore the approved device or disable the stale persistent configuration."
		} else if configured.State != "enabled" {
			state = HealthUnavailable
			summary = "Persistent mount configuration state is uncertain"
			explanation = fmt.Sprintf("KITPro could not establish a healthy persistent configuration for %s.", configured.Target)
			action = "Review the Storage configuration before attempting another change."
		}
		evidence := configured.Detail
		if evidence == "" {
			evidence = fmt.Sprintf("state=%s mounted=%t source_available=%t", configured.State, configured.Mounted, configured.SourceAvailable)
		}
		b.add("storage.persistence."+safeID(configured.ResourceID), "storage", state, summary, explanation, evidence, action, "#host-storage", Status{State: Available})
	}
}

func evaluateNetwork(b *healthBuilder, inventory *NetworkInventory, capability Status) {
	if inventory == nil {
		state := HealthUnavailable
		if capability.State == Unsupported {
			state = HealthUnsupported
		}
		b.add("network.inventory", "network", state, "Network inventory unavailable", "Interfaces, routes, and resolver configuration could not be evaluated.", capability.Detail, "Review Networking inventory availability.", "#host-network", capability)
		return
	}
	state := HealthHealthy
	switch inventory.Summary.Health {
	case "not_configured":
		state = HealthNotConfigured
	case "unavailable":
		state = HealthUnavailable
	case "degraded":
		state = HealthDegraded
	}
	b.add("network.configuration", "network", state, "Observed network configuration", inventory.Summary.HealthDetail, fmt.Sprintf("%d active interfaces, %d host default routes, %d resolvers", inventory.Summary.ActiveCount, inventory.Summary.DefaultRouteCount, inventory.Summary.ResolverCount), "Use Networking diagnostics when connectivity is expected.", "#host-network", Status{State: Available})
	var errorsAndDrops uint64
	for _, iface := range inventory.Interfaces {
		if iface.Classification == "loopback" {
			continue
		}
		errorsAndDrops += iface.Stats.RXErrors + iface.Stats.TXErrors + iface.Stats.RXDropped + iface.Stats.TXDropped
		if iface.AdministrativeState == "up" && iface.Carrier != nil && !*iface.Carrier && iface.Classification == "physical" {
			b.add("network.carrier."+safeID(iface.Name), "network", HealthWarning, "Interface has no carrier", fmt.Sprintf("%s is administratively up without carrier.", iface.Name), "carrier absent", "Check the link if this interface is expected to be active.", "#host-network", iface.Status)
		}
	}
	if errorsAndDrops > 0 {
		b.add("network.counters", "network", HealthWarning, "Network errors or drops observed", "Interface counters contain errors or drops. Historical or intentional drops may not indicate a current outage.", fmt.Sprintf("combined errors and drops: %d", errorsAndDrops), "Compare counters over time and inspect the affected interface.", "#host-network", Status{State: Available})
	} else {
		b.add("network.counters", "network", HealthHealthy, "No interface errors observed", "No packet errors or drops were present in the current snapshot.", "combined errors and drops: 0", "", "#host-network", Status{State: Available})
	}
}

func evaluateApplications(b *healthBuilder, p PlatformHealthInput, t HealthThresholds) {
	for id, observation := range map[string]Status{"helper": p.Helper, "runtime": p.Runtime, "authentication": p.Authentication} {
		state := HealthHealthy
		if observation.State != Available {
			state = HealthCritical
		}
		b.add("platform."+id, "platform", state, "KITPro "+id, observation.Detail, observation.Detail, "Review KITPro service readiness.", "#settings", observation)
	}
	if p.Management.State != Available {
		b.add("applications.inventory", "applications", HealthUnavailable, "Application health unavailable", "KITPro application, ingress, certificate, operation, and backup state could not be evaluated.", p.Management.Detail, "Review the KITPro control database and API logs.", "#applications", p.Management)
		return
	}
	appState := HealthHealthy
	if p.FailedApps > 0 || p.ReadinessFailures > 0 {
		appState = HealthCritical
	} else if p.StaleReadiness > 0 {
		appState = HealthDegraded
	} else if p.RecentFailedOps > 0 {
		appState = HealthWarning
	}
	b.add("applications.summary", "applications", appState, "Managed applications", fmt.Sprintf("%d installed, %d running, %d stopped, %d failed.", p.InstalledApps, p.RunningApps, p.StoppedApps, p.FailedApps), fmt.Sprintf("%d readiness failures; %d stale readiness observations; %d recent failed operations", p.ReadinessFailures, p.StaleReadiness, p.RecentFailedOps), "Review affected applications and recent operations.", "#applications", Status{State: Available})
	ingressState := HealthHealthy
	if p.IngressHeld > 0 {
		ingressState = HealthWarning
	}
	if p.IngressRoutes == 0 {
		ingressState = HealthNotConfigured
	}
	b.add("applications.ingress", "applications", ingressState, "Managed ingress", fmt.Sprintf("%d active routes; %d held for readiness.", p.IngressRoutes, p.IngressHeld), "", "Review ingress routes awaiting readiness.", "#applications", Status{State: Available})
	certState := HealthHealthy
	if p.CertificateFailures > 0 {
		certState = HealthCritical
	} else if p.CertificateExpiring > 0 {
		certState = HealthWarning
	} else if p.Certificates == 0 {
		certState = HealthNotConfigured
	}
	b.add("applications.certificates", "applications", certState, "Certificates", fmt.Sprintf("%d configured, %d expiring, %d failed.", p.Certificates, p.CertificateExpiring, p.CertificateFailures), "", "Review certificate state and renewal readiness.", "#settings", Status{State: Available})
	backupState := HealthHealthy
	if p.InstalledApps > 0 && p.Backups == 0 {
		backupState = HealthWarning
	}
	b.add("applications.backups", "applications", backupState, "Application backups", fmt.Sprintf("%d completed application backups are recorded.", p.Backups), "", "Create and verify backups for important application data.", "#applications", Status{State: Available})
}

func evaluateSecurity(b *healthBuilder, security Status, p PlatformHealthInput) {
	state := HealthUnavailable
	if security.State == Available {
		text := strings.ToLower(security.Detail)
		state = HealthHealthy
		if strings.Contains(text, "permissive") || strings.Contains(text, "disabled") {
			state = HealthWarning
		}
	} else if security.State == Unsupported {
		state = HealthUnsupported
	}
	b.add("security.enforcement", "security", state, "Linux security enforcement", security.Detail, security.Detail, "Review the platform security policy when enforcement is disabled.", "#host-overview", security)
	for id, observation := range map[string]Status{"time": p.TimeSynchronization, "confinement": p.Confinement} {
		state := HealthHealthy
		if observation.State == Available && strings.Contains(strings.ToLower(observation.Detail), "disabled") {
			state = HealthWarning
		} else if observation.State == NotConfigured {
			state = HealthNotConfigured
		} else if observation.State == Unsupported {
			state = HealthUnsupported
		} else if observation.State != Available {
			state = HealthUnavailable
		}
		b.add("security."+id, "security", state, map[string]string{"time": "System time synchronization", "confinement": "KITPro service confinement"}[id], observation.Detail, observation.Detail, "Review the host security configuration.", "#settings", observation)
	}
}

func evaluateJournal(b *healthBuilder, journal JournalHealthInput) {
	state := HealthHealthy
	switch journal.Capability.State {
	case NotConfigured:
		state = HealthNotConfigured
	case Unsupported:
		state = HealthUnsupported
	case PermissionDenied, Unavailable, Failed:
		state = HealthUnavailable
	}
	if journal.CriticalCount > 0 {
		state = HealthWarning
	}
	summary := "Journal health evidence"
	explanation := journal.Capability.Detail
	if journal.CriticalCount > 0 {
		explanation = fmt.Sprintf("%d recent critical journal entries were observed; message content is not included.", journal.CriticalCount)
	}
	b.add("journal.critical", "journal", state, summary, explanation, fmt.Sprintf("recent critical entry count: %d", journal.CriticalCount), "Open Logs when authorized to inspect bounded details.", "#host-logs", journal.Capability)
}

func safeID(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
		if b.Len() >= 80 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
func healthContains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func finite(v float64) bool { return !math.IsInf(v, 0) && !math.IsNaN(v) }
