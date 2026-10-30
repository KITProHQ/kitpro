package hostadmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	systemdbus "github.com/coreos/go-systemd/v22/dbus"
)

type fakeUnits struct {
	files  []systemdbus.UnitFile
	loaded []systemdbus.UnitStatus
	fail   bool
}

func (f *fakeUnits) Close() {}
func (f *fakeUnits) ListUnitsByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitStatus, error) {
	if f.fail {
		return nil, errors.New("private bus failure")
	}
	return f.loaded, nil
}
func (f *fakeUnits) ListUnitFilesByPatternsContext(context.Context, []string, []string) ([]systemdbus.UnitFile, error) {
	if f.fail {
		return nil, errors.New("private bus failure")
	}
	return f.files, nil
}
func (f *fakeUnits) ListUnitsByNamesContext(_ context.Context, _ []string) ([]systemdbus.UnitStatus, error) {
	return f.loaded, nil
}
func (f *fakeUnits) GetUnitPropertiesContext(context.Context, string) (map[string]any, error) {
	return map[string]any{"Requires": []string{"network.target"}, "FragmentPath": "/usr/lib/systemd/system/alpha.service"}, nil
}
func (f *fakeUnits) GetUnitTypePropertiesContext(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"MainPID": uint32(123), "Result": "exit-code"}, nil
}
func serviceFixture(t *testing.T, f *fakeUnits) ServiceProvider {
	t.Helper()
	path := filepath.Join(t.TempDir(), "systemd")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	return ServiceProvider{SystemdRuntime: path, Open: func(context.Context) (UnitConnection, error) { return f, nil }, UnitTrust: func(string) bool { return true }}
}
func TestServiceListFiltersAndPages(t *testing.T) {
	f := &fakeUnits{files: []systemdbus.UnitFile{{Path: "/usr/lib/systemd/system/alpha.service", Type: "enabled"}, {Path: "/usr/lib/systemd/system/beta.service", Type: "disabled"}}, loaded: []systemdbus.UnitStatus{{Name: "alpha.service", Description: "Alpha server", LoadState: "loaded", ActiveState: "failed", SubState: "failed"}}}
	p := serviceFixture(t, f)
	page, e := p.List(context.Background(), ServiceQuery{State: "all", Limit: 1})
	if e != nil || page.Total != 2 || len(page.Items) != 1 || page.NextOffset == nil || *page.NextOffset != 1 || page.Items[0].MainPID != 123 || page.Items[0].Failure != "exit-code" {
		t.Fatalf("page: %#v %v", page, e)
	}
	next, e := p.List(context.Background(), ServiceQuery{State: "inactive", Limit: 10, Search: "beta"})
	if e != nil || next.Total != 1 || next.Items[0].Name != "beta.service" || next.Items[0].Enabled != "disabled" {
		t.Fatalf("filter: %#v %v", next, e)
	}
	failed, e := p.List(context.Background(), ServiceQuery{State: "failed", Limit: 10})
	if e != nil || failed.Total != 1 || failed.Items[0].Name != "alpha.service" {
		t.Fatalf("failed: %#v %v", failed, e)
	}
}
func TestServiceDetailAndMissing(t *testing.T) {
	f := &fakeUnits{files: []systemdbus.UnitFile{{Path: "/usr/lib/systemd/system/alpha.service", Type: "enabled"}}, loaded: []systemdbus.UnitStatus{{Name: "alpha.service", LoadState: "loaded", ActiveState: "failed"}}}
	p := serviceFixture(t, f)
	v, e := p.Detail(context.Background(), "alpha.service")
	if e != nil || v.MainPID != 123 || len(v.Dependencies) != 1 {
		t.Fatalf("detail: %#v %v", v, e)
	}
	if _, e = p.Detail(context.Background(), "missing.service"); !errors.Is(e, ErrMissing) {
		t.Fatalf("missing: %v", e)
	}
	if _, e = p.Detail(context.Background(), "../etc/passwd"); !errors.Is(e, ErrInvalid) {
		t.Fatalf("invalid: %v", e)
	}
}
func TestServiceUnavailableAndUnsupported(t *testing.T) {
	if _, e := (ServiceProvider{}).List(context.Background(), ServiceQuery{State: "all", Limit: 1}); !errors.Is(e, ErrUnsupported) {
		t.Fatalf("unsupported: %v", e)
	}
	p := serviceFixture(t, &fakeUnits{fail: true})
	if _, e := p.List(context.Background(), ServiceQuery{State: "all", Limit: 1}); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("unavailable: %v", e)
	}
}

func TestServicePermissionClassification(t *testing.T) {
	if !errors.Is(serviceBackendError(errors.New("org.freedesktop.DBus.Error.AccessDenied")), ErrPermission) {
		t.Fatal("D-Bus denial not classified")
	}
}
