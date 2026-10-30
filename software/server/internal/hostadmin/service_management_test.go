package hostadmin

import (
	"context"
	"errors"
	"sync"
	"testing"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
)

type fakeMutationUnits struct {
	fakeUnits
	active, enabled string
	canReload       bool
	jobResult       string
	dispatched      ServiceAction
	dispatchError   error
	disappearAfter  bool
	resultActive    string
	requires        []string
}

type graphUnits struct {
	fakeUnits
	props map[string]map[string]any
}

func (g *graphUnits) GetUnitPropertiesContext(_ context.Context, name string) (map[string]any, error) {
	if p, ok := g.props[name]; ok {
		return p, nil
	}
	return nil, errors.New("missing metadata")
}

func (f *fakeMutationUnits) ListUnitsByNamesContext(context.Context, []string) ([]systemdbus.UnitStatus, error) {
	if f.disappearAfter && f.dispatched != "" {
		return nil, errors.New("unit disappeared")
	}
	active := f.active
	if f.dispatched != "" && f.resultActive != "" {
		active = f.resultActive
	}
	return []systemdbus.UnitStatus{{Name: "kitpro-qualification.service", Description: "Qualification fixture", LoadState: "loaded", ActiveState: active, SubState: map[bool]string{true: "running", false: "dead"}[active == "active"]}}, nil
}
func (f *fakeMutationUnits) ListUnitFilesByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitFile, error) {
	return []systemdbus.UnitFile{{Path: "/usr/lib/systemd/system/kitpro-qualification.service", Type: f.enabled}}, nil
}
func (f *fakeMutationUnits) GetUnitPropertiesContext(context.Context, string) (map[string]any, error) {
	requires := f.requires
	if len(requires) == 0 {
		requires = []string{"basic.target"}
	}
	return map[string]any{"Id": "kitpro-qualification.service", "Names": []string{"kitpro-qualification.service"}, "Requires": requires, "FragmentPath": "/etc/systemd/system/kitpro-qualification.service"}, nil
}
func (f *fakeMutationUnits) GetUnitTypePropertiesContext(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"MainPID": uint32(321), "Result": "success", "CanReload": f.canReload}, nil
}
func (f *fakeMutationUnits) dispatch(action ServiceAction, ch chan<- string) (int, error) {
	f.dispatched = action
	if f.dispatchError != nil {
		return 0, f.dispatchError
	}
	switch action {
	case ActionStart, ActionRestart:
		f.active = "active"
	case ActionStop:
		f.active = "inactive"
	}
	if f.jobResult != "blocked" {
		result := f.jobResult
		if result == "" {
			result = "done"
		}
		ch <- result
	}
	return 1, nil
}
func (f *fakeMutationUnits) StartUnitContext(_ context.Context, _ string, _ string, ch chan<- string) (int, error) {
	return f.dispatch(ActionStart, ch)
}
func (f *fakeMutationUnits) StopUnitContext(_ context.Context, _ string, _ string, ch chan<- string) (int, error) {
	return f.dispatch(ActionStop, ch)
}
func (f *fakeMutationUnits) RestartUnitContext(_ context.Context, _ string, _ string, ch chan<- string) (int, error) {
	return f.dispatch(ActionRestart, ch)
}
func (f *fakeMutationUnits) ReloadUnitContext(_ context.Context, _ string, _ string, ch chan<- string) (int, error) {
	return f.dispatch(ActionReload, ch)
}
func (f *fakeMutationUnits) EnableUnitFilesContext(context.Context, []string, bool, bool) (bool, []systemdbus.EnableUnitFileChange, error) {
	f.dispatched, f.enabled = ActionEnable, "enabled"
	return true, nil, nil
}
func (f *fakeMutationUnits) DisableUnitFilesContext(context.Context, []string, bool) ([]systemdbus.DisableUnitFileChange, error) {
	f.dispatched, f.enabled = ActionDisable, "disabled"
	return nil, nil
}

func TestServicePolicyFailsClosedAndProtectsAliases(t *testing.T) {
	service := Service{Name: "example.service", Active: "active", Enabled: "enabled", CanReload: true, UnitFile: "/etc/systemd/system/example.service", UnitTrusted: true, MetadataComplete: true}
	policy := NewServicePolicy(true, "example.service")
	if item, _ := FindEligibility(policy.Evaluate(service, []string{"sshd.service"}), ActionRestart); item.Allowed || item.State != "protected" {
		t.Fatalf("protected alias eligibility = %#v", item)
	}
	if item, _ := FindEligibility(policy.Evaluate(Service{Name: "other.service"}, nil), ActionStart); item.Allowed || item.State != "requires_maintenance_workflow" {
		t.Fatalf("unknown service eligibility = %#v", item)
	}
	if item, _ := FindEligibility(NewServicePolicy(false, "example.service").Evaluate(service, nil), ActionStart); item.Allowed || item.State != "not_configured" {
		t.Fatalf("disabled gate eligibility = %#v", item)
	}
}

func TestServicePolicyIsActionSpecific(t *testing.T) {
	policy := NewServicePolicy(true, "kitpro-qualification.service")
	items := policy.Evaluate(Service{Name: "kitpro-qualification.service", Active: "inactive", Enabled: "disabled", UnitFile: "/etc/systemd/system/kitpro-qualification.service", UnitTrusted: true, MetadataComplete: true}, nil)
	start, _ := FindEligibility(items, ActionStart)
	stop, _ := FindEligibility(items, ActionStop)
	reload, _ := FindEligibility(items, ActionReload)
	enable, _ := FindEligibility(items, ActionEnable)
	if !start.Allowed || stop.Allowed || reload.Allowed || !enable.Allowed {
		t.Fatalf("unexpected action policy: %#v", items)
	}
}

func TestCuratedCatalogUsesExactActionScopeAndTrustedDefinitions(t *testing.T) {
	policy := NewServicePolicy(true, "example.service=start|reload;other.service=stop")
	service := Service{Name: "example.service", Active: "inactive", Enabled: "disabled", UnitFile: "/etc/systemd/system/example.service", UnitTrusted: true, MetadataComplete: true}
	items := policy.Evaluate(service, nil)
	start, _ := FindEligibility(items, ActionStart)
	stop, _ := FindEligibility(items, ActionStop)
	if !start.Allowed || stop.Allowed {
		t.Fatalf("unexpected scoped policy: %#v", items)
	}
	untrusted := service
	untrusted.UnitTrusted = false
	if item, _ := FindEligibility(policy.Evaluate(untrusted, nil), ActionStart); item.Allowed || item.State != "untrusted_unit_definition" {
		t.Fatalf("untrusted definition accepted: %#v", item)
	}
	unknown := service
	unknown.Name = "unknown.service"
	if item, _ := FindEligibility(policy.Evaluate(unknown, nil), ActionStart); item.Allowed || item.State != "requires_maintenance_workflow" {
		t.Fatalf("unknown unit accepted: %#v", item)
	}
}

func TestCatalogDeniesProtectedAndUnreviewedConsequences(t *testing.T) {
	policy := NewServicePolicy(true, "example.service=start|stop")
	base := Service{Name: "example.service", Active: "inactive", Enabled: "disabled", UnitFile: "/etc/systemd/system/example.service", UnitTrusted: true, MetadataComplete: true}
	base.Relationships.Requires = []string{"docker.service"}
	if item, _ := FindEligibility(policy.Evaluate(base, nil), ActionStart); item.Allowed || item.State != "protected_dependency" {
		t.Fatalf("protected dependency accepted: %#v", item)
	}
	base.Relationships.Requires = []string{"unreviewed.service"}
	if item, _ := FindEligibility(policy.Evaluate(base, nil), ActionStart); item.Allowed || len(item.Concerns) != 1 {
		t.Fatalf("unreviewed dependency accepted: %#v", item)
	}
}

func TestCatalogDeniesTemplateDefinitionAndProtectedAlias(t *testing.T) {
	policy := NewServicePolicy(true, "worker@.service=start;alias.service=start")
	template := Service{Name: "worker@.service", UnitFile: "/etc/systemd/system/worker@.service", UnitTrusted: true, MetadataComplete: true}
	if item, _ := FindEligibility(policy.Evaluate(template, nil), ActionStart); item.State != "template_requires_instance" {
		t.Fatalf("template policy: %#v", item)
	}
	alias := Service{Name: "alias.service", UnitFile: "/etc/systemd/system/alias.service", UnitTrusted: true, MetadataComplete: true}
	if item, _ := FindEligibility(policy.Evaluate(alias, []string{"sshd.service"}), ActionStart); item.State != "protected" {
		t.Fatalf("alias policy: %#v", item)
	}
}

func TestDependencyGraphIsCycleSafeAndFindsTransitiveProtection(t *testing.T) {
	conn := &graphUnits{props: map[string]map[string]any{
		"child.service":   {"Requires": []string{"example.service", "docker.service"}},
		"example.service": {"Requires": []string{"child.service"}},
	}}
	policy := NewServicePolicy(true, "example.service=start;child.service=start")
	service := Service{Name: "example.service", Relationships: ServiceRelationships{Requires: []string{"child.service"}}}
	concerns := graphConcerns(context.Background(), conn, policy, service, ActionStart)
	if len(concerns) != 1 || concerns[0] != "docker.service" {
		t.Fatalf("graph concerns = %#v", concerns)
	}
}

func TestServiceControllerTracksJobAndResultingState(t *testing.T) {
	fake := &fakeMutationUnits{active: "inactive", enabled: "disabled"}
	controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return fake, nil }}
	result, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionStart)
	if err != nil || fake.dispatched != ActionStart || result.Previous.Active != "inactive" || result.Result.Active != "active" || result.SystemdResult != "done" {
		t.Fatalf("result=%#v dispatched=%q err=%v", result, fake.dispatched, err)
	}
}

func TestServiceControllerRejectsFailedJobAndProtectedUnitBeforeDispatch(t *testing.T) {
	fake := &fakeMutationUnits{active: "active", enabled: "enabled", canReload: true, jobResult: "failed"}
	controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return fake, nil }}
	if _, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionRestart); err == nil {
		t.Fatal("failed systemd job accepted")
	}
	protected := NewServicePolicy(true, "docker.service")
	items := protected.Evaluate(Service{Name: "docker.service", Active: "active", Enabled: "enabled"}, nil)
	if action, _ := FindEligibility(items, ActionStop); action.Allowed || action.State != "protected" {
		t.Fatalf("docker policy = %#v", action)
	}
	if !errors.Is(serviceBackendError(errors.New("org.freedesktop.DBus.Error.AccessDenied")), ErrPermission) {
		t.Fatal("permission error classification changed")
	}
}

func TestServiceControllerFailsBeforeDispatchWhenDBusIsUnavailable(t *testing.T) {
	controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return nil, errors.New("dbus unavailable") }}
	_, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionStart)
	var mutationErr *ServiceMutationError
	if !errors.As(err, &mutationErr) || mutationErr.Code != "ServiceBackendUnavailable" || mutationErr.Status != "failed" || mutationErr.Dispatched {
		t.Fatalf("err=%#v", err)
	}
}

func TestServiceControllerRevalidatesDependenciesImmediatelyBeforeDispatch(t *testing.T) {
	policy := NewServicePolicy(true, "")
	preview := Service{Name: "kitpro-qualification.service", Active: "inactive", Enabled: "disabled", UnitFile: "/etc/systemd/system/kitpro-qualification.service", UnitTrusted: true, MetadataComplete: true}
	if eligibility, _ := FindEligibility(policy.Evaluate(preview, nil), ActionStart); !eligibility.Allowed {
		t.Fatalf("preview unexpectedly denied: %#v", eligibility)
	}
	fake := &fakeMutationUnits{active: "inactive", enabled: "disabled", requires: []string{"docker.service"}}
	controller := &ServiceController{Policy: policy, UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return fake, nil }}
	_, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionStart)
	var mutationErr *ServiceMutationError
	if !errors.As(err, &mutationErr) || mutationErr.Code != "ServiceActionDenied" || mutationErr.Dispatched || fake.dispatched != "" {
		t.Fatalf("changed dependency was not denied before dispatch: err=%#v dispatched=%q", err, fake.dispatched)
	}
}

func TestServiceControllerClassifiesUncertainOutcomes(t *testing.T) {
	tests := []struct {
		name string
		fake *fakeMutationUnits
		ctx  func() (context.Context, context.CancelFunc)
		code string
	}{
		{name: "dispatch disconnect", fake: &fakeMutationUnits{active: "inactive", enabled: "disabled", dispatchError: errors.New("dbus disconnected")}, ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, code: "ServiceActionOutcomeUnknown"},
		{name: "completion signal lost", fake: &fakeMutationUnits{active: "inactive", enabled: "disabled", jobResult: "blocked"}, ctx: func() (context.Context, context.CancelFunc) { return context.WithTimeout(context.Background(), 1) }, code: "ServiceActionTimedOut"},
		{name: "unit disappears", fake: &fakeMutationUnits{active: "inactive", enabled: "disabled", disappearAfter: true}, ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, code: "ServiceActionOutcomeUnknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return test.fake, nil }}
			ctx, cancel := test.ctx()
			defer cancel()
			_, err := controller.Mutate(ctx, "kitpro-qualification.service", ActionStart)
			var mutationErr *ServiceMutationError
			if !errors.As(err, &mutationErr) || mutationErr.Code != test.code || !mutationErr.Dispatched {
				t.Fatalf("err=%#v", err)
			}
		})
	}
}

func TestServiceControllerRejectsConcurrentOperation(t *testing.T) {
	fake := &fakeMutationUnits{active: "inactive", enabled: "disabled"}
	controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return fake, nil }}
	lock := &sync.Mutex{}
	lock.Lock()
	controller.locks.Store("kitpro-qualification.service", lock)
	_, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionStart)
	var mutationErr *ServiceMutationError
	if !errors.As(err, &mutationErr) || mutationErr.Code != "OperationConflict" || mutationErr.Dispatched {
		t.Fatalf("concurrent err=%#v", err)
	}
	lock.Unlock()
}

func TestServiceControllerDetectsResultingStateMismatch(t *testing.T) {
	fake := &fakeMutationUnits{active: "inactive", enabled: "disabled", resultActive: "inactive"}
	controller := &ServiceController{Policy: NewServicePolicy(true, ""), UnitTrust: func(string) bool { return true }, Open: func(context.Context) (MutationConnection, error) { return fake, nil }}
	_, err := controller.Mutate(context.Background(), "kitpro-qualification.service", ActionStart)
	var mutationErr *ServiceMutationError
	if !errors.As(err, &mutationErr) || mutationErr.Code != "ServiceStateMismatch" || mutationErr.Status != "failed" || !mutationErr.Dispatched {
		t.Fatalf("mismatch err=%#v", err)
	}
}
