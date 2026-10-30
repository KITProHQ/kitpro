package podman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

const testImage = "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0"

type fakeRunner struct {
	calls     []string
	responses map[string]string
	errors    map[string]error
}

func (f *fakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	call := command + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if err := f.errors[call]; err != nil {
		return nil, err
	}
	return []byte(f.responses[call]), nil
}

func testRuntime(t *testing.T) (*Runtime, *fakeRunner, string, string) {
	t.Helper()
	root := t.TempDir()
	units := filepath.Join(root, "quadlet")
	environment := filepath.Join(root, "runtime")
	runner := &fakeRunner{responses: map[string]string{}, errors: map[string]error{}}
	return NewWith(Config{UnitDir: units, EnvironmentDir: environment, PodmanPath: "podman", SystemctlPath: "systemctl"}, runner), runner, units, environment
}

func TestCreateContainerPlanRendersProtectedQuadlet(t *testing.T) {
	runtime, runner, units, environment := testRuntime(t)
	plan := containers.ContainerPlan{
		Image:          testImage,
		Name:           "kitpro-paperless-inst-12345678-web-g1",
		Network:        "kitpro-net-inst-12345678-g1",
		User:           "1000:1000",
		Labels:         map[string]string{"com.kitpro.managed": "true", "com.kitpro.instance": "inst-12345678"},
		Command:        []string{"serve", "--safe"},
		Environment:    []string{"PUBLIC=value", "SECRET=do-not-copy"},
		RestartPolicy:  "unless-stopped",
		NetworkAliases: []string{"web"},
		Dependencies:   []string{"kitpro-paperless-inst-12345678-broker-g1"},
		Storage: []containers.StorageMount{
			{HostPath: "/srv/kitpro/apps/paperless/inst-12345678/data", ContainerPath: "/data"},
			{HostPath: "/mnt/library", ContainerPath: "/library", ReadOnly: true},
		},
		PortBindings: map[string][]containers.PortBinding{"8000/tcp": {{HostIP: "127.0.0.1", HostPort: "20000"}}},
		Devices:      []containers.DeviceMapping{{PathOnHost: "/dev/dri/renderD128", PathInContainer: "/dev/dri/renderD128", CgroupPermissions: "rwm"}},
	}
	name, err := runtime.CreateContainerPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if name != plan.Name {
		t.Fatalf("unexpected stable runtime identity: %s", name)
	}
	unitPath := filepath.Join(units, plan.Name+".container")
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, expected := range []string{
		"Image=" + testImage,
		"ContainerName=" + plan.Name,
		"Network=kitpro-net-inst-12345678-g1.network",
		"NoNewPrivileges=true",
		"Volume=/srv/kitpro/apps/paperless/inst-12345678/data:/data:Z",
		"Volume=/mnt/library:/library:ro",
		"PublishPort=127.0.0.1:20000:8000",
		"NetworkAlias=web",
		"AddDevice=\"/dev/dri/renderD128:/dev/dri/renderD128:rwm\"",
		"Requires=kitpro-paperless-inst-12345678-broker-g1.service",
		"After=kitpro-paperless-inst-12345678-broker-g1.service",
		"Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "do-not-copy") || strings.Contains(text, "SECRET=") {
		t.Fatal("secret leaked into Quadlet file")
	}
	if strings.Contains(text, "/mnt/library:/library:ro,Z") {
		t.Fatal("imported storage was relabeled")
	}
	if strings.Contains(text, "Volume=\"") {
		t.Fatal("Quadlet volume value was quoted and would pass the quote into Podman mount options")
	}
	envPath := filepath.Join(environment, plan.Name+".env")
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(envData) != "PUBLIC=value\nSECRET=do-not-copy\n" {
		t.Fatalf("unexpected environment file: %q", envData)
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("environment mode is %o", info.Mode().Perm())
	}
	metadata, err := os.ReadFile(filepath.Join(environment, plan.Name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), "do-not-copy") || strings.Contains(string(metadata), "SECRET") {
		t.Fatal("secret leaked into runtime metadata")
	}
	if len(runner.calls) != 1 || runner.calls[0] != "systemctl daemon-reload" {
		t.Fatalf("unexpected command calls: %#v", runner.calls)
	}
}

func TestCreateNetworkAndLifecycleUseGeneratedServices(t *testing.T) {
	runtime, runner, units, _ := testRuntime(t)
	name := "kitpro-net-inst-12345678-g1"
	if _, err := runtime.CreateNetwork(name, map[string]string{"com.kitpro.managed": "true"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(units, name+".network"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "NetworkName="+name) || !strings.Contains(text, "NetworkDeleteOnStop=true") || strings.Contains(text, "Internal=true") {
		t.Fatalf("unexpected network unit:\n%s", text)
	}
	if err := runtime.Start("kitpro-app-inst-12345678-g1"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop("kitpro-app-inst-12345678-g1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl start " + name + "-network.service",
		"systemctl start kitpro-app-inst-12345678-g1.service",
		"systemctl stop kitpro-app-inst-12345678-g1.service",
	}
	if fmt.Sprint(runner.calls) != fmt.Sprint(want) {
		t.Fatalf("unexpected lifecycle calls: %#v", runner.calls)
	}
}

func TestStoppedQuadletHasSyntheticInspectableState(t *testing.T) {
	runtime, runner, _, _ := testRuntime(t)
	plan := containers.ContainerPlan{Image: testImage, Name: "kitpro-app-inst-12345678-g1", Network: "kitpro-net-inst-12345678-g1", Labels: map[string]string{"com.kitpro.managed": "true"}, PortBindings: map[string][]containers.PortBinding{"80/tcp": {{HostIP: "127.0.0.1", HostPort: "20001"}}}}
	if _, err := runtime.CreateContainerPlan(plan); err != nil {
		t.Fatal(err)
	}
	call := "podman inspect --format json " + plan.Name
	runner.errors[call] = fmt.Errorf("container absent after systemd stop")
	observed, err := runtime.Inspect(plan.Name)
	if err != nil {
		t.Fatal(err)
	}
	state := observed["State"].(map[string]any)
	if state["Status"] != "stopped" || state["Running"] != false {
		t.Fatalf("unexpected synthetic state: %#v", state)
	}
	host := observed["HostConfig"].(map[string]any)
	ports := host["PortBindings"].(map[string]any)
	if len(ports) != 1 {
		t.Fatalf("synthetic exposure missing: %#v", observed)
	}
}

func TestForeignNetworkMemberIncludesStoppedContainers(t *testing.T) {
	runtime, runner, _, _ := testRuntime(t)
	call := "podman ps --all --filter network=kitpro-net-inst-12345678-g1 --format json"
	runner.responses[call] = `[{"Id":"abc","Names":["kitpro-owned-inst-12345678-g1"]},{"Id":"def","Names":["foreign-stopped"]}]`
	foreign, err := runtime.HasForeignNetworkMember("kitpro-net-inst-12345678-g1", "kitpro-owned-inst-12345678-g1")
	if err != nil {
		t.Fatal(err)
	}
	if !foreign {
		t.Fatal("stopped foreign network member was not detected")
	}
	runner.responses[call] = `[{"Id":"abc","Names":["kitpro-owned-inst-12345678-g1"]}]`
	foreign, err = runtime.HasForeignNetworkMember("kitpro-net-inst-12345678-g1", "kitpro-owned-inst-12345678-g1")
	if err != nil || foreign {
		t.Fatalf("owned member was rejected: foreign=%v err=%v", foreign, err)
	}
}

func TestPlanValidationRejectsWildcardAndSecretNewline(t *testing.T) {
	runtime, _, _, _ := testRuntime(t)
	base := containers.ContainerPlan{Image: testImage, Name: "kitpro-app-inst-12345678-g1", Network: "kitpro-net-inst-12345678-g1"}
	badPort := base
	badPort.PortBindings = map[string][]containers.PortBinding{"80/tcp": {{HostPort: "20000"}}}
	if _, err := runtime.CreateContainerPlan(badPort); err == nil {
		t.Fatal("wildcard binding accepted")
	}
	badEnvironment := base
	badEnvironment.Environment = []string{"SECRET=line1\nline2"}
	if _, err := runtime.CreateContainerPlan(badEnvironment); err == nil {
		t.Fatal("multiline environment entry accepted")
	}
}

func TestImagePolicyAcceptsCodebergAndRejectsLookalikeHosts(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, image := range []string{
		"docker.io/library/busybox" + digest,
		"ghcr.io/example/project" + digest,
		"codeberg.org/forgejo/forgejo" + digest,
		"codeberg.org/unrelated/project" + digest,
	} {
		if !validImage(image) {
			t.Fatalf("trusted host image rejected: %s", image)
		}
	}
	for _, image := range []string{
		"evil.example/project/image" + digest,
		"evil.codeberg.org/project/image" + digest,
		"codeberg.org.evil.example/project/image" + digest,
		"codeberg.org/forgejo/forgejo:16.0.5",
	} {
		if validImage(image) {
			t.Fatalf("untrusted or mutable image accepted: %s", image)
		}
	}
}

func TestCreateContainerPlanBracketsIPv6PublishAddress(t *testing.T) {
	runtime, _, units, _ := testRuntime(t)
	plan := containers.ContainerPlan{
		Image:   testImage,
		Name:    "kitpro-app-inst-12345678-g1",
		Network: "kitpro-net-inst-12345678-g1",
		PortBindings: map[string][]containers.PortBinding{
			"8080/tcp": {{HostIP: "::1", HostPort: "20003"}},
		},
	}
	if _, err := runtime.CreateContainerPlan(plan); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(units, plan.Name+".container"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "PublishPort=[::1]:20003:8080") {
		t.Fatalf("IPv6 address was not bracketed:\n%s", unit)
	}
}

func TestCreateContainerPlanRendersMultipleTCPAndUDPBindings(t *testing.T) {
	runtime, _, units, _ := testRuntime(t)
	plan := containers.ContainerPlan{Image: testImage, Name: "kitpro-app-inst-network01-g1", Network: "kitpro-net-inst-network01-g1", PortBindings: map[string][]containers.PortBinding{"53/tcp": {{HostIP: "10.0.0.2", HostPort: "53"}}, "53/udp": {{HostIP: "10.0.0.2", HostPort: "53"}}, "80/tcp": {{HostIP: "127.0.0.1", HostPort: "20000"}}}}
	if _, err := runtime.CreateContainerPlan(plan); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(units, plan.Name+".container"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{"PublishPort=10.0.0.2:53:53\n", "PublishPort=10.0.0.2:53:53/udp\n", "PublishPort=127.0.0.1:20000:80\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}
