package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

type Repairer struct {
	Runtime  containers.LifecycleRuntime
	Store    Store
	Evidence Evidence
}

func (r Repairer) Repair(ctx context.Context, installation, operationID string, fencingToken int64, action string) (ReconciliationResult, error) {
	if r.Runtime == nil || r.Store.DB == nil || r.Evidence == nil {
		return ReconciliationResult{}, errors.New("repair dependencies unavailable")
	}
	current, err := (Reconciler{Runtime: r.Runtime, Store: r.Store}).Reconcile(ctx, installation)
	if err != nil {
		return current, err
	}
	if err = r.Evidence.RecordPhase(ctx, operationID, fencingToken, "repair_validation", "confirmed", map[string]string{"repair_action": action, "classification": string(current.State)}); err != nil {
		return current, err
	}
	switch action {
	case RepairStartActive:
		if current.RecommendedAction != RepairStartActive && current.State != ReconciliationConsistent {
			return current, errors.New("active runtime is not eligible for exact start repair")
		}
		if err = r.startActive(ctx, installation, operationID, fencingToken); err != nil {
			return current, err
		}
	case RepairCleanupResources:
		if current.RecommendedAction != RepairCleanupResources && current.State != ReconciliationConsistent {
			return current, errors.New("installation is not eligible for exact resource cleanup")
		}
		if err = r.cleanupNonActive(ctx, installation, operationID, fencingToken); err != nil {
			return current, err
		}
	case RepairAcknowledgeRetainedLoss:
		if current.RecommendedAction != RepairAcknowledgeRetainedLoss && current.State != ReconciliationConsistent {
			return current, errors.New("retained generation loss is not eligible for acknowledgement")
		}
		if err = r.acknowledgeRetainedLoss(ctx, installation, operationID, fencingToken); err != nil {
			return current, err
		}
	default:
		return current, errors.New("unsupported repair action")
	}
	if err = r.Evidence.RecordPhase(ctx, operationID, fencingToken, "repair_verification", "intent", map[string]string{"repair_action": action}); err != nil {
		return current, err
	}
	result, err := (Reconciler{Runtime: r.Runtime, Store: r.Store}).Reconcile(ctx, installation)
	if err != nil {
		return result, err
	}
	if err = r.Evidence.RecordPhase(ctx, operationID, fencingToken, "repair_verification", "confirmed", map[string]string{"classification": string(result.State)}); err != nil {
		return result, err
	}
	return result, nil
}

func (r Repairer) startActive(ctx context.Context, installation, operationID string, token int64) error {
	generation, err := r.Store.MultiCurrent(ctx, installation)
	if err != nil {
		return err
	}
	if generation.Status != "active" {
		return errors.New("active generation unavailable")
	}
	plan := repairPlan(generation, operationID, token)
	for _, component := range generation.Components {
		observed, observeErr := r.verifyExactComponent(ctx, generation, component)
		if observeErr != nil {
			return observeErr
		}
		if observed.State == containers.RuntimeRunning {
			continue
		}
		if observed.State != containers.RuntimeStopped {
			return fmt.Errorf("component %s cannot be started from %s", component.ID, observed.State)
		}
		for _, dependency := range component.DependsOn {
			dependencyComponent := componentByID(generation.Components, dependency)
			if dependencyComponent == nil {
				return errors.New("stored dependency topology is incomplete")
			}
			dependencyObserved, dependencyErr := r.verifyExactComponent(ctx, generation, *dependencyComponent)
			if dependencyErr != nil || dependencyObserved.State != containers.RuntimeRunning {
				return fmt.Errorf("dependency %s is not running before %s", dependency, component.ID)
			}
		}
		if err = r.mutateComponent(ctx, plan, generation, component, "repair_start", containers.RuntimeRunning); err != nil {
			return err
		}
		if err = r.Store.RecordMultiContainer(ctx, plan, component.ID, component.ContainerID, "", "", "running", true); err != nil {
			return err
		}
	}
	return nil
}

func (r Repairer) cleanupNonActive(ctx context.Context, installation, operationID string, token int64) error {
	generations, err := r.Store.ReconciliationGenerations(ctx, installation)
	if err != nil {
		return err
	}
	for _, generation := range generations {
		if generation.Status == "active" || generation.Status == "verification_required" || generation.Status == "retained" {
			continue
		}
		plan := repairPlan(generation, operationID, token)
		for _, component := range reverseComponents(generation.Components) {
			observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
			if observeErr != nil {
				return observeErr
			}
			if !observed.Exists {
				byName, nameErr := r.Runtime.ObserveContainer(ctx, component.ContainerName)
				if nameErr != nil {
					return nameErr
				}
				if byName.Exists {
					return errors.New("cleanup refused because expected name is occupied by another runtime object")
				}
				continue
			}
			if _, observeErr = r.verifyExactComponent(ctx, generation, component); observeErr != nil {
				return observeErr
			}
			if observed.State == containers.RuntimeRunning {
				if err = r.mutateComponent(ctx, plan, generation, component, "repair_stop", containers.RuntimeStopped); err != nil {
					return err
				}
			}
			if err = r.mutateComponent(ctx, plan, generation, component, "repair_remove", containers.RuntimeMissing); err != nil {
				return err
			}
		}
		network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if observeErr != nil {
			return observeErr
		}
		if network.Exists && generation.NetworkID != "" && network.ID != generation.NetworkID {
			return errors.New("cleanup refused because network identity changed")
		}
		if network.Exists {
			if err = r.Evidence.RecordPhase(ctx, operationID, token, "repair_remove_network", "intent", map[string]string{"generation": fmt.Sprint(generation.Generation), "network_id": network.ID}); err != nil {
				return err
			}
			removeErr := r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName)
			removed, verifyErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
			if verifyErr != nil {
				return UnknownOutcomeError{Phase: PhaseCleanup, Cause: firstError(removeErr, verifyErr)}
			}
			if removed.Exists {
				return firstError(removeErr, errors.New("cleanup network removal was not applied"))
			}
			if err = r.Evidence.RecordPhase(ctx, operationID, token, "repair_remove_network", "confirmed", map[string]string{"generation": fmt.Sprint(generation.Generation)}); err != nil {
				return err
			}
		}
		if err = r.Store.MarkNonActiveRemoved(ctx, generation, operationID, token); err != nil {
			return err
		}
	}
	return nil
}

func (r Repairer) acknowledgeRetainedLoss(ctx context.Context, installation, operationID string, token int64) error {
	generations, err := r.Store.ReconciliationGenerations(ctx, installation)
	if err != nil {
		return err
	}
	changed := false
	for _, generation := range generations {
		if generation.Status != "retained" {
			continue
		}
		allMissing := true
		for _, component := range generation.Components {
			observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
			if observeErr != nil {
				return observeErr
			}
			if observed.Exists {
				allMissing = false
			}
		}
		if !allMissing {
			continue
		}
		network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if observeErr != nil {
			return observeErr
		}
		if network.Exists {
			if err = r.Store.MarkRetainedCleanupPending(ctx, generation, operationID, token); err != nil {
				return err
			}
		} else if err = r.Store.MarkNonActiveRemoved(ctx, generation, operationID, token); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return errors.New("no exactly missing retained generation found")
	}
	return nil
}

func (s Store) MarkRetainedCleanupPending(ctx context.Context, generation MultiGeneration, operationID string, token int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, generation.InstallationID, operationID, token); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='cleanup_pending',cleanup_state='pending' WHERE installation_id=? AND runtime_generation=? AND status='retained'`, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("retained cleanup transition lost")
	}
	return tx.Commit()
}

func (r Repairer) mutateComponent(ctx context.Context, plan MultiPlan, generation MultiGeneration, component Component, action string, want containers.RuntimeState) error {
	if err := r.Store.BeginComponentStep(ctx, plan, component.ID, action, component.ContainerID, 1); err != nil {
		return err
	}
	if err := r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, action, "intent", map[string]string{"component_id": component.ID, "runtime_id": component.ContainerID}); err != nil {
		return err
	}
	if err := r.Store.DispatchComponentStep(ctx, plan, component.ID, action, 1); err != nil {
		return err
	}
	var mutationErr error
	switch action {
	case "repair_start":
		mutationErr = r.Runtime.StartContainer(ctx, component.ContainerID)
	case "repair_stop":
		mutationErr = r.Runtime.StopContainer(ctx, component.ContainerID)
	case "repair_remove":
		mutationErr = r.Runtime.RemoveContainer(ctx, component.ContainerID)
	default:
		return errors.New("unsupported repair mutation")
	}
	observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
	if observeErr != nil {
		_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, "", "unknown", stableError(mutationErr, observeErr))
		return UnknownOutcomeError{Phase: PhaseCleanup, Cause: firstError(mutationErr, observeErr)}
	}
	applied := observed.State == want
	if want == containers.RuntimeMissing {
		applied = !observed.Exists || observed.State == containers.RuntimeMissing
	}
	if !applied {
		detail := stableError(mutationErr, errors.New("repair postcondition was not achieved"))
		_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "confirmed_absent", detail)
		return errors.New(detail)
	}
	if want != containers.RuntimeMissing {
		verified, verifyErr := r.verifyExactComponent(ctx, generation, component)
		if verifyErr != nil || verified.State != want {
			detail := stableError(mutationErr, firstError(verifyErr, errors.New("repair identity verification failed")))
			_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "unknown", detail)
			return UnknownOutcomeError{Phase: PhaseBeforeVerification, Cause: errors.New(detail)}
		}
	}
	detail := ""
	if mutationErr != nil {
		detail = mutationErr.Error()
	}
	if err := r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "confirmed_applied", detail); err != nil {
		return err
	}
	return r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, action, "confirmed", map[string]string{"component_id": component.ID})
}

func (r Repairer) verifyExactComponent(ctx context.Context, generation MultiGeneration, component Component) (containers.ContainerObservation, error) {
	observed, err := r.Runtime.ObserveContainer(ctx, component.ContainerID)
	if err != nil {
		return observed, err
	}
	if !observed.Exists || observed.ID != component.ContainerID || observed.Name != component.ContainerName || observed.ImageReference != component.Image || observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
		return observed, errors.New("repair refused because component ownership is not exact")
	}
	if component.ImageID != "" && observed.ImageID != component.ImageID {
		return observed, errors.New("repair refused because component image identity changed")
	}
	if component.ConfigurationHash != "" && observationHash(observed) != component.ConfigurationHash {
		return observed, errors.New("repair refused because component configuration changed")
	}
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil {
		return observed, err
	}
	attachment, attached := observed.Networks[generation.NetworkName]
	stoppedBeforeFirstStart := (generation.Status == "prepared" || generation.Status == "candidate" || generation.Status == "failed" || generation.Status == "cleanup_pending") && observed.State == containers.RuntimeStopped && attachment.NetworkID == "" && observed.NetworkMode == generation.NetworkName
	if !network.Exists || (generation.NetworkID != "" && network.ID != generation.NetworkID) || !attached || (attachment.NetworkID != network.ID && !stoppedBeforeFirstStart) {
		return observed, errors.New("repair refused because component network identity changed")
	}
	return observed, nil
}

func repairPlan(generation MultiGeneration, operationID string, token int64) MultiPlan {
	plan := MultiPlan{OperationID: operationID, InstallationID: generation.InstallationID, ApplicationID: generation.ApplicationID, ReleaseID: generation.ReleaseID, Generation: generation.Generation, ExpectedGeneration: generation.Generation, FencingToken: token, NetworkName: generation.NetworkName, PlanHash: generation.PlanHash, TopologyHash: generation.TopologyHash, DataPath: generation.DataPath, ExposureMode: generation.ExposureMode, ServiceID: generation.ServiceID, HostAddress: generation.HostAddress, HostPort: generation.HostPort, ContainerPort: generation.ContainerPort, ServiceProtocol: generation.ServiceProtocol}
	for _, component := range generation.Components {
		plan.Components = append(plan.Components, MultiComponentPlan{ID: component.ID, Image: component.Image, ContainerName: component.ContainerName, DependsOn: append([]string(nil), component.DependsOn...), StartOrdinal: component.StartOrdinal})
	}
	return plan
}

func (s Store) MarkNonActiveRemoved(ctx context.Context, generation MultiGeneration, operationID string, token int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = verifyGenerationFence(ctx, tx, generation.InstallationID, operationID, token); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='removed',cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status IN ('retained','cleanup_pending','failed','candidate','prepared')`, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("non-active generation cleanup transition lost")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_components SET state='removed' WHERE installation_id=? AND runtime_generation=?`, generation.InstallationID, generation.Generation); err != nil {
		return err
	}
	return tx.Commit()
}
