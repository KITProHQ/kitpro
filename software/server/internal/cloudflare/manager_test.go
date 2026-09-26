package cloudflare

import (
	"context"
	"errors"
	"testing"
)

type fakeAPI struct {
	validateErr error
	deleteCalls int
	routes      map[string]string
}

func (f *fakeAPI) ValidateCredential(context.Context, string, string, string, string) (Tunnel, error) {
	if f.validateErr != nil {
		return Tunnel{}, f.validateErr
	}
	return Tunnel{ID: testTunnel, AccountID: testAccount, ConfigSrc: "cloudflare", Status: "healthy"}, nil
}
func (f *fakeAPI) TunnelToken(context.Context, string, string, string) (string, error) {
	return "runtime-tunnel-token-0123456789", nil
}
func (f *fakeAPI) AddRoute(_ context.Context, _ string, _, _, _ string, hostname string) (DNSRecord, error) {
	if f.routes == nil {
		f.routes = map[string]string{}
	}
	if _, exists := f.routes[hostname]; exists {
		return DNSRecord{}, &Error{Code: "route_conflict"}
	}
	f.routes[hostname] = testRecord
	return DNSRecord{ID: testRecord, Name: hostname, Type: "CNAME", Content: testTunnel + ".cfargotunnel.com"}, nil
}
func (f *fakeAPI) RemoveRoute(_ context.Context, _ string, _, _, _, hostname, recordID string) error {
	if f.routes[hostname] != recordID {
		return &Error{Code: "route_ownership_conflict"}
	}
	delete(f.routes, hostname)
	return nil
}
func (f *fakeAPI) DeleteTunnel(context.Context, string, string, string) error {
	f.deleteCalls++
	return nil
}

type fakeRuntime struct {
	started  bool
	failNext bool
}

func (f *fakeRuntime) Start(string) error {
	if f.failNext {
		f.failNext = false
		return errors.New("runtime offline")
	}
	f.started = true
	return nil
}
func (f *fakeRuntime) Stop() error { f.started = false; return nil }
func (f *fakeRuntime) Status(context.Context) RuntimeStatus {
	if f.started {
		return RuntimeStatus{State: "ready", Code: "connected"}
	}
	return RuntimeStatus{State: "offline", Code: "runtime_stopped"}
}
func (f *fakeRuntime) Restart(providerID string) error {
	f.started = false
	return f.Start(providerID)
}

func configuredManager(t *testing.T) (Manager, ProviderConfig, *fakeAPI, *fakeRuntime) {
	t.Helper()
	api := &fakeAPI{}
	runtime := &fakeRuntime{}
	manager := Manager{Store: CredentialStore{Root: t.TempDir()}, API: api, Runtime: runtime}
	config, err := manager.Configure(context.Background(), testProviderConfig(), "api-token-01234567890123456789")
	if err != nil {
		t.Fatal(err)
	}
	return manager, config, api, runtime
}

func TestManagerLifecycleAndRouteOperationsAreIndependent(t *testing.T) {
	manager, config, api, runtime := configuredManager(t)
	if config.Enabled || runtime.started {
		t.Fatal("provider enabled during configuration")
	}
	config, err := manager.Enable(config.ProviderID)
	if err != nil || !config.Enabled || !runtime.started {
		t.Fatalf("enable config=%+v err=%v", config, err)
	}
	config, err = manager.AddRoute(context.Background(), config.ProviderID, "app.example.test")
	if err != nil || config.Routes["app.example.test"] != testRecord {
		t.Fatalf("route config=%+v err=%v", config, err)
	}
	config, err = manager.Disable(config.ProviderID)
	if err != nil || config.Enabled || runtime.started || len(api.routes) != 1 {
		t.Fatal("local disable removed remote state or left runtime active")
	}
	config, err = manager.RemoveRemoteRoute(context.Background(), config.ProviderID, "app.example.test")
	if err != nil || len(config.Routes) != 0 || len(api.routes) != 0 {
		t.Fatalf("remove config=%+v err=%v", config, err)
	}
	if err = manager.DeleteRemoteTunnel(context.Background(), config.ProviderID); err != nil || api.deleteCalls != 1 {
		t.Fatalf("delete calls=%d err=%v", api.deleteCalls, err)
	}
}

func TestInvalidReplacementAndRuntimeFailureRetainWorkingCredential(t *testing.T) {
	manager, config, api, runtime := configuredManager(t)
	if _, err := manager.Enable(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	_, originalToken, _, err := manager.Store.Load(config.ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	api.validateErr = &Error{Code: "invalid_token"}
	if _, err = manager.Configure(context.Background(), config, "replacement-token-01234567890123"); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	_, retainedToken, _, _ := manager.Store.Load(config.ProviderID)
	if retainedToken != originalToken || !runtime.started {
		t.Fatal("invalid replacement disturbed working provider")
	}
	api.validateErr = nil
	runtime.failNext = true
	if _, err = manager.Configure(context.Background(), config, "valid-replacement-token-0123456789"); err == nil {
		t.Fatal("runtime failure accepted replacement")
	}
	_, retainedToken, _, _ = manager.Store.Load(config.ProviderID)
	if retainedToken != originalToken || !runtime.started {
		t.Fatal("failed runtime restart did not restore old credential and runtime")
	}
}

func TestUnlinkPreservesRemoteResources(t *testing.T) {
	manager, config, api, _ := configuredManager(t)
	if _, err := manager.Enable(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddRoute(context.Background(), config.ProviderID, "app.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unlink(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	if len(api.routes) != 1 || api.deleteCalls != 0 {
		t.Fatal("local unlink deleted remote resources")
	}
	if _, _, _, err := manager.Store.Load(config.ProviderID); err == nil {
		t.Fatal("local credentials survived unlink")
	}
}
