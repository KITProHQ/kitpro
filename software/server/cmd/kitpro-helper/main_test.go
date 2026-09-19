package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/hardware"
	"github.com/kitpro/kitpro/software/server/internal/lifecycle"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/multicontainer"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/platform"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type startupLifecycleRuntime struct {
	networks   map[string]containers.NetworkObservation
	containers map[string]containers.ContainerObservation
}

func (r *startupLifecycleRuntime) PullImage(context.Context, string) error {
	return errors.New("not used")
}
func (r *startupLifecycleRuntime) ObserveImage(context.Context, string) (containers.ImageObservation, error) {
	return containers.ImageObservation{}, errors.New("not used")
}
func (r *startupLifecycleRuntime) CreateLifecycleNetwork(context.Context, string, map[string]string) (containers.NetworkObservation, error) {
	return containers.NetworkObservation{}, errors.New("not used")
}
func (r *startupLifecycleRuntime) ObserveNetwork(_ context.Context, name string) (containers.NetworkObservation, error) {
	return r.networks[name], nil
}
func (r *startupLifecycleRuntime) CreateLifecycleContainer(context.Context, containers.ContainerPlan) (string, error) {
	return "", errors.New("not used")
}
func (r *startupLifecycleRuntime) ObserveContainer(_ context.Context, id string) (containers.ContainerObservation, error) {
	if observed, ok := r.containers[id]; ok {
		return observed, nil
	}
	return containers.ContainerObservation{State: containers.RuntimeMissing}, nil
}
func (r *startupLifecycleRuntime) StartContainer(context.Context, string) error {
	return errors.New("not used")
}
func (r *startupLifecycleRuntime) StopContainer(context.Context, string) error {
	return errors.New("not used")
}
func (r *startupLifecycleRuntime) RemoveContainer(context.Context, string) error {
	return errors.New("not used")
}
func (r *startupLifecycleRuntime) RemoveLifecycleNetwork(context.Context, string) error {
	return errors.New("not used")
}
func (r *startupLifecycleRuntime) WaitContainer(context.Context, string, containers.RuntimeState, time.Duration) (containers.ContainerObservation, error) {
	return containers.ContainerObservation{}, errors.New("not used")
}

func TestRuntimeSocketNamesAreRejected(t *testing.T) {
	for _, value := range []string{"/run/docker.sock", "/run/podman/podman.sock", "/run/containerd/containerd.sock"} {
		if !containsRuntimeSocket(value) {
			t.Fatalf("runtime socket was not recognized: %s", value)
		}
	}
	if containsRuntimeSocket("/srv/kitpro/apps/podman.socket/data") {
		t.Fatal("ordinary path was treated as a runtime socket")
	}
}

func TestSelectRuntimeUsesExplicitOrInstallablePlatform(t *testing.T) {
	rocky := platform.Platform{ID: "rocky", Version: "10.1", ContainerRuntime: "podman", Installable: true}
	if got, err := selectRuntime("", rocky); err != nil || got != "podman" {
		t.Fatalf("Rocky runtime = %q, %v", got, err)
	}
	if got, err := selectRuntime("docker", rocky); err != nil || got != "docker" {
		t.Fatalf("explicit runtime = %q, %v", got, err)
	}
	if _, err := selectRuntime("containerd", rocky); err == nil {
		t.Fatal("unknown explicit runtime accepted")
	}
	rhel := platform.Platform{ID: "rhel", Version: "10.0", ContainerRuntime: "podman", Installable: false}
	if _, err := selectRuntime("", rhel); err == nil {
		t.Fatal("unvalidated RHEL host silently accepted")
	}
}

func TestExpectedAPIUIDFromConfiguredNumericValue(t *testing.T) {
	t.Setenv("KITPRO_API_UID", "1234")
	uid, err := expectedAPIUID()
	if err != nil {
		t.Fatal(err)
	}
	if uid != 1234 {
		t.Fatalf("uid = %d", uid)
	}
}

func trustedCatalogRequest(t *testing.T, appID string) protocol.Request {
	t.Helper()
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := entries[appID]
	if !ok {
		t.Fatalf("catalog application %q not found", appID)
	}
	instance := "inst-" + strings.ReplaceAll(appID, "-", "") + "01"
	dataPath := "/srv/kitpro/apps/" + appID + "/" + instance + "/data"
	plan, err := manifest.Resolve(entry.Manifest, entry.Manifest.Releases[0].Version, instance, "kitpro-net-"+instance+"-g1", dataPath)
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{
		Version: 1, ID: "op-1234567890abcdef", Operation: "InstallApplication",
		ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID, InstanceID: plan.InstanceID,
		RuntimeGeneration: 1, Image: plan.ImageDigest, NetworkName: plan.NetworkName,
		DataPath: plan.DataPath, RestartPolicy: plan.Restart, ExposureMode: "internal",
	}
	for _, variable := range plan.Environment {
		request.Environment = append(request.Environment, protocol.EnvVar{Name: variable.Name, Value: variable.Value, Secret: variable.Secret, Generate: variable.Generate})
	}
	for _, storage := range plan.Storage {
		request.Storage = append(request.Storage, protocol.StorageMount{ID: storage.ID, ContainerPath: storage.ContainerPath, HostPath: "/srv/kitpro/apps/" + appID + "/" + instance + "/" + storage.ID, ReadOnly: storage.ReadOnly})
	}
	for _, service := range plan.Services {
		request.Services = append(request.Services, protocol.Service{ID: service.ID, Protocol: service.Protocol, ContainerPort: service.ContainerPort})
	}
	return request
}

func TestApplicationPlanValidationAcceptsEveryRealCatalogApp(t *testing.T) {
	for _, appID := range []string{"freshrss", "mealie", "memos", "uptime-kuma"} {
		t.Run(appID, func(t *testing.T) {
			if err := validateApplicationPlan(trustedCatalogRequest(t, appID)); err != nil {
				t.Fatalf("trusted catalog plan rejected: %v", err)
			}
		})
	}
}

func TestExpectedAPIUIDRejectsInvalidNumericValue(t *testing.T) {
	t.Setenv("KITPRO_API_UID", "not-a-uid")
	if _, err := expectedAPIUID(); err == nil {
		t.Fatal("invalid UID accepted")
	}
}

func openAlpha11HelperFixture(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	script, err := os.ReadFile(filepath.Join("..", "..", "internal", "state", "testdata", "alpha11_helper_schema7.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAlpha11LegacyMultiBackfillsAndReconcilesAtStartup(t *testing.T) {
	db := openAlpha11HelperFixture(t, "valid.db")
	if count, err := backfillLegacyMultiAtStartup(context.Background(), db); err != nil || count != 1 {
		t.Fatalf("backfill count=%d err=%v", count, err)
	}
	var componentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runtime_components WHERE installation_id='inst-paperless1' AND runtime_generation=1`).Scan(&componentCount); err != nil || componentCount != 2 {
		t.Fatalf("component count=%d err=%v", componentCount, err)
	}
	networkName := "kitpro-net-inst-paperless1-g1"
	networkID := "network-paperless"
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-paperless1", "com.kitpro.runtime-generation": "1"}
	runtime := &startupLifecycleRuntime{
		networks: map[string]containers.NetworkObservation{networkName: {Exists: true, ID: networkID, Name: networkName}},
		containers: map[string]containers.ContainerObservation{
			"paperless-broker": {Exists: true, ID: "paperless-broker", Name: "kitpro-paperless-ngx-inst-paperless1-broker-g1", ImageReference: "docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7", State: containers.RuntimeRunning, Labels: labels, Networks: map[string]containers.NetworkAttachment{networkName: {NetworkID: networkID}}},
			"paperless-web":    {Exists: true, ID: "paperless-web", Name: "kitpro-paperless-ngx-inst-paperless1-web-g1", ImageReference: "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", State: containers.RuntimeRunning, Labels: labels, Networks: map[string]containers.NetworkAttachment{networkName: {NetworkID: networkID}}},
		},
	}
	if count, err := lifecycle.ReconcileAll(context.Background(), lifecycle.Store{DB: db}, runtime); err != nil || count != 2 {
		// The exact alpha.11 fixture also contains one migrated single install.
		t.Fatalf("reconciled=%d err=%v", count, err)
	}
	result, err := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), "inst-paperless1")
	if err != nil || result.State != lifecycle.ReconciliationConsistent || result.RuntimeState != "running" {
		t.Fatalf("reconciliation=%#v err=%v", result, err)
	}
	single, err := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), "inst-busybox01")
	if err != nil || single.CheckedGeneration != 1 || single.State != lifecycle.ReconciliationRuntimeMissing {
		t.Fatalf("migrated single reconciliation=%#v err=%v", single, err)
	}
}

func TestAlpha11AmbiguousLegacyMultiBecomesActionRequired(t *testing.T) {
	db := openAlpha11HelperFixture(t, "ambiguous.db")
	if _, err := db.Exec(`UPDATE component_ownership SET image_digest='docker.io/example/mismatch@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE installation_id='inst-paperless1' AND component_id='web'`); err != nil {
		t.Fatal(err)
	}
	if count, err := backfillLegacyMultiAtStartup(context.Background(), db); err != nil || count != 1 {
		t.Fatalf("backfill count=%d err=%v", count, err)
	}
	var generations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id='inst-paperless1'`).Scan(&generations); err != nil || generations != 0 {
		t.Fatalf("ambiguous topology was guessed: generations=%d err=%v", generations, err)
	}
	result, err := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), "inst-paperless1")
	if err != nil || result.State != lifecycle.ReconciliationActionRequired || len(result.MismatchCodes) != 1 || result.MismatchCodes[0] != lifecycle.MismatchOwnershipAmbiguous {
		t.Fatalf("reconciliation=%#v err=%v", result, err)
	}
}

func validFreshRSSRequest() protocol.Request {
	return protocol.Request{
		Version: 1, ID: "op-1234567890abcdef", Operation: "InstallApplication",
		ApplicationID: "freshrss", ReleaseID: "1.29.1", InstanceID: "inst-12345678", RuntimeGeneration: 1,
		Image:       "docker.io/freshrss/freshrss@sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21",
		NetworkName: "kitpro-net-inst-12345678-g1", DataPath: "/srv/kitpro/apps/freshrss/inst-12345678/data", RestartPolicy: "unless-stopped",
		Environment:  []protocol.EnvVar{{Name: "TZ", Value: "UTC"}},
		Storage:      []protocol.StorageMount{{ID: "data", ContainerPath: "/var/www/FreshRSS/data", HostPath: "/srv/kitpro/apps/freshrss/inst-12345678/data"}, {ID: "extensions", ContainerPath: "/var/www/FreshRSS/extensions", HostPath: "/srv/kitpro/apps/freshrss/inst-12345678/extensions"}},
		Services:     []protocol.Service{{ID: "web", Protocol: "http", ContainerPort: 80}},
		ExposureMode: "internal",
	}
}

func TestApplicationPlanValidationAcceptsTrustedCatalogPlan(t *testing.T) {
	if err := validateApplicationPlan(validFreshRSSRequest()); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	q := validFreshRSSRequest()
	q.Operation, q.RuntimeGeneration = "ConfigureServiceExposure", 2
	q.ExposureMode, q.ServiceID, q.ServiceProtocol, q.ContainerPort = "loopback", "web", "http", 80
	q.HostAddress, q.HostPort = "127.0.0.1", 20000
	q.NetworkName = "kitpro-net-inst-12345678-g2"
	if err := validateApplicationPlan(q); err != nil {
		t.Fatalf("valid loopback plan rejected: %v", err)
	}
}

func TestApplicationPlanValidationRejectsTampering(t *testing.T) {
	t.Setenv("KITPRO_LAN_BIND_ADDRESS", "10.10.0.115")
	for name, mutate := range map[string]func(*protocol.Request){
		"host-path": func(r *protocol.Request) { r.Storage[0].HostPath = "/srv/kitpro/apps/freshrss/inst-other/data" },
		"image":     func(r *protocol.Request) { r.Image = "docker.io/freshrss/freshrss:latest" },
		"release":   func(r *protocol.Request) { r.ReleaseID = "other" },
		"restart":   func(r *protocol.Request) { r.RestartPolicy = "always" },
		"command":   func(r *protocol.Request) { r.Command = []string{"/bin/sh", "-c", "id"} },
		"secret-env": func(r *protocol.Request) {
			r.Environment = []protocol.EnvVar{{Name: "TOKEN", Secret: true}}
		},
		"service": func(r *protocol.Request) { r.Services[0].ID = "admin" },
		"container-port": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "loopback", "web", "http", 81, "127.0.0.1", 20000
		},
		"protocol": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "loopback", "web", "udp", 80, "127.0.0.1", 20000
		},
		"wildcard-v4": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "loopback", "web", "http", 80, "0.0.0.0", 20000
		},
		"wildcard-v6": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "lan", "web", "http", 80, "::", 20000
		},
		"wrong-lan": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "lan", "web", "http", 80, "10.10.0.116", 20000
		},
		"port-range": func(r *protocol.Request) {
			r.ExposureMode, r.ServiceID, r.ServiceProtocol, r.ContainerPort, r.HostAddress, r.HostPort = "loopback", "web", "http", 80, "127.0.0.1", 19999
		},
	} {
		q := validFreshRSSRequest()
		mutate(&q)
		if err := validateApplicationPlan(q); err == nil {
			t.Errorf("accepted %s tampering", name)
		}
	}
}

func TestMultiComponentPlanValidationRejectsTamperedComponent(t *testing.T) {
	m := manifest.Manifest{SchemaVersion: manifest.MultiContainerSchemaVersion, ID: "paperless", Name: "Paperless", Releases: []manifest.Release{{Version: "2.20.15", Registry: "ghcr.io", Repository: "paperless-ngx/paperless-ngx", Digest: "sha256:835974fc3368fc6714aa38542db7a1f0f542d03244e39b981e519aefc100f355", Platform: "linux/amd64"}}, Components: []manifest.Component{{ID: "web", Release: "2.20.15"}, {ID: "worker", Release: "2.20.15", DependsOn: []string{"web"}}}}
	q := protocol.Request{ApplicationID: "paperless", InstanceID: "inst-12345678", RuntimeGeneration: 1, NetworkName: "kitpro-net-inst-12345678-g1", Components: []protocol.Component{{ID: "web", Image: "ghcr.io/paperless-ngx/paperless-ngx@sha256:835974fc3368fc6714aa38542db7a1f0f542d03244e39b981e519aefc100f355"}, {ID: "worker", Image: "ghcr.io/paperless-ngx/paperless-ngx@sha256:0000000000000000000000000000000000000000000000000000000000000000", DependsOn: []string{"web"}}}}
	if err := validateMultiComponentPlan(q, m); err == nil {
		t.Fatal("tampered component accepted")
	}
}

func TestMultiComponentPlanValidationAcceptsTrustedComponents(t *testing.T) {
	m := manifest.Manifest{SchemaVersion: manifest.MultiContainerSchemaVersion, ID: "paperless-ngx", Name: "Paperless", Releases: []manifest.Release{{Version: "2.20.15", Registry: "docker.io", Repository: "paperlessngx/paperless-ngx", Digest: "sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", Platform: "linux/amd64"}, {Version: "redis-7.4.11", Registry: "docker.io", Repository: "library/redis", Digest: "sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7", Platform: "linux/amd64"}}, Services: []manifest.Service{{ID: "web", Name: "Web", Protocol: "http", ContainerPort: 8000}}, Components: []manifest.Component{{ID: "broker", Release: "redis-7.4.11"}, {ID: "web", Release: "2.20.15", DependsOn: []string{"broker"}}}}
	q := protocol.Request{ApplicationID: "paperless-ngx", ReleaseID: "2.20.15", Image: "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", InstanceID: "inst-12345678", RuntimeGeneration: 1, NetworkName: "kitpro-net-inst-12345678-g1", ExposureMode: "internal", Services: []protocol.Service{{ID: "web", Protocol: "http", ContainerPort: 8000}}, Components: []protocol.Component{{ID: "broker", Image: "docker.io/library/redis@sha256:71da9275c5f3fcb97d0fa0c8c5b36cc995327265420f17a04bfd544f458059f7"}, {ID: "web", Image: "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", DependsOn: []string{"broker"}}}}
	if err := validateMultiComponentPlan(q, m); err != nil {
		t.Fatalf("trusted components rejected: %v", err)
	}
}

func TestTrustedRecreationRequiresGenerationAndStablePort(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	base := validFreshRSSRequest()
	if err = validateTrustedRecreation(db, base); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, base.InstanceID, "container", "name", base.NetworkName, base.Image, base.DataPath, "now", 1, base.ApplicationID, base.ReleaseID, "loopback", "web", "127.0.0.1", 20000, 80, "http")
	if err != nil {
		t.Fatal(err)
	}
	base.RuntimeGeneration, base.HostPort = 2, 20000
	if err = validateTrustedRecreation(db, base); err != nil {
		t.Fatalf("valid recreation rejected: %v", err)
	}
	base.HostPort = 20001
	if err = validateTrustedRecreation(db, base); err == nil {
		t.Fatal("changed persisted assignment accepted")
	}
	base.HostPort, base.RuntimeGeneration = 20000, 3
	if err = validateTrustedRecreation(db, base); err == nil {
		t.Fatal("runtime generation skip accepted")
	}
}

func TestLegacyMultiOwnershipBackfillsOnlyExactTrustedTopology(t *testing.T) {
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	trusted := entries["paperless-ngx"].Manifest
	nodes := make([]manifest.Component, 0, len(trusted.Components))
	request := protocol.Request{InstanceID: "inst-paperless01", ApplicationID: trusted.ID, ReleaseID: trusted.Releases[0].Version, RuntimeGeneration: 5, NetworkName: "kitpro-net-inst-paperless01-g5", DataPath: "/srv/kitpro/apps/paperless-ngx/inst-paperless01/data", ExposureMode: "internal"}
	for _, component := range trusted.Components {
		var image string
		for _, release := range trusted.Releases {
			if release.Version == component.Release {
				image = release.Registry + "/" + release.Repository + "@" + release.Digest
			}
		}
		request.Components = append(request.Components, protocol.Component{ID: component.ID, Image: image, DependsOn: append([]string(nil), component.DependsOn...)})
		nodes = append(nodes, manifest.Component{ID: component.ID, DependsOn: append([]string(nil), component.DependsOn...)})
	}
	topology, err := multicontainer.BuildTopology(nodes)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                                                       string
		tamperImage, missing, mixedGeneration, unexpectedComponent bool
	}{
		{name: "exact"},
		{name: "unexpected-image", tamperImage: true},
		{name: "missing-component", missing: true},
		{name: "mixed-generation", mixedGeneration: true},
		{name: "unexpected-component", unexpectedComponent: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, openErr := state.Open(filepath.Join(t.TempDir(), "helper.db"))
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			if openErr = state.Migrate(context.Background(), db, true); openErr != nil {
				t.Fatal(openErr)
			}
			for index, component := range request.Components {
				if testCase.missing && index == len(request.Components)-1 {
					continue
				}
				image := component.Image
				if testCase.tamperImage && index == 0 {
					image += "-tampered"
				}
				componentID := component.ID
				if testCase.unexpectedComponent && index == 0 {
					componentID = "unexpected"
				}
				generation := 4
				if testCase.mixedGeneration && index == len(request.Components)-1 {
					generation = 5
				}
				if _, openErr = db.Exec(`INSERT INTO component_ownership(installation_id,component_id,container_id,container_name,network_name,image_digest,runtime_generation,created_at) VALUES(?,?,?,?,?,?,?,'now')`, request.InstanceID, componentID, "container-"+componentID, "name-"+componentID, "legacy-network", image, generation); openErr != nil {
					t.Fatal(openErr)
				}
			}
			migrationErr := backfillLegacyMulti(db, request, topology, trusted)
			if testCase.name != "exact" {
				if migrationErr == nil {
					t.Fatal("ambiguous legacy ownership was migrated")
				}
				return
			}
			if migrationErr != nil {
				t.Fatal(migrationErr)
			}
			var status, hash string
			var components int
			if openErr = db.QueryRow(`SELECT status,topology_hash FROM runtime_generations WHERE installation_id=? AND runtime_generation=4`, request.InstanceID).Scan(&status, &hash); openErr != nil {
				t.Fatal(openErr)
			}
			if openErr = db.QueryRow(`SELECT COUNT(*) FROM runtime_components WHERE installation_id=? AND runtime_generation=4`, request.InstanceID).Scan(&components); openErr != nil {
				t.Fatal(openErr)
			}
			if status != "verification_required" || hash != topology.Hash || components != len(request.Components) {
				t.Fatalf("status=%s hash=%s components=%d", status, hash, components)
			}
		})
	}
}

func TestTrustedApplicationUpdateMayChangePinnedReleaseOnlyThroughUpdateOperation(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	base := validFreshRSSRequest()
	_, err = db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, base.InstanceID, "container", "name", base.NetworkName, base.Image, base.DataPath, "now", 1, base.ApplicationID, base.ReleaseID, "loopback", "web", "127.0.0.1", 20000, 80, "http")
	if err != nil {
		t.Fatal(err)
	}
	base.RuntimeGeneration, base.HostPort = 2, 20000
	base.ReleaseID = "trusted-maintenance-release"
	base.Image = "docker.io/freshrss/freshrss@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base.Operation = "InstallApplication"
	if err = validateTrustedRecreation(db, base); err == nil {
		t.Fatal("release change accepted as ordinary recreation")
	}
	base.Operation = "UpdateApplication"
	if err = validateTrustedRecreation(db, base); err != nil {
		t.Fatalf("trusted update transition rejected: %v", err)
	}
}

func TestTrustedExposureMustMatchBeforeStart(t *testing.T) {
	exact := map[string]any{"HostConfig": map[string]any{"PortBindings": map[string]any{
		"80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20000"}},
	}}}
	if !trustedExposureMatches(exact, "loopback", "127.0.0.1", 20000, 80, "http") {
		t.Fatal("exact trusted exposure rejected")
	}
	for name, observed := range map[string]map[string]any{
		"wrong-port": {"HostConfig": map[string]any{"PortBindings": map[string]any{
			"80/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "20001"}},
		}}},
		"wildcard": {"HostConfig": map[string]any{"PortBindings": map[string]any{
			"80/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "20000"}},
		}}},
		"missing": {"HostConfig": map[string]any{"PortBindings": map[string]any{}}},
	} {
		if trustedExposureMatches(observed, "loopback", "127.0.0.1", 20000, 80, "http") {
			t.Errorf("accepted %s exposure drift before start", name)
		}
	}
	if !trustedExposureMatches(map[string]any{"HostConfig": map[string]any{"PortBindings": map[string]any{}}}, "internal", "", 20000, 0, "") {
		t.Fatal("internal runtime without publication rejected")
	}
}

func TestGeneratedEnvironmentSecretPersistsAndIsNotReturnedAsMetadata(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	declaration := []protocol.EnvVar{{Name: "APP_SECRET", Secret: true, Generate: "random-hex-32"}}
	first, err := resolvedEnvironment(db, "inst-secret01", "", declaration)
	if err != nil || len(first) != 1 || !strings.HasPrefix(first[0], "APP_SECRET=") || len(strings.TrimPrefix(first[0], "APP_SECRET=")) != 64 {
		t.Fatalf("generated environment: %v %#v", err, first)
	}
	second, err := resolvedEnvironment(db, "inst-secret01", "", declaration)
	if err != nil || second[0] != first[0] {
		t.Fatalf("secret changed across recreation: %v %#v %#v", err, first, second)
	}
	other, err := resolvedEnvironment(db, "inst-secret02", "", declaration)
	if err != nil || other[0] == first[0] {
		t.Fatalf("secret was not isolated: %v %#v", err, other)
	}
	if _, err = resolvedEnvironment(db, "inst-secret01", "", []protocol.EnvVar{{Name: "BAD", Secret: true, Generate: "weak"}}); err == nil {
		t.Fatal("weak secret generator accepted")
	}
}

func TestTrustedRuntimeIdentityRejectsDrift(t *testing.T) {
	if !trustedRuntimeIdentityMatches("navidrome", "", map[string]any{"User": "1000:1000"}) {
		t.Fatal("trusted identity rejected")
	}
	for _, user := range []string{"", "0:0", "1000:44", "65534:65534"} {
		if trustedRuntimeIdentityMatches("navidrome", "", map[string]any{"User": user}) {
			t.Fatalf("identity drift accepted: %q", user)
		}
	}
}

func TestHardwareRequirementsAreExactAndComponentScoped(t *testing.T) {
	want := []manifest.Accelerator{{Class: "video.vaapi", Optional: true, CPUFallback: true}}
	if !hardwareRequirementsEqual([]protocol.HardwareRequirement{{Class: "video.vaapi", Optional: true, CPUFallback: true}}, want) {
		t.Fatal("exact hardware requirement rejected")
	}
	for _, got := range [][]protocol.HardwareRequirement{
		{{Class: "/dev/dri/renderD128", Optional: true, CPUFallback: true}},
		{{Class: "video.vaapi", Optional: false, CPUFallback: true}},
		{{Class: "video.vaapi", Optional: true, CPUFallback: true}, {Class: "gpu.amd"}},
	} {
		if hardwareRequirementsEqual(got, want) {
			t.Fatalf("mismatched hardware accepted: %#v", got)
		}
	}
	web := protocol.Component{ID: "web"}
	worker := protocol.Component{ID: "worker", Hardware: []protocol.HardwareRequirement{{Class: "gpu.amd"}}}
	if len(web.Hardware) != 0 || len(worker.Hardware) != 1 {
		t.Fatal("component device scope spread")
	}
}

func TestCPUAssignmentRejectsUnexpectedDeviceMapping(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO hardware_assignments(installation_id,component_id,device_class,mode,runtime_generation,created_at) VALUES('inst-hardware01','','gpu.nvidia','cpu',1,'now')`); err != nil {
		t.Fatal(err)
	}
	if err = validateHardwareObservation(db, "inst-hardware01", "", map[string]any{}); err != nil {
		t.Fatalf("clean CPU runtime rejected: %v", err)
	}
	host := map[string]any{"Devices": []any{map[string]any{"PathOnHost": "/dev/sda", "PathInContainer": "/dev/sda"}}}
	if err = validateHardwareObservation(db, "inst-hardware01", "", host); err == nil {
		t.Fatal("unexpected block device mapping accepted")
	}
}

func TestDeviceAssignmentPersistsAllTrustedIdentityFields(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	assignment := hardware.Assignment{
		Class:         hardware.NVIDIAClass,
		Vendor:        "nvidia",
		StableID:      "0000:06:10.0:10de:2571",
		NVIDIARuntime: true,
	}
	if err = persistHardware(db, "inst-hardware02", "model", 3, runtimeHardware{Assignments: []hardware.Assignment{assignment}}); err != nil {
		t.Fatalf("persist device assignment: %v", err)
	}
	var class, mode, vendor, stableID, devices string
	var generation int
	if err = db.QueryRow(`SELECT device_class,mode,vendor,stable_id,resolved_devices,runtime_generation FROM hardware_assignments WHERE installation_id=? AND component_id=?`, "inst-hardware02", "model").Scan(&class, &mode, &vendor, &stableID, &devices, &generation); err != nil {
		t.Fatal(err)
	}
	if class != hardware.NVIDIAClass || mode != "device" || vendor != "nvidia" || stableID != assignment.StableID || devices != "[]" || generation != 3 {
		t.Fatalf("unexpected persisted assignment: %q %q %q %q %q %d", class, mode, vendor, stableID, devices, generation)
	}
}

func TestHardwareAssignmentViewIsBoundedAndOmitsRawDevicePaths(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO hardware_assignments(installation_id,component_id,device_class,mode,vendor,stable_id,resolved_devices,runtime_generation,created_at) VALUES('inst-assignment01','model','gpu.nvidia','device','nvidia','0000:01:00.0:10de:2571','["/dev/nvidia0"]',4,'now')`); err != nil {
		t.Fatal(err)
	}
	view, err := hardwareAssignmentView(db, "inst-assignment01")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "/dev/") || !strings.Contains(string(encoded), "gpu.nvidia") {
		t.Fatalf("unsafe or incomplete assignment view: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"available":false`) {
		t.Fatalf("stale identity not marked unavailable: %s", encoded)
	}
	if _, err := hardwareAssignmentView(db, "/dev/sda"); err == nil {
		t.Fatal("unbounded identity accepted")
	}
}
