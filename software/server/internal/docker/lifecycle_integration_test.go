package docker

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

// Release validation enables this test explicitly. It proves Docker reserves a
// published port at start, not create: the stopped candidate can be completely
// created while the old generation is still serving the same host port.
func TestDockerCreatesStoppedSamePortCandidateBeforeCutover(t *testing.T) {
	if os.Getenv("KITPRO_DOCKER_INTEGRATION") != "1" {
		t.Skip("set KITPRO_DOCKER_INTEGRATION=1")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := New()
	suffix := fmt.Sprint(time.Now().UnixNano())
	network := "kitpro-contract-" + suffix
	oldName := network + "-old"
	newName := network + "-new"
	labels := map[string]string{"com.kitpro.managed": "true", "com.kitpro.instance": "contract"}
	if _, err = client.CreateLifecycleNetwork(ctx, network, labels); err != nil {
		t.Fatal(err)
	}
	defer client.RemoveLifecycleNetwork(context.Background(), network)
	plan := containers.ContainerPlan{Image: "nginx:1.27-alpine", Network: network, Labels: labels, PortBindings: map[string][]containers.PortBinding{"80/tcp": {{HostIP: "127.0.0.1", HostPort: fmt.Sprint(port)}}}}
	plan.Name = oldName
	oldID, err := client.CreateLifecycleContainer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer client.RemoveContainer(context.Background(), oldID)
	if err = client.StartContainer(ctx, oldID); err != nil {
		t.Fatal(err)
	}
	plan.Name = newName
	newID, err := client.CreateLifecycleContainer(ctx, plan)
	if err != nil {
		t.Fatalf("Docker rejected stopped same-port candidate: %v", err)
	}
	defer client.RemoveContainer(context.Background(), newID)
	observed, err := client.ObserveContainer(ctx, newID)
	if err != nil || observed.State != containers.RuntimeStopped {
		t.Fatalf("candidate state=%s err=%v", observed.State, err)
	}
	if err = client.StopContainer(ctx, oldID); err != nil {
		t.Fatal(err)
	}
	if err = client.StartContainer(ctx, newID); err != nil {
		t.Fatal(err)
	}
	observed, err = client.WaitContainer(ctx, newID, containers.RuntimeRunning, 10*time.Second)
	if err != nil || observed.State != containers.RuntimeRunning {
		t.Fatalf("candidate did not take over port: %v", err)
	}
	_ = client.StopContainer(context.Background(), newID)
}
