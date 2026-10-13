package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func beginSingleOperation(t *testing.T, h harness, operation string) (protocol.Request, int64) {
	t.Helper()
	if _, err := h.coordinator.Complete(context.Background(), h.plan.OperationID, h.plan.FencingToken, protocol.Response{OK: true}); err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operations.NewID(), Operation: operation, OperationRevision: 1, InstanceID: "inst-one", RuntimeGeneration: 1}
	decision, err := h.coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.coordinator.AuthorizeMutation(context.Background(), request.OperationID, decision.FencingToken); err != nil {
		t.Fatal(err)
	}
	return request, decision.FencingToken
}

func singleRunner(h harness) SingleRunner {
	return SingleRunner{Runtime: h.runtime, Store: Store{DB: h.db}, Evidence: h.coordinator}
}

func setSingleState(t *testing.T, h harness, state containers.RuntimeState) {
	t.Helper()
	observed := h.runtime.containers["old-id"]
	observed.State = state
	h.runtime.containers["old-id"] = observed
	if _, err := h.db.Exec(`UPDATE runtime_components SET state=? WHERE installation_id='inst-one' AND runtime_generation=1 AND component_id='app'`, state); err != nil {
		t.Fatal(err)
	}
}

func markSingleMigrated(t *testing.T, h harness) {
	t.Helper()
	if _, err := h.db.Exec(`UPDATE runtime_generations SET status='verification_required',observed_network_id='',verified_at=NULL,committed_at=NULL WHERE installation_id='inst-one' AND runtime_generation=1`); err != nil {
		t.Fatal(err)
	}
}

func TestSingleStateStartStopAndIdempotence(t *testing.T) {
	for _, test := range []struct {
		name, operation, action string
		initial, want           containers.RuntimeState
		wantMutation            string
	}{
		{"start stopped", "StartApplication", "start", containers.RuntimeStopped, containers.RuntimeRunning, "start:old-id"},
		{"start running", "StartApplication", "start", containers.RuntimeRunning, containers.RuntimeRunning, ""},
		{"stop running", "StopApplication", "stop", containers.RuntimeRunning, containers.RuntimeStopped, "stop:old-id"},
		{"stop stopped", "StopApplication", "stop", containers.RuntimeStopped, containers.RuntimeStopped, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, true)
			setSingleState(t, h, test.initial)
			request, token := beginSingleOperation(t, h, test.operation)
			result, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, test.action)
			if err != nil || result.RuntimeState != string(test.want) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			observed := h.runtime.containers["old-id"]
			if observed.State != test.want {
				t.Fatalf("runtime state=%s", observed.State)
			}
			mutations := strings.Join(h.runtime.mutations, ",")
			if test.wantMutation == "" && mutations != "" {
				t.Fatalf("idempotent operation dispatched runtime mutation: %s", mutations)
			}
			if test.wantMutation != "" && !strings.Contains(mutations, test.wantMutation) {
				t.Fatalf("missing mutation %q in %q", test.wantMutation, mutations)
			}
		})
	}
}

func TestSingleStateExactReplayDoesNotRedispatch(t *testing.T) {
	h := newHarness(t, true)
	request, token := beginSingleOperation(t, h, "StopApplication")
	result, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.coordinator.Complete(context.Background(), request.OperationID, token, protocol.Response{OK: true, Result: result}); err != nil {
		t.Fatal(err)
	}
	mutations := len(h.runtime.mutations)
	replay, err := h.coordinator.Begin(context.Background(), request, 0, request.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Execute || !replay.Response.OK || replay.Response.State != "succeeded" {
		t.Fatalf("replay=%#v", replay)
	}
	if len(h.runtime.mutations) != mutations {
		t.Fatal("exact replay redispatched the runtime mutation")
	}
}

func TestSingleRestartIsOneOrderedDurableOperation(t *testing.T) {
	h := newHarness(t, true)
	request, token := beginSingleOperation(t, h, "RestartApplication")
	result, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "restart")
	if err != nil || result.RuntimeState != "running" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := strings.Join(h.runtime.mutations, ","); got != "stop:old-id,start:old-id" {
		t.Fatalf("restart order=%q", got)
	}
	steps, err := (Store{DB: h.db}).ComponentSteps(context.Background(), request.OperationID)
	if err != nil || len(steps) != 2 || steps[0].Action != "start" && steps[1].Action != "start" {
		t.Fatalf("durable restart steps=%#v err=%v", steps, err)
	}
}

func TestSingleTransportLossUsesFreshObservation(t *testing.T) {
	for _, action := range []string{"start", "stop"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t, true)
			initial := containers.RuntimeStopped
			operation := "StartApplication"
			if action == "stop" {
				initial = containers.RuntimeRunning
				operation = "StopApplication"
			}
			setSingleState(t, h, initial)
			h.runtime.mutateFail[action] = errors.New("transport response lost")
			request, token := beginSingleOperation(t, h, operation)
			if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, action); err != nil {
				t.Fatal(err)
			}
			steps, err := (Store{DB: h.db}).ComponentSteps(context.Background(), request.OperationID)
			if err != nil || len(steps) != 1 || steps[0].OutcomeConfidence != "confirmed_applied" || !strings.Contains(steps[0].ErrorDetail, "response lost") {
				t.Fatalf("steps=%#v err=%v", steps, err)
			}
		})
	}
}

func TestSingleRestartFailureLeavesObservedStoppedState(t *testing.T) {
	h := newHarness(t, true)
	h.runtime.fail["start"] = errors.New("start rejected")
	request, token := beginSingleOperation(t, h, "RestartApplication")
	if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "restart"); err == nil {
		t.Fatal("restart failure accepted")
	}
	if h.runtime.containers["old-id"].State != containers.RuntimeStopped {
		t.Fatal("failed restart did not preserve observed stopped state")
	}
}

func TestSingleRemoveResponseLossPreservesManagedStorage(t *testing.T) {
	h := newHarness(t, true)
	h.runtime.mutateFail["remove"] = errors.New("remove response lost")
	request, token := beginSingleOperation(t, h, "RemoveApplication")
	result, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "remove")
	if err != nil || result.RuntimeState != "runtime_removed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var status, dataPath string
	if err = h.db.QueryRow(`SELECT status,data_path FROM runtime_generations WHERE installation_id='inst-one' AND runtime_generation=1`).Scan(&status, &dataPath); err != nil {
		t.Fatal(err)
	}
	if status != "removed" || dataPath != "/data" {
		t.Fatalf("status=%q data=%q", status, dataPath)
	}
}

func TestSingleOperationRejectsStaleFenceMissingAndDrift(t *testing.T) {
	t.Run("stale fence", func(t *testing.T) {
		h := newHarness(t, true)
		request, token := beginSingleOperation(t, h, "StopApplication")
		if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token+1, "stop"); err == nil || len(h.runtime.mutations) != 0 {
			t.Fatalf("stale fence err=%v mutations=%v", err, h.runtime.mutations)
		}
	})
	t.Run("missing", func(t *testing.T) {
		h := newHarness(t, true)
		delete(h.runtime.containers, "old-id")
		request, token := beginSingleOperation(t, h, "StartApplication")
		if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "start"); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("missing runtime err=%v", err)
		}
	})
	t.Run("configuration drift", func(t *testing.T) {
		h := newHarness(t, true)
		observed := h.runtime.containers["old-id"]
		observed.RestartPolicy = "always"
		h.runtime.containers["old-id"] = observed
		request, token := beginSingleOperation(t, h, "StopApplication")
		if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "stop"); err == nil || !strings.Contains(err.Error(), "configuration") {
			t.Fatalf("drift err=%v", err)
		}
	})
	t.Run("unverified migrated configuration", func(t *testing.T) {
		h := newHarness(t, true)
		markSingleMigrated(t, h)
		if _, err := h.db.Exec(`UPDATE runtime_components SET configuration_hash='' WHERE installation_id='inst-one' AND runtime_generation=1 AND component_id='app'`); err != nil {
			t.Fatal(err)
		}
		request, token := beginSingleOperation(t, h, "StopApplication")
		if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "stop"); err == nil || !strings.Contains(err.Error(), "not verified") || len(h.runtime.mutations) != 0 {
			t.Fatalf("unverified migration err=%v mutations=%v", err, h.runtime.mutations)
		}
	})
}

func TestSingleStopAllowsExactOwnedUnstableRuntime(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    containers.RuntimeState
		migrated bool
	}{
		{name: "migrated restarting", state: containers.RuntimeState("restarting"), migrated: true},
		{name: "migrated paused", state: containers.RuntimeState("paused"), migrated: true},
		{name: "native restarting", state: containers.RuntimeState("restarting")},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, true)
			if test.migrated {
				markSingleMigrated(t, h)
			}
			observed := h.runtime.containers["old-id"]
			observed.State = test.state
			h.runtime.containers["old-id"] = observed
			request, token := beginSingleOperation(t, h, "StopApplication")
			result, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "stop")
			if err != nil || result.RuntimeState != "stopped" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if got := strings.Join(h.runtime.mutations, ","); got != "stop:old-id" {
				t.Fatalf("mutations=%q", got)
			}
			var generationStatus, componentState, configurationHash string
			if err = h.db.QueryRow(`SELECT g.status,c.state,c.configuration_hash FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id='inst-one' AND g.runtime_generation=1`).Scan(&generationStatus, &componentState, &configurationHash); err != nil {
				t.Fatal(err)
			}
			if generationStatus != "active" || componentState != "stopped" || configurationHash == "" {
				t.Fatalf("generation=%q component=%q hash=%q", generationStatus, componentState, configurationHash)
			}
		})
	}
}

func TestMigratedExactRunningAndStoppedRemainLifecycleEligible(t *testing.T) {
	for _, state := range []containers.RuntimeState{containers.RuntimeRunning, containers.RuntimeStopped} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t, true)
			markSingleMigrated(t, h)
			setSingleState(t, h, state)
			request, token := beginSingleOperation(t, h, "StopApplication")
			if _, err := singleRunner(h).Operate(context.Background(), request.InstanceID, request.OperationID, token, "stop"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
