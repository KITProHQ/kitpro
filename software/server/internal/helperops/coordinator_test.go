package helperops

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func testCoordinator(t *testing.T) (Coordinator, *sql.DB) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	return Coordinator{DB: db}, db
}

func mutation(id, requestID, installation, operation string) protocol.Request {
	return protocol.Request{Version: 2, ID: requestID, OperationID: id, Operation: operation, OperationRevision: 1, InstanceID: installation, RuntimeGeneration: 2}
}

func TestBeginReplayConflictAndDurableResult(t *testing.T) {
	coordinator, db := testCoordinator(t)
	ctx := context.Background()
	request := mutation("op-11111111111111111111111111111111", "req-one", "inst-example01", "UpdateApplication")
	first, err := coordinator.Begin(ctx, request, 1001, request.InstanceID)
	if err != nil || !first.Execute || first.FencingToken != 1 {
		t.Fatalf("first decision=%#v err=%v", first, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM helper_operations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("operation count=%d err=%v", count, err)
	}
	var expectedGeneration int
	if err = db.QueryRow(`SELECT expected_generation FROM helper_operations WHERE operation_id=?`, request.OperationID).Scan(&expectedGeneration); err != nil || expectedGeneration != 1 {
		t.Fatalf("expected base generation=%d err=%v", expectedGeneration, err)
	}
	replay := request
	replay.ID = "req-two"
	decision, err := coordinator.Begin(ctx, replay, 1001, replay.InstanceID)
	if err != nil || decision.Execute || decision.Response.State != StateAccepted || decision.Response.RequestID != "req-two" {
		t.Fatalf("accepted replay=%#v err=%v", decision, err)
	}
	if err = coordinator.AuthorizeMutation(ctx, request.OperationID, first.FencingToken); err != nil {
		t.Fatal(err)
	}
	completed, err := coordinator.Complete(ctx, request.OperationID, first.FencingToken, protocol.Response{OK: true, RequestID: request.ID, Result: map[string]string{"status": "done"}})
	if err != nil || completed.State != StateSucceeded {
		t.Fatalf("complete=%#v err=%v", completed, err)
	}
	decision, err = coordinator.Begin(ctx, replay, 1001, replay.InstanceID)
	if err != nil || decision.Execute || !decision.Response.OK || decision.Response.State != StateSucceeded {
		t.Fatalf("terminal replay=%#v err=%v", decision, err)
	}
	conflict := replay
	conflict.Operation = "RemoveApplication"
	decision, err = coordinator.Begin(ctx, conflict, 1001, conflict.InstanceID)
	if err != nil || decision.Response.ErrorCode != "OperationConflict" || decision.Execute {
		t.Fatalf("hash conflict=%#v err=%v", decision, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM helper_operations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay changed durable count=%d err=%v", count, err)
	}
}

func TestExplicitRepairTakesOverRecoveryLeaseAndFencesPriorExecutor(t *testing.T) {
	coordinator, db := testCoordinator(t)
	ctx := context.Background()
	interrupted := mutation("op-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "req-interrupted", "inst-repair0001", "UpdateApplication")
	first, err := coordinator.Begin(ctx, interrupted, 1001, interrupted.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(ctx, interrupted.OperationID, first.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Complete(ctx, interrupted.OperationID, first.FencingToken, protocol.Response{ErrorCode: "RecoveryRequired", Error: "unknown runtime outcome"}); err != nil {
		t.Fatal(err)
	}
	repair := mutation("op-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "req-repair", interrupted.InstanceID, "RepairInstallation")
	repair.RepairAction = "start_active"
	decision, err := coordinator.Begin(ctx, repair, 1001, repair.InstanceID)
	if err != nil || !decision.Execute || decision.FencingToken != first.FencingToken+2 {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	var priorState, holder, leaseState string
	var token int64
	if err = db.QueryRow(`SELECT state FROM helper_operations WHERE operation_id=?`, interrupted.OperationID).Scan(&priorState); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT operation_id,fencing_token,state FROM installation_leases WHERE installation_id=?`, interrupted.InstanceID).Scan(&holder, &token, &leaseState); err != nil {
		t.Fatal(err)
	}
	if priorState != StateSuperseded || holder != repair.OperationID || token != decision.FencingToken || leaseState != "held" {
		t.Fatalf("prior=%q holder=%q token=%d lease=%q", priorState, holder, token, leaseState)
	}
	if _, err = coordinator.Complete(ctx, interrupted.OperationID, first.FencingToken, protocol.Response{OK: true}); err == nil {
		t.Fatal("stale interrupted executor committed after repair takeover")
	}
}

func TestInstallationSerializationAndFencing(t *testing.T) {
	coordinator, _ := testCoordinator(t)
	ctx := context.Background()
	requests := []protocol.Request{
		mutation("op-22222222222222222222222222222222", "req-a", "inst-shared0001", "CreateApplicationBackup"),
		mutation("op-33333333333333333333333333333333", "req-b", "inst-shared0001", "UpdateApplication"),
	}
	type result struct {
		operationID string
		decision    Decision
		err         error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			decision, err := coordinator.Begin(ctx, request, 1001, request.InstanceID)
			results <- result{request.OperationID, decision, err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	executors, conflicts := 0, 0
	var winner Decision
	var winnerID string
	for result := range results {
		if result.decision.Execute {
			executors++
			winner = result.decision
			winnerID = result.operationID
		} else if result.decision.Response.ErrorCode == "OperationConflict" {
			conflicts++
		}
	}
	if executors != 1 || conflicts != 1 {
		t.Fatalf("executors=%d conflicts=%d", executors, conflicts)
	}
	other, err := coordinator.Begin(ctx, mutation("op-44444444444444444444444444444444", "req-c", "inst-other0001", "RestoreApplicationBackup"), 1001, "inst-other0001")
	if err != nil || !other.Execute {
		t.Fatalf("unrelated installation blocked: %#v %v", other, err)
	}
	if err = coordinator.AuthorizeMutation(ctx, winnerID, winner.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Complete(ctx, winnerID, winner.FencingToken, protocol.Response{OK: true}); err != nil {
		t.Fatal(err)
	}
	next := mutation("op-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "req-next", "inst-shared0001", "StartApplication")
	nextDecision, err := coordinator.Begin(ctx, next, 1001, next.InstanceID)
	if err != nil || !nextDecision.Execute || nextDecision.FencingToken != winner.FencingToken+1 {
		t.Fatalf("next fencing token=%d winner=%d err=%v", nextDecision.FencingToken, winner.FencingToken, err)
	}
	if _, err = coordinator.Complete(ctx, winnerID, winner.FencingToken, protocol.Response{OK: true}); err == nil {
		t.Fatal("stale completed executor committed twice")
	}
}

func TestRestartClassificationBlocksUntilAdministrativeResolution(t *testing.T) {
	coordinator, db := testCoordinator(t)
	ctx := context.Background()
	acceptedRequest := mutation("op-55555555555555555555555555555555", "req-a", "inst-accepted01", "StartApplication")
	accepted, err := coordinator.Begin(ctx, acceptedRequest, 1001, acceptedRequest.InstanceID)
	if err != nil || !accepted.Execute {
		t.Fatal(err)
	}
	executingRequest := mutation("op-66666666666666666666666666666666", "req-b", "inst-executing1", "RestoreApplicationBackup")
	executing, err := coordinator.Begin(ctx, executingRequest, 1001, executingRequest.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AuthorizeMutation(ctx, executingRequest.OperationID, executing.FencingToken); err != nil {
		t.Fatal(err)
	}
	count, err := coordinator.RecoverInterrupted(ctx)
	if err != nil || count != 2 {
		t.Fatalf("recovered=%d err=%v", count, err)
	}
	acceptedRecord, _ := coordinator.Get(ctx, acceptedRequest.OperationID)
	if acceptedRecord.State != StateFailed || acceptedRecord.OutcomeConfidence != "confirmed_absent" {
		t.Fatalf("accepted classification=%#v", acceptedRecord)
	}
	executingRecord, _ := coordinator.Get(ctx, executingRequest.OperationID)
	if executingRecord.State != StateActionRequired || executingRecord.OutcomeConfidence != "unknown" {
		t.Fatalf("executing classification=%#v", executingRecord)
	}
	blocked, blockedErr := coordinator.Begin(ctx, mutation("op-77777777777777777777777777777777", "req-c", executingRequest.InstanceID, "StartApplication"), 1001, executingRequest.InstanceID)
	if blockedErr == nil || blocked.Response.ActiveOperationID != executingRequest.OperationID {
		t.Fatalf("recovery lease bypassed: %#v %v", blocked, blockedErr)
	}
	if _, err = coordinator.Complete(ctx, executingRequest.OperationID, executing.FencingToken, protocol.Response{OK: true}); err == nil {
		t.Fatal("stale pre-restart executor committed")
	}
	if err = coordinator.ResolveActionRequired(ctx, executingRequest.OperationID); err != nil {
		t.Fatal(err)
	}
	var leaseState string
	if err = db.QueryRow(`SELECT state FROM installation_leases WHERE installation_id=?`, executingRequest.InstanceID).Scan(&leaseState); err != nil || leaseState != "released" {
		t.Fatalf("lease state=%q err=%v", leaseState, err)
	}
}

func TestBackupUpdateAndRestoreRestartUseSameInstallationLease(t *testing.T) {
	for _, pair := range []struct{ first, second string }{{"CreateApplicationBackup", "UpdateApplication"}, {"RestoreApplicationBackup", "StartApplication"}} {
		coordinator, _ := testCoordinator(t)
		first, err := coordinator.Begin(context.Background(), mutation("op-88888888888888888888888888888888", "req-a", "inst-lockscope1", pair.first), 1001, "inst-lockscope1")
		if err != nil || !first.Execute {
			t.Fatal(err)
		}
		second, err := coordinator.Begin(context.Background(), mutation("op-99999999999999999999999999999999", "req-b", "inst-lockscope1", pair.second), 1001, "inst-lockscope1")
		var conflict ConflictError
		if !errors.As(err, &conflict) || second.Response.ErrorCode != "OperationConflict" {
			t.Fatalf("%s versus %s was not serialized: %#v %v", pair.first, pair.second, second, err)
		}
	}
}
