package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

type identityRuntime struct {
	*fakeRuntime
	err error
}

func (r identityRuntime) ObserveRuntimeIdentity(context.Context) (containers.RuntimeIdentityObservation, error) {
	if r.err != nil {
		return containers.RuntimeIdentityObservation{}, r.err
	}
	return containers.RuntimeIdentityObservation{Name: "docker", ID: "daemon-one", Version: "test"}, nil
}

func finishHarnessOperation(t *testing.T, h harness) {
	t.Helper()
	if _, err := h.coordinator.Complete(context.Background(), h.plan.OperationID, h.plan.FencingToken, protocol.Response{OK: false, Error: "test setup"}); err != nil {
		t.Fatal(err)
	}
}

func TestReconciliationDistinguishesExactStoppedMissingDriftAndRuntimeUnavailable(t *testing.T) {
	h := newHarness(t, true)
	finishHarnessOperation(t, h)
	reconciler := Reconciler{Runtime: identityRuntime{fakeRuntime: h.runtime}, Store: Store{DB: h.db}}

	result, err := reconciler.Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationConsistent || result.RuntimeIdentity == "" {
		t.Fatalf("exact result=%#v err=%v", result, err)
	}
	drifted := h.runtime.containers["old-id"]
	drifted.User = "1234:1234"
	h.runtime.containers["old-id"] = drifted
	result, err = reconciler.Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationActionRequired || !hasMismatch(result, MismatchConfiguration) {
		t.Fatalf("configuration drift result=%#v err=%v", result, err)
	}
	drifted.User = ""
	h.runtime.containers["old-id"] = drifted

	old := h.runtime.containers["old-id"]
	old.State = containers.RuntimeStopped
	h.runtime.containers["old-id"] = old
	result, err = reconciler.Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationRepairable || result.RecommendedAction != RepairStartActive {
		t.Fatalf("stopped result=%#v err=%v", result, err)
	}

	delete(h.runtime.containers, "old-id")
	result, err = reconciler.Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationRuntimeMissing || result.RecommendedAction != RepairRecreateGeneration {
		t.Fatalf("missing result=%#v err=%v", result, err)
	}

	foreign := old
	foreign.ID = "foreign-id"
	h.runtime.containers[foreign.ID] = foreign
	result, err = reconciler.Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationActionRequired || !hasMismatch(result, MismatchOwnershipAmbiguous) {
		t.Fatalf("foreign result=%#v err=%v", result, err)
	}

	result, err = (Reconciler{Runtime: identityRuntime{fakeRuntime: h.runtime, err: errors.New("daemon down")}, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationRuntimeUnknown || !hasMismatch(result, MismatchRuntimeUnreachable) {
		t.Fatalf("unavailable result=%#v err=%v", result, err)
	}
}

func TestReconciliationClassifiesExactUnstableRuntimeAsDegraded(t *testing.T) {
	for _, state := range []containers.RuntimeState{containers.RuntimeState("restarting"), containers.RuntimeState("paused")} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t, true)
			finishHarnessOperation(t, h)
			observed := h.runtime.containers["old-id"]
			observed.State = state
			h.runtime.containers["old-id"] = observed
			result, err := (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
			if err != nil || result.State != ReconciliationDegraded || result.RuntimeState != string(state) || result.RecommendedAction != RepairNone || !hasMismatch(result, MismatchActiveContainerUnstable) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestReconciliationBlocksUnclassifiedRuntimeState(t *testing.T) {
	h := newHarness(t, true)
	finishHarnessOperation(t, h)
	observed := h.runtime.containers["old-id"]
	observed.State = containers.RuntimeUnknown
	h.runtime.containers["old-id"] = observed
	result, err := (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationActionRequired || result.RuntimeState != "unknown" || result.RecommendedAction != RepairNone || !hasMismatch(result, MismatchRuntimeStateUnknown) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestExactStoppedRepairKeepsGenerationAndExactReplayDoesNotMutate(t *testing.T) {
	h := newHarness(t, true)
	finishHarnessOperation(t, h)
	old := h.runtime.containers["old-id"]
	old.State = containers.RuntimeStopped
	h.runtime.containers["old-id"] = old
	h.runtime.mutateFail["start:old-id"] = errors.New("start response lost")

	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RepairInstallation", OperationRevision: 1, InstanceID: "inst-one", RepairAction: RepairStartActive}
	decision, err := h.coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	result, err := (Repairer{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Repair(context.Background(), request.InstanceID, request.OperationID, decision.FencingToken, request.RepairAction)
	if err != nil || result.State != ReconciliationConsistent {
		t.Fatalf("repair result=%#v err=%v", result, err)
	}
	if result.CheckedGeneration != 1 || h.runtime.containers["old-id"].State != containers.RuntimeRunning {
		t.Fatalf("repair changed generation or failed to start: %#v", result)
	}
	if _, err = h.coordinator.Complete(context.Background(), request.OperationID, decision.FencingToken, protocol.Response{OK: true, Result: result}); err != nil {
		t.Fatal(err)
	}
	mutations := len(h.runtime.mutations)
	replay := request
	replay.ID = operations.NewRequestID()
	replayed, err := h.coordinator.Begin(context.Background(), replay, 0, replay.InstanceID)
	if err != nil || replayed.Execute || !replayed.Response.OK {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	if len(h.runtime.mutations) != mutations {
		t.Fatal("exact repair replay repeated runtime mutation")
	}
}

func TestMissingRuntimeControlledRecreateAdvancesGeneration(t *testing.T) {
	h := newHarness(t, true)
	delete(h.runtime.containers, "old-id")
	h.plan.AllowMissingActive = true
	h.plan.RollbackSafe = false
	result, err := (Runner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation=%d", result.Generation)
	}
	var active int
	if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestRetainedGenerationLossAndUnexpectedRunningAreClassifiedWithoutChangingActive(t *testing.T) {
	h := newHarness(t, true)
	if _, err := (Runner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Replace(context.Background(), h.plan); err != nil {
		t.Fatal(err)
	}
	result, err := (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range result.Components {
		if component.Role == "retained" && component.ExpectedRuntime != "stopped" {
			t.Fatalf("retained expected runtime=%q", component.ExpectedRuntime)
		}
	}
	delete(h.runtime.containers, "old-id")
	result, err = (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationDegraded || !hasMismatch(result, MismatchRetainedGenerationMissing) || result.CheckedGeneration != 2 {
		t.Fatalf("missing retained result=%#v err=%v", result, err)
	}
	var active int
	if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active=%d err=%v", active, err)
	}

	labels := map[string]string{"com.kitpro.managed": "true", "com.kitpro.instance": "inst-one", "com.kitpro.resource": "application", "com.kitpro.runtime-generation": "1"}
	oldPlan := containers.ContainerPlan{Image: "repo/app@sha256:old", Name: "kitpro-app-inst-one-g1", Network: "kitpro-net-inst-one-g1", Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data", ContainerPath: "/data"}}}
	h.runtime.containers["old-id"] = observationFromPlan("old-id", oldPlan, "network-old", containers.RuntimeRunning)
	result, err = (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil || result.State != ReconciliationActionRequired || !hasMismatch(result, MismatchRetainedGenerationRunning) {
		t.Fatalf("running retained result=%#v err=%v", result, err)
	}
}

func TestReconciliationAcceptsStoppedCandidateBeforeFirstNetworkAttachment(t *testing.T) {
	h := newHarness(t, true)
	runner := Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator, Hook: func(_ context.Context, phase Phase) error {
		if phase == PhaseAfterOldStop {
			return ErrSimulatedInterruption
		}
		return nil
	}}
	if _, err := runner.Replace(context.Background(), h.plan); err == nil {
		t.Fatal("expected interruption")
	}
	candidateID := "container-" + h.plan.ContainerName
	candidate := h.runtime.containers[candidateID]
	candidate.NetworkMode = h.plan.NetworkName
	candidate.Networks[h.plan.NetworkName] = containers.NetworkAttachment{}
	h.runtime.containers[candidateID] = candidate
	if _, err := h.coordinator.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := (Reconciler{Runtime: h.runtime, Store: h.Store()}).Reconcile(context.Background(), h.plan.InstallationID)
	if err != nil || result.State != ReconciliationRepairable || result.RecommendedAction != RepairStartActive || hasMismatch(result, MismatchNetwork) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestMultiComponentReconciliationObservesWholeGenerationAndRepairsInOrder(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	if _, err := coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{OK: false, Error: "test setup"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-db", "old-redis"} {
		observed := runtime.containers[id]
		observed.State = containers.RuntimeStopped
		runtime.containers[id] = observed
	}
	result, err := (Reconciler{Runtime: runtime, Store: *store}).Reconcile(context.Background(), "inst-multi")
	if err != nil || result.State != ReconciliationDegraded || !hasMismatch(result, MismatchDependencyState) || len(result.Components) != 4 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RepairInstallation", OperationRevision: 1, InstanceID: "inst-multi", RepairAction: RepairStartActive}
	decision, err := coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	before := len(runtime.mutations)
	result, err = (Repairer{Runtime: runtime, Store: *store, Evidence: coordinator}).Repair(context.Background(), request.InstanceID, request.OperationID, decision.FencingToken, request.RepairAction)
	if err != nil || result.State != ReconciliationConsistent {
		t.Fatalf("repair result=%#v err=%v", result, err)
	}
	if !isSubsequence(runtime.mutations[before:], []string{"start:old-db", "start:old-redis"}) {
		t.Fatalf("repair order=%v", runtime.mutations[before:])
	}
}

func TestMultiComponentRepairStopsAfterMiddleComponentFailure(t *testing.T) {
	store, runtime, coordinator, plan := newMultiHarness(t, true)
	if _, err := coordinator.Complete(context.Background(), plan.OperationID, plan.FencingToken, protocol.Response{OK: false, Error: "test setup"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-db", "old-redis"} {
		observed := runtime.containers[id]
		observed.State = containers.RuntimeStopped
		runtime.containers[id] = observed
	}
	runtime.fail["start:old-redis"] = errors.New("injected middle component failure")
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RepairInstallation", OperationRevision: 1, InstanceID: "inst-multi", RepairAction: RepairStartActive}
	decision, err := coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	_, err = (Repairer{Runtime: runtime, Store: *store, Evidence: coordinator}).Repair(context.Background(), request.InstanceID, request.OperationID, decision.FencingToken, request.RepairAction)
	if err == nil {
		t.Fatal("expected injected repair failure")
	}
	if runtime.containers["old-db"].State != containers.RuntimeRunning || runtime.containers["old-redis"].State != containers.RuntimeStopped {
		t.Fatalf("unexpected partial state db=%s redis=%s", runtime.containers["old-db"].State, runtime.containers["old-redis"].State)
	}
	if containsString(runtime.mutations, "start:old-worker") || containsString(runtime.mutations, "start:old-web") {
		t.Fatalf("repair continued past failure: %v", runtime.mutations)
	}
}

func TestCleanupDebtRemovesOnlyExactNonActiveRuntimeAndNeverStorage(t *testing.T) {
	h := newHarness(t, true)
	finishHarnessOperation(t, h)
	_, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES('inst-one',0,'failed-op','app','old','cleanup_pending','candidate-network','candidate-network-id','candidate-plan','/persistent/data','internal','now','pending')`)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"com.kitpro.managed": "true", "com.kitpro.instance": "inst-one", "com.kitpro.runtime-generation": "0"}
	plan := containers.ContainerPlan{Image: "repo/app@sha256:old", Name: "candidate-name", Network: "candidate-network", Labels: labels}
	h.runtime.networks[plan.Network] = containers.NetworkObservation{Exists: true, ID: "candidate-network-id", Name: plan.Network, Labels: labels}
	h.runtime.containers["candidate-id"] = observationFromPlan("candidate-id", plan, "", containers.RuntimeStopped)
	_, err = h.db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at,verified_at) VALUES('inst-one',0,'app','candidate-name','candidate-id',?,'image-id',?,'stopped','now','now')`, plan.Image, observationHash(h.runtime.containers["candidate-id"]))
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Reconciler{Runtime: h.runtime, Store: Store{DB: h.db}}).Reconcile(context.Background(), "inst-one")
	if err != nil || result.RecommendedAction != RepairCleanupResources {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	// Simulate a lost successful remove response. Fresh observation must prove
	// absence and prevent a second destructive dispatch.
	h.runtime.mutateFail["remove:candidate-id"] = errors.New("remove response lost")

	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: "RepairInstallation", OperationRevision: 1, InstanceID: "inst-one", RepairAction: RepairCleanupResources}
	decision, err := h.coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	result, err = (Repairer{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Repair(context.Background(), request.InstanceID, request.OperationID, decision.FencingToken, request.RepairAction)
	if err != nil || result.State != ReconciliationConsistent {
		t.Fatalf("cleanup result=%#v err=%v", result, err)
	}
	var dataPath, status string
	if err = h.db.QueryRow(`SELECT data_path,status FROM runtime_generations WHERE installation_id='inst-one' AND runtime_generation=0`).Scan(&dataPath, &status); err != nil || dataPath != "/persistent/data" || status != "removed" {
		t.Fatalf("data=%q status=%q err=%v", dataPath, status, err)
	}
}

func hasMismatch(result ReconciliationResult, want MismatchCode) bool {
	for _, code := range result.MismatchCodes {
		if code == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
