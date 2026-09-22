package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func newMultiHarness(t *testing.T, existing bool) (*Store, *fakeRuntime, helperops.Coordinator, MultiPlan) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	runtime := newFakeRuntime()
	ids := []string{"db", "redis", "worker", "web"}
	dependencies := map[string][]string{"db": {}, "redis": {"db"}, "worker": {"redis"}, "web": {"worker"}}
	expected := 0
	if existing {
		expected = 1
		labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-multi", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "1"}
		networkName := "kitpro-net-inst-multi-g1"
		runtime.networks[networkName] = containers.NetworkObservation{Exists: true, ID: "network-old", Name: networkName, Labels: labels}
		_, err = db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,topology_hash,data_path,exposure_mode,created_at,verified_at,committed_at,cleanup_state) VALUES('inst-multi',1,'seed','multi','old','active',?,'network-old','old-plan','topology','/data','internal','now','now','now','clean')`, networkName)
		if err != nil {
			t.Fatal(err)
		}
		for ordinal, id := range ids {
			name := fmt.Sprintf("kitpro-multi-inst-multi-%s-g1", id)
			image := "repo/" + id + "@sha256:old"
			componentLabels := cloneLabels(labels)
			componentLabels["com.kitpro.component"] = id
			containerPlan := containers.ContainerPlan{Image: image, Name: name, Network: networkName, Labels: componentLabels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data/" + id, ContainerPath: "/data"}}}
			containerID := "old-" + id
			runtime.containers[containerID] = observationFromPlan(containerID, containerPlan, "network-old", containers.RuntimeRunning)
			_, err = db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,dependencies_json,start_ordinal,created_at,verified_at) VALUES('inst-multi',1,?,?,?,?,'image-id',?,'running',?,?,'now','now')`, id, name, containerID, image, observationHash(runtime.containers[containerID]), jsonDependencies(dependencies[id]), ordinal)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	operationID := operations.NewID()
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: "UpdateApplication", OperationRevision: 1, InstanceID: "inst-multi", RuntimeGeneration: expected + 1, ApplicationID: "multi", ReleaseID: "new"}
	coordinator := helperops.Coordinator{DB: db}
	decision, err := coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), operationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	plan := MultiPlan{OperationID: operationID, InstallationID: "inst-multi", ApplicationID: "multi", ReleaseID: "new", Generation: expected + 1, ExpectedGeneration: expected, FencingToken: decision.FencingToken, NetworkName: fmt.Sprintf("kitpro-net-inst-multi-g%d", expected+1), PlanHash: "plan", TopologyHash: "topology", DataPath: "/data", ExposureMode: "internal"}
	for ordinal, id := range ids {
		name := fmt.Sprintf("kitpro-multi-inst-multi-%s-g%d", id, expected+1)
		labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-multi", ownership.LabelResource: "application", "com.kitpro.runtime-generation": fmt.Sprint(expected + 1), "com.kitpro.component": id}
		image := "repo/" + id + "@sha256:new"
		containerPlan := containers.ContainerPlan{Image: image, Name: name, Network: plan.NetworkName, Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data/" + id, ContainerPath: "/data"}}, NetworkAliases: []string{id}}
		plan.Components = append(plan.Components, MultiComponentPlan{ID: id, Image: image, ContainerName: name, DependsOn: dependencies[id], StartOrdinal: ordinal, Container: containerPlan})
	}
	return &Store{DB: db}, runtime, coordinator, plan
}

func TestMultiReplacementUsesDependencyOrderAndAtomicCommit(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	result, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Replace(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 || len(result.Components) != 4 {
		t.Fatalf("unexpected result: %#v", result)
	}
	var active, retained, activeComponents int
	if err = store.DB.QueryRow(`SELECT SUM(status='active'),SUM(status='retained') FROM runtime_generations WHERE installation_id='inst-multi'`).Scan(&active, &retained); err != nil {
		t.Fatal(err)
	}
	if err = store.DB.QueryRow(`SELECT COUNT(*) FROM runtime_components c JOIN runtime_generations g USING(installation_id,runtime_generation) WHERE g.installation_id='inst-multi' AND g.status='active' AND c.state='running'`).Scan(&activeComponents); err != nil {
		t.Fatal(err)
	}
	if active != 1 || retained != 1 || activeComponents != 4 {
		t.Fatalf("active=%d retained=%d components=%d", active, retained, activeComponents)
	}
	wantSequence := []string{"stop:old-web", "stop:old-worker", "stop:old-redis", "stop:old-db", "start:container-kitpro-multi-inst-multi-db-g2", "start:container-kitpro-multi-inst-multi-redis-g2", "start:container-kitpro-multi-inst-multi-worker-g2", "start:container-kitpro-multi-inst-multi-web-g2"}
	if !isSubsequence(runtime.mutations, wantSequence) {
		t.Fatalf("mutations do not preserve dependency order:\n%v", runtime.mutations)
	}
}

func TestPrepareMultiReusesCleanRemovedGeneration(t *testing.T) {
	store, _, _, plan := newMultiHarness(t, true)
	_, err := store.DB.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,topology_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES('inst-multi',2,'old-attempt','multi','new','removed','old-network','old-plan','topology','/data','internal','then','clean')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range plan.Components {
		_, err = store.DB.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,image_digest,state,dependencies_json,start_ordinal,created_at) VALUES('inst-multi',2,?,?,?,'removed',?,?, 'then')`, component.ID, "old-"+component.ID, component.Image, jsonDependencies(component.DependsOn), component.StartOrdinal)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = store.PrepareMulti(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	var operationID, status string
	var unknown int
	if err = store.DB.QueryRow(`SELECT creating_operation_id,status FROM runtime_generations WHERE installation_id='inst-multi' AND runtime_generation=2`).Scan(&operationID, &status); err != nil {
		t.Fatal(err)
	}
	if err = store.DB.QueryRow(`SELECT COUNT(*) FROM runtime_components WHERE installation_id='inst-multi' AND runtime_generation=2 AND state='unknown'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if operationID != plan.OperationID || status != "prepared" || unknown != len(plan.Components) {
		t.Fatalf("operation=%q status=%q unknown=%d", operationID, status, unknown)
	}
}

func TestMultiPreparationFailureLeavesActiveGenerationRunning(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "create" && component == "worker" && point == "before_dispatch" {
			return errors.New("injected create failure")
		}
		return nil
	}}
	if _, err := runner.Replace(context.Background(), plan); err == nil {
		t.Fatal("expected failure")
	}
	for _, id := range []string{"old-db", "old-redis", "old-worker", "old-web"} {
		if runtime.containers[id].State != containers.RuntimeRunning {
			t.Fatalf("%s was stopped", id)
		}
	}
	var active int
	if err := store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestMultiPartialStartIsDurableAndNeverCommits(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	plan.RollbackSafe = false
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "start" && component == "worker" && point == "before_dispatch" {
			return errors.New("injected start failure")
		}
		return nil
	}}
	_, err := runner.Replace(context.Background(), plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want action-required outcome, got %v", err)
	}
	var active int
	if err = store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, err)
	}
	steps, err := store.ComponentSteps(context.Background(), plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStep(steps, 2, "db", "start", "confirmed_applied") || !hasStep(steps, 2, "redis", "start", "confirmed_applied") || !hasStep(steps, 2, "worker", "start", "not_dispatched") || hasAction(steps, 2, "web", "start") {
		t.Fatalf("partial progress not represented: %#v", steps)
	}
}

func TestMultiCandidateStartFailureAtEveryPositionNeverCommits(t *testing.T) {
	for _, target := range []string{"db", "worker", "web"} {
		t.Run(target, func(t *testing.T) {
			store, runtime, coordinator, plan := newMultiHarness(t, true)
			runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
				if action == "start" && component == target && point == "before_dispatch" {
					return errors.New("injected start failure")
				}
				return nil
			}}
			if _, err := runner.Replace(context.Background(), plan); err == nil {
				t.Fatal("expected failure")
			}
			var active, candidateActive int
			if err := store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id='inst-multi' AND runtime_generation=2 AND status='active'`).Scan(&candidateActive); err != nil {
				t.Fatal(err)
			}
			if active != 1 || candidateActive != 0 {
				t.Fatalf("active=%d candidate_active=%d", active, candidateActive)
			}
		})
	}
}

func TestMultiTransportLossIsClassifiedByFreshObservation(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "start" && component == "worker" && point == "after_mutation_transport_error" {
			return errors.New("simulated response loss")
		}
		return nil
	}}
	if _, err := runner.Replace(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	steps, err := store.ComponentSteps(context.Background(), plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStep(steps, 2, "worker", "start", "confirmed_applied") {
		t.Fatalf("transport-loss outcome was not reconciled: %#v", steps)
	}
}

func TestMultiStopTransportLossUsesObservationAndNeverRepeats(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runtime.mutateFail["stop"] = errors.New("simulated stop response loss")
	if _, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Replace(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	steps, err := store.ComponentSteps(context.Background(), plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStep(steps, 1, "web", "stop", "confirmed_applied") {
		t.Fatalf("stop response loss was not classified: %#v", steps)
	}
	for _, id := range []string{"old-db", "old-redis", "old-worker", "old-web"} {
		count := 0
		for _, event := range runtime.mutations {
			if event == "stop:"+id {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s stop repeated %d times: %v", id, count, runtime.mutations)
		}
	}
}

func TestMultiUnknownStopOutcomeRequiresReconciliation(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "stop" && component == "web" && point == "after_dispatch" {
			runtime.fail["observe-old-web"] = errors.New("observation unavailable")
		}
		return nil
	}}
	_, err := runner.Replace(context.Background(), plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want reconciliation, got %v", err)
	}
	var active int
	if queryErr := store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); queryErr != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, queryErr)
	}
}

func TestMultiCommitRejectsPartialVerificationAndStaleFence(t *testing.T) {
	store, _, _, plan := newMultiHarness(t, false)
	if err := store.PrepareMulti(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordMultiNetwork(context.Background(), plan, "network-new"); err != nil {
		t.Fatal(err)
	}
	for index, component := range plan.Components {
		verified := index != len(plan.Components)-1
		state := "running"
		if !verified {
			state = "stopped"
		}
		if err := store.RecordMultiContainer(context.Background(), plan, component.ID, "id-"+component.ID, "image", "config", state, verified); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CommitMulti(context.Background(), plan); err == nil {
		t.Fatal("partial application generation committed")
	}
	if err := store.RecordMultiContainer(context.Background(), plan, "web", "id-web", "image", "config", "running", true); err != nil {
		t.Fatal(err)
	}
	stale := plan
	stale.FencingToken++
	if err := store.CommitMulti(context.Background(), stale); err == nil {
		t.Fatal("stale fence committed multi-component generation")
	}
	var active int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestMultiGenerationCommitFailureRequiresReconciliation(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	if _, err := store.DB.Exec(`CREATE TRIGGER reject_multi_generation_commit BEFORE UPDATE OF status ON runtime_generations WHEN NEW.status='active' AND NEW.runtime_generation=2 BEGIN SELECT RAISE(ABORT,'injected commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Replace(context.Background(), plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want reconciliation, got %v", err)
	}
	var active int
	if queryErr := store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); queryErr != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, queryErr)
	}
}

func TestOrdinaryMultiLifecycleUsesPersistedOrder(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}
	if _, err := runner.Operate(context.Background(), plan.InstallationID, plan.OperationID, plan.FencingToken, "stop"); err != nil {
		t.Fatal(err)
	}
	if !isSubsequence(runtime.mutations, []string{"stop:old-web", "stop:old-worker", "stop:old-redis", "stop:old-db"}) {
		t.Fatalf("stop order=%v", runtime.mutations)
	}
	if _, err := coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{OK: true}); err != nil {
		t.Fatal(err)
	}
	startRequest := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "StartApplication", OperationRevision: 1, InstanceID: plan.InstallationID, RuntimeGeneration: 1}
	decision, err := coordinator.Begin(context.Background(), startRequest, 0, plan.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), startRequest.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	runtime.mutations = nil
	if _, err = runner.Operate(context.Background(), plan.InstallationID, startRequest.OperationID, decision.FencingToken, "start"); err != nil {
		t.Fatal(err)
	}
	if !reflectStrings(runtime.mutations, []string{"start:old-db", "start:old-redis", "start:old-worker", "start:old-web"}) {
		t.Fatalf("start order=%v", runtime.mutations)
	}
}

func TestOrdinaryMultiRestartIsOneOrderedDurableOperation(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	result, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Operate(context.Background(), plan.InstallationID, plan.OperationID, plan.FencingToken, "restart")
	if err != nil || result.RuntimeState != "running" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	want := []string{"stop:old-web", "stop:old-worker", "stop:old-redis", "stop:old-db", "start:old-db", "start:old-redis", "start:old-worker", "start:old-web"}
	if !reflectStrings(runtime.mutations, want) {
		t.Fatalf("restart order=%v", runtime.mutations)
	}
	steps, err := store.ComponentSteps(context.Background(), plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStep(steps, 1, "web", "stop", "confirmed_applied") || !hasStep(steps, 1, "web", "start", "confirmed_applied") {
		t.Fatalf("restart evidence=%#v", steps)
	}
}

func TestMultiExactReplayDoesNotRepeatRuntimeMutations(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	result, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Replace(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{OK: true, Result: result}); err != nil {
		t.Fatal(err)
	}
	before := len(runtime.mutations)
	replay := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: plan.OperationID, Operation: "UpdateApplication", OperationRevision: 1, InstanceID: plan.InstallationID, RuntimeGeneration: plan.Generation, ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID}
	decision, err := coordinator.Begin(context.Background(), replay, 0, plan.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Execute || !decision.Response.OK || len(runtime.mutations) != before {
		t.Fatalf("replay executed=%v response=%#v mutations=%d/%d", decision.Execute, decision.Response, len(runtime.mutations), before)
	}
}

func TestMultiRecoveryForwardCommitsOnlyCompleteVerifiedGeneration(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "commit" && component == "application" && point == "before_dispatch" {
			return ErrSimulatedInterruption
		}
		return nil
	}}
	_, err := runner.Replace(context.Background(), plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want interrupted commit, got %v", err)
	}
	if _, err = coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{ErrorCode: "RecoveryRequired", Error: err.Error()}); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverInterrupted(context.Background(), *store, runtime)
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	var active int
	if err = store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestMultiRecoveryRefusesForwardCommitWhilePreviousComponentRuns(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "commit" && component == "application" && point == "before_dispatch" {
			return ErrSimulatedInterruption
		}
		return nil
	}}
	_, err := runner.Replace(context.Background(), plan)
	if err == nil {
		t.Fatal("expected interruption")
	}
	if _, err = coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{ErrorCode: "RecoveryRequired", Error: err.Error()}); err != nil {
		t.Fatal(err)
	}
	old := runtime.containers["old-web"]
	old.State = containers.RuntimeRunning
	runtime.containers["old-web"] = old
	recovered, err := RecoverInterrupted(context.Background(), *store, runtime)
	if err != nil || recovered != 0 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	var active int
	if err = store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestMultiCleanupFailureBecomesDebtAfterSuccessfulCommit(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-multi", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "0"}
	if _, err := store.DB.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,topology_hash,data_path,exposure_mode,created_at,verified_at,committed_at,cleanup_state) VALUES('inst-multi',0,'older','multi','older','retained','old-network','old-network-id','old','topology','/data','internal','now','now','now','not_required')`); err != nil {
		t.Fatal(err)
	}
	runtime.networks["old-network"] = containers.NetworkObservation{Exists: true, ID: "old-network-id", Name: "old-network", Labels: labels}
	for ordinal, id := range []string{"db", "redis", "worker", "web"} {
		name := "older-" + id
		image := "repo/" + id + "@sha256:older"
		componentLabels := cloneLabels(labels)
		componentLabels["com.kitpro.component"] = id
		containerPlan := containers.ContainerPlan{Image: image, Name: name, Network: "old-network", Labels: componentLabels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data/" + id, ContainerPath: "/data"}}}
		runtimeID := "older-" + id + "-id"
		runtime.containers[runtimeID] = observationFromPlan(runtimeID, containerPlan, "old-network-id", containers.RuntimeStopped)
		dependencies := map[string]string{"db": "[]", "redis": `["db"]`, "worker": `["redis"]`, "web": `["worker"]`}[id]
		if _, err := store.DB.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,dependencies_json,start_ordinal,created_at,verified_at) VALUES('inst-multi',0,?,?,?,?, 'image',?,'stopped',?,?,'now','now')`, id, name, runtimeID, image, observationHash(runtime.containers[runtimeID]), dependencies, ordinal); err != nil {
			t.Fatal(err)
		}
	}
	runner := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "cleanup" && point == "before_dispatch" {
			return errors.New("injected cleanup outage")
		}
		return nil
	}}
	result, err := runner.Replace(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CleanupDeferred {
		t.Fatal("cleanup debt was not reported")
	}
	var status, cleanup string
	if err = store.DB.QueryRow(`SELECT status,cleanup_state FROM runtime_generations WHERE installation_id='inst-multi' AND runtime_generation=0`).Scan(&status, &cleanup); err != nil {
		t.Fatal(err)
	}
	if status != "cleanup_pending" || cleanup != "pending" {
		t.Fatalf("status=%s cleanup=%s", status, cleanup)
	}
}

func TestCleanFailedCandidateCanRetrySameTargetGeneration(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	first := MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator, ComponentHook: func(_ context.Context, action, component, point string) error {
		if action == "create" && component == "worker" && point == "before_dispatch" {
			return errors.New("injected preparation failure")
		}
		return nil
	}}
	_, firstErr := first.Replace(context.Background(), plan)
	if firstErr == nil {
		t.Fatal("expected first attempt to fail")
	}
	if _, err := coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{Error: firstErr.Error()}); err != nil {
		t.Fatal(err)
	}
	retryRequest := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "UpdateApplication", OperationRevision: 1, InstanceID: plan.InstallationID, RuntimeGeneration: plan.Generation, ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID}
	decision, err := coordinator.Begin(context.Background(), retryRequest, 0, plan.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), retryRequest.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	plan.OperationID, plan.FencingToken = retryRequest.OperationID, decision.FencingToken
	for index := range plan.Components {
		plan.Components[index].Container.Labels["com.kitpro.operation"] = retryRequest.OperationID
	}
	result, err := (MultiRunner{Runtime: runtime, Store: *store, Evidence: coordinator}).Replace(context.Background(), plan)
	if err != nil || result.Generation != 2 {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	var active int
	if err = store.DB.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-multi' AND status='active'`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func cloneLabels(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		out[key] = value
	}
	return out
}
func jsonDependencies(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	result := "["
	for index, value := range values {
		if index > 0 {
			result += ","
		}
		result += `"` + value + `"`
	}
	return result + "]"
}
func isSubsequence(values, wanted []string) bool {
	index := 0
	for _, value := range values {
		if index < len(wanted) && value == wanted[index] {
			index++
		}
	}
	return index == len(wanted)
}
func hasStep(steps []ComponentStep, generation int, component, action, confidence string) bool {
	for _, step := range steps {
		if step.Generation == generation && step.ComponentID == component && step.Action == action && step.OutcomeConfidence == confidence {
			return true
		}
	}
	return false
}
func hasAction(steps []ComponentStep, generation int, component, action string) bool {
	for _, step := range steps {
		if step.Generation == generation && step.ComponentID == component && step.Action == action {
			return true
		}
	}
	return false
}
func reflectStrings(values, wanted []string) bool {
	if len(values) != len(wanted) {
		return false
	}
	for index := range values {
		if values[index] != wanted[index] {
			return false
		}
	}
	return true
}
