package hostadmin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
)

type ServiceAction string

const (
	ActionStart   ServiceAction = "start"
	ActionStop    ServiceAction = "stop"
	ActionRestart ServiceAction = "restart"
	ActionReload  ServiceAction = "reload"
	ActionEnable  ServiceAction = "enable"
	ActionDisable ServiceAction = "disable"
)

var serviceActions = []ServiceAction{ActionStart, ActionStop, ActionRestart, ActionReload, ActionEnable, ActionDisable}

type ActionEligibility struct {
	Action       ServiceAction `json:"action"`
	Allowed      bool          `json:"allowed"`
	State        string        `json:"state"`
	Reason       string        `json:"reason,omitempty"`
	Concerns     []string      `json:"dependency_concerns,omitempty"`
	Expected     string        `json:"expected_transition,omitempty"`
	Confirmation bool          `json:"confirmation_required"`
	Uncertain    bool          `json:"uncertain"`
}

type ServiceActionPreview struct {
	Unit        string            `json:"unit"`
	Action      ServiceAction     `json:"action"`
	Current     string            `json:"current_state"`
	Eligibility ActionEligibility `json:"eligibility"`
}

type ServicePolicy struct {
	Enabled bool
	Allowed map[string]map[ServiceAction]bool
}

func NewServicePolicy(enabled bool, configured string) ServicePolicy {
	allowed := map[string]map[ServiceAction]bool{}
	allowAllActions(allowed, "kitpro-qualification.service")
	for _, entry := range strings.FieldsFunc(configured, func(r rune) bool { return r == ',' || r == ';' }) {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		name := strings.TrimSpace(parts[0])
		if !ValidUnit(name) {
			continue
		}
		if len(parts) == 1 {
			allowAllActions(allowed, name)
			continue
		}
		actions := map[ServiceAction]bool{}
		for _, raw := range strings.Split(parts[1], "|") {
			action := ServiceAction(strings.TrimSpace(raw))
			if ValidServiceAction(action) {
				actions[action] = true
			}
		}
		if len(actions) > 0 {
			allowed[name] = actions
		}
	}
	return ServicePolicy{Enabled: enabled, Allowed: allowed}
}

func allowAllActions(allowed map[string]map[ServiceAction]bool, name string) {
	actions := map[ServiceAction]bool{}
	for _, action := range serviceActions {
		actions[action] = true
	}
	allowed[name] = actions
}

func ValidServiceAction(action ServiceAction) bool {
	for _, candidate := range serviceActions {
		if action == candidate {
			return true
		}
	}
	return false
}

var protectedUnitFragments = []string{
	"kitpro-", "ssh.service", "sshd.service", "dbus.service", "systemd-", "networkmanager.service",
	"systemd-networkd.service", "systemd-resolved.service", "networking.service", "wicked.service",
	"connman.service", "netctl", "iwd.service", "wpa_supplicant.service", "dhcpcd.service",
	"docker.service", "docker.socket", "containerd.service", "podman.service", "podman.socket",
	"nginx.service", "apparmor.service", "firewalld.service", "ufw.service", "nftables.service",
	"cryptsetup", "lvm2", "multipathd.service", "mdmonitor.service", "zfs", "iscsid.service",
	"iscsi.service", "nfs-server.service", "rpc-statd.service", "remote-fs", "local-fs", "udisks2.service",
}

func protectedIdentity(name string) (bool, string) {
	lower := strings.ToLower(name)
	if name == "kitpro-qualification.service" {
		return false, ""
	}
	for _, fragment := range protectedUnitFragments {
		if strings.Contains(lower, fragment) {
			return true, "The unit is part of host management, access, networking, storage, security, or application infrastructure."
		}
	}
	return false, ""
}

func (p ServicePolicy) Evaluate(service Service, aliases []string) []ActionEligibility {
	service.Aliases = append(service.Aliases, aliases...)
	p.Assess(&service)
	return service.Actions
}

func (p ServicePolicy) Assess(service *Service) {
	if service.Canonical == "" {
		service.Canonical = service.Name
	}
	category, protection := classifyService(service.Name)
	identities := append([]string{service.Name}, service.Aliases...)
	for _, identity := range identities {
		if protected, reason := protectedIdentity(identity); protected {
			category, protection = classifyService(identity)
			if protection == "" {
				protection = reason
			}
			service.Category, service.Management, service.Protection = category, "protected", protection
			service.Actions = deniedActions("protected", protection)
			return
		}
	}
	if !p.Enabled {
		service.Category, service.Management, service.Protection = category, "not_configured", "Service management is disabled on this server."
		service.Actions = deniedActions("not_configured", service.Protection)
		return
	}
	allowed, approved := p.Allowed[service.Name]
	if !approved {
		service.Category, service.Management, service.Protection = category, "not_eligible", protection
		service.Actions = deniedActions("requires_maintenance_workflow", "This unit is not in the operator-approved service catalog.")
		return
	}
	if strings.Contains(service.Name, "@.") {
		service.Category, service.Management, service.Protection = CategoryCustom, "not_eligible", "Template unit definitions require an exact reviewed instance."
		service.Actions = deniedActions("template_requires_instance", service.Protection)
		return
	}
	if !service.MetadataComplete {
		service.Category, service.Management, service.Protection = CategoryUnknown, "review_required", "Complete systemd metadata is required before this service can be managed."
		service.Actions = deniedActions("metadata_unavailable", service.Protection)
		return
	}
	if service.UnitFile == "" || !service.UnitTrusted {
		service.Category, service.Management, service.Protection = CategoryCustom, "review_required", "The unit definition is not a root-owned, non-writable file in a trusted systemd directory."
		service.Actions = deniedActions("untrusted_unit_definition", service.Protection)
		return
	}
	service.Category, service.Management, service.Protection = CategoryApproved, "managed", ""
	if strings.HasPrefix(service.UnitFile, "/etc/systemd/system/") {
		service.Category = CategoryCustom
	}
	result := make([]ActionEligibility, 0, len(serviceActions))
	for _, action := range serviceActions {
		item := ActionEligibility{Action: action, Allowed: allowed[action], State: "eligible", Expected: expectedTransition(action), Confirmation: true}
		if !item.Allowed {
			item.State, item.Reason = "not_approved", "This action is not approved for this catalog entry."
		}
		concerns := p.dependencyConcerns(*service, action)
		if len(concerns) > 0 {
			item.Allowed, item.State, item.Reason, item.Concerns = false, "protected_dependency", "The operation could affect protected or unreviewed dependencies.", concerns
		}
		switch action {
		case ActionStart:
			if service.Active == "active" || service.Active == "activating" {
				item.Allowed, item.State, item.Reason = false, "unsupported", "The service is already active."
			}
		case ActionStop:
			if service.Active != "active" {
				item.Allowed, item.State, item.Reason = false, "unsupported", "Only an active service can be stopped."
			}
		case ActionRestart:
			if service.Active != "active" {
				item.Allowed, item.State, item.Reason = false, "unsupported", "Only an active service can be restarted."
			}
		case ActionReload:
			if service.Active != "active" || !service.CanReload {
				item.Allowed, item.State, item.Reason = false, "unsupported", "The service does not advertise reload support while active."
			}
		case ActionEnable:
			if service.Enabled == "enabled" || service.Enabled == "static" || service.Enabled == "masked" {
				item.Allowed, item.State, item.Reason = false, "unsupported", "The current unit-file state cannot be enabled by this action."
			}
		case ActionDisable:
			if service.Enabled != "enabled" {
				item.Allowed, item.State, item.Reason = false, "unsupported", "Only an enabled service can be disabled."
			}
		}
		result = append(result, item)
	}
	service.Actions = result
}

const maxDependencyGraphUnits = 64

func graphConcerns(ctx context.Context, conn UnitConnection, policy ServicePolicy, service Service, action ServiceAction) []string {
	queue := append([]string{}, consequenceUnits(service.Relationships, action)...)
	seen := map[string]bool{service.Name: true}
	concerns := []string{}
	for len(queue) > 0 && len(seen) < maxDependencyGraphUnits {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if protected, _ := protectedIdentity(name); protected {
			concerns = append(concerns, name)
			continue
		}
		if unreviewedConsequence(name) {
			if _, reviewed := policy.Allowed[name]; !reviewed {
				concerns = append(concerns, name+" (unreviewed consequence)")
				continue
			}
		}
		if !ValidUnit(name) {
			continue
		}
		props, err := conn.GetUnitPropertiesContext(ctx, name)
		if err != nil {
			concerns = append(concerns, name+" (metadata unavailable)")
			continue
		}
		queue = append(queue, consequenceUnits(serviceRelationships(props), action)...)
	}
	if len(queue) > 0 {
		concerns = append(concerns, "dependency graph exceeded the safety limit")
	}
	sort.Strings(concerns)
	if len(concerns) > 16 {
		concerns = concerns[:16]
	}
	return concerns
}

func consequenceUnits(r ServiceRelationships, action ServiceAction) []string {
	switch action {
	case ActionStart:
		return append(append(append(append(append([]string{}, r.Requires...), r.Wants...), r.Requisite...), r.BindsTo...), r.Conflicts...)
	case ActionStop, ActionRestart:
		return append(append(append(append(append(append([]string{}, r.PartOf...), r.RequiredBy...), r.WantedBy...), r.BindsTo...), r.TriggeredBy...), r.Triggers...)
	case ActionEnable, ActionDisable:
		return append(append([]string{}, r.RequiredBy...), r.WantedBy...)
	}
	return nil
}

func denyForGraph(items []ActionEligibility, action ServiceAction, concerns []string) []ActionEligibility {
	if len(concerns) == 0 {
		return items
	}
	for i := range items {
		if items[i].Action == action {
			items[i].Allowed = false
			items[i].State = "protected_dependency"
			items[i].Reason = "The bounded dependency analysis found protected or uncertain consequences."
			items[i].Concerns = concerns
			items[i].Uncertain = true
		}
	}
	return items
}

func deniedActions(state, reason string) []ActionEligibility {
	result := make([]ActionEligibility, 0, len(serviceActions))
	for _, action := range serviceActions {
		result = append(result, ActionEligibility{Action: action, State: state, Reason: reason, Expected: expectedTransition(action), Confirmation: true, Uncertain: state == "unavailable"})
	}
	return result
}

func expectedTransition(action ServiceAction) string {
	switch action {
	case ActionStart:
		return "inactive to active"
	case ActionStop:
		return "active to inactive"
	case ActionRestart:
		return "active through restart to active"
	case ActionReload:
		return "active configuration reload"
	case ActionEnable:
		return "unit enabled for future activation"
	case ActionDisable:
		return "unit disabled for future activation"
	}
	return "unknown"
}

func (p ServicePolicy) dependencyConcerns(service Service, action ServiceAction) []string {
	var candidates []string
	r := service.Relationships
	switch action {
	case ActionStart:
		candidates = append(candidates, r.Requires...)
		candidates = append(candidates, r.Wants...)
		candidates = append(candidates, r.Requisite...)
		candidates = append(candidates, r.BindsTo...)
		candidates = append(candidates, r.Conflicts...)
	case ActionStop, ActionRestart:
		candidates = append(candidates, r.PartOf...)
		candidates = append(candidates, r.RequiredBy...)
		candidates = append(candidates, r.WantedBy...)
		candidates = append(candidates, r.TriggeredBy...)
		candidates = append(candidates, r.Triggers...)
		candidates = append(candidates, r.BindsTo...)
	case ActionEnable, ActionDisable:
		candidates = append(candidates, r.RequiredBy...)
		candidates = append(candidates, r.WantedBy...)
	}
	seen := map[string]bool{}
	result := []string{}
	if (action == ActionStop || action == ActionRestart) && r.StopWhenUnneeded {
		result = append(result, "StopWhenUnneeded may propagate to dependencies")
	}
	for _, name := range candidates {
		_, reviewed := p.Allowed[name]
		unreviewedService := unreviewedConsequence(name) && !reviewed
		if protected, _ := protectedIdentity(name); (protected || unreviewedService) && !seen[name] {
			seen[name] = true
			result = append(result, name)
			if len(result) == 16 {
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func unreviewedConsequence(name string) bool {
	for _, suffix := range []string{".service", ".socket", ".mount", ".automount", ".path"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func FindEligibility(items []ActionEligibility, action ServiceAction) (ActionEligibility, bool) {
	for _, item := range items {
		if item.Action == action {
			return item, true
		}
	}
	return ActionEligibility{}, false
}

type ServiceMutationResult struct {
	Unit          string        `json:"unit"`
	Action        ServiceAction `json:"action"`
	Previous      Service       `json:"previous"`
	Result        Service       `json:"result"`
	SystemdResult string        `json:"systemd_result"`
}

type ServiceMutationError struct {
	Code       string
	Status     string
	Dispatched bool
	Cause      error
}

func (e *ServiceMutationError) Error() string {
	if e == nil || e.Cause == nil {
		return "service operation failed"
	}
	return e.Cause.Error()
}
func (e *ServiceMutationError) Unwrap() error { return e.Cause }

func mutationError(code, status string, dispatched bool, cause error) error {
	return &ServiceMutationError{Code: code, Status: status, Dispatched: dispatched, Cause: cause}
}

type MutationConnection interface {
	UnitConnection
	StartUnitContext(context.Context, string, string, chan<- string) (int, error)
	StopUnitContext(context.Context, string, string, chan<- string) (int, error)
	RestartUnitContext(context.Context, string, string, chan<- string) (int, error)
	ReloadUnitContext(context.Context, string, string, chan<- string) (int, error)
	EnableUnitFilesContext(context.Context, []string, bool, bool) (bool, []systemdbus.EnableUnitFileChange, error)
	DisableUnitFilesContext(context.Context, []string, bool) ([]systemdbus.DisableUnitFileChange, error)
}

type ServiceController struct {
	Open      func(context.Context) (MutationConnection, error)
	Policy    ServicePolicy
	UnitTrust func(string) bool
	locks     sync.Map
}

func NewServiceController(policy ServicePolicy) *ServiceController {
	return &ServiceController{Open: func(ctx context.Context) (MutationConnection, error) {
		return systemdbus.NewSystemConnectionContext(ctx)
	}, Policy: policy, UnitTrust: trustedUnitDefinition}
}

func (c *ServiceController) Mutate(ctx context.Context, name string, action ServiceAction) (ServiceMutationResult, error) {
	if !ValidUnit(name) || !ValidServiceAction(action) {
		return ServiceMutationResult{}, mutationError("InvalidRequest", "rejected", false, ErrInvalid)
	}
	lockValue, _ := c.locks.LoadOrStore(name, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	if !lock.TryLock() {
		return ServiceMutationResult{}, mutationError("OperationConflict", "rejected", false, errors.New("service operation already in progress"))
	}
	defer lock.Unlock()
	conn, err := c.Open(ctx)
	if err != nil {
		return ServiceMutationResult{}, mutationError("ServiceBackendUnavailable", "failed", false, serviceBackendError(err))
	}
	defer conn.Close()
	previous, aliases, err := serviceFromConnection(ctx, conn, name, c.UnitTrust)
	if err != nil {
		return ServiceMutationResult{}, mutationError("ServiceMetadataUnavailable", "rejected", false, err)
	}
	items := c.Policy.Evaluate(previous, aliases)
	items = denyForGraph(items, action, graphConcerns(ctx, conn, c.Policy, previous, action))
	eligibility, ok := FindEligibility(items, action)
	if !ok || !eligibility.Allowed {
		return ServiceMutationResult{}, mutationError("ServiceActionDenied", "rejected", false, fmt.Errorf("service action denied: %s", eligibility.Reason))
	}
	result := ServiceMutationResult{Unit: previous.Name, Action: action, Previous: previous}
	job := make(chan string, 1)
	switch action {
	case ActionStart:
		_, err = conn.StartUnitContext(ctx, previous.Name, "replace", job)
	case ActionStop:
		_, err = conn.StopUnitContext(ctx, previous.Name, "replace", job)
	case ActionRestart:
		_, err = conn.RestartUnitContext(ctx, previous.Name, "replace", job)
	case ActionReload:
		_, err = conn.ReloadUnitContext(ctx, previous.Name, "replace", job)
	case ActionEnable:
		_, _, err = conn.EnableUnitFilesContext(ctx, []string{previous.Name}, false, false)
		result.SystemdResult = "done"
	case ActionDisable:
		_, err = conn.DisableUnitFilesContext(ctx, []string{previous.Name}, false)
		result.SystemdResult = "done"
	}
	if err != nil {
		return result, mutationError("ServiceActionOutcomeUnknown", "outcome_unknown", true, fmt.Errorf("systemd dispatch outcome unknown: %w", serviceBackendError(err)))
	}
	if action != ActionEnable && action != ActionDisable {
		select {
		case result.SystemdResult = <-job:
			if result.SystemdResult != "done" {
				return result, mutationError("ServiceActionFailed", "failed", true, fmt.Errorf("systemd job failed: %s", boundedText(result.SystemdResult, 80)))
			}
		case <-ctx.Done():
			return result, mutationError("ServiceActionTimedOut", "timed_out", true, fmt.Errorf("systemd job timed out with outcome unknown: %w", ctx.Err()))
		}
	}
	result.Result, _, err = serviceFromConnection(ctx, conn, previous.Name, c.UnitTrust)
	if err != nil {
		return result, mutationError("ServiceActionOutcomeUnknown", "outcome_unknown", true, fmt.Errorf("resulting service state unavailable: %w", err))
	}
	if !expectedServiceState(action, result.Result) {
		return result, mutationError("ServiceStateMismatch", "failed", true, fmt.Errorf("systemd completed but resulting state did not match the request"))
	}
	return result, nil
}

func expectedServiceState(action ServiceAction, service Service) bool {
	switch action {
	case ActionStart, ActionRestart:
		return service.Active == "active"
	case ActionStop:
		return service.Active == "inactive"
	case ActionReload:
		return service.Active == "active"
	case ActionEnable:
		return service.Enabled == "enabled"
	case ActionDisable:
		return service.Enabled == "disabled"
	}
	return false
}

func serviceFromConnection(ctx context.Context, conn UnitConnection, name string, trust func(string) bool) (Service, []string, error) {
	provider := ServiceProvider{SystemdRuntime: "/run/systemd/system"}
	_ = provider
	loaded, err := conn.ListUnitsByNamesContext(ctx, []string{name})
	if err != nil {
		return Service{}, nil, serviceBackendError(err)
	}
	files, err := conn.ListUnitFilesByPatternsContext(ctx, nil, []string{name})
	if err != nil {
		return Service{}, nil, serviceBackendError(err)
	}
	service := Service{Name: name, Loaded: "not_loaded", Active: "inactive", Sub: "dead", Enabled: "unknown"}
	found := false
	for _, file := range files {
		if strings.HasSuffix(file.Path, "/"+name) || file.Path == name {
			service.Enabled, found = boundedText(file.Type, 64), true
		}
	}
	for _, unit := range loaded {
		if unit.Name == name && unit.LoadState != "not-found" {
			service.Description, service.Loaded, service.Active, service.Sub = boundedText(unit.Description, 256), boundedText(unit.LoadState, 64), boundedText(unit.ActiveState, 64), boundedText(unit.SubState, 64)
			found = true
		}
	}
	if !found {
		return Service{}, nil, ErrMissing
	}
	props, err := conn.GetUnitPropertiesContext(ctx, name)
	if err != nil {
		return Service{}, nil, serviceBackendError(err)
	}
	aliases := stringSlice(props["Names"])
	if id, ok := props["Id"].(string); ok && ValidUnit(id) {
		service.Name = id
	}
	if trust == nil {
		trust = trustedUnitDefinition
	}
	applyUnitMetadata(&service, props, trust)
	typeProps, err := conn.GetUnitTypePropertiesContext(ctx, service.Name, "Service")
	if err != nil {
		return Service{}, nil, serviceBackendError(err)
	}
	service.MainPID = uint32Value(typeProps["MainPID"])
	service.Failure = failure(typeProps)
	if canReload, ok := typeProps["CanReload"].(bool); ok {
		service.CanReload = canReload
	}
	sort.Strings(aliases)
	return service, aliases, nil
}

func stringSlice(value any) []string {
	items, _ := value.([]string)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if ValidUnit(item) && len(result) < 32 {
			result = append(result, item)
		}
	}
	return result
}
