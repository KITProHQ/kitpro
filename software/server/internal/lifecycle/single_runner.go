package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

// SingleRunner applies ordinary lifecycle changes to one helper-owned active
// generation. It records every runtime dispatch in lifecycle_component_steps
// and verifies the exact runtime object after the call returns.
type SingleRunner struct {
	Runtime  containers.LifecycleRuntime
	Store    Store
	Evidence Evidence
}

func (r SingleRunner) Operate(ctx context.Context, installation, operationID string, fencingToken int64, action string) (Result, error) {
	if r.Runtime == nil || r.Store.DB == nil || r.Evidence == nil {
		return Result{}, errors.New("single-component lifecycle dependencies unavailable")
	}
	if action != "start" && action != "stop" && action != "restart" && action != "remove" {
		return Result{}, errors.New("unsupported single-component lifecycle action")
	}
	if err := r.Evidence.RecordPhase(ctx, operationID, fencingToken, "single_lifecycle_validation", "intent", map[string]string{"lifecycle_action": action}); err != nil {
		return Result{}, err
	}
	generation, err := r.Store.Current(ctx, installation)
	if err != nil {
		return Result{}, err
	}
	if generation.Status == "removed" {
		return Result{}, errors.New("active runtime is missing; reconciliation and recreation are required")
	}
	observed, network, err := r.observeExact(ctx, generation)
	if err != nil {
		return Result{}, err
	}
	if generation.Status == "verification_required" {
		if !singleRuntimeStateClassifiable(observed.State) {
			return Result{}, errors.New("migrated runtime state is not safely classifiable")
		}
		persistedState := string(observed.State)
		if observed.State == containers.RuntimeRestarting || observed.State == containers.RuntimePaused {
			persistedState = string(containers.RuntimeUnknown)
		}
		if err = r.Store.PromoteMigratedState(ctx, generation, network.ID, observed.ImageID, observationHash(observed), persistedState, operationID, fencingToken); err != nil {
			return Result{}, err
		}
		generation.Status = "active"
		generation.NetworkID = network.ID
		generation.Component.ImageID = observed.ImageID
		generation.Component.ConfigurationHash = observationHash(observed)
		generation.Component.State = persistedState
	}
	if generation.Status != "active" {
		return Result{}, errors.New("active single-component generation unavailable")
	}
	plan := MultiPlan{OperationID: operationID, InstallationID: installation, ApplicationID: generation.ApplicationID, ReleaseID: generation.ReleaseID, Generation: generation.Generation, ExpectedGeneration: generation.Generation, FencingToken: fencingToken, NetworkName: generation.NetworkName, PlanHash: generation.PlanHash, DataPath: generation.DataPath, ExposureMode: generation.ExposureMode, ServiceID: generation.ServiceID, HostAddress: generation.HostAddress, HostPort: generation.HostPort, ContainerPort: generation.ContainerPort, ServiceProtocol: generation.ServiceProtocol}
	component := generation.Component
	mutator := MultiRunner{Runtime: r.Runtime, Store: r.Store, Evidence: r.Evidence}

	apply := func(mutation string, want containers.RuntimeState) error {
		current, _, observeErr := r.observeExact(ctx, generation)
		if observeErr != nil {
			return observeErr
		}
		if current.State == want {
			if err := r.Store.BeginComponentStep(ctx, plan, component.ID, mutation, component.ContainerID, 1); err != nil {
				return err
			}
			if err := r.Store.FinishComponentStep(ctx, plan, component.ID, mutation, 1, component.ContainerID, "confirmed_applied", "already in requested state"); err != nil {
				return err
			}
			return r.Evidence.RecordPhase(ctx, operationID, fencingToken, "single_"+mutation, "confirmed", map[string]string{"runtime_id": component.ContainerID, "idempotent": "true"})
		}
		if mutation == "start" && current.State != containers.RuntimeStopped {
			return errors.New("active runtime must be stopped before it can be started")
		}
		if mutation == "stop" && !singleRuntimeStateStoppable(current.State) {
			return errors.New("active runtime state is not safely stoppable")
		}
		if err := mutator.mutateExisting(ctx, plan, component, mutation, want); err != nil {
			return err
		}
		state := "stopped"
		if want == containers.RuntimeRunning {
			state = "running"
		}
		if err := r.Store.RecordMultiContainer(ctx, plan, component.ID, component.ContainerID, "", "", state, want == containers.RuntimeRunning); err != nil {
			return UnknownOutcomeError{Phase: phaseForAction(mutation), Cause: err}
		}
		return r.Evidence.RecordPhase(ctx, operationID, fencingToken, "single_"+mutation, "confirmed", map[string]string{"runtime_id": component.ContainerID})
	}

	switch action {
	case "start":
		err = apply("start", containers.RuntimeRunning)
	case "stop":
		err = apply("stop", containers.RuntimeStopped)
	case "restart":
		if err = apply("stop", containers.RuntimeStopped); err == nil {
			err = apply("start", containers.RuntimeRunning)
		}
	case "remove":
		if singleRuntimeStateStoppable(observed.State) {
			err = apply("stop", containers.RuntimeStopped)
		}
		if err == nil {
			err = mutator.mutateExisting(ctx, plan, component, "remove", containers.RuntimeMissing)
		}
		if err == nil {
			if phaseErr := r.Evidence.RecordPhase(ctx, operationID, fencingToken, "single_remove_network", "intent", map[string]string{"network_id": network.ID}); phaseErr != nil {
				err = phaseErr
			} else {
				removeErr := r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName)
				removed, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
				if observeErr != nil {
					err = UnknownOutcomeError{Phase: PhaseCleanup, Cause: firstError(removeErr, observeErr)}
				} else if removed.Exists {
					err = firstError(removeErr, errors.New("active network removal did not reach the requested state"))
				} else if phaseErr = r.Evidence.RecordPhase(ctx, operationID, fencingToken, "single_remove_network", "confirmed", nil); phaseErr != nil {
					err = UnknownOutcomeError{Phase: PhaseCleanup, Cause: phaseErr}
				}
			}
		}
		if err == nil {
			err = r.Store.MarkActiveRemoved(ctx, installation, generation.Generation, operationID, fencingToken)
		}
	}
	if err != nil {
		return Result{}, err
	}
	state := map[string]string{"start": "running", "stop": "stopped", "restart": "running", "remove": "runtime_removed"}[action]
	return Result{Generation: generation.Generation, ReleaseID: generation.ReleaseID, RuntimeState: state, ContainerName: component.ContainerName, ContainerID: component.ContainerID, NetworkName: generation.NetworkName, ExposureMode: generation.ExposureMode, ServiceID: generation.ServiceID, HostAddress: generation.HostAddress, HostPort: generation.HostPort}, nil
}

func singleRuntimeStateClassifiable(state containers.RuntimeState) bool {
	return state == containers.RuntimeRunning || state == containers.RuntimeStopped || state == containers.RuntimeRestarting || state == containers.RuntimePaused
}

func singleRuntimeStateStoppable(state containers.RuntimeState) bool {
	return state == containers.RuntimeRunning || state == containers.RuntimeRestarting || state == containers.RuntimePaused
}

func (r SingleRunner) observeExact(ctx context.Context, generation Generation) (containers.ContainerObservation, containers.NetworkObservation, error) {
	observed, err := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerID)
	if err != nil {
		return observed, containers.NetworkObservation{}, UnknownOutcomeError{Phase: PhaseBeforeVerification, Cause: err}
	}
	if !observed.Exists {
		byName, nameErr := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerName)
		if nameErr != nil {
			return observed, containers.NetworkObservation{}, UnknownOutcomeError{Phase: PhaseBeforeVerification, Cause: nameErr}
		}
		if byName.Exists {
			return observed, containers.NetworkObservation{}, errors.New("expected runtime name is occupied by another object")
		}
		return observed, containers.NetworkObservation{}, errors.New("active runtime is missing; reconciliation and recreation are required")
	}
	if observed.ID != generation.Component.ContainerID || observed.Name != generation.Component.ContainerName || observed.ImageReference != generation.Component.Image || observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
		return observed, containers.NetworkObservation{}, errors.New("active runtime ownership is not exact")
	}
	if generation.Component.ImageID != "" && observed.ImageID != generation.Component.ImageID {
		return observed, containers.NetworkObservation{}, errors.New("active runtime image identity changed")
	}
	if generation.Status == "verification_required" && generation.Component.ConfigurationHash == "" {
		return observed, containers.NetworkObservation{}, errors.New("migrated runtime configuration is not verified")
	}
	if generation.Component.ConfigurationHash != "" && observationHash(observed) != generation.Component.ConfigurationHash {
		return observed, containers.NetworkObservation{}, errors.New("active runtime configuration changed")
	}
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil {
		return observed, network, UnknownOutcomeError{Phase: PhaseBeforeVerification, Cause: err}
	}
	attachment, attached := observed.Networks[generation.NetworkName]
	if !network.Exists || (generation.NetworkID != "" && network.ID != generation.NetworkID) || !attached || attachment.NetworkID != network.ID {
		return observed, network, errors.New("active runtime network identity changed")
	}
	return observed, network, nil
}
