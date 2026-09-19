package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type fakeRuntime struct {
	images     map[string]containers.ImageObservation
	networks   map[string]containers.NetworkObservation
	containers map[string]containers.ContainerObservation
	mutations  []string
	fail       map[string]error
	mutateFail map[string]error
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{images: map[string]containers.ImageObservation{}, networks: map[string]containers.NetworkObservation{}, containers: map[string]containers.ContainerObservation{}, fail: map[string]error{}, mutateFail: map[string]error{}}
}
func (f *fakeRuntime) PullImage(_ context.Context, image string) error {
	f.mutations = append(f.mutations, "pull")
	if err := f.fail["pull"]; err != nil {
		return err
	}
	f.images[image] = containers.ImageObservation{Exists: true, ID: "image-new", RepoDigests: []string{image}}
	return f.mutateFail["pull"]
}
func (f *fakeRuntime) ObserveImage(_ context.Context, image string) (containers.ImageObservation, error) {
	return f.images[image], nil
}
func (f *fakeRuntime) CreateLifecycleNetwork(_ context.Context, name string, labels map[string]string) (containers.NetworkObservation, error) {
	f.mutations = append(f.mutations, "create-network")
	if err := f.fail["create-network"]; err != nil {
		return containers.NetworkObservation{}, err
	}
	n := containers.NetworkObservation{Exists: true, ID: "network-" + name, Name: name, Labels: labels}
	f.networks[name] = n
	return n, f.mutateFail["create-network"]
}
func (f *fakeRuntime) ObserveNetwork(_ context.Context, name string) (containers.NetworkObservation, error) {
	return f.networks[name], nil
}
func (f *fakeRuntime) CreateLifecycleContainer(_ context.Context, p containers.ContainerPlan) (string, error) {
	f.mutations = append(f.mutations, "create-container")
	if err := f.fail["create-container"]; err != nil {
		return "", err
	}
	id := "container-" + p.Name
	f.containers[id] = observationFromPlan(id, p, f.networks[p.Network].ID, containers.RuntimeStopped)
	return id, f.mutateFail["create-container"]
}
func (f *fakeRuntime) ObserveContainer(_ context.Context, id string) (containers.ContainerObservation, error) {
	if err := f.fail["observe-"+id]; err != nil {
		return containers.ContainerObservation{}, err
	}
	o, ok := f.containers[id]
	if !ok {
		for _, candidate := range f.containers {
			if candidate.Name == id {
				return candidate, nil
			}
		}
	}
	if !ok {
		return containers.ContainerObservation{State: containers.RuntimeMissing}, nil
	}
	return o, nil
}
func (f *fakeRuntime) StartContainer(_ context.Context, id string) error {
	f.mutations = append(f.mutations, "start:"+id)
	if err := f.fail["start:"+id]; err != nil {
		return err
	}
	if err := f.fail["start"]; err != nil {
		delete(f.fail, "start")
		return err
	}
	o := f.containers[id]
	o.State = containers.RuntimeRunning
	f.containers[id] = o
	if err := f.mutateFail["start:"+id]; err != nil {
		return err
	}
	return f.mutateFail["start"]
}
func (f *fakeRuntime) StopContainer(_ context.Context, id string) error {
	f.mutations = append(f.mutations, "stop:"+id)
	if err := f.fail["stop:"+id]; err != nil {
		return err
	}
	if err := f.fail["stop"]; err != nil {
		return err
	}
	o := f.containers[id]
	o.State = containers.RuntimeStopped
	f.containers[id] = o
	if err := f.mutateFail["stop:"+id]; err != nil {
		return err
	}
	return f.mutateFail["stop"]
}
func (f *fakeRuntime) RemoveContainer(_ context.Context, id string) error {
	f.mutations = append(f.mutations, "remove:"+id)
	if err := f.fail["remove:"+id]; err != nil {
		return err
	}
	if err := f.fail["remove"]; err != nil {
		return err
	}
	delete(f.containers, id)
	if err := f.mutateFail["remove:"+id]; err != nil {
		return err
	}
	return f.mutateFail["remove"]
}
func (f *fakeRuntime) RemoveLifecycleNetwork(_ context.Context, name string) error {
	f.mutations = append(f.mutations, "remove-network:"+name)
	delete(f.networks, name)
	return f.fail["remove-network"]
}
func (f *fakeRuntime) WaitContainer(ctx context.Context, id string, want containers.RuntimeState, _ time.Duration) (containers.ContainerObservation, error) {
	o, e := f.ObserveContainer(ctx, id)
	if e == nil && o.State != want {
		e = errors.New("wrong state")
	}
	return o, e
}

func observationFromPlan(id string, p containers.ContainerPlan, networkID string, state containers.RuntimeState) containers.ContainerObservation {
	mounts := make([]containers.MountObservation, 0, len(p.Storage))
	for _, m := range p.Storage {
		mounts = append(mounts, containers.MountObservation{Source: m.HostPath, Destination: m.ContainerPath, ReadOnly: m.ReadOnly})
	}
	return containers.ContainerObservation{Exists: true, ID: id, Name: p.Name, ImageID: "image-id", ImageReference: p.Image, State: state, Labels: p.Labels, User: p.User, Command: p.Command, Environment: p.Environment, RestartPolicy: p.RestartPolicy, Mounts: mounts, Networks: map[string]containers.NetworkAttachment{p.Network: {NetworkID: networkID}}, NetworkMode: p.Network, PortBindings: p.PortBindings, Devices: p.Devices, DeviceRequests: p.DeviceRequests}
}

type harness struct {
	db          *sql.DB
	runtime     *fakeRuntime
	coordinator helperops.Coordinator
	plan        Plan
}

func newHarness(t *testing.T, existing bool) harness {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	rt := newFakeRuntime()
	expected := 0
	if existing {
		expected = 1
		labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-one", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "1"}
		oldPlan := containers.ContainerPlan{Image: "repo/app@sha256:old", Name: "kitpro-app-inst-one-g1", Network: "kitpro-net-inst-one-g1", Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data", ContainerPath: "/data"}}}
		rt.networks[oldPlan.Network] = containers.NetworkObservation{Exists: true, ID: "network-old", Name: oldPlan.Network, Labels: labels}
		rt.containers["old-id"] = observationFromPlan("old-id", oldPlan, "network-old", containers.RuntimeRunning)
		_, err = db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,verified_at,committed_at,cleanup_state) VALUES('inst-one',1,'seed','app','old','active',?,'network-old','seed','/data','internal','now','now','now','clean')`, oldPlan.Network)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at,verified_at) VALUES('inst-one',1,'app',?,'old-id',?,'image-id',?,'running','now','now')`, oldPlan.Name, oldPlan.Image, observationHash(rt.containers["old-id"]))
		if err != nil {
			t.Fatal(err)
		}
	}
	op := operations.NewID()
	req := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: op, Operation: "InstallApplication", OperationRevision: 1, InstanceID: "inst-one", RuntimeGeneration: expected + 1, Image: "repo/app@sha256:new", ApplicationID: "app", ReleaseID: "new", NetworkName: "kitpro-net-inst-one-g2", DataPath: "/data", RestartPolicy: "unless-stopped", ExposureMode: "internal"}
	coord := helperops.Coordinator{DB: db}
	decision, err := coord.Begin(context.Background(), req, 0, req.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coord.AuthorizeMutation(context.Background(), op, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-one", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "2", "com.kitpro.operation": op}
	plan := Plan{OperationID: op, InstallationID: "inst-one", ApplicationID: "app", ReleaseID: "new", Generation: expected + 1, ExpectedGeneration: expected, FencingToken: decision.FencingToken, Image: req.Image, NetworkName: req.NetworkName, ContainerName: "kitpro-app-inst-one-g2", PlanHash: "plan", DataPath: "/data", ExposureMode: "internal", Container: containers.ContainerPlan{Image: req.Image, Name: "kitpro-app-inst-one-g2", Network: req.NetworkName, Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data", ContainerPath: "/data"}}}}
	return harness{db: db, runtime: rt, coordinator: coord, plan: plan}
}

func TestReplacementCommitsOneActiveAndRetainsPrevious(t *testing.T) {
	h := newHarness(t, true)
	result, err := (Runner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation %d", result.Generation)
	}
	var active, retained int
	if err = h.db.QueryRow(`SELECT SUM(status='active'),SUM(status='retained') FROM runtime_generations WHERE installation_id='inst-one'`).Scan(&active, &retained); err != nil {
		t.Fatal(err)
	}
	if active != 1 || retained != 1 {
		t.Fatalf("active=%d retained=%d", active, retained)
	}
	if h.runtime.containers["old-id"].State != containers.RuntimeStopped {
		t.Fatal("old generation was not retained stopped")
	}
}

func TestPrepareReusesOnlyCleanRemovedGeneration(t *testing.T) {
	h := newHarness(t, true)
	store := Store{DB: h.db}
	_, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES('inst-one',2,'old-attempt','app','new','removed','old-network','old-plan','/data','internal','then','clean')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,image_digest,state,created_at) VALUES('inst-one',2,'app','old-container','repo/app@sha256:old','removed','then')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Prepare(context.Background(), h.plan); err != nil {
		t.Fatal(err)
	}
	var operationID, status, containerName, componentState string
	if err = h.db.QueryRow(`SELECT g.creating_operation_id,g.status,c.container_name,c.state FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id='inst-one' AND g.runtime_generation=2`).Scan(&operationID, &status, &containerName, &componentState); err != nil {
		t.Fatal(err)
	}
	if operationID != h.plan.OperationID || status != "prepared" || containerName != h.plan.ContainerName || componentState != "unknown" {
		t.Fatalf("operation=%q status=%q container=%q component=%q", operationID, status, containerName, componentState)
	}
}

func TestReplacementRetriesCleanRemovedCandidateWithoutActiveGeneration(t *testing.T) {
	h := newHarness(t, true)
	if _, err := h.db.Exec(`UPDATE runtime_generations SET status='removed',cleanup_state='clean' WHERE installation_id='inst-one' AND runtime_generation=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE runtime_components SET state='removed' WHERE installation_id='inst-one' AND runtime_generation=1`); err != nil {
		t.Fatal(err)
	}
	delete(h.runtime.containers, "old-id")
	delete(h.runtime.networks, "kitpro-net-inst-one-g1")
	if _, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES('inst-one',2,'failed-install','app','new','removed','failed-network','failed-network-id','failed-plan','/data','internal','now','clean')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at,verified_at) VALUES('inst-one',2,'app','failed-container','failed-id',?,'image-id','failed-hash','removed','now','now')`, h.plan.Image); err != nil {
		t.Fatal(err)
	}
	result, err := (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	if err != nil || result.Generation != 2 || result.RuntimeState != "running" {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	var generation int
	var status string
	if err = h.db.QueryRow(`SELECT runtime_generation,status FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&generation, &status); err != nil || generation != 2 || status != "active" {
		t.Fatalf("generation=%d status=%q err=%v", generation, status, err)
	}
}

func TestPrepareRejectsUnresolvedGeneration(t *testing.T) {
	h := newHarness(t, true)
	_, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,data_path,exposure_mode,created_at,cleanup_state) VALUES('inst-one',2,'old-attempt','app','new','failed','old-network','old-plan','/data','internal','then','pending')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = (Store{DB: h.db}).Prepare(context.Background(), h.plan); err == nil {
		t.Fatal("unresolved generation was reused")
	}
}

func TestPreparationFailuresNeverStopOrAdvanceActive(t *testing.T) {
	for _, failure := range []string{"pull", "create-network", "create-container"} {
		t.Run(failure, func(t *testing.T) {
			h := newHarness(t, true)
			h.runtime.fail[failure] = errors.New("injected")
			_, err := (Runner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Replace(context.Background(), h.plan)
			if err == nil {
				t.Fatal("expected failure")
			}
			if h.runtime.containers["old-id"].State != containers.RuntimeRunning {
				t.Fatal("active stopped")
			}
			var gen int
			if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&gen); err != nil || gen != 1 {
				t.Fatalf("active=%d err=%v", gen, err)
			}
		})
	}
}

func TestMutationTransportLossRequiresReconciliation(t *testing.T) {
	h := newHarness(t, true)
	h.runtime.mutateFail["stop"] = errors.New("transport lost")
	_, err := (Runner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want unknown outcome, got %v", err)
	}
	var gen int
	_ = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&gen)
	if gen != 1 {
		t.Fatalf("advanced generation %d", gen)
	}
}

func TestFailureInjectionAtEveryLifecycleBoundaryPreservesCommitInvariant(t *testing.T) {
	phases := []Phase{PhaseBeforePull, PhaseAfterPull, PhaseBeforeNetwork, PhaseAfterNetwork, PhaseBeforeContainer, PhaseAfterContainer, PhaseBeforeOldStop, PhaseAfterOldStop, PhaseBeforeCandidateStart, PhaseAfterCandidateStart, PhaseBeforeVerification, PhaseAfterVerification, PhaseBeforeCommit, PhaseAfterCommit}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t, true)
			runner := Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator, Hook: func(_ context.Context, at Phase) error {
				if at == phase {
					return ErrSimulatedInterruption
				}
				return nil
			}}
			_, err := runner.Replace(context.Background(), h.plan)
			if err == nil {
				t.Fatal("expected interruption")
			}
			var active int
			if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&active); err != nil {
				t.Fatal(err)
			}
			want := 1
			if phase == PhaseAfterCommit {
				want = 2
			}
			if active != want {
				t.Fatalf("active=%d want=%d", active, want)
			}
			for _, mutation := range h.runtime.mutations {
				if mutation == "remove:old-id" {
					t.Fatal("old generation removed before retention cleanup")
				}
			}
		})
	}
}

func TestCreateMutationThenTransportLossIsUnknown(t *testing.T) {
	for _, action := range []string{"create-network", "create-container"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t, true)
			h.runtime.mutateFail[action] = errors.New("transport lost")
			_, err := (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan)
			var unknown UnknownOutcomeError
			if !errors.As(err, &unknown) {
				t.Fatalf("want unknown, got %v", err)
			}
			if h.runtime.containers["old-id"].State != containers.RuntimeRunning {
				t.Fatal("active changed during preparation uncertainty")
			}
		})
	}
}

func TestGenerationDatabaseCommitFailureRequiresReconciliation(t *testing.T) {
	h := newHarness(t, true)
	_, err := h.db.Exec(`CREATE TRIGGER reject_generation_commit BEFORE UPDATE OF status ON runtime_generations WHEN NEW.status='active' AND NEW.runtime_generation=2 BEGIN SELECT RAISE(ABORT,'injected commit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	var unknown UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("want reconciliation, got %v", err)
	}
	var active int
	if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active=%d err=%v", active, err)
	}
	if h.runtime.containers["old-id"].State != containers.RuntimeStopped {
		t.Fatal("old recovery runtime was not retained")
	}
}

func TestCandidateStartFailureUsesConservativeRollbackPolicy(t *testing.T) {
	for _, safe := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-release", false: "release-update"}[safe], func(t *testing.T) {
			h := newHarness(t, true)
			h.plan.RollbackSafe = safe
			h.runtime.fail["start"] = errors.New("start rejected")
			_, err := (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan)
			if err == nil {
				t.Fatal("expected failure")
			}
			old := h.runtime.containers["old-id"]
			if safe {
				if old.State != containers.RuntimeRunning {
					t.Fatal("safe rollback did not restart old generation")
				}
				var unknown UnknownOutcomeError
				if errors.As(err, &unknown) {
					t.Fatalf("safe rollback reported unknown: %v", err)
				}
			} else {
				if old.State != containers.RuntimeStopped {
					t.Fatal("release update was automatically rolled back")
				}
				var unknown UnknownOutcomeError
				if !errors.As(err, &unknown) {
					t.Fatalf("update failure should require reconciliation: %v", err)
				}
			}
		})
	}
}

func TestSuccessfulReplacementCleansOnlyGenerationOlderThanRetained(t *testing.T) {
	h := newHarness(t, true)
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-one", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "0"}
	p := containers.ContainerPlan{Image: "repo/app@sha256:older", Name: "kitpro-app-inst-one-g0", Network: "kitpro-net-inst-one-g0", Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data", ContainerPath: "/data"}}}
	h.runtime.networks[p.Network] = containers.NetworkObservation{Exists: true, ID: "network-older", Name: p.Network, Labels: labels}
	h.runtime.containers["older-id"] = observationFromPlan("older-id", p, "network-older", containers.RuntimeStopped)
	_, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,verified_at,committed_at,retired_at,cleanup_state) VALUES('inst-one',0,'older','app','older','retained',?,'network-older','older','/data','internal','now','now','now','now','not_required')`, p.Network)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at,verified_at) VALUES('inst-one',0,'app',?,'older-id',?,'image-id',?,'stopped','now','now')`, p.Name, p.Image, observationHash(h.runtime.containers["older-id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan); err != nil {
		t.Fatal(err)
	}
	var retained, removed int
	if err = h.db.QueryRow(`SELECT SUM(status='retained'),SUM(status='removed') FROM runtime_generations WHERE installation_id='inst-one'`).Scan(&retained, &removed); err != nil {
		t.Fatal(err)
	}
	if retained != 1 || removed != 1 {
		t.Fatalf("retained=%d removed=%d", retained, removed)
	}
	if _, ok := h.runtime.containers["old-id"]; !ok {
		t.Fatal("immediate previous generation was removed")
	}
	if _, ok := h.runtime.containers["older-id"]; ok {
		t.Fatal("older stale generation was not cleaned")
	}
}

func TestCleanupFailureLeavesDebtWithoutFailingCommittedReplacement(t *testing.T) {
	h := newHarness(t, true)
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: "inst-one", ownership.LabelResource: "application", "com.kitpro.runtime-generation": "0"}
	p := containers.ContainerPlan{Image: "repo/app@sha256:older", Name: "kitpro-app-inst-one-g0", Network: "kitpro-net-inst-one-g0", Labels: labels, RestartPolicy: "unless-stopped", Storage: []containers.StorageMount{{HostPath: "/data", ContainerPath: "/data"}}}
	h.runtime.networks[p.Network] = containers.NetworkObservation{Exists: true, ID: "network-older", Name: p.Network, Labels: labels}
	h.runtime.containers["older-id"] = observationFromPlan("older-id", p, "network-older", containers.RuntimeStopped)
	if _, err := h.db.Exec(`INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,data_path,exposure_mode,created_at,verified_at,committed_at,retired_at,cleanup_state) VALUES('inst-one',0,'older','app','older','retained',?,'network-older','older','/data','internal','now','now','now','now','not_required')`, p.Network); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,created_at,verified_at) VALUES('inst-one',0,'app',?,'older-id',?,'image-id',?,'stopped','now','now')`, p.Name, p.Image, observationHash(h.runtime.containers["older-id"])); err != nil {
		t.Fatal(err)
	}
	result, err := (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator, Hook: func(_ context.Context, p Phase) error {
		if p == PhaseCleanup {
			return errors.New("cleanup unavailable")
		}
		return nil
	}}).Replace(context.Background(), h.plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CleanupDeferred {
		t.Fatal("cleanup debt not reported")
	}
	var status, cleanup string
	if err = h.db.QueryRow(`SELECT status,cleanup_state FROM runtime_generations WHERE installation_id='inst-one' AND runtime_generation=0`).Scan(&status, &cleanup); err != nil {
		t.Fatal(err)
	}
	if status != "cleanup_pending" || cleanup != "pending" {
		t.Fatalf("status=%s cleanup=%s", status, cleanup)
	}
	var active int
	if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestStaleFenceCannotCommitCandidate(t *testing.T) {
	h := newHarness(t, false)
	store := h.Store()
	if err := store.Prepare(context.Background(), h.plan); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNetwork(context.Background(), h.plan, "network-new"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordContainer(context.Background(), h.plan, "container-new", "image-new", "config", "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordState(context.Background(), h.plan, "running", true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE installation_leases SET fencing_token=fencing_token+1 WHERE installation_id=?`, h.plan.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), h.plan); err == nil {
		t.Fatal("stale fence committed generation")
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id=? AND status='active'`, h.plan.InstallationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("active generations=%d", count)
	}
}

func TestLegacyRuntimeRemovedInstallationRecreatesWithoutReinstall(t *testing.T) {
	h := newHarness(t, false)
	_, err := h.db.Exec(`INSERT INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES('inst-one','','','','repo/app@sha256:new','/data','now',1,'app','new','internal','','',0,0,'')`)
	if err != nil {
		t.Fatal(err)
	}
	h.plan.ExpectedGeneration = 1
	h.plan.Generation = 2
	result, err := (Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator}).Replace(context.Background(), h.plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation=%d", result.Generation)
	}
	var status string
	if err = h.db.QueryRow(`SELECT status FROM runtime_generations WHERE installation_id='inst-one' AND runtime_generation=1`).Scan(&status); err != nil || status != "removed" {
		t.Fatalf("legacy tombstone status=%s err=%v", status, err)
	}
}

func TestRestartRecoveryForwardCommitsExactRunningCandidate(t *testing.T) {
	h := newHarness(t, true)
	runner := Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator, Hook: func(_ context.Context, p Phase) error {
		if p == PhaseAfterCandidateStart {
			return ErrSimulatedInterruption
		}
		return nil
	}}
	if _, err := runner.Replace(context.Background(), h.plan); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := h.coordinator.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverInterrupted(context.Background(), h.Store(), h.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d", recovered)
	}
	var generation int
	if err = h.db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id='inst-one' AND status='active'`).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("active=%d err=%v", generation, err)
	}
	var operationState, leaseState string
	if err = h.db.QueryRow(`SELECT o.state,l.state FROM helper_operations o JOIN installation_leases l ON l.operation_id=o.operation_id WHERE o.operation_id=?`, h.plan.OperationID).Scan(&operationState, &leaseState); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" || leaseState != "released" {
		t.Fatalf("operation=%s lease=%s", operationState, leaseState)
	}
}

func TestRestartAfterOldStopRemainsActionRequired(t *testing.T) {
	h := newHarness(t, true)
	runner := Runner{Runtime: h.runtime, Store: h.Store(), Evidence: h.coordinator, Hook: func(_ context.Context, p Phase) error {
		if p == PhaseAfterOldStop {
			return ErrSimulatedInterruption
		}
		return nil
	}}
	if _, err := runner.Replace(context.Background(), h.plan); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := h.coordinator.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverInterrupted(context.Background(), h.Store(), h.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 {
		t.Fatalf("unexpected recovery=%d", recovered)
	}
	var state string
	if err = h.db.QueryRow(`SELECT state FROM helper_operations WHERE operation_id=?`, h.plan.OperationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "action_required" {
		t.Fatalf("state=%s", state)
	}
}

func TestImageContainsDigestAcceptsDockerHubCanonicalReferences(t *testing.T) {
	digest := "sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21"
	for _, test := range []struct {
		name      string
		requested string
		observed  string
	}{
		{name: "docker hub namespace", requested: "docker.io/freshrss/freshrss@" + digest, observed: "freshrss/freshrss@" + digest},
		{name: "docker hub official image", requested: "docker.io/library/busybox@" + digest, observed: "busybox@" + digest},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := containers.ImageObservation{Exists: true, RepoDigests: []string{test.observed}}
			if !imageContainsDigest(image, test.requested) {
				t.Fatalf("equivalent Docker Hub digest reference was rejected")
			}
		})
	}
}

func TestImageContainsDigestRejectsRepositoryOrDigestMismatch(t *testing.T) {
	wanted := "docker.io/freshrss/freshrss@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, observed := range []string{
		"other/freshrss@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"freshrss/freshrss@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	} {
		image := containers.ImageObservation{Exists: true, RepoDigests: []string{observed}}
		if imageContainsDigest(image, wanted) {
			t.Fatalf("mismatched digest reference %q was accepted", observed)
		}
	}
}

func (h harness) Store() Store { return Store{DB: h.db} }
