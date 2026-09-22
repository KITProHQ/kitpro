package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

func (s Store) PrepareMulti(ctx context.Context, plan MultiPlan) error {
	if len(plan.Components) < 2 {
		return errors.New("multi-component plan requires at least two components")
	}
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
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_components WHERE installation_id=? AND runtime_generation=?`, plan.InstallationID, plan.Generation).Scan(&count); err != nil {
			return err
		}
		if count != len(plan.Components) {
			return errors.New("failed target generation topology cannot be safely reused")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE runtime_components SET start_ordinal=start_ordinal+100 WHERE installation_id=? AND runtime_generation=?`, plan.InstallationID, plan.Generation); err != nil {
			return err
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_generations SET creating_operation_id=?,application_id=?,release_id=?,status='prepared',network_name=?,observed_network_id='',plan_hash=?,topology_hash=?,data_path=?,exposure_mode=?,service_id=?,host_address=?,host_port=?,container_port=?,service_protocol=?,created_at=?,verified_at=NULL,committed_at=NULL,retired_at=NULL,cleanup_state='not_required' WHERE installation_id=? AND runtime_generation=? AND status IN ('failed','removed') AND cleanup_state='clean'`, plan.OperationID, plan.ApplicationID, plan.ReleaseID, plan.NetworkName, plan.PlanHash, plan.TopologyHash, plan.DataPath, plan.ExposureMode, plan.ServiceID, plan.HostAddress, plan.HostPort, plan.ContainerPort, plan.ServiceProtocol, now, plan.InstallationID, plan.Generation)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("clean target generation reuse lost")
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,topology_hash,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state) VALUES(?,?,?,?,?,'prepared',?,?,?,?,?,?,?,?,?,?,?,'not_required')`, plan.InstallationID, plan.Generation, plan.OperationID, plan.ApplicationID, plan.ReleaseID, plan.NetworkName, plan.PlanHash, plan.TopologyHash, plan.DataPath, plan.ExposureMode, plan.ServiceID, plan.HostAddress, plan.HostPort, plan.ContainerPort, plan.ServiceProtocol, now)
		if err != nil {
			return err
		}
	}
	for ordinal, component := range plan.Components {
		if component.StartOrdinal != ordinal {
			return errors.New("component plan is not in normalized dependency order")
		}
		dependencies, _ := json.Marshal(component.DependsOn)
		if reusingCleanGeneration {
			result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_components SET container_name=?,observed_container_id='',image_digest=?,observed_image_id='',configuration_hash='',state='unknown',dependencies_json=?,start_ordinal=?,created_at=?,started_at=NULL,verified_at=NULL WHERE installation_id=? AND runtime_generation=? AND component_id=?`, component.ContainerName, component.Image, string(dependencies), ordinal, now, plan.InstallationID, plan.Generation, component.ID)
			if updateErr != nil {
				return updateErr
			}
			if changed, _ := result.RowsAffected(); changed != 1 {
				return errors.New("failed target generation component topology changed")
			}
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,image_digest,state,dependencies_json,start_ordinal,created_at) VALUES(?,?,?,?,?,'unknown',?,?,?)`, plan.InstallationID, plan.Generation, component.ID, component.ContainerName, component.Image, string(dependencies), ordinal, now)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// BackfillLegacyMulti preserves legacy component_ownership evidence after the
// helper has matched it to exactly one trusted catalog topology. It never
// infers ownership from runtime names or labels.
func (s Store) BackfillLegacyMulti(ctx context.Context, generation MultiGeneration) error {
	if len(generation.Components) < 2 || generation.TopologyHash == "" {
		return errors.New("legacy multi-component topology is incomplete")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_generations WHERE installation_id=?`, generation.InstallationID).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_generations(installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,plan_hash,topology_hash,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,created_at,cleanup_state) VALUES(?,?,?,?,?,'verification_required',?,?,?,?,?,?,?,?,?,?,?,'not_required')`, generation.InstallationID, generation.Generation, "legacy-multi-migration", generation.ApplicationID, generation.ReleaseID, generation.NetworkName, generation.PlanHash, generation.TopologyHash, generation.DataPath, generation.ExposureMode, generation.ServiceID, generation.HostAddress, generation.HostPort, generation.ContainerPort, generation.ServiceProtocol, s.now())
	if err != nil {
		return err
	}
	for _, component := range generation.Components {
		dependencies, _ := json.Marshal(component.DependsOn)
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_components(installation_id,runtime_generation,component_id,container_name,observed_container_id,image_digest,state,dependencies_json,start_ordinal,created_at) VALUES(?,?,?,?,?,?,'unknown',?,?,?)`, generation.InstallationID, generation.Generation, component.ID, component.ContainerName, component.ContainerID, component.Image, string(dependencies), component.StartOrdinal, s.now())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) PromoteMigratedMulti(ctx context.Context, generation MultiGeneration, networkID string, observations map[string]containers.ContainerObservation, runtimeState, operationID string, fencingToken int64) error {
	if runtimeState != "running" && runtimeState != "stopped" {
		return errors.New("migrated generation state is not promotable")
	}
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, generation.InstallationID, operationID, fencingToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',observed_network_id=?,verified_at=?,committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status='verification_required'`, networkID, now, now, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("migrated multi-component generation is not promotable")
	}
	for _, component := range generation.Components {
		observed, ok := observations[component.ID]
		if !ok {
			return errors.New("migrated component observation is incomplete")
		}
		result, err = tx.ExecContext(ctx, `UPDATE runtime_components SET observed_image_id=?,configuration_hash=?,state=?,verified_at=? WHERE installation_id=? AND runtime_generation=? AND component_id=? AND observed_container_id=?`, observed.ImageID, observationHash(observed), runtimeState, now, generation.InstallationID, generation.Generation, component.ID, component.ContainerID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("migrated component promotion lost")
		}
	}
	return tx.Commit()
}

func (s Store) RecordMultiNetwork(ctx context.Context, plan MultiPlan, networkID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='candidate',observed_network_id=? WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status='prepared'`, networkID, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("candidate network transition lost")
	}
	return nil
}

func (s Store) RecordMultiContainer(ctx context.Context, plan MultiPlan, componentID, containerID, imageID, configHash, state string, verified bool) error {
	now := s.now()
	verifiedAt := any(nil)
	if verified {
		verifiedAt = now
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_components SET observed_container_id=CASE WHEN ?<>'' THEN ? ELSE observed_container_id END,observed_image_id=CASE WHEN ?<>'' THEN ? ELSE observed_image_id END,configuration_hash=CASE WHEN ?<>'' THEN ? ELSE configuration_hash END,state=?,started_at=CASE WHEN ?='running' THEN COALESCE(started_at,?) ELSE started_at END,verified_at=COALESCE(?,verified_at) WHERE installation_id=? AND runtime_generation=? AND component_id=?`, containerID, containerID, imageID, imageID, configHash, configHash, state, state, now, verifiedAt, plan.InstallationID, plan.Generation, componentID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("component state transition lost")
	}
	return tx.Commit()
}

func (s Store) MultiCurrent(ctx context.Context, installation string) (MultiGeneration, error) {
	var generation int
	err := s.DB.QueryRowContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND status IN ('active','verification_required','removed') ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'verification_required' THEN 1 ELSE 2 END,runtime_generation DESC LIMIT 1`, installation).Scan(&generation)
	if err != nil {
		return MultiGeneration{}, err
	}
	return s.LoadMultiGeneration(ctx, installation, generation)
}

func (s Store) MultiByOperation(ctx context.Context, operationID string) (MultiGeneration, error) {
	var installation string
	var generation int
	err := s.DB.QueryRowContext(ctx, `SELECT installation_id,runtime_generation FROM runtime_generations WHERE creating_operation_id=? ORDER BY runtime_generation DESC LIMIT 1`, operationID).Scan(&installation, &generation)
	if err != nil {
		return MultiGeneration{}, err
	}
	return s.LoadMultiGeneration(ctx, installation, generation)
}

func (s Store) LoadMultiGeneration(ctx context.Context, installation string, generation int) (MultiGeneration, error) {
	var g MultiGeneration
	err := s.DB.QueryRowContext(ctx, `SELECT installation_id,runtime_generation,creating_operation_id,application_id,release_id,status,network_name,observed_network_id,plan_hash,topology_hash,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol,cleanup_state FROM runtime_generations WHERE installation_id=? AND runtime_generation=?`, installation, generation).Scan(&g.InstallationID, &g.Generation, &g.CreatingOperationID, &g.ApplicationID, &g.ReleaseID, &g.Status, &g.NetworkName, &g.NetworkID, &g.PlanHash, &g.TopologyHash, &g.DataPath, &g.ExposureMode, &g.ServiceID, &g.HostAddress, &g.HostPort, &g.ContainerPort, &g.ServiceProtocol, &g.CleanupState)
	if err != nil {
		return g, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT component_id,container_name,observed_container_id,image_digest,observed_image_id,configuration_hash,state,dependencies_json,start_ordinal FROM runtime_components WHERE installation_id=? AND runtime_generation=? ORDER BY start_ordinal,component_id`, installation, generation)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var component Component
		var dependencies string
		if err = rows.Scan(&component.ID, &component.ContainerName, &component.ContainerID, &component.Image, &component.ImageID, &component.ConfigurationHash, &component.State, &dependencies, &component.StartOrdinal); err != nil {
			return g, err
		}
		if err = json.Unmarshal([]byte(dependencies), &component.DependsOn); err != nil {
			return g, fmt.Errorf("stored component topology: %w", err)
		}
		g.Components = append(g.Components, component)
	}
	return g, rows.Err()
}

func (s Store) BeginComponentStep(ctx context.Context, plan MultiPlan, componentID, action, expectedID string, attempt int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO lifecycle_component_steps(operation_id,installation_id,runtime_generation,component_id,action,attempt,expected_runtime_id,intent_at,outcome_confidence) VALUES(?,?,?,?,?,?,?,?,'not_dispatched')`, plan.OperationID, plan.InstallationID, plan.Generation, componentID, action, attempt, expectedID, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) DispatchComponentStep(ctx context.Context, plan MultiPlan, componentID, action string, attempt int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE lifecycle_component_steps SET dispatched_at=?,outcome_confidence='unknown' WHERE operation_id=? AND runtime_generation=? AND component_id=? AND action=? AND attempt=? AND dispatched_at IS NULL`, s.now(), plan.OperationID, plan.Generation, componentID, action, attempt)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("component mutation dispatch evidence lost")
	}
	return tx.Commit()
}

func (s Store) FinishComponentStep(ctx context.Context, plan MultiPlan, componentID, action string, attempt int, observedID, confidence, detail string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE lifecycle_component_steps SET observed_runtime_id=?,verified_at=?,outcome_confidence=?,error_detail=? WHERE operation_id=? AND runtime_generation=? AND component_id=? AND action=? AND attempt=?`, observedID, s.now(), confidence, detail, plan.OperationID, plan.Generation, componentID, action, attempt)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("component mutation result evidence lost")
	}
	return tx.Commit()
}

func (s Store) ComponentSteps(ctx context.Context, operationID string) ([]ComponentStep, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT operation_id,installation_id,runtime_generation,component_id,action,attempt,expected_runtime_id,observed_runtime_id,intent_at,COALESCE(dispatched_at,''),COALESCE(verified_at,''),outcome_confidence,error_detail FROM lifecycle_component_steps WHERE operation_id=? ORDER BY runtime_generation,component_id,action,attempt`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComponentStep
	for rows.Next() {
		var step ComponentStep
		if err = rows.Scan(&step.OperationID, &step.InstallationID, &step.Generation, &step.ComponentID, &step.Action, &step.Attempt, &step.ExpectedRuntimeID, &step.ObservedRuntimeID, &step.IntentAt, &step.DispatchedAt, &step.VerifiedAt, &step.OutcomeConfidence, &step.ErrorDetail); err != nil {
			return nil, err
		}
		out = append(out, step)
	}
	return out, rows.Err()
}

// CommitMulti is the only multi-component active-generation switch. The row
// count and each component's verified state are checked inside the same fence-
// protected transaction that retires N and activates N+1.
func (s Store) CommitMulti(ctx context.Context, plan MultiPlan) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, plan.InstallationID, plan.OperationID, plan.FencingToken); err != nil {
		return err
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
	var status, topologyHash string
	if err = tx.QueryRowContext(ctx, `SELECT status,topology_hash FROM runtime_generations WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=?`, plan.InstallationID, plan.Generation, plan.OperationID).Scan(&status, &topologyHash); err != nil {
		return err
	}
	if status != "candidate" || topologyHash != plan.TopologyHash {
		return errors.New("candidate application topology is not committable")
	}
	var total, verified int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),SUM(CASE WHEN state='running' AND configuration_hash<>'' AND verified_at IS NOT NULL THEN 1 ELSE 0 END) FROM runtime_components WHERE installation_id=? AND runtime_generation=?`, plan.InstallationID, plan.Generation).Scan(&total, &verified); err != nil {
		return err
	}
	if total != len(plan.Components) || verified != total {
		return errors.New("not every required candidate component is verified")
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
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',verified_at=?,committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status='candidate'`, now, now, plan.InstallationID, plan.Generation, plan.OperationID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("candidate generation activation lost")
	}
	facts, _ := json.Marshal(map[string]string{"previous_generation": fmt.Sprint(plan.ExpectedGeneration), "active_generation": fmt.Sprint(plan.Generation), "component_count": fmt.Sprint(total)})
	if _, err = tx.ExecContext(ctx, `INSERT INTO helper_operation_events(operation_id,installation_id,fencing_token,event_kind,phase,facts_json,created_at) VALUES(?,?,?,?,?,?,?)`, plan.OperationID, plan.InstallationID, plan.FencingToken, "generation_committed", "generation_commit", string(facts), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) RecoverForwardMulti(ctx context.Context, candidate MultiGeneration, result Result) error {
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
		return errors.New("multi-component recovery fence is not valid")
	}
	var total, verified int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),SUM(CASE WHEN state='running' AND configuration_hash<>'' AND verified_at IS NOT NULL THEN 1 ELSE 0 END) FROM runtime_components WHERE installation_id=? AND runtime_generation=?`, candidate.InstallationID, candidate.Generation).Scan(&total, &verified); err != nil {
		return err
	}
	if total != len(candidate.Components) || verified != total || total < 2 || candidate.TopologyHash == "" {
		return errors.New("multi-component candidate is not eligible for forward recovery")
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
			result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='retained',retired_at=?,cleanup_state='not_required' WHERE installation_id=? AND runtime_generation=? AND status='active'`, now, candidate.InstallationID, expected)
			if updateErr != nil {
				return updateErr
			}
			if changed, _ := result.RowsAffected(); changed != 1 {
				return errors.New("recovery retirement lost")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_generations SET status='cleanup_pending',cleanup_state='pending' WHERE installation_id=? AND status='retained' AND runtime_generation<>?`, candidate.InstallationID, expected); err != nil {
		return err
	}
	resultUpdate, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='active',committed_at=?,cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status='candidate'`, now, candidate.InstallationID, candidate.Generation)
	if err != nil {
		return err
	}
	if changed, _ := resultUpdate.RowsAffected(); changed != 1 {
		return errors.New("recovery activation lost")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE helper_operations SET state='succeeded',current_phase='recovered_multi_generation_commit',outcome_confidence='confirmed_applied',recovery_disposition='completed',result_json=?,error_code='',error_detail='',updated_at=?,completed_at=? WHERE operation_id=? AND state='action_required' AND fencing_token=?`, string(encoded), now, now, candidate.CreatingOperationID, operationToken); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_leases SET state='released',recovery_required=0,updated_at=? WHERE installation_id=? AND operation_id=? AND fencing_token=? AND state='recovery_required'`, now, candidate.InstallationID, candidate.CreatingOperationID, leaseToken); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) RecordRecoveredMultiVerification(ctx context.Context, candidate MultiGeneration) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runtime_generations WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=?`, candidate.InstallationID, candidate.Generation, candidate.CreatingOperationID).Scan(&status); err != nil {
		return err
	}
	if status != "candidate" {
		return errors.New("only a candidate generation can record recovered verification")
	}
	for _, component := range candidate.Components {
		if component.ContainerID == "" || component.ConfigurationHash == "" {
			return errors.New("candidate recovery evidence is incomplete")
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_components SET state='running',verified_at=? WHERE installation_id=? AND runtime_generation=? AND component_id=? AND observed_container_id=? AND configuration_hash=?`, now, candidate.InstallationID, candidate.Generation, component.ID, component.ContainerID, component.ConfigurationHash)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errors.New("candidate recovery verification lost")
		}
	}
	return tx.Commit()
}

func verifyGenerationFence(ctx context.Context, tx *sql.Tx, installation, operation string, token int64) error {
	var holder, leaseState string
	var leaseToken, operationToken int64
	if err := tx.QueryRowContext(ctx, `SELECT l.operation_id,l.fencing_token,l.state,o.fencing_token FROM installation_leases l JOIN helper_operations o ON o.operation_id=l.operation_id WHERE l.installation_id=?`, installation).Scan(&holder, &leaseToken, &leaseState, &operationToken); err != nil {
		return err
	}
	if holder != operation || leaseState != "held" || leaseToken != token || operationToken != token {
		return errors.New("stale fencing token cannot commit generation")
	}
	return nil
}

func (s Store) MarkMultiFailed(ctx context.Context, plan MultiPlan, cleanupState string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runtime_generations SET status='failed',cleanup_state=? WHERE installation_id=? AND runtime_generation=? AND creating_operation_id=? AND status IN ('prepared','candidate')`, cleanupState, plan.InstallationID, plan.Generation, plan.OperationID)
	return err
}

func (s Store) MultiOlderRetained(ctx context.Context, installation string, keep int) ([]MultiGeneration, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND (status IN ('retained','cleanup_pending') OR (status='failed' AND cleanup_state='pending')) AND runtime_generation<>? ORDER BY runtime_generation`, installation, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var generations []int
	for rows.Next() {
		var generation int
		if err = rows.Scan(&generation); err != nil {
			return nil, err
		}
		generations = append(generations, generation)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out []MultiGeneration
	for _, generation := range generations {
		g, loadErr := s.LoadMultiGeneration(ctx, installation, generation)
		if loadErr != nil {
			return nil, loadErr
		}
		out = append(out, g)
	}
	return out, nil
}

func sortedComponentsReverse(components []Component) []Component {
	out := append([]Component(nil), components...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartOrdinal > out[j].StartOrdinal })
	return out
}
