package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientUsesUnixTransport(t *testing.T) {
	if New() == nil {
		t.Fatal("nil client")
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
