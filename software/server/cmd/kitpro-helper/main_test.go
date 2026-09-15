package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

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

func TestRuntimeRemovalRetainsTrustedInstallationForRecreate(t *testing.T) {
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
	if err = markRuntimeRemoved(db, base.InstanceID); err != nil {
		t.Fatal(err)
	}
	var containerID, containerName, networkName string
	if err = db.QueryRow(`SELECT container_id,container_name,network_name FROM ownership WHERE instance_id=?`, base.InstanceID).Scan(&containerID, &containerName, &networkName); err != nil {
		t.Fatalf("trusted installation was deleted: %v", err)
	}
	if containerID != "" || containerName != "" || networkName != "" {
		t.Fatalf("disposable runtime identity retained: %q %q %q", containerID, containerName, networkName)
	}
	base.RuntimeGeneration, base.HostPort = 2, 20000
	if err = validateTrustedRecreation(db, base); err != nil {
		t.Fatalf("recreate after intentional runtime removal rejected: %v", err)
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
