package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/exposure"
)

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s Store) now() string {
	if s.Now != nil {
		return s.Now().UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func (s Store) Current(ctx context.Context, installation string) (Generation, error) {
	g, err := scanGeneration(s.DB.QueryRowContext(ctx, generationSelect+` WHERE g.installation_id=? AND g.status IN ('active','verification_required','removed') ORDER BY CASE g.status WHEN 'active' THEN 0 WHEN 'verification_required' THEN 1 ELSE 2 END, g.runtime_generation DESC LIMIT 1`, installation))
	if err == nil {
		g.Bindings, err = s.loadBindings(ctx, g.InstallationID, g.Generation)
	}
	return g, err
}

func (s Store) BackfillInstallation(ctx context.Context, installation string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state) SELECT instance_id,runtime_generation,'legacy-migration',application_id,release_id,'verification_required',network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,'not_required' FROM ownership WHERE instance_id=? AND runtime_generation>0 AND container_id<>''`, installation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,state,created_at) SELECT instance_id,runtime_generation,'app',container_name,container_id,image_digest,'unknown',created_at FROM ownership WHERE instance_id=? AND runtime_generation>0 AND container_id<>''`, installation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state) SELECT instance_id,runtime_generation,'legacy-migration',application_id,release_id,'removed',network_name,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,'clean' FROM ownership WHERE instance_id=? AND runtime_generation>0 AND container_id=''`, installation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,state,created_at) SELECT instance_id,runtime_generation,'app',container_name,container_id,image_digest,'removed',created_at FROM ownership WHERE instance_id=? AND runtime_generation>0 AND container_id=''`, installation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO runtime_generation_bindings(installation_id,runtime_generation,service_id,transport,container_port,mode,host_address,host_port) SELECT instance_id,runtime_generation,service_id,CASE WHEN service_protocol='udp' THEN 'udp' ELSE 'tcp' END,container_port,exposure_mode,host_address,host_port FROM ownership WHERE instance_id=? AND service_id<>'' AND container_port BETWEEN 1 AND 65535`, installation)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) ByOperation(ctx context.Context, operationID string) (Generation, error) {
	g, err := scanGeneration(s.DB.QueryRowContext(ctx, generationSelect+` WHERE g.creating_operation_id=? ORDER BY g.runtime_generation DESC LIMIT 1`, operationID))
	if err == nil {
		g.Bindings, err = s.loadBindings(ctx, g.InstallationID, g.Generation)
	}
	return g, err
}

func (s Store) PromoteMigrated(ctx context.Context, generation Generation, networkID, imageID, configurationHash string) error {
	return s.PromoteMigratedState(ctx, generation, networkID, imageID, configurationHash, "running", "", 0)
}

func (s Store) RecordMigratedConfiguration(ctx context.Context, installation string, generation int, configurationHash string) error {
	if configurationHash == "" {
		return errors.New("migrated configuration hash is required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE runtime_components SET configuration_hash=? WHERE installation_id=? AND runtime_generation=? AND component_id='app' AND configuration_hash='' AND EXISTS (SELECT 1 FROM runtime_generations g WHERE g.installation_id=runtime_components.installation_id AND g.runtime_generation=runtime_components.runtime_generation AND g.status='verification_required' AND g.topology_hash='')`, configurationHash, installation, generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("migrated configuration expectation is not recordable")
	}
	return nil
}

func (s Store) PromoteMigratedState(ctx context.Context, generation Generation, networkID, imageID, configurationHash, runtimeState, operationID string, token int64) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if operationID != "" {
		if err = verifyGenerationFence(ctx, tx, generation.InstallationID, operationID, token); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',observed_network_id=?,verified_at=?,committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status='verification_required'`, networkID, now, now, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("migrated generation is not promotable")
	}
	_, err = tx.ExecContext(ctx, `UPDATE runtime_components SET observed_image_id=?,configuration_hash=?,state=?,verified_at=? WHERE installation_id=? AND runtime_generation=? AND component_id='app'`, imageID, configurationHash, runtimeState, now, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) MarkActiveRemoved(ctx context.Context, installation string, generation int, operationID string, token int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, installation, operationID, token); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='removed',cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status='active'`, installation, generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("active runtime removal transition lost")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_components SET state='removed' WHERE installation_id=? AND runtime_generation=?`, installation, generation); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) Prepare(ctx context.Context, plan Plan) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingStatus, existingCleanup string
	existingErr := tx.QueryRowContext(ctx, `SELECT status,cleanup_state FROM runtime_generations WHERE installation_id=? AND runtime_generation=?`, plan.InstallationID, plan.Generation).Scan(&existingStatus, &existingCleanup)
	reusingCleanGeneration := existingErr == nil
	if reusingCleanGeneration {
		if (existingStatus != "failed" && existingStatus != "removed") || existingCleanup != "clean" {
			return errors.New("target generation already has unresolved lifecycle evidence")
		}
		var componentID string
		if err = tx.QueryRowContext(ctx, `SELECT component_id FROM runtime_components WHERE installation_id=? AND runtime_generation=?`, plan.InstallationID, plan.Generation).Scan(&componentID); err != nil {
			return err
		}
		if componentID != "app" {
			return errors.New("clean target generation topology cannot be safely reused")
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_generations SET creating_operation_id=?,application_id=?,release_id=?,status='prepared',network_name=?,observed_network_id='',plan_hash=?,topology_hash='',data_path=?,exposure_mode='internal',service_id='',host_address='',host_port=0,container_port=0,service_protocol='',created_at=?,verified_at=NULL,committed_at=NULL,retired_at=NULL,cleanup_state='not_required' WHERE installation_id=? AND runtime_generation=? AND status IN ('failed','removed') AND cleanup_state='clean'`, plan.OperationID, plan.ApplicationID, plan.ReleaseID, plan.NetworkName, plan.PlanHash, plan.DataPath, now, plan.InstallationID, plan.Generation)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("clean target generation reuse lost")
		}
		result, updateErr = tx.ExecContext(ctx, `UPDATE runtime_components SET container_name=?,observed_container_id='',image_digest=?,observed_image_id='',configuration_hash='',state='unknown',dependencies_json='[]',start_ordinal=0,created_at=?,started_at=NULL,verified_at=NULL WHERE installation_id=? AND runtime_generation=? AND component_id='app'`, plan.ContainerName, plan.Image, now, plan.InstallationID, plan.Generation)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("removed target generation component reuse lost")
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,data_path,created_at,cleanup_state) VALUES(?,?,?,?,?,'prepared',?,?,?,?,'not_required')`, plan.InstallationID, plan.Generation, plan.OperationID, plan.ApplicationID, plan.ReleaseID, plan.NetworkName, plan.PlanHash, plan.DataPath, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,image_digest,state,created_at) VALUES(?,?,'app',?,?,'unknown',?)`, plan.InstallationID, plan.Generation, plan.ContainerName, plan.Image, now)
		if err != nil {
			return err
		}
	}
	if err = replaceBindings(ctx, tx, plan.InstallationID, plan.Generation, plan.Bindings); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) RecordNetwork(ctx context.Context, plan Plan, networkID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='candidate',observed_network_id=? WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status='prepared'`, networkID, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("candidate network transition lost")
	}
	return nil
}

func (s Store) RecordContainer(ctx context.Context, plan Plan, containerID, imageID, configHash, state string) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_components SET observed_container_id=?,observed_image_id=?,configuration_hash=?,state=?,created_at=?,verified_at=CASE WHEN ?='stopped' THEN ? ELSE verified_at END WHERE installation_id=? AND runtime_generation=? AND component_id='app' AND EXISTS (SELECT 1 FROM runtime_generations WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status='candidate')`, containerID, imageID, configHash, state, now, state, now, plan.InstallationID, plan.Generation, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("candidate container transition lost")
	}
	return tx.Commit()
}

func (s Store) RecordState(ctx context.Context, plan Plan, state string, verified bool) error {
	now := s.now()
	verifiedAt := any(nil)
	if verified {
		verifiedAt = now
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE runtime_components SET state=?,started_at=CASE WHEN ?='running' THEN COALESCE(started_at,?) ELSE started_at END,verified_at=COALESCE(?,verified_at) WHERE installation_id=? AND runtime_generation=? AND component_id='app'`, state, state, now, verifiedAt, plan.InstallationID, plan.Generation)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("candidate state transition lost")
	}
	return nil
}

// Commit is the sole authoritative generation switch. It verifies the helper
// fence, the expected current generation, and candidate verification in the
// same SQLite transaction that retires N and activates N+1.
func (s Store) Commit(ctx context.Context, plan Plan) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var holder, leaseState string
	var leaseToken, operationToken int64
	if err = tx.QueryRowContext(ctx, `SELECT l.operation_id,l.fencing_token,l.state,o.fencing_token FROM installation_leases l JOIN helper_operations o ON o.operation_id=l.operation_id WHERE l.installation_id=?`, plan.InstallationID).Scan(&holder, &leaseToken, &leaseState, &operationToken); err != nil {
		return err
	}
	if holder != plan.OperationID || leaseState != "held" || leaseToken != plan.FencingToken || operationToken != plan.FencingToken {
		return errors.New("stale fencing token cannot commit generation")
	}
	var current int
	currentActive := true
	err = tx.QueryRowContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND status='active'`, plan.InstallationID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		currentActive = false
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(runtime_generation),0) FROM runtime_generations WHERE installation_id=? AND status='removed'`, plan.InstallationID).Scan(&current)
	}
	if err != nil {
		return err
	}
	if current != plan.ExpectedGeneration {
		return fmt.Errorf("active generation changed: expected %d observed %d", plan.ExpectedGeneration, current)
	}
	var status, componentState, configHash string
	if err = tx.QueryRowContext(ctx, `SELECT g.status,c.state,c.configuration_hash FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id=? AND g.runtime_generation=? AND g.creating_operation_id=? AND c.component_id='app'`, plan.InstallationID, plan.Generation, plan.OperationID).Scan(&status, &componentState, &configHash); err != nil {
		return err
	}
	if status != "candidate" || componentState != "running" || configHash == "" {
		return errors.New("candidate generation has not been verified")
	}
	if currentActive {
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='retained',retired_at=?,cleanup_state='not_required' WHERE installation_id=? AND runtime_generation=? AND status='active'`, now, plan.InstallationID, plan.ExpectedGeneration)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("active generation retirement lost")
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_generations SET status='cleanup_pending',cleanup_state='pending' WHERE installation_id=? AND status='retained' AND runtime_generation<>?`, plan.InstallationID, plan.ExpectedGeneration); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',verified_at=COALESCE(verified_at,?),committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status='candidate'`, now, now, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("candidate generation activation lost")
	}
	facts, _ := json.Marshal(map[string]string{"previous_generation": fmt.Sprint(plan.ExpectedGeneration), "active_generation": fmt.Sprint(plan.Generation)})
	_, err = tx.ExecContext(ctx, `INSERT INTO helper_operation_events(operation_id,installation_id,fencing_token,event_kind,phase,facts_json,created_at) VALUES(?,?,?,?,?,?,?)`, plan.OperationID, plan.InstallationID, plan.FencingToken, "generation_committed", "generation_commit", string(facts), now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) MarkFailed(ctx context.Context, plan Plan, cleanupState string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='failed',cleanup_state=? WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status IN ('prepared','candidate')`, cleanupState, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("candidate failure transition lost")
	}
	return nil
}

func (s Store) OlderRetained(ctx context.Context, installation string, keepGeneration int) ([]Generation, error) {
	rows, err := s.DB.QueryContext(ctx, generationSelect+` WHERE g.installation_id=? AND (g.status IN ('retained','cleanup_pending') OR (g.status='failed' AND g.cleanup_state='pending')) AND g.runtime_generation<>? ORDER BY g.runtime_generation`, installation, keepGeneration)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Generation
	for rows.Next() {
		generation, scanErr := scanGeneration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, generation)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Bindings, err = s.loadBindings(ctx, out[i].InstallationID, out[i].Generation)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s Store) MarkRemoved(ctx context.Context, generation Generation) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='removed',cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status IN ('retained','cleanup_pending','failed')`, generation.InstallationID, generation.Generation)
	if err == nil {
		_, err = s.DB.ExecContext(ctx, `UPDATE runtime_components SET state='removed' WHERE installation_id=? AND runtime_generation=?`, generation.InstallationID, generation.Generation)
	}
	return err
}

func (s Store) MarkCleanupPending(ctx context.Context, generation Generation) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='cleanup_pending',cleanup_state='pending' WHERE installation_id=? AND runtime_generation=? AND status='retained'`, generation.InstallationID, generation.Generation)
	return err
}

// RecoverForward atomically makes an already verified, still-exact candidate
// authoritative and resolves the recovery lease created by helper startup.
// Runtime observation must happen immediately before this transaction.
func (s Store) RecoverForward(ctx context.Context, candidate Generation, result Result) error {
	now := s.now()
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var operationState, leaseState, holder string
	var operationToken, leaseToken int64
	if err = tx.QueryRowContext(ctx, `SELECT o.state,o.fencing_token,l.state,l.operation_id,l.fencing_token FROM helper_operations o JOIN installation_leases l ON l.installation_id=o.installation_id WHERE o.operation_id=?`, candidate.CreatingOperationID).Scan(&operationState, &operationToken, &leaseState, &holder, &leaseToken); err != nil {
		return err
	}
	if operationState != "action_required" || leaseState != "recovery_required" || holder != candidate.CreatingOperationID || operationToken != leaseToken {
		return errors.New("generation recovery fence is not valid")
	}
	var status, componentState, configHash string
	if err = tx.QueryRowContext(ctx, `SELECT g.status,c.state,c.configuration_hash FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id=? AND g.runtime_generation=? AND g.creating_operation_id=? AND c.component_id='app'`, candidate.InstallationID, candidate.Generation, candidate.CreatingOperationID).Scan(&status, &componentState, &configHash); err != nil {
		return err
	}
	if status != "candidate" || componentState != "running" || configHash == "" {
		return errors.New("candidate is not eligible for forward recovery")
	}
	expected := candidate.Generation - 1
	if expected > 0 {
		var active int
		activeErr := tx.QueryRowContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND status='active'`, candidate.InstallationID).Scan(&active)
		if errors.Is(activeErr, sql.ErrNoRows) {
			if err = tx.QueryRowContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND runtime_generation=? AND status='removed'`, candidate.InstallationID, expected).Scan(&active); err != nil {
				return errors.New("recovery baseline generation changed")
			}
		} else if activeErr != nil {
			return activeErr
		} else {
			if active != expected {
				return errors.New("active generation changed during recovery")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE runtime_generations SET status='retained',retired_at=?,cleanup_state='not_required' WHERE installation_id=? AND runtime_generation=? AND status='active'`, now, candidate.InstallationID, expected); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_generations SET status='cleanup_pending',cleanup_state='pending' WHERE installation_id=? AND status='retained' AND runtime_generation<>?`, candidate.InstallationID, expected); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status='candidate'`, now, candidate.InstallationID, candidate.Generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='succeeded',current_phase='recovered_generation_commit',outcome_confidence='confirmed_applied',recovery_disposition='completed',result_json=?,error_code='',error_detail='',updated_at=?,completed_at=? WHERE operation_id=? AND state='action_required' AND fencing_token=?`, string(encoded), now, now, candidate.CreatingOperationID, operationToken); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',recovery_required=0,updated_at=? WHERE installation_id=? AND operation_id=? AND fencing_token=? AND state='recovery_required'`, now, candidate.InstallationID, candidate.CreatingOperationID, leaseToken); err != nil {
		return err
	}
	facts, _ := json.Marshal(map[string]string{"active_generation": fmt.Sprint(candidate.Generation)})
	if _, err = tx.ExecContext(ctx, `INSERT INTO helper_operation_events(operation_id,installation_id,fencing_token,event_kind,phase,facts_json,created_at) VALUES(?,?,?,?,?,?,?)`, candidate.CreatingOperationID, candidate.InstallationID, operationToken, "generation_recovered", "recovered_generation_commit", string(facts), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) FinalizeCommittedRecovery(ctx context.Context, generation Generation, result Result) error {
	now := s.now()
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var token int64
	if err = tx.QueryRowContext(ctx, `SELECT o.fencing_token FROM helper_operations o JOIN installation_leases l ON l.installation_id=o.installation_id AND l.operation_id=o.operation_id AND l.fencing_token=o.fencing_token WHERE o.operation_id=? AND o.state='action_required' AND l.state='recovery_required'`, generation.CreatingOperationID).Scan(&token); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='succeeded',current_phase='recovered_post_commit',outcome_confidence='confirmed_applied',recovery_disposition='completed',result_json=?,error_code='',error_detail='',updated_at=?,completed_at=? WHERE operation_id=? AND state='action_required'`, string(encoded), now, now, generation.CreatingOperationID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',recovery_required=0,updated_at=? WHERE installation_id=? AND operation_id=? AND fencing_token=?`, now, generation.InstallationID, generation.CreatingOperationID, token); err != nil {
		return err
	}
	return tx.Commit()
}

const generationSelect = `SELECT g.installation_id,g.runtime_generation,g.creating_operation_id,g.application_id,g.release_id,g.status,g.network_name,g.observed_network_id,g.plan_hash,g.data_path,g.cleanup_state,c.component_id,c.container_name,c.observed_container_id,c.image_digest,c.observed_image_id,c.configuration_hash,c.state FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation)`

type scanner interface{ Scan(...any) error }

func scanGeneration(row scanner) (Generation, error) {
	var g Generation
	err := row.Scan(&g.InstallationID, &g.Generation, &g.CreatingOperationID, &g.ApplicationID, &g.ReleaseID, &g.Status, &g.NetworkName, &g.NetworkID, &g.PlanHash, &g.DataPath, &g.CleanupState, &g.Component.ID, &g.Component.ContainerName, &g.Component.ContainerID, &g.Component.Image, &g.Component.ImageID, &g.Component.ConfigurationHash, &g.Component.State)
	return g, err
}

type bindingExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func replaceBindings(ctx context.Context, db bindingExecer, installation string, generation int, bindings []exposure.ServiceBinding) error {
	if len(bindings) > exposure.MaxBindings {
		return errors.New("too many service bindings")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM runtime_generation_bindings WHERE installation_id=? AND runtime_generation=?`, installation, generation); err != nil {
		return err
	}
	for _, b := range exposure.Normalize(bindings) {
		if _, err := db.ExecContext(ctx, `INSERT INTO runtime_generation_bindings(installation_id,runtime_generation,service_id,transport,container_port,mode,host_address,host_port) VALUES(?,?,?,?,?,?,?,?)`, installation, generation, b.ServiceID, b.Transport, b.ContainerPort, b.Mode, b.HostAddress, b.HostPort); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) loadBindings(ctx context.Context, installation string, generation int) ([]exposure.ServiceBinding, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT service_id,transport,container_port,mode,host_address,host_port FROM runtime_generation_bindings WHERE installation_id=? AND runtime_generation=? ORDER BY service_id`, installation, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exposure.ServiceBinding
	for rows.Next() {
		var b exposure.ServiceBinding
		if err = rows.Scan(&b.ServiceID, &b.Transport, &b.ContainerPort, &b.Mode, &b.HostAddress, &b.HostPort); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
