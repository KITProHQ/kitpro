package appconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/docker"
)

const syncthingImage = "docker.io/syncthing/syncthing@sha256:84dcf202b0890f795c4c3899d35a5ac7369bb8b72b5c270078c50247da4ddeef"

type liveSyncthingNode struct {
	name, alias, root, syncRoot, id, apiKey string
	guiPort                                 int
}

// This opt-in acceptance test proves actual manual-address TCP synchronization
// with the catalog-pinned image. It never uses discovery, relays, NAT traversal,
// QUIC, host networking, privilege, capabilities, or user data.
func TestDockerSyncthingTCPOnlyManualPeerSync(t *testing.T) {
	if os.Getenv("KITPRO_TEST_SYNCTHING") != "1" {
		t.Skip("set KITPRO_TEST_SYNCTHING=1 to use the local Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	requireLoopbackPortAvailable(t, 22000)
	runtime := docker.New()
	suffix := fmt.Sprint(time.Now().UnixNano())
	network := "kitpro-syncthing-test-" + suffix
	labels := map[string]string{"com.kitpro.test": "syncthing-tcp-only"}
	if _, err := runtime.CreateNetwork(network, labels); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.RemoveNetwork(network) })

	nodes := []*liveSyncthingNode{
		{name: "kitpro-syncthing-a-" + suffix, alias: "node-a", root: filepath.Join(t.TempDir(), "config"), syncRoot: filepath.Join(t.TempDir(), "sync"), guiPort: freeLoopbackPort(t)},
		{name: "kitpro-syncthing-b-" + suffix, alias: "node-b", root: filepath.Join(t.TempDir(), "config"), syncRoot: filepath.Join(t.TempDir(), "sync"), guiPort: freeLoopbackPort(t)},
	}
	for _, item := range nodes {
		if err := os.MkdirAll(item.root, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(item.syncRoot, 0750); err != nil {
			t.Fatal(err)
		}
		bootstrap, err := runtime.CreateLifecycleContainer(ctx, containers.ContainerPlan{
			Image: syncthingImage, Name: item.name + "-bootstrap", Network: "none", User: "1000:1000", Labels: labels,
			Command: []string{"generate", "--no-port-probing"}, Storage: []containers.StorageMount{{HostPath: item.root, ContainerPath: "/var/syncthing"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = runtime.StartContainer(ctx, bootstrap); err != nil {
			t.Fatal(err)
		}
		if _, err = runtime.WaitContainer(ctx, bootstrap, containers.RuntimeStopped, 30*time.Second); err != nil {
			t.Fatal(err)
		}
		if err = runtime.RemoveContainer(ctx, bootstrap); err != nil {
			t.Fatal(err)
		}
		if err = Apply(SyncthingTCPOnlyV1, item.root, RuntimeOwner{UID: 1000, GID: 1000}); err != nil {
			t.Fatal(err)
		}
		item.apiKey = readSyncthingAPIKey(t, item.root)
		portBindings := map[string][]containers.PortBinding{
			"8384/tcp": {{HostIP: "127.0.0.1", HostPort: fmt.Sprint(item.guiPort)}},
		}
		if item == nodes[0] {
			portBindings["22000/tcp"] = []containers.PortBinding{{HostIP: "127.0.0.1", HostPort: "22000"}}
		}
		containerID, err := runtime.CreateLifecycleContainer(ctx, containers.ContainerPlan{
			Image: syncthingImage, Name: item.name, Network: network, NetworkAliases: []string{item.alias}, User: "1000:1000", Labels: labels,
			Environment:  []string{"STGUIADDRESS=0.0.0.0:8384", "STNOBROWSER=true", "STNORESTART=true", "STNOUPGRADE=true", "STNOPORTPROBING=true"},
			Storage:      []containers.StorageMount{{HostPath: item.root, ContainerPath: "/var/syncthing"}, {HostPath: item.syncRoot, ContainerPath: "/sync"}},
			PortBindings: portBindings, RestartPolicy: "unless-stopped",
		})
		if err != nil {
			t.Fatal(err)
		}
		item.id = containerID
		itemID := item.id
		t.Cleanup(func() {
			_ = runtime.StopContainer(context.Background(), itemID)
			_ = runtime.RemoveContainer(context.Background(), itemID)
		})
		if err = runtime.StartContainer(ctx, item.id); err != nil {
			t.Fatal(err)
		}
		waitForSyncthingGUI(t, ctx, item)
		observed, err := runtime.ObserveContainer(ctx, item.id)
		if err != nil || observed.NetworkMode != network || len(observed.Devices) != 0 || len(observed.DeviceRequests) != 0 {
			t.Fatalf("unexpected runtime authority: %#v err=%v", observed, err)
		}
		if len(observed.PortBindings) != len(portBindings) || observed.PortBindings["22000/udp"] != nil || observed.PortBindings["21027/udp"] != nil {
			t.Fatalf("unexpected published bindings: %#v", observed.PortBindings)
		}
		var status struct {
			MyID string `json:"myID"`
		}
		syncthingRequest(t, ctx, item, http.MethodGet, "/rest/system/status", nil, &status)
		if status.MyID == "" {
			t.Fatal("Syncthing did not generate a device ID")
		}
		item.apiKey = readSyncthingAPIKey(t, item.root)
	}

	ids := make([]string, len(nodes))
	for index, item := range nodes {
		var status struct {
			MyID string `json:"myID"`
		}
		syncthingRequest(t, ctx, item, http.MethodGet, "/rest/system/status", nil, &status)
		ids[index] = status.MyID
	}
	for index, item := range nodes {
		peer := nodes[1-index]
		peerID := ids[1-index]
		syncthingRequest(t, ctx, item, http.MethodPut, "/rest/config/devices/"+peerID, map[string]any{
			"deviceID": peerID, "name": peer.alias, "addresses": []string{"tcp://" + peer.alias + ":22000"},
		}, nil)
		syncthingRequest(t, ctx, item, http.MethodPut, "/rest/config/folders/kitpro-sync", map[string]any{
			"id": "kitpro-sync", "label": "KITPro validation", "path": "/sync", "type": "sendreceive",
			"devices": []map[string]string{{"deviceID": ids[index]}, {"deviceID": peerID}},
		}, nil)
	}
	marker := []byte("bounded Syncthing TCP validation\n")
	if err := os.WriteFile(filepath.Join(nodes[0].syncRoot, "phase7.txt"), marker, 0640); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(nodes[1].syncRoot, "phase7.txt"))
		if err == nil && bytes.Equal(data, marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("manual TCP peer did not synchronize test file: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	beforeID := ids[0]
	if err := runtime.StopContainer(ctx, nodes[0].id); err != nil {
		t.Fatal(err)
	}
	if err := Verify(SyncthingTCPOnlyV1, nodes[0].root); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartContainer(ctx, nodes[0].id); err != nil {
		t.Fatal(err)
	}
	waitForSyncthingGUI(t, ctx, nodes[0])
	var restarted struct {
		MyID string `json:"myID"`
	}
	syncthingRequest(t, ctx, nodes[0], http.MethodGet, "/rest/system/status", nil, &restarted)
	if restarted.MyID != beforeID {
		t.Fatalf("device identity changed across restart: %s -> %s", beforeID, restarted.MyID)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func requireLoopbackPortAvailable(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skipf("safe loopback port %d is unavailable: %v", port, err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func readSyncthingAPIKey(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "config", "config.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var configuration struct {
		GUI struct {
			APIKey string `xml:"apikey"`
		} `xml:"gui"`
	}
	if err = xml.Unmarshal(data, &configuration); err != nil || configuration.GUI.APIKey == "" {
		t.Fatalf("read GUI API key: key=%q err=%v", configuration.GUI.APIKey, err)
	}
	return configuration.GUI.APIKey
}

func waitForSyncthingGUI(t *testing.T, ctx context.Context, item *liveSyncthingNode) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/rest/noauth/health", item.guiPort), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Syncthing GUI did not become ready: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func syncthingRequest(t *testing.T, ctx context.Context, item *liveSyncthingNode, method, path string, body any, target any) {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", item.guiPort, path), &payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", item.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("Syncthing %s %s: HTTP %s", method, path, response.Status)
	}
	if target != nil {
		if err = json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}
