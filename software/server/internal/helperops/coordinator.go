// Package helperops owns durable privileged-operation acceptance, replay, and
// per-installation serialization. Staged lifecycle code records its phases
// through this coordinator; other mutations remain in one legacy phase.
package helperops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

const (
	StateAccepted       = "accepted"
	StateExecuting      = "executing"
	StateReconciling    = "reconciling"
	StateSucceeded      = "succeeded"
	StateFailed         = "failed"
	StateActionRequired = "action_required"
	StateCancelled      = "cancelled"
	StateSuperseded     = "superseded"

	maxResultBytes = 32 * 1024
	maxErrorBytes  = 512
)

var operationIDPattern = regexp.MustCompile(`^op-[a-f0-9]{32}$`)
var acceptanceLocks sync.Map

type Record struct {
	OperationID, RequestHash, RequestJSON                       string
	ProtocolVersion, OperationRevision                          int
	OperationKind, InstallationID                               string
	CallerUID                                                   uint32
	State, CurrentPhase, OutcomeConfidence, RecoveryDisposition string
	ExpectedGeneration                                          sql.NullInt64
	FencingToken                                                sql.NullInt64
	AcceptedAt, StartedAt, UpdatedAt, CompletedAt               sql.NullString
	ResultJSON, ErrorCode, ErrorDetail                          string
}

type Decision struct {
	Execute      bool
	FencingToken int64
	Response     protocol.Response
}

type ConflictError struct{ ActiveOperationID string }

func (e ConflictError) Error() string {
	return "installation already has an active privileged operation"
}

type Coordinator struct {
	DB  *sql.DB
	Now func() time.Time
}

func (c Coordinator) now() string {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return now().UTC().Format(time.RFC3339Nano)
}

// Begin binds an immutable operation ID to its canonical semantic request.
// Replays return the existing state and never create another executor.
func (c Coordinator) Begin(ctx context.Context, request protocol.Request, callerUID uint32, scope string) (Decision, error) {
	if c.DB == nil {
		return Decision{}, errors.New("helper operation database unavailable")
	}
	if request.Version != 2 || request.OperationRevision != 1 {
		return Decision{}, errors.New("durable mutations require protocol version 2 operation revision 1")
	}
	if !operationIDPattern.MatchString(request.OperationID) {
		return Decision{}, errors.New("invalid operation ID")
	}
	if scope == "" || len(scope) > 128 {
		return Decision{}, errors.New("invalid operation scope")
	}
	// SQLite permits only one writer. Serializing the short acceptance
	// transaction in-process avoids snapshot-upgrade races while runtime work
	// for unrelated installations remains concurrent. The durable lease row,
	// not this mutex, remains authoritative across processes and restarts.
	lockValue, _ := acceptanceLocks.LoadOrStore(c.DB, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	canonical, err := protocol.Canonical(request)
	if err != nil {
		return Decision{}, err
	}
	hash, err := protocol.Hash(request)
	if err != nil {
		return Decision{}, err
	}
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return Decision{}, err
	}
	defer tx.Rollback()

	existing, err := getTx(ctx, tx, request.OperationID)
	if err == nil {
		if existing.RequestHash != hash {
			return Decision{Response: protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: existing.State, ErrorCode: "OperationConflict", Error: "operation ID was already used for different semantic contents"}}, tx.Commit()
		}
		return Decision{Response: responseFor(existing, request.ID)}, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Decision{}, err
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO helper_operations(
		operation_id,request_hash,request_json,protocol_version,operation_revision,operation_kind,installation_id,caller_uid,state,current_phase,outcome_confidence,recovery_disposition,expected_generation,accepted_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,'accepted','not_dispatched','reconcile_first',?,?,?)`,
		request.OperationID, hash, string(canonical), request.Version, request.OperationRevision, request.Operation, scope, callerUID, StateAccepted, expectedGeneration(request), now, now)
	if err != nil {
		return Decision{}, err
	}

	var activeID, leaseState string
	var token int64
	leaseErr := tx.QueryRowContext(ctx, `SELECT operation_id,fencing_token,state FROM installation_leases WHERE installation_id=?`, scope).Scan(&activeID, &token, &leaseState)
	switch {
	case errors.Is(leaseErr, sql.ErrNoRows):
		token = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO installation_leases(installation_id,operation_id,fencing_token,state,acquired_at,updated_at,recovery_required) VALUES(?,?,?,'held',?,?,0)`, scope, request.OperationID, token, now, now)
	case leaseErr != nil:
		return Decision{}, leaseErr
	case leaseState == "released":
		token++
		_, err = tx.ExecContext(ctx, `UPDATE installation_leases SET operation_id=?,fencing_token=?,state='held',acquired_at=?,updated_at=?,recovery_required=0 WHERE installation_id=? AND fencing_token=? AND state='released'`, request.OperationID, token, now, now, scope, token-1)
	case (request.Operation == "RepairInstallation" || (request.Operation == "InstallApplication" && request.RepairAction == "recreate_generation")) && leaseState == "recovery_required":
		// An explicit repair is the only new operation allowed to take over an
		// interrupted operation's recovery lease. Preserve the old receipt as
		// superseded and advance the fence before the repair can mutate runtime.
		var priorState string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM helper_operations WHERE operation_id=?`, activeID).Scan(&priorState); err != nil {
			return Decision{}, err
		}
		if priorState != StateActionRequired {
			return Decision{}, errors.New("recovery lease holder is not repairable")
		}
		token++
		if _, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='superseded',current_phase='superseded_by_repair',recovery_disposition='completed',updated_at=?,completed_at=? WHERE operation_id=? AND state='action_required'`, now, now, activeID); err == nil {
			err = appendEvent(ctx, tx, activeID, scope, &token, "operation_superseded_by_repair", "superseded_by_repair", map[string]string{"repair_operation_id": request.OperationID}, now)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE installation_leases SET operation_id=?,fencing_token=?,state='held',acquired_at=?,updated_at=?,recovery_required=0 WHERE installation_id=? AND operation_id=? AND state='recovery_required'`, request.OperationID, token, now, now, scope, activeID)
		}
	case activeID == request.OperationID:
		// This can only occur after a partially committed migration or recovery.
		err = nil
	default:
		_, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='failed',current_phase='lease_conflict',outcome_confidence='not_dispatched',recovery_disposition='retry_after_validation',updated_at=?,completed_at=?,error_code='OperationConflict',error_detail=? WHERE operation_id=?`, now, now, sanitize("installation already has active operation "+activeID), request.OperationID)
		if err == nil {
			err = appendEvent(ctx, tx, request.OperationID, scope, nil, "operation_conflict", "lease_conflict", map[string]string{"active_operation_id": activeID}, now)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return Decision{}, err
		}
		return Decision{Response: protocol.Response{RequestID: request.ID, OperationID: request.OperationID, State: StateFailed, Phase: "lease_conflict", ActiveOperationID: activeID, ErrorCode: "OperationConflict", Error: "installation already has an active privileged operation"}}, ConflictError{ActiveOperationID: activeID}
	}
	if err != nil {
		return Decision{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE helper_operations SET fencing_token=?,updated_at=? WHERE operation_id=?`, token, now, request.OperationID)
	if err != nil {
		return Decision{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Decision{}, errors.New("operation lease acquisition lost")
	}
	if err = appendEvent(ctx, tx, request.OperationID, scope, &token, "operation_accepted", "accepted", nil, now); err != nil {
		return Decision{}, err
	}
	if err = tx.Commit(); err != nil {
		return Decision{}, err
	}
	return Decision{Execute: true, FencingToken: token}, nil
}

// AuthorizeMutation moves an accepted mutation into execution. Typed operators
// record durable intent before every external mutation.
func (c Coordinator) AuthorizeMutation(ctx context.Context, operationID string, token int64) error {
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyFence(ctx, tx, operationID, token); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE helper_operations SET state='executing',current_phase='mutation_authorized',outcome_confidence='unknown',recovery_disposition='reconcile_first',started_at=COALESCE(started_at,?),updated_at=? WHERE operation_id=? AND fencing_token=? AND state='accepted'`, now, now, operationID, token)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("operation is not eligible for execution")
	}
	var scope string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope); err != nil {
		return err
	}
	if err = appendEvent(ctx, tx, operationID, scope, &token, "mutation_authorized", "mutation_authorized", nil, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordPhase durably brackets staged lifecycle mutations. Callers record an
// intent before external runtime work and a confirmed observation afterward.
func (c Coordinator) RecordPhase(ctx context.Context, operationID string, token int64, phase, outcome string, facts map[string]string) error {
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyFence(ctx, tx, operationID, token); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE helper_operations SET current_phase=?,updated_at=? WHERE operation_id=? AND fencing_token=? AND state='executing'`, phase, now, operationID, token)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("stale executor cannot record lifecycle phase")
	}
	var scope string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope); err != nil {
		return err
	}
	if err = appendEvent(ctx, tx, operationID, scope, &token, "lifecycle_phase_"+outcome, phase, facts, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (c Coordinator) Complete(ctx context.Context, operationID string, token int64, response protocol.Response) (protocol.Response, error) {
	now := c.now()
	resultJSON := ""
	if response.Result != nil {
		encoded, err := json.Marshal(response.Result)
		if err != nil {
			return protocol.Response{}, err
		}
		if len(encoded) > maxResultBytes {
			return protocol.Response{}, errors.New("operation result too large")
		}
		resultJSON = string(encoded)
	}
	state, outcome, recovery := StateSucceeded, "confirmed_applied", "completed"
	errorCode, detail := "", ""
	if !response.OK {
		state, outcome, recovery = StateFailed, "mixed", "retry_after_validation"
		errorCode = response.ErrorCode
		if errorCode == "" {
			errorCode = "ExecutionFailed"
		}
		detail = sanitize(response.Error)
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, err
	}
	defer tx.Rollback()
	if err = verifyFence(ctx, tx, operationID, token); err != nil {
		return protocol.Response{}, err
	}
	if response.ErrorCode == "RecoveryRequired" {
		newToken := token + 1
		result, updateErr := tx.ExecContext(ctx, `UPDATE helper_operations SET state='action_required',current_phase='runtime_reconciliation_required',outcome_confidence='unknown',recovery_disposition='administrator_action',fencing_token=?,updated_at=?,completed_at=?,error_code='RecoveryRequired',error_detail=? WHERE operation_id=? AND fencing_token=? AND state='executing'`, newToken, now, now, detail, operationID, token)
		if updateErr != nil {
			return protocol.Response{}, updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return protocol.Response{}, errors.New("stale executor cannot require reconciliation")
		}
		var scope string
		if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope); err != nil {
			return protocol.Response{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE installation_leases SET fencing_token=?,state='recovery_required',updated_at=?,recovery_required=1 WHERE installation_id=? AND operation_id=? AND fencing_token=? AND state='held'`, newToken, now, scope, operationID, token); err != nil {
			return protocol.Response{}, err
		}
		if err = appendEvent(ctx, tx, operationID, scope, &newToken, "runtime_reconciliation_required", "runtime_reconciliation_required", nil, now); err != nil {
			return protocol.Response{}, err
		}
		if err = tx.Commit(); err != nil {
			return protocol.Response{}, err
		}
		record, getErr := c.Get(ctx, operationID)
		if getErr != nil {
			return protocol.Response{}, getErr
		}
		return responseFor(record, response.RequestID), nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE helper_operations SET state=?,current_phase='completed',outcome_confidence=?,recovery_disposition=?,updated_at=?,completed_at=?,result_json=?,error_code=?,error_detail=? WHERE operation_id=? AND fencing_token=? AND state='executing'`, state, outcome, recovery, now, now, resultJSON, errorCode, detail, operationID, token)
	if err != nil {
		return protocol.Response{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return protocol.Response{}, errors.New("stale executor cannot commit operation result")
	}
	var scope string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope); err != nil {
		return protocol.Response{}, err
	}
	if err = appendEvent(ctx, tx, operationID, scope, &token, "operation_"+state, "completed", nil, now); err != nil {
		return protocol.Response{}, err
	}
	leaseResult, err := tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',updated_at=?,recovery_required=0 WHERE installation_id=? AND operation_id=? AND fencing_token=? AND state='held'`, now, scope, operationID, token)
	if err != nil {
		return protocol.Response{}, err
	}
	if changed, _ := leaseResult.RowsAffected(); changed != 1 {
		return protocol.Response{}, errors.New("stale executor cannot release operation lease")
	}
	if err = tx.Commit(); err != nil {
		return protocol.Response{}, err
	}
	record, err := c.Get(ctx, operationID)
	if err != nil {
		return protocol.Response{}, err
	}
	return responseFor(record, response.RequestID), nil
}

// CompleteRecovery finalizes an interrupted operation after a recovery-specific
// observer proves either the requested result or a safe rollback. It never
// re-dispatches the original mutation.
func (c Coordinator) CompleteRecovery(ctx context.Context, operationID string, token int64, response protocol.Response) (protocol.Response, error) {
	now := c.now()
	state, outcome, recovery := StateSucceeded, "confirmed_applied", "completed"
	errorCode, detail, resultJSON := "", "", ""
	if response.Result != nil {
		encoded, err := json.Marshal(response.Result)
		if err != nil || len(encoded) > maxResultBytes {
			return protocol.Response{}, errors.New("recovery result is invalid")
		}
		resultJSON = string(encoded)
	}
	if !response.OK {
		state, outcome = StateFailed, "confirmed_absent"
		errorCode, detail = response.ErrorCode, sanitize(response.Error)
		if errorCode == "" {
			errorCode = "RecoveryRolledBack"
		}
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, err
	}
	defer tx.Rollback()
	var installation string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=? AND fencing_token=? AND state='action_required'`, operationID, token).Scan(&installation); err != nil {
		return protocol.Response{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE helper_operations SET state=?,current_phase='recovery_completed',outcome_confidence=?,recovery_disposition=?,updated_at=?,completed_at=?,result_json=?,error_code=?,error_detail=? WHERE operation_id=? AND fencing_token=? AND state='action_required'`, state, outcome, recovery, now, now, resultJSON, errorCode, detail, operationID, token)
	if err != nil {
		return protocol.Response{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return protocol.Response{}, errors.New("stale recovery executor cannot commit operation result")
	}
	lease, err := tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',updated_at=?,recovery_required=0 WHERE installation_id=? AND operation_id=? AND fencing_token=? AND state='recovery_required'`, now, installation, operationID, token)
	if err != nil {
		return protocol.Response{}, err
	}
	if changed, _ := lease.RowsAffected(); changed != 1 {
		return protocol.Response{}, errors.New("stale recovery executor cannot release operation lease")
	}
	if err = appendEvent(ctx, tx, operationID, installation, &token, "operation_"+state, "recovery_completed", nil, now); err != nil {
		return protocol.Response{}, err
	}
	if err = tx.Commit(); err != nil {
		return protocol.Response{}, err
	}
	return c.Response(ctx, operationID, response.RequestID)
}

func (c Coordinator) Get(ctx context.Context, operationID string) (Record, error) {
	return getRow(c.DB.QueryRowContext(ctx, operationSelect+` WHERE operation_id=?`, operationID))
}

func (c Coordinator) Response(ctx context.Context, operationID, requestID string) (protocol.Response, error) {
	record, err := c.Get(ctx, operationID)
	if err != nil {
		return protocol.Response{}, err
	}
	return responseFor(record, requestID), nil
}

// RecoverInterrupted invalidates every pre-restart fencing token. Accepted work
// has no recorded mutation intent and can fail safely. Executing work is
// ambiguous in this slice, so it becomes action_required and keeps a blocking
// recovery lease until a root administrator resolves it deliberately.
func (c Coordinator) RecoverInterrupted(ctx context.Context) (int, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT operation_id,state,current_phase FROM helper_operations WHERE state IN ('accepted','executing','reconciling') ORDER BY accepted_at`)
	if err != nil {
		return 0, err
	}
	type interrupted struct{ id, state, phase string }
	var items []interrupted
	for rows.Next() {
		var item interrupted
		if err = rows.Scan(&item.id, &item.state, &item.phase); err != nil {
			_ = rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, item := range items {
		if err = c.markReconciling(ctx, item.id); err != nil {
			return 0, err
		}
		if err = c.recoverOne(ctx, item.id, item.state); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func (c Coordinator) markReconciling(ctx context.Context, operationID string) error {
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var scope string
	var token int64
	if err = tx.QueryRowContext(ctx, `SELECT installation_id,fencing_token FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope, &token); err != nil {
		return err
	}
	token++
	if _, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='reconciling',current_phase='restart_observation',outcome_confidence='unknown',recovery_disposition='reconcile_first',fencing_token=?,updated_at=? WHERE operation_id=?`, token, now, operationID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_leases SET fencing_token=?,state='recovery_required',updated_at=?,recovery_required=1 WHERE installation_id=? AND operation_id=?`, token, now, scope, operationID); err != nil {
		return err
	}
	if err = appendEvent(ctx, tx, operationID, scope, &token, "restart_reconciliation_started", "restart_observation", nil, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (c Coordinator) recoverOne(ctx context.Context, operationID, priorState string) error {
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var scope string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope); err != nil {
		return err
	}
	var token int64
	if err = tx.QueryRowContext(ctx, `SELECT fencing_token FROM installation_leases WHERE installation_id=? AND operation_id=?`, scope, operationID).Scan(&token); err != nil {
		return err
	}
	if priorState == StateAccepted {
		_, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='failed',current_phase='restart_before_mutation',outcome_confidence='confirmed_absent',recovery_disposition='retry_after_validation',updated_at=?,completed_at=?,error_code='HelperRestarted',error_detail='helper restarted before mutation intent was recorded' WHERE operation_id=?`, now, now, operationID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',updated_at=?,recovery_required=0 WHERE installation_id=? AND operation_id=?`, now, scope, operationID)
		}
		if err == nil {
			err = appendEvent(ctx, tx, operationID, scope, &token, "restart_classified_no_mutation", "restart_before_mutation", nil, now)
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='action_required',current_phase='interrupted_legacy_mutation',outcome_confidence='unknown',recovery_disposition='administrator_action',updated_at=?,completed_at=?,error_code='RecoveryRequired',error_detail='helper restarted after mutation intent; inspect the installation before releasing the recovery lock' WHERE operation_id=?`, now, now, operationID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='recovery_required',updated_at=?,recovery_required=1 WHERE installation_id=? AND operation_id=?`, now, scope, operationID)
		}
		if err == nil {
			err = appendEvent(ctx, tx, operationID, scope, &token, "restart_requires_administrator", "interrupted_legacy_mutation", nil, now)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveActionRequired is the narrow root-operated recovery action for this
// slice. It releases a blocked installation without inventing runtime success.
func (c Coordinator) ResolveActionRequired(ctx context.Context, operationID string) error {
	now := c.now()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var scope string
	if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM helper_operations WHERE operation_id=? AND state='action_required'`, operationID).Scan(&scope); err != nil {
		return fmt.Errorf("operation is not awaiting administrator recovery: %w", err)
	}
	var token int64
	if err = tx.QueryRowContext(ctx, `SELECT fencing_token FROM installation_leases WHERE installation_id=? AND operation_id=? AND state='recovery_required'`, scope, operationID).Scan(&token); err != nil {
		return fmt.Errorf("recovery lease unavailable: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='failed',current_phase='administrator_resolved',recovery_disposition='completed',updated_at=?,completed_at=?,error_code='AdministratorResolved',error_detail='administrator inspected the interrupted operation and released its recovery lock' WHERE operation_id=?`, now, now, operationID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',updated_at=?,recovery_required=0 WHERE installation_id=? AND operation_id=? AND fencing_token=?`, now, scope, operationID, token)
	}
	if err == nil {
		err = appendEvent(ctx, tx, operationID, scope, &token, "administrator_released_recovery_lock", "administrator_resolved", nil, now)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

const operationSelect = `SELECT operation_id,request_hash,request_json,protocol_version,operation_revision,operation_kind,installation_id,caller_uid,state,current_phase,outcome_confidence,recovery_disposition,expected_generation,fencing_token,accepted_at,started_at,updated_at,completed_at,result_json,error_code,error_detail FROM helper_operations`

func getTx(ctx context.Context, tx *sql.Tx, operationID string) (Record, error) {
	return getRow(tx.QueryRowContext(ctx, operationSelect+` WHERE operation_id=?`, operationID))
}

type rowScanner interface{ Scan(...any) error }

func getRow(row rowScanner) (Record, error) {
	var r Record
	var caller int64
	err := row.Scan(&r.OperationID, &r.RequestHash, &r.RequestJSON, &r.ProtocolVersion, &r.OperationRevision, &r.OperationKind, &r.InstallationID, &caller, &r.State, &r.CurrentPhase, &r.OutcomeConfidence, &r.RecoveryDisposition, &r.ExpectedGeneration, &r.FencingToken, &r.AcceptedAt, &r.StartedAt, &r.UpdatedAt, &r.CompletedAt, &r.ResultJSON, &r.ErrorCode, &r.ErrorDetail)
	r.CallerUID = uint32(caller)
	return r, err
}

func responseFor(record Record, requestID string) protocol.Response {
	response := protocol.Response{RequestID: requestID, OperationID: record.OperationID, State: record.State, Phase: record.CurrentPhase, ErrorCode: record.ErrorCode, Error: record.ErrorDetail}
	if record.ResultJSON != "" {
		var result any
		if json.Unmarshal([]byte(record.ResultJSON), &result) == nil {
			response.Result = result
		}
	}
	response.OK = record.State == StateSucceeded || record.State == StateAccepted || record.State == StateExecuting || record.State == StateReconciling
	response.Retryable = record.RecoveryDisposition == "retry_after_validation"
	return response
}

func verifyFence(ctx context.Context, tx *sql.Tx, operationID string, token int64) error {
	var scope string
	var operationToken int64
	if err := tx.QueryRowContext(ctx, `SELECT installation_id,fencing_token FROM helper_operations WHERE operation_id=?`, operationID).Scan(&scope, &operationToken); err != nil {
		return err
	}
	var holder string
	var leaseToken int64
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT operation_id,fencing_token,state FROM installation_leases WHERE installation_id=?`, scope).Scan(&holder, &leaseToken, &state); err != nil {
		return err
	}
	if holder != operationID || operationToken != token || leaseToken != token || state != "held" {
		return errors.New("stale operation fencing token")
	}
	return nil
}

func appendEvent(ctx context.Context, tx *sql.Tx, operationID, scope string, token *int64, kind, phase string, facts map[string]string, now string) error {
	encoded := "{}"
	if len(facts) != 0 {
		value, err := json.Marshal(facts)
		if err != nil {
			return err
		}
		encoded = string(value)
	}
	var persistedToken any
	if token != nil {
		persistedToken = *token
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO helper_operation_events(operation_id,installation_id,fencing_token,event_kind,phase,facts_json,created_at) VALUES(?,?,?,?,?,?,?)`, operationID, scope, persistedToken, kind, phase, encoded, now)
	return err
}

func expectedGeneration(request protocol.Request) any {
	value := request.RuntimeGeneration
	switch request.Operation {
	case "InstallApplication", "ConfigureServiceExposure", "UpdateApplication":
		// These existing handlers receive the target generation and independently
		// validate that helper ownership is at target-1. Persist that base so
		// recovery evidence describes the precondition, not the desired result.
		value--
	}
	if value < 0 {
		return nil
	}
	return value
}

func sanitize(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) > maxErrorBytes {
		value = value[:maxErrorBytes]
	}
	return value
}
