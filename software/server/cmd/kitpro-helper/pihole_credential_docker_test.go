package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

// TestDockerPiHoleCredentialReveal is an opt-in live acceptance test. It binds
// DNS only to the explicitly supplied local address, never changes host DNS,
// and authenticates through the same generated credential exposed by the
// manifest-authorized helper operation.
//
//	KITPRO_TEST_PIHOLE_CREDENTIAL=1 KITPRO_TEST_PIHOLE_ADDRESS=192.0.2.10 \
//	  go test ./cmd/kitpro-helper -run TestDockerPiHoleCredentialReveal -v
func TestDockerPiHoleCredentialReveal(t *testing.T) {
	if os.Getenv("KITPRO_TEST_PIHOLE_CREDENTIAL") != "1" {
		t.Skip("set KITPRO_TEST_PIHOLE_CREDENTIAL=1 and KITPRO_TEST_PIHOLE_ADDRESS to use the local Docker daemon")
	}
	hostAddress := os.Getenv("KITPRO_TEST_PIHOLE_ADDRESS")
	if hostAddress == "" || net.ParseIP(hostAddress) == nil {
		t.Fatal("KITPRO_TEST_PIHOLE_ADDRESS must be an explicitly selected local IP address")
	}
	assertPort53Available(t, hostAddress)

	adminListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	adminPort := adminListener.Addr().(*net.TCPAddr).Port
	adminListener.Close()

	root := t.TempDir()
	db, err := state.Open(filepath.Join(root, "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := entries["pihole"]
	declaration := make([]protocol.EnvVar, 0, len(entry.Manifest.Environment))
	for _, variable := range entry.Manifest.Environment {
		declaration = append(declaration, protocol.EnvVar{Name: variable.Name, Value: variable.Value, Secret: variable.Secret, Generate: variable.Generate})
	}

	const instance = "inst-piholelive01"
	environment, err := resolvedEnvironment(db, instance, "", declaration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,created_at,cleanup_state) VALUES(?,1,'live-credential-test','pihole','2026.09.0','active','live-network',?,'now','clean')`, instance, root); err != nil {
		t.Fatal(err)
	}
	disclosure := revealCredentialThroughHelper(t, db, protocol.Request{Version: 2, ID: "request-live-reveal", Operation: "RevealApplicationCredential", InstanceID: instance, ApplicationID: "pihole", CredentialID: "admin-password"})

	runtime := docker.New()
	suffix := fmt.Sprint(time.Now().UnixNano())
	networkName := "kitpro-pihole-credential-" + suffix
	containerName := networkName + "-g1"
	labels := map[string]string{"com.kitpro.managed": "true", "com.kitpro.instance": instance, "com.kitpro.test": "pihole-credential"}
	if _, err = runtime.CreateNetwork(networkName, labels); err != nil {
		t.Fatal(err)
	}
	var containerID string
	t.Cleanup(func() {
		if containerID != "" {
			_ = runtime.Stop(containerID)
			_ = runtime.Remove(containerID)
		}
		_ = runtime.RemoveNetwork(networkName)
	})

	plan := containers.ContainerPlan{
		Image:         "docker.io/pihole/pihole@sha256:bd3fc82ee1b1473a45fc074379dcd9fd7ce3e933809c44e10c0df9b22fd5de63",
		Name:          containerName,
		Network:       networkName,
		Labels:        labels,
		Environment:   environment,
		RestartPolicy: "unless-stopped",
		Storage:       []containers.StorageMount{{HostPath: filepath.Join(root, "pihole"), ContainerPath: "/etc/pihole"}},
		PortBindings: map[string][]containers.PortBinding{
			"80/tcp": {{HostIP: "127.0.0.1", HostPort: fmt.Sprint(adminPort)}},
			"53/tcp": {{HostIP: hostAddress, HostPort: "53"}},
			"53/udp": {{HostIP: hostAddress, HostPort: "53"}},
		},
	}
	if err = os.MkdirAll(plan.Storage[0].HostPath, 0750); err != nil {
		t.Fatal(err)
	}
	start := func(candidate containers.ContainerPlan) {
		containerID, err = runtime.CreateContainerPlan(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err = runtime.Start(containerID); err != nil {
			t.Fatal(err)
		}
		waitForPiHoleAuthentication(t, adminPort, disclosure.Value)
	}
	start(plan)
	lookupPiHoleDNS(t, hostAddress, "udp")
	lookupPiHoleDNS(t, hostAddress, "tcp")

	if err = runtime.Stop(containerID); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(containerID); err != nil {
		t.Fatal(err)
	}
	waitForPiHoleAuthentication(t, adminPort, disclosure.Value)

	if err = runtime.Stop(containerID); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Remove(containerID); err != nil {
		t.Fatal(err)
	}
	containerID = ""
	recreatedEnvironment, err := resolvedEnvironment(db, instance, "", declaration)
	if err != nil || strings.Join(recreatedEnvironment, "\x00") != strings.Join(environment, "\x00") {
		t.Fatal("runtime recreation did not reuse the generated Pi-hole credential")
	}
	plan.Name = networkName + "-g2"
	plan.Environment = recreatedEnvironment
	start(plan)
	if _, err = db.Exec(`UPDATE runtime_generations SET status='removed' WHERE installation_id=?`, instance); err != nil {
		t.Fatal(err)
	}
	revealCredentialThroughHelper(t, db, protocol.Request{Version: 2, ID: "request-live-retained", Operation: "RevealApplicationCredential", InstanceID: instance, ApplicationID: "pihole", CredentialID: "admin-password"})
}

func revealCredentialThroughHelper(t *testing.T, db *sql.DB, request protocol.Request) protocol.CredentialDisclosure {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			serve(connection, uint32(os.Getuid()), db, helperops.Coordinator{DB: db})
		}
	}()
	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = protocol.Write(connection, request); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := protocol.ReadResponse(connection)
	connection.Close()
	<-done
	if err != nil || !response.OK {
		t.Fatal("generated Pi-hole credential was not available through the protected helper operation")
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var disclosure protocol.CredentialDisclosure
	if err = json.Unmarshal(encoded, &disclosure); err != nil || disclosure.ID != "admin-password" || disclosure.Value == "" {
		t.Fatal("protected helper operation returned an invalid credential disclosure")
	}
	return disclosure
}

func assertPort53Available(t *testing.T, address string) {
	t.Helper()
	if err := exposure.VerifyLocalAddress(address); err != nil {
		t.Fatalf("selected address is not assigned locally: %v", err)
	}
	listeners, err := exposure.HostListeners()
	if err != nil {
		t.Fatalf("host listener preflight failed: %v", err)
	}
	for _, transport := range []exposure.Transport{exposure.TCP, exposure.UDP} {
		requested := exposure.ServiceBinding{Transport: transport, Mode: exposure.LAN, HostAddress: address, HostPort: 53}
		for _, listener := range listeners {
			if exposure.BindingsConflict(requested, listener) {
				t.Fatalf("selected address port 53/%s conflicts with %s:%d/%s", transport, listener.HostAddress, listener.HostPort, listener.Transport)
			}
		}
	}
}

func waitForPiHoleAuthentication(t *testing.T, port int, password string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := json.Marshal(map[string]string{"password": password})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d/api/auth", port), "application/json", bytes.NewReader(payload))
		if err == nil {
			var result struct {
				Session struct {
					Valid bool `json:"valid"`
				} `json:"session"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && result.Session.Valid {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("Pi-hole did not accept the helper-revealed credential before the readiness deadline")
}

func lookupPiHoleDNS(t *testing.T, address, network string) {
	t.Helper()
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(address, "53"))
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := resolver.LookupHost(ctx, "example.com"); err != nil {
		t.Fatalf("Pi-hole DNS query over %s failed: %v", network, err)
	}
}
