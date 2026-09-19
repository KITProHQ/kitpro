package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type recordingLifecycleRuntime struct {
	containers.LifecycleRuntime
	events []string
}

func (r *recordingLifecycleRuntime) StartContainer(ctx context.Context, id string) error {
	r.events = append(r.events, "start:"+id)
	return r.LifecycleRuntime.StartContainer(ctx, id)
}
func (r *recordingLifecycleRuntime) StopContainer(ctx context.Context, id string) error {
	r.events = append(r.events, "stop:"+id)
	return r.LifecycleRuntime.StopContainer(ctx, id)
}
func (r *recordingLifecycleRuntime) RemoveContainer(ctx context.Context, id string) error {
	r.events = append(r.events, "remove:"+id)
	return r.LifecycleRuntime.RemoveContainer(ctx, id)
}

// This opt-in acceptance check uses a small pinned local fixture to prove the
// coordinator's ordering and retention behavior against Docker rather than a
// mock. Release validation enables it explicitly on Debian and Arch VMs.
func TestDockerMultiComponentReplacementAndRetention(t *testing.T) {
	if os.Getenv("KITPRO_DOCKER_INTEGRATION") != "1" {
		t.Skip("set KITPRO_DOCKER_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingLifecycleRuntime{LifecycleRuntime: docker.New()}
	coordinator := helperops.Coordinator{DB: db}
	suffix := fmt.Sprint(time.Now().UnixNano())
	installation := "inst-docker" + suffix[len(suffix)-8:]
	image := "archlinux@sha256:82b1b08faae9d61e3e7e13d562f4d09114d939105b0d59ff34140f3bd418593a"
	dataRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(dataRoot, "preserve.txt"), []byte("persistent"), 0600); err != nil {
		t.Fatal(err)
	}

	makePlan := func(generation, expected int, operationID string, token int64) MultiPlan {
		network := fmt.Sprintf("kitpro-multi-contract-%s-g%d", suffix, generation)
		plan := MultiPlan{OperationID: operationID, InstallationID: installation, ApplicationID: "docker-contract", ReleaseID: "fixture", Generation: generation, ExpectedGeneration: expected, FencingToken: token, NetworkName: network, PlanHash: fmt.Sprintf("plan-%d", generation), TopologyHash: "db-web", DataPath: dataRoot, ExposureMode: "internal", RollbackSafe: true}
		for ordinal, id := range []string{"db", "web"} {
			depends := []string{}
			if id == "web" {
				depends = []string{"db"}
			}
			name := fmt.Sprintf("kitpro-multi-contract-%s-%s-g%d", suffix, id, generation)
			labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: installation, ownership.LabelResource: "application", "com.kitpro.runtime-generation": fmt.Sprint(generation), "com.kitpro.component": id}
			containerPlan := containers.ContainerPlan{Image: image, Name: name, Network: network, Labels: labels, Command: []string{"sleep", "300"}, RestartPolicy: "no", NetworkAliases: []string{id}, Storage: []containers.StorageMount{{HostPath: dataRoot, ContainerPath: "/kitpro-data"}}}
			plan.Components = append(plan.Components, MultiComponentPlan{ID: id, Image: image, ContainerName: name, DependsOn: depends, StartOrdinal: ordinal, Container: containerPlan})
		}
		return plan
	}
	t.Cleanup(func() {
		for generation := 1; generation <= 3; generation++ {
			for _, id := range []string{"web", "db"} {
				name := fmt.Sprintf("kitpro-multi-contract-%s-%s-g%d", suffix, id, generation)
				_ = runtime.StopContainer(context.Background(), name)
				_ = runtime.RemoveContainer(context.Background(), name)
			}
			_ = runtime.RemoveLifecycleNetwork(context.Background(), fmt.Sprintf("kitpro-multi-contract-%s-g%d", suffix, generation))
		}
	})

	begin := func(generation int) (protocol.Request, int64) {
		request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "UpdateApplication", OperationRevision: 1, InstanceID: installation, RuntimeGeneration: generation, ApplicationID: "docker-contract", ReleaseID: "fixture"}
		decision, beginErr := coordinator.Begin(ctx, request, 0, installation)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if beginErr = coordinator.AuthorizeMutation(ctx, request.OperationID, decision.FencingToken); beginErr != nil {
			t.Fatal(beginErr)
		}
		return request, decision.FencingToken
	}

	firstRequest, firstToken := begin(1)
	first := makePlan(1, 0, firstRequest.OperationID, firstToken)
	if _, err = (MultiRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Replace(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Complete(ctx, first.OperationID, firstToken, protocol.Response{OK: true}); err != nil {
		t.Fatal(err)
	}
	secondRequest, secondToken := begin(2)
	second := makePlan(2, 1, secondRequest.OperationID, secondToken)
	result, err := (MultiRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Replace(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 || result.CleanupDeferred {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err = coordinator.Complete(ctx, second.OperationID, secondToken, protocol.Response{OK: true, Result: result}); err != nil {
		t.Fatal(err)
	}
	active, err := (Store{DB: db}).LoadMultiGeneration(ctx, installation, 2)
	if err != nil {
		t.Fatal(err)
	}
	web := componentByID(active.Components, "web")
	if web == nil {
		t.Fatal("active web component missing from helper state")
	}
	if err = runtime.StopContainer(ctx, web.ContainerID); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := (Reconciler{Runtime: runtime, Store: Store{DB: db}}).Reconcile(ctx, installation)
	if err != nil || reconciliation.State != ReconciliationDegraded || reconciliation.RecommendedAction != RepairStartActive {
		t.Fatalf("manual-stop reconciliation=%#v err=%v", reconciliation, err)
	}
	repairRequest := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RepairInstallation", OperationRevision: 1, InstanceID: installation, RepairAction: RepairStartActive}
	repairDecision, err := coordinator.Begin(ctx, repairRequest, 0, installation)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(ctx, repairRequest.OperationID, repairDecision.FencingToken); err != nil {
		t.Fatal(err)
	}
	reconciliation, err = (Repairer{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Repair(ctx, installation, repairRequest.OperationID, repairDecision.FencingToken, repairRequest.RepairAction)
	if err != nil || reconciliation.State != ReconciliationConsistent {
		t.Fatalf("manual-stop repair=%#v err=%v", reconciliation, err)
	}
	if _, err = coordinator.Complete(ctx, repairRequest.OperationID, repairDecision.FencingToken, protocol.Response{OK: true, Result: reconciliation}); err != nil {
		t.Fatal(err)
	}
	thirdRequest, thirdToken := begin(3)
	third := makePlan(3, 2, thirdRequest.OperationID, thirdToken)
	thirdRunner := MultiRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "create" && component == "web" && point == "before_dispatch" {
			return errors.New("injected candidate preparation failure")
		}
		return nil
	}}
	if _, thirdErr := thirdRunner.Replace(ctx, third); thirdErr == nil {
		t.Fatal("expected candidate preparation failure")
	}
	failedNetwork, observeErr := runtime.ObserveNetwork(ctx, third.NetworkName)
	if observeErr != nil || failedNetwork.Exists {
		t.Fatalf("failed candidate network was not cleaned: %#v err=%v", failedNetwork, observeErr)
	}
	for _, component := range second.Components {
		observed, componentErr := runtime.ObserveContainer(ctx, component.ContainerName)
		if componentErr != nil || observed.State != containers.RuntimeRunning {
			t.Fatalf("active %s changed during failed preparation: state=%s err=%v", component.ID, observed.State, componentErr)
		}
	}

	old, err := (Store{DB: db}).LoadMultiGeneration(ctx, installation, 1)
	if err != nil || old.Status != "retained" {
		t.Fatalf("old generation=%#v err=%v", old, err)
	}
	for _, component := range old.Components {
		observed, observeErr := runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil || observed.State != containers.RuntimeStopped {
			t.Fatalf("retained %s state=%s err=%v", component.ID, observed.State, observeErr)
		}
	}
	if contents, readErr := os.ReadFile(filepath.Join(dataRoot, "preserve.txt")); readErr != nil || string(contents) != "persistent" {
		t.Fatalf("persistent data changed: %q err=%v", contents, readErr)
	}
	if !isSubsequence(runtime.events, []string{"stop:" + old.Components[1].ContainerID, "stop:" + old.Components[0].ContainerID}) {
		t.Fatalf("Docker stop order=%v", runtime.events)
	}

	// Cleanup is explicit and generation-scoped; no persistent path is removed.
	for _, generation := range []MultiGeneration{old} {
		for _, component := range reverseComponents(generation.Components) {
			_ = runtime.RemoveContainer(context.Background(), component.ContainerID)
		}
		_ = runtime.RemoveLifecycleNetwork(context.Background(), generation.NetworkName)
	}
	candidate, _ := (Store{DB: db}).LoadMultiGeneration(ctx, installation, 2)
	for _, component := range reverseComponents(candidate.Components) {
		_ = runtime.StopContainer(context.Background(), component.ContainerID)
		_ = runtime.RemoveContainer(context.Background(), component.ContainerID)
	}
	_ = runtime.RemoveLifecycleNetwork(context.Background(), candidate.NetworkName)
	for _, networkName := range []string{old.NetworkName, candidate.NetworkName} {
		observed, observeErr := runtime.ObserveNetwork(ctx, networkName)
		if observeErr != nil || observed.Exists {
			t.Fatalf("network %s was not cleaned: %#v err=%v", networkName, observed, observeErr)
		}
	}
	if contents, readErr := os.ReadFile(filepath.Join(dataRoot, "preserve.txt")); readErr != nil || string(contents) != "persistent" {
		t.Fatalf("cleanup changed persistent data: %q err=%v", contents, readErr)
	}
}

// This opt-in contract exercises ordinary single-component lifecycle changes
// through the same durable coordinator and typed runtime interface used in
// production. Persistent storage must remain outside runtime-only removal.
func TestDockerSingleStateOperationsPreserveStorage(t *testing.T) {
	if os.Getenv("KITPRO_DOCKER_INTEGRATION") != "1" {
		t.Skip("set KITPRO_DOCKER_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingLifecycleRuntime{LifecycleRuntime: docker.New()}
	coordinator := helperops.Coordinator{DB: db}
	suffix := fmt.Sprint(time.Now().UnixNano())
	installation := "inst-single" + suffix[len(suffix)-8:]
	network := "kitpro-single-contract-" + suffix
	containerName := network + "-g1"
	image := "archlinux@sha256:82b1b08faae9d61e3e7e13d562f4d09114d939105b0d59ff34140f3bd418593a"
	dataRoot := t.TempDir()
	marker := filepath.Join(dataRoot, "preserve.txt")
	if err = os.WriteFile(marker, []byte("persistent"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.StopContainer(context.Background(), containerName)
		_ = runtime.RemoveContainer(context.Background(), containerName)
		_ = runtime.RemoveLifecycleNetwork(context.Background(), network)
	})

	installRequest := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "InstallApplication", OperationRevision: 1, InstanceID: installation, RuntimeGeneration: 1, ApplicationID: "docker-contract", ReleaseID: "fixture"}
	installDecision, err := coordinator.Begin(ctx, installRequest, 0, installation)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(ctx, installRequest.OperationID, installDecision.FencingToken); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: installation, ownership.LabelResource: "application", "com.kitpro.runtime-generation": "1", "com.kitpro.operation": installRequest.OperationID}
	plan := Plan{OperationID: installRequest.OperationID, InstallationID: installation, ApplicationID: "docker-contract", ReleaseID: "fixture", Generation: 1, FencingToken: installDecision.FencingToken, Image: image, NetworkName: network, ContainerName: containerName, PlanHash: "single-state-contract", DataPath: dataRoot, ExposureMode: "internal", RollbackSafe: true, Container: containers.ContainerPlan{Image: image, Name: containerName, Network: network, Labels: labels, Command: []string{"sleep", "300"}, RestartPolicy: "no", Storage: []containers.StorageMount{{HostPath: dataRoot, ContainerPath: "/kitpro-data"}}}}
	installed, err := (Runner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Replace(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Complete(ctx, installRequest.OperationID, installDecision.FencingToken, protocol.Response{OK: true, Result: installed}); err != nil {
		t.Fatal(err)
	}

	operate := func(operation, action, want string) {
		t.Helper()
		request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: operation, OperationRevision: 1, InstanceID: installation, RuntimeGeneration: 1}
		decision, beginErr := coordinator.Begin(ctx, request, 0, installation)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if beginErr = coordinator.AuthorizeMutation(ctx, request.OperationID, decision.FencingToken); beginErr != nil {
			t.Fatal(beginErr)
		}
		result, operationErr := (SingleRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Operate(ctx, installation, request.OperationID, decision.FencingToken, action)
		if operationErr != nil || result.RuntimeState != want {
			t.Fatalf("%s result=%#v err=%v", action, result, operationErr)
		}
		if _, operationErr = coordinator.Complete(ctx, request.OperationID, decision.FencingToken, protocol.Response{OK: true, Result: result}); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	operate("StopApplication", "stop", "stopped")
	operate("StartApplication", "start", "running")
	operate("RestartApplication", "restart", "running")
	operate("RemoveApplication", "remove", "runtime_removed")
	observed, err := runtime.ObserveContainer(ctx, containerName)
	if err != nil || observed.Exists {
		t.Fatalf("runtime-only removal left container: %#v err=%v", observed, err)
	}
	if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "persistent" {
		t.Fatalf("runtime-only removal changed persistent data: %q err=%v", contents, readErr)
	}
}

func TestDockerMigratedRestartLoopReconcilesAndStopsExactlyOnce(t *testing.T) {
	if os.Getenv("KITPRO_DOCKER_INTEGRATION") != "1" {
		t.Skip("set KITPRO_DOCKER_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingLifecycleRuntime{LifecycleRuntime: docker.New()}
	coordinator := helperops.Coordinator{DB: db}
	suffix := fmt.Sprint(time.Now().UnixNano())
	installation := "inst-loop" + suffix[len(suffix)-8:]
	networkName := "kitpro-loop-contract-" + suffix
	containerName := networkName + "-g1"
	image := "archlinux@sha256:82b1b08faae9d61e3e7e13d562f4d09114d939105b0d59ff34140f3bd418593a"
	dataRoot := t.TempDir()
	marker := filepath.Join(dataRoot, "preserve.txt")
	if err = os.WriteFile(marker, []byte("persistent"), 0600); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: installation, ownership.LabelResource: "application", "com.kitpro.runtime-generation": "1"}
	if err = runtime.PullImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	network, err := runtime.CreateLifecycleNetwork(ctx, networkName, labels)
	if err != nil {
		t.Fatal(err)
	}
	plan := containers.ContainerPlan{Image: image, Name: containerName, Network: networkName, Labels: labels, Command: []string{"sh", "-c", "exit 1"}, RestartPolicy: "always", Storage: []containers.StorageMount{{HostPath: dataRoot, ContainerPath: "/kitpro-data"}}}
	containerID, err := runtime.CreateLifecycleContainer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.StopContainer(context.Background(), containerID)
		_ = runtime.RemoveContainer(context.Background(), containerID)
		_ = runtime.RemoveLifecycleNetwork(context.Background(), networkName)
	})
	if err = runtime.StartContainer(ctx, containerID); err != nil {
		t.Fatal(err)
	}
	var observed containers.ContainerObservation
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		observed, err = runtime.ObserveContainer(ctx, containerID)
		if err == nil && observed.State == containers.RuntimeRestarting {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || observed.State != containers.RuntimeRestarting {
		t.Fatalf("restart loop not observed: %#v err=%v", observed, err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES(?,1,'legacy-migration','docker-contract','fixture','verification_required',?,?,'legacy',?,'internal',?,'not_required')`, installation, networkName, network.ID, dataRoot, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at) VALUES(?,1,'app',?,?,?,?,?,'unknown',?)`, installation, containerName, containerID, image, observed.ImageID, observationHash(observed), now); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := (Reconciler{Runtime: runtime, Store: Store{DB: db}}).Reconcile(ctx, installation)
	if err != nil || reconciliation.State != ReconciliationDegraded || reconciliation.RuntimeState != "restarting" || reconciliation.RecommendedAction != RepairNone {
		t.Fatalf("reconciliation=%#v err=%v", reconciliation, err)
	}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "StopApplication", OperationRevision: 1, InstanceID: installation, RuntimeGeneration: 1}
	decision, err := coordinator.Begin(ctx, request, 0, installation)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(ctx, request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	before := len(runtime.events)
	result, err := (SingleRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Operate(ctx, installation, request.OperationID, decision.FencingToken, "stop")
	if err != nil || result.RuntimeState != "stopped" {
		t.Fatalf("stop result=%#v err=%v", result, err)
	}
	if _, err = coordinator.Complete(ctx, request.OperationID, decision.FencingToken, protocol.Response{OK: true, Result: result}); err != nil {
		t.Fatal(err)
	}
	stopped, err := runtime.ObserveContainer(ctx, containerID)
	if err != nil || stopped.State != containers.RuntimeStopped {
		t.Fatalf("post-stop observation=%#v err=%v", stopped, err)
	}
	replay, err := coordinator.Begin(ctx, request, 0, installation)
	if err != nil || replay.Execute || !replay.Response.OK || len(runtime.events) != before+1 {
		t.Fatalf("replay=%#v events=%v err=%v", replay, runtime.events[before:], err)
	}
	var status, componentState string
	if err = db.QueryRow(`SELECT g.status,c.state FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id=? AND g.runtime_generation=1`, installation).Scan(&status, &componentState); err != nil || status != "active" || componentState != "stopped" {
		t.Fatalf("status=%q component=%q err=%v", status, componentState, err)
	}
	if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "persistent" {
		t.Fatalf("persistent data changed: %q err=%v", contents, readErr)
	}
}

func TestDockerOrphanCandidateCleanupWithoutActiveGeneration(t *testing.T) {
	if os.Getenv("KITPRO_DOCKER_INTEGRATION") != "1" {
		t.Skip("set KITPRO_DOCKER_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	runtime := &recordingLifecycleRuntime{LifecycleRuntime: docker.New()}
	coordinator := helperops.Coordinator{DB: db}
	suffix := fmt.Sprint(time.Now().UnixNano())
	installation := "inst-orphan" + suffix[len(suffix)-8:]
	image := "archlinux@sha256:82b1b08faae9d61e3e7e13d562f4d09114d939105b0d59ff34140f3bd418593a"
	dataRoot := t.TempDir()
	marker := filepath.Join(dataRoot, "preserve.txt")
	if err = os.WriteFile(marker, []byte("persistent"), 0600); err != nil {
		t.Fatal(err)
	}

	begin := func(operation string, generation int) (protocol.Request, int64) {
		request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: operation, OperationRevision: 1, InstanceID: installation, RuntimeGeneration: generation, ApplicationID: "docker-contract", ReleaseID: "fixture"}
		decision, beginErr := coordinator.Begin(ctx, request, 0, installation)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if beginErr = coordinator.AuthorizeMutation(ctx, request.OperationID, decision.FencingToken); beginErr != nil {
			t.Fatal(beginErr)
		}
		return request, decision.FencingToken
	}
	makePlan := func(request protocol.Request, token int64, generation, expected int, command []string) Plan {
		networkName := fmt.Sprintf("kitpro-orphan-contract-%s-g%d", suffix, generation)
		containerName := fmt.Sprintf("kitpro-orphan-contract-%s-g%d", suffix, generation)
		labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: installation, ownership.LabelResource: "application", "com.kitpro.runtime-generation": fmt.Sprint(generation), "com.kitpro.operation": request.OperationID}
		container := containers.ContainerPlan{Image: image, Name: containerName, Network: networkName, Labels: labels, Command: command, RestartPolicy: "no", Storage: []containers.StorageMount{{HostPath: dataRoot, ContainerPath: "/kitpro-data"}}}
		return Plan{OperationID: request.OperationID, InstallationID: installation, ApplicationID: "docker-contract", ReleaseID: "fixture", Generation: generation, ExpectedGeneration: expected, FencingToken: token, Image: image, NetworkName: networkName, ContainerName: containerName, PlanHash: fmt.Sprintf("plan-%d-%s", generation, request.OperationID), DataPath: dataRoot, ExposureMode: "internal", Container: container}
	}
	t.Cleanup(func() {
		for generation := 1; generation <= 2; generation++ {
			name := fmt.Sprintf("kitpro-orphan-contract-%s-g%d", suffix, generation)
			_ = runtime.StopContainer(context.Background(), name)
			_ = runtime.RemoveContainer(context.Background(), name)
			_ = runtime.RemoveLifecycleNetwork(context.Background(), name)
		}
	})

	installRequest, installToken := begin("InstallApplication", 1)
	firstPlan := makePlan(installRequest, installToken, 1, 0, []string{"sleep", "300"})
	installed, err := (Runner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Replace(ctx, firstPlan)
	if err != nil || installed.Generation != 1 {
		t.Fatalf("initial install=%#v err=%v", installed, err)
	}
	if _, err = coordinator.Complete(ctx, installRequest.OperationID, installToken, protocol.Response{OK: true, Result: installed}); err != nil {
		t.Fatal(err)
	}

	removeRequest, removeToken := begin("RemoveApplication", 1)
	removed, err := (SingleRunner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Operate(ctx, installation, removeRequest.OperationID, removeToken, "remove")
	if err != nil || removed.RuntimeState != "runtime_removed" {
		t.Fatalf("remove=%#v err=%v", removed, err)
	}
	if _, err = coordinator.Complete(ctx, removeRequest.OperationID, removeToken, protocol.Response{OK: true, Result: removed}); err != nil {
		t.Fatal(err)
	}

	failedRequest, failedToken := begin("InstallApplication", 2)
	failedPlan := makePlan(failedRequest, failedToken, 2, 1, []string{"sh", "-c", "exit 1"})
	_, replaceErr := (Runner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator, Hook: func(_ context.Context, phase Phase) error {
		if phase == PhaseBeforeVerification {
			time.Sleep(500 * time.Millisecond)
		}
		return nil
	}}).Replace(ctx, failedPlan)
	if replaceErr == nil {
		t.Fatal("failing candidate unexpectedly committed")
	}
	if _, err = coordinator.Complete(ctx, failedRequest.OperationID, failedToken, protocol.Response{OK: false, Error: replaceErr.Error()}); err != nil {
		t.Fatal(err)
	}
	var active int
	if err = db.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id=? AND status='active'`, installation).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active generations=%d err=%v", active, err)
	}
	reconciliation, err := (Reconciler{Runtime: runtime, Store: Store{DB: db}}).Reconcile(ctx, installation)
	if err != nil || reconciliation.State != ReconciliationCleanupPending || reconciliation.RecommendedAction != RepairCleanupResources || reconciliation.CheckedGeneration != 2 {
		t.Fatalf("orphan reconciliation=%#v err=%v", reconciliation, err)
	}

	repairRequest, repairToken := begin("RepairInstallation", 2)
	repaired, err := (Repairer{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Repair(ctx, installation, repairRequest.OperationID, repairToken, RepairCleanupResources)
	if err != nil || repaired.State != ReconciliationConsistent || repaired.RuntimeState != "runtime_removed" || repaired.CheckedGeneration != 2 {
		t.Fatalf("cleanup repair=%#v err=%v", repaired, err)
	}
	if _, err = coordinator.Complete(ctx, repairRequest.OperationID, repairToken, protocol.Response{OK: true, Result: repaired}); err != nil {
		t.Fatal(err)
	}
	failedContainer, err := runtime.ObserveContainer(ctx, failedPlan.ContainerName)
	if err != nil || failedContainer.Exists {
		t.Fatalf("candidate container remained: %#v err=%v", failedContainer, err)
	}
	failedNetwork, err := runtime.ObserveNetwork(ctx, failedPlan.NetworkName)
	if err != nil || failedNetwork.Exists {
		t.Fatalf("candidate network remained: %#v err=%v", failedNetwork, err)
	}

	retryRequest, retryToken := begin("InstallApplication", 2)
	retryPlan := makePlan(retryRequest, retryToken, 2, 1, []string{"sleep", "300"})
	retried, err := (Runner{Runtime: runtime, Store: Store{DB: db}, Evidence: coordinator}).Replace(ctx, retryPlan)
	if err != nil || retried.Generation != 2 || retried.RuntimeState != "running" {
		t.Fatalf("retry install=%#v err=%v", retried, err)
	}
	if _, err = coordinator.Complete(ctx, retryRequest.OperationID, retryToken, protocol.Response{OK: true, Result: retried}); err != nil {
		t.Fatal(err)
	}
	if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "persistent" {
		t.Fatalf("persistent data changed: %q err=%v", contents, readErr)
	}
	var generation int
	var status string
	if err = db.QueryRow(`SELECT runtime_generation,status FROM runtime_generations WHERE installation_id=? AND status='active'`, installation).Scan(&generation, &status); err != nil || generation != 2 || status != "active" {
		t.Fatalf("final generation=%d status=%q err=%v", generation, status, err)
	}
}
