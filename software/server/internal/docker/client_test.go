package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientUsesUnixTransport(t *testing.T) {
	if New() == nil {
		t.Fatal("nil client")
	}
}

func TestTypedLifecycleObservations(t *testing.T) {
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := ""
		switch r.URL.RequestURI() {
		case "/containers/running/json":
			body = `{"Id":"running","Name":"/app","Image":"sha256:image","Config":{"Image":"repo/app@sha256:digest","Labels":{"managed":"true"},"User":"1000:1000","Cmd":["serve"],"Env":["A=B"]},"State":{"Running":true,"Status":"running","Health":{"Status":"healthy"}},"HostConfig":{"NetworkMode":"network","RestartPolicy":{"Name":"unless-stopped"},"PortBindings":{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"20000"}]}},"Mounts":[{"Source":"/data","Destination":"/data","RW":true}],"NetworkSettings":{"Networks":{"network":{"NetworkID":"network-id","Aliases":["app"]}}}}`
		case "/containers/stopped/json":
			body = `{"Id":"stopped","Name":"/stopped","Config":{"Image":"repo/app@sha256:digest"},"State":{"Running":false,"Status":"exited"},"HostConfig":{},"NetworkSettings":{"Networks":{}}}`
		case "/containers/restarting/json":
			body = `{"Id":"restarting","Name":"/restarting","Config":{"Image":"repo/app@sha256:digest"},"State":{"Running":true,"Status":"restarting"},"HostConfig":{},"NetworkSettings":{"Networks":{}}}`
		case "/containers/paused/json":
			body = `{"Id":"paused","Name":"/paused","Config":{"Image":"repo/app@sha256:digest"},"State":{"Running":true,"Status":"paused"},"HostConfig":{},"NetworkSettings":{"Networks":{}}}`
		case "/containers/missing/json":
			status = http.StatusNotFound
		case "/images/repo%2Fapp@sha256:digest/json":
			body = `{"Id":"sha256:image","RepoDigests":["repo/app@sha256:digest"]}`
		case "/networks/network":
			body = `{"Id":"network-id","Name":"network","Labels":{"managed":"true"}}`
		case "/info":
			body = `{"ID":"daemon-id","ServerVersion":"29.0.0"}`
		default:
			t.Fatalf("unexpected request %s", r.URL.RequestURI())
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	running, err := client.ObserveContainer(context.Background(), "running")
	if err != nil {
		t.Fatal(err)
	}
	if running.State != containers.RuntimeRunning || running.Health != "healthy" || running.NetworkMode != "network" || running.PortBindings["80/tcp"][0].HostIP != "127.0.0.1" || running.Networks["network"].NetworkID != "network-id" {
		t.Fatalf("bad running observation: %#v", running)
	}
	stopped, err := client.ObserveContainer(context.Background(), "stopped")
	if err != nil || stopped.State != containers.RuntimeStopped {
		t.Fatalf("stopped=%#v err=%v", stopped, err)
	}
	missing, err := client.ObserveContainer(context.Background(), "missing")
	if err != nil || missing.State != containers.RuntimeMissing || missing.Exists {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
	restarting, err := client.ObserveContainer(context.Background(), "restarting")
	if err != nil || restarting.State != containers.RuntimeState("restarting") {
		t.Fatalf("restarting=%#v err=%v", restarting, err)
	}
	paused, err := client.ObserveContainer(context.Background(), "paused")
	if err != nil || paused.State != containers.RuntimeState("paused") {
		t.Fatalf("paused=%#v err=%v", paused, err)
	}
	image, err := client.ObserveImage(context.Background(), "repo/app@sha256:digest")
	if err != nil || !image.Exists || image.RepoDigests[0] != "repo/app@sha256:digest" {
		t.Fatalf("image=%#v err=%v", image, err)
	}
	network, err := client.ObserveNetwork(context.Background(), "network")
	if err != nil || network.ID != "network-id" {
		t.Fatalf("network=%#v err=%v", network, err)
	}
	identity, err := client.ObserveRuntimeIdentity(context.Background())
	if err != nil || identity.Name != "docker" || identity.ID != "daemon-id" || identity.Version != "29.0.0" {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
}

func TestPullDrainsLargeProgressStream(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 300*1024)
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.RequestURI(), "/images/create?fromImage=") {
			t.Fatalf("unexpected pull request: %s %s", r.Method, r.URL.RequestURI())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	if err := client.Pull("registry.example/app@sha256:digest"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateContainerPlanPortBindings(t *testing.T) {
	for name, bindings := range map[string]map[string][]PortBinding{
		"internal": nil,
		"loopback": {"80/tcp": {{HostIP: "127.0.0.1", HostPort: "20000"}}},
		"lan":      {"80/tcp": {{HostIP: "10.10.0.115", HostPort: "20000"}}},
	} {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
			})}}
			if _, err := client.CreateContainerPlan(ContainerPlan{Image: "image", Name: "name", Network: "network", PortBindings: bindings}); err != nil {
				t.Fatal(err)
			}
			host := body["HostConfig"].(map[string]any)
			observed, present := host["PortBindings"]
			if bindings == nil && present {
				t.Fatal("internal plan published a host port")
			}
			if bindings != nil {
				encoded, _ := json.Marshal(observed)
				if strings.Contains(string(encoded), "0.0.0.0") || strings.Contains(string(encoded), `"HostIp":""`) {
					t.Fatalf("wildcard binding generated: %s", encoded)
				}
			}
		})
	}
}

func TestCreateContainerPlanRestartPolicy(t *testing.T) {
	var body map[string]any
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.CreateContainerPlan(ContainerPlan{Image: "image", Name: "name", Network: "network", RestartPolicy: "unless-stopped"}); err != nil {
		t.Fatal(err)
	}
	host := body["HostConfig"].(map[string]any)
	restart := host["RestartPolicy"].(map[string]any)
	if restart["Name"] != "unless-stopped" {
		t.Fatalf("unexpected restart policy: %#v", restart)
	}
}

func TestCreateContainerPlanUsesOnlyResolvedDevices(t *testing.T) {
	var body map[string]any
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
	})}}
	plan := ContainerPlan{Image: "image", Name: "name", Network: "network", Devices: []DeviceMapping{{PathOnHost: "/dev/dri/renderD128", PathInContainer: "/dev/dri/renderD128", CgroupPermissions: "rwm"}}}
	if _, err := client.CreateContainerPlan(plan); err != nil {
		t.Fatal(err)
	}
	host := body["HostConfig"].(map[string]any)
	encoded, _ := json.Marshal(host["Devices"])
	if !strings.Contains(string(encoded), "renderD128") || strings.Contains(string(encoded), "/dev/dri/card") {
		t.Fatalf("unexpected device map: %s", encoded)
	}
	if host["Privileged"] != false {
		t.Fatal("device plan became privileged")
	}
}

func TestCreateContainerPlanSetsTrustedUser(t *testing.T) {
	var body map[string]any
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"container"}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.CreateContainerPlan(ContainerPlan{Image: "image", Name: "name", Network: "network", User: "1000:1000"}); err != nil {
		t.Fatal(err)
	}
	if body["User"] != "1000:1000" {
		t.Fatalf("trusted user missing: %#v", body)
	}
}

func TestHasForeignNetworkMemberFindsStoppedContainerOmittedFromNetworkInspect(t *testing.T) {
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.RequestURI() {
		case "/networks/kitpro-network":
			body = `{"Containers":{"owned":{}}}`
		case "/containers/json?all=true":
			body = `[{"Id":"owned"},{"Id":"stopped-foreign"}]`
		case "/containers/stopped-foreign/json":
			body = `{"NetworkSettings":{"Networks":{"kitpro-network":{}}}}`
		default:
			t.Fatalf("unexpected Docker request: %s", r.URL.RequestURI())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	foreign, err := client.HasForeignNetworkMember("kitpro-network", "owned")
	if err != nil {
		t.Fatal(err)
	}
	if !foreign {
		t.Fatal("stopped foreign container was not detected")
	}
}

func TestHasForeignNetworkMemberAcceptsOwnedContainerOnly(t *testing.T) {
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.RequestURI() {
		case "/networks/kitpro-network":
			body = `{"Containers":{"owned":{}}}`
		case "/containers/json?all=true":
			body = `[{"Id":"owned"}]`
		default:
			t.Fatalf("unexpected Docker request: %s", r.URL.RequestURI())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	foreign, err := client.HasForeignNetworkMember("kitpro-network", "owned")
	if err != nil {
		t.Fatal(err)
	}
	if foreign {
		t.Fatal("owned container was classified as foreign")
	}
}
