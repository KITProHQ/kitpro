package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

type ComponentHook func(context.Context, string, string, string) error

type MultiRunner struct {
	Runtime       containers.LifecycleRuntime
	Store         Store
	Evidence      Evidence
	ComponentHook ComponentHook
}

// Operate applies ordinary lifecycle changes to the helper-authoritative
// active generation using its persisted topology. It never reconstructs order
// from a newer catalog manifest.
func (r MultiRunner) Operate(ctx context.Context, installation, operationID string, fencingToken int64, action string) (Result, error) {
	if r.Runtime == nil || r.Store.DB == nil || r.Evidence == nil {
		return Result{}, errors.New("multi-component lifecycle dependencies unavailable")
	}
	if err := r.Evidence.RecordPhase(ctx, operationID, fencingToken, "multi_lifecycle_validation", "intent", map[string]string{"lifecycle_action": action}); err != nil {
		return Result{}, err
	}
	generation, err := r.Store.MultiCurrent(ctx, installation)
	if err != nil {
		return Result{}, err
	}
	if generation.Status == "verification_required" {
		runtimeState := "running"
		if err = r.verifyGeneration(ctx, generation, containers.RuntimeRunning); err != nil {
			runtimeState = "stopped"
			if err = r.verifyGeneration(ctx, generation, containers.RuntimeStopped); err != nil {
				return Result{}, errors.New("legacy multi-component runtime is partial or ambiguous")
			}
		}
		network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if observeErr != nil || !network.Exists {
			return Result{}, errors.New("legacy multi-component network is ambiguous")
		}
		observations := map[string]containers.ContainerObservation{}
		for _, component := range generation.Components {
			observed, componentErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
			if componentErr != nil {
				return Result{}, componentErr
			}
			observations[component.ID] = observed
		}
		if err = r.Store.PromoteMigratedMulti(ctx, generation, network.ID, observations, runtimeState, operationID, fencingToken); err != nil {
			return Result{}, err
		}
		generation, err = r.Store.MultiCurrent(ctx, installation)
		if err != nil {
			return Result{}, err
		}
	}
	if generation.Status != "active" || len(generation.Components) < 2 {
		return Result{}, errors.New("active multi-component generation unavailable")
	}
	plan := MultiPlan{OperationID: operationID, InstallationID: installation, ApplicationID: generation.ApplicationID, ReleaseID: generation.ReleaseID, Generation: generation.Generation, ExpectedGeneration: generation.Generation, FencingToken: fencingToken, NetworkName: generation.NetworkName, PlanHash: generation.PlanHash, TopologyHash: generation.TopologyHash, DataPath: generation.DataPath, Bindings: generation.Bindings}
	for _, component := range generation.Components {
		plan.Components = append(plan.Components, MultiComponentPlan{ID: component.ID, Image: component.Image, ContainerName: component.ContainerName, DependsOn: component.DependsOn, StartOrdinal: component.StartOrdinal})
	}
	if err = r.checkpoint(ctx, plan, PhaseBeforeVerification, "intent", map[string]string{"lifecycle_action": action}); err != nil {
		return Result{}, err
	}
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil || !network.Exists || network.ID != generation.NetworkID {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeVerification, Cause: errors.New("active network identity is ambiguous")}
	}
	apply := func(components []Component, mutation string, want containers.RuntimeState) error {
		mutated := 0
		for _, component := range components {
			if mutationErr := r.mutateExisting(ctx, plan, component, mutation, want); mutationErr != nil {
				if mutated > 0 {
					return UnknownOutcomeError{Phase: phaseForAction(mutation), Cause: mutationErr}
				}
				return mutationErr
			}
			state := "stopped"
			if want == containers.RuntimeRunning {
				state = "running"
			}
			if want == containers.RuntimeMissing {
				state = "removed"
			}
			if updateErr := r.Store.RecordMultiContainer(ctx, plan, component.ID, component.ContainerID, "", "", state, want == containers.RuntimeRunning); updateErr != nil {
				return UnknownOutcomeError{Phase: phaseForAction(mutation), Cause: updateErr}
			}
			mutated++
		}
		return nil
	}
	switch action {
	case "start":
		err = apply(generation.Components, "start", containers.RuntimeRunning)
	case "stop":
		err = apply(reverseComponents(generation.Components), "stop", containers.RuntimeStopped)
	case "restart":
		if err = apply(reverseComponents(generation.Components), "stop", containers.RuntimeStopped); err == nil {
			err = apply(generation.Components, "start", containers.RuntimeRunning)
		}
	case "remove":
		if err = apply(reverseComponents(generation.Components), "stop", containers.RuntimeStopped); err == nil {
			err = apply(reverseComponents(generation.Components), "remove", containers.RuntimeMissing)
		}
		if err == nil {
			observed, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
			if observeErr != nil || (observed.Exists && observed.ID != generation.NetworkID) {
				err = UnknownOutcomeError{Phase: PhaseCleanup, Cause: errors.New("active network identity is ambiguous")}
			} else if observed.Exists {
				removeErr := r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName)
				removed, verifyErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
				if verifyErr != nil || removed.Exists {
					cause := firstError(removeErr, verifyErr)
					if cause == nil {
						cause = errors.New("active network removal did not reach the requested state")
					}
					err = UnknownOutcomeError{Phase: PhaseCleanup, Cause: cause}
				}
			}
		}
		if err == nil {
			err = r.Store.MarkActiveRemoved(ctx, installation, generation.Generation, operationID, fencingToken)
		}
	default:
		return Result{}, errors.New("unsupported multi-component lifecycle action")
	}
	if err != nil {
		return Result{}, err
	}
	result := Result{Generation: generation.Generation, ReleaseID: generation.ReleaseID, RuntimeState: map[string]string{"start": "running", "stop": "stopped", "restart": "running", "remove": "runtime_removed"}[action], NetworkName: generation.NetworkName, Bindings: generation.Bindings, Components: map[string]string{}}
	for _, component := range generation.Components {
		result.Components[component.ID] = component.ContainerID
	}
	return result, nil
}

func (r MultiRunner) Replace(ctx context.Context, plan MultiPlan) (Result, error) {
	if r.Runtime == nil || r.Store.DB == nil || r.Evidence == nil {
		return Result{}, errors.New("multi-component lifecycle dependencies unavailable")
	}
	if err := validateMultiPlan(plan); err != nil {
		return Result{}, err
	}
	active, err := r.loadActive(ctx, plan)
	if err != nil {
		return Result{}, err
	}
	if active.Generation != plan.ExpectedGeneration || plan.Generation != plan.ExpectedGeneration+1 {
		return Result{}, fmt.Errorf("stale runtime generation: expected %d active %d target %d", plan.ExpectedGeneration, active.Generation, plan.Generation)
	}
	if err = r.Store.PrepareMulti(ctx, plan); err != nil {
		return Result{}, err
	}

	// Pull every unique digest before any active component is stopped.
	pulled := map[string]bool{}
	imageDefaultUsers := map[string]string{}
	imageDefaultVolumes := map[string][]string{}
	for _, component := range plan.Components {
		if pulled[component.Image] {
			continue
		}
		if err = r.stepIntent(ctx, plan, component.ID, "pull", component.Image); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		if err = r.runHook(ctx, "pull", component.ID, "before_dispatch"); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		if err = r.Store.DispatchComponentStep(ctx, plan, component.ID, "pull", 1); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		pullCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		err = r.Runtime.PullImage(pullCtx, component.Image)
		cancel()
		if hookErr := r.runHook(ctx, "pull", component.ID, "after_dispatch"); err == nil && hookErr != nil {
			err = hookErr
		}
		if err == nil {
			err = r.runHook(ctx, "pull", component.ID, "after_mutation_success")
		}
		image, observeErr := r.Runtime.ObserveImage(ctx, component.Image)
		if observeErr != nil {
			_ = r.Store.FinishComponentStep(ctx, plan, component.ID, "pull", 1, "", "unknown", stableError(err, observeErr))
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterPull, Cause: firstError(err, observeErr)}
		}
		if !imageContainsDigest(image, component.Image) {
			cause := observeErr
			if cause == nil {
				cause = firstError(err, errors.New("pulled digest could not be verified"))
			}
			_ = r.Store.FinishComponentStep(ctx, plan, component.ID, "pull", 1, image.ID, "confirmed_absent", cause.Error())
			return Result{}, r.failBeforeCutover(ctx, plan, fmt.Errorf("pull %s: %w", component.ID, cause))
		}
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		if err = r.Store.FinishComponentStep(ctx, plan, component.ID, "pull", 1, image.ID, "confirmed_applied", detail); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterPull, Cause: err}
		}
		pulled[component.Image] = true
		imageDefaultUsers[component.Image] = image.ConfiguredUser
		imageDefaultVolumes[component.Image] = append([]string(nil), image.ConfiguredVolumes...)
	}
	for index := range plan.Components {
		plan.Components[index].Container.ImageDefaultUser = imageDefaultUsers[plan.Components[index].Image]
		plan.Components[index].Container.ImageDefaultVolumes = append([]string(nil), imageDefaultVolumes[plan.Components[index].Image]...)
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeNetwork, "intent", nil); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, err)
	}
	labels := map[string]string{}
	for key, value := range plan.Components[0].Container.Labels {
		if key != "com.kitpro.component" {
			labels[key] = value
		}
	}
	if err = r.runHook(ctx, "network", "application", "before_dispatch"); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, err)
	}
	network, err := r.Runtime.CreateLifecycleNetwork(ctx, plan.NetworkName, labels)
	if err == nil {
		err = r.runHook(ctx, "network", "application", "after_mutation_success")
	}
	if err != nil {
		observed, observeErr := r.Runtime.ObserveNetwork(ctx, plan.NetworkName)
		if observeErr != nil || observed.Exists {
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeNetwork, Cause: err}
		}
		return Result{}, r.failBeforeCutover(ctx, plan, err)
	}
	if err = r.Store.RecordMultiNetwork(ctx, plan, network.ID); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterNetwork, Cause: err}
	}

	created := make([]string, 0, len(plan.Components))
	configHashes := map[string]string{}
	for _, component := range plan.Components {
		if err = r.stepIntent(ctx, plan, component.ID, "create", component.ContainerName); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		if err = r.runHook(ctx, "create", component.ID, "before_dispatch"); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		if err = r.Store.DispatchComponentStep(ctx, plan, component.ID, "create", 1); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
		containerID, createErr := r.Runtime.CreateLifecycleContainer(ctx, component.Container)
		if hookErr := r.runHook(ctx, "create", component.ID, "after_dispatch"); createErr == nil && hookErr != nil {
			createErr = hookErr
		}
		if createErr == nil {
			createErr = r.runHook(ctx, "create", component.ID, "after_mutation_success")
		}
		if hookErr := r.runHook(ctx, "create", component.ID, "after_mutation_transport_error"); createErr == nil && hookErr != nil {
			createErr = hookErr
		}
		if createErr != nil {
			observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerName)
			if observeErr != nil {
				_ = r.Store.FinishComponentStep(ctx, plan, component.ID, "create", 1, "", "unknown", createErr.Error())
				return Result{}, UnknownOutcomeError{Phase: PhaseBeforeContainer, Cause: createErr}
			}
			if !observed.Exists {
				_ = r.Store.FinishComponentStep(ctx, plan, component.ID, "create", 1, "", "confirmed_absent", createErr.Error())
				return Result{}, r.failBeforeCutover(ctx, plan, createErr)
			}
			containerID = observed.ID
		}
		// Persist the runtime identity before verification so cleanup and later
		// reconciliation can always find a successfully created component.
		if err = r.Store.RecordMultiContainer(ctx, plan, component.ID, containerID, "", "", "created", false); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
		}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, containerID)
		if observeErr != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: observeErr}
		}
		if verifyErr := verifyMultiCandidate(plan, component, network, observed, containers.RuntimeStopped); verifyErr != nil {
			_ = r.Store.FinishComponentStep(ctx, plan, component.ID, "create", 1, observed.ID, "unknown", verifyErr.Error())
			return Result{}, r.failBeforeCutover(ctx, plan, verifyErr)
		}
		hash := observationHash(observed)
		if err = r.Store.RecordMultiContainer(ctx, plan, component.ID, observed.ID, observed.ImageID, hash, "stopped", true); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
		}
		if err = r.Store.FinishComponentStep(ctx, plan, component.ID, "create", 1, observed.ID, "confirmed_applied", ""); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
		}
		created = append(created, component.ID)
		configHashes[component.ID] = hash
	}

	// Fresh pre-cutover observations protect against drift between preparation
	// and the first destructive mutation.
	activeObserved := map[string]containers.ContainerObservation{}
	if active.Status == "active" {
		if plan.AllowMissingActive {
			activeObserved, err = r.observeRepairableGeneration(ctx, active)
		} else {
			err = r.verifyGeneration(ctx, active, containers.RuntimeRunning)
			if err == nil {
				for _, component := range active.Components {
					activeObserved[component.ID], err = r.Runtime.ObserveContainer(ctx, component.ContainerID)
					if err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, err)
		}
	}
	if err = r.verifyPrepared(ctx, plan, configHashes); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, err)
	}

	stoppedOld := []string{}
	if active.Status == "active" {
		oldPlan := plan
		oldPlan.Generation = active.Generation
		for _, component := range reverseComponents(active.Components) {
			observed := activeObserved[component.ID]
			if !observed.Exists || observed.State == containers.RuntimeStopped {
				continue
			}
			if err = r.mutateExisting(ctx, oldPlan, component, "stop", containers.RuntimeStopped); err != nil {
				return Result{}, r.afterCutoverFailure(ctx, plan, active, created, stoppedOld, nil, PhaseBeforeOldStop, err)
			}
			if err = r.Store.RecordMultiContainer(ctx, oldPlan, component.ID, component.ContainerID, "", "", "stopped", false); err != nil {
				return Result{}, r.afterCutoverFailure(ctx, plan, active, created, append(stoppedOld, component.ID), nil, PhaseAfterOldStop, err)
			}
			stoppedOld = append(stoppedOld, component.ID)
		}
	}

	started := []string{}
	for _, component := range plan.Components {
		stored, loadErr := r.Store.LoadMultiGeneration(ctx, plan.InstallationID, plan.Generation)
		if loadErr != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCandidateStart, Cause: loadErr}
		}
		candidate := componentByID(stored.Components, component.ID)
		if candidate == nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCandidateStart, Cause: errors.New("candidate component evidence missing")}
		}
		if err = r.mutateExisting(ctx, plan, *candidate, "start", containers.RuntimeRunning); err != nil {
			return Result{}, r.afterCutoverFailure(ctx, plan, active, created, stoppedOld, started, PhaseBeforeCandidateStart, err)
		}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, candidate.ContainerID)
		if observeErr != nil {
			return Result{}, r.afterCutoverFailure(ctx, plan, active, created, stoppedOld, started, PhaseBeforeVerification, observeErr)
		}
		if hookErr := r.runHook(ctx, "verify", component.ID, "failure"); hookErr != nil {
			return Result{}, r.afterCutoverFailure(ctx, plan, active, created, stoppedOld, started, PhaseBeforeVerification, hookErr)
		}
		network, observeErr = r.Runtime.ObserveNetwork(ctx, plan.NetworkName)
		if observeErr != nil || verifyMultiCandidate(plan, component, network, observed, containers.RuntimeRunning) != nil || observationHash(observed) != configHashes[component.ID] {
			if observeErr == nil {
				observeErr = errors.New("candidate runtime verification failed")
			}
			return Result{}, r.afterCutoverFailure(ctx, plan, active, created, stoppedOld, started, PhaseBeforeVerification, observeErr)
		}
		if err = r.Store.RecordMultiContainer(ctx, plan, component.ID, observed.ID, observed.ImageID, configHashes[component.ID], "running", true); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterVerification, Cause: err}
		}
		started = append(started, component.ID)
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeCommit, "intent", map[string]string{"components": fmt.Sprint(len(plan.Components))}); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCommit, Cause: err}
	}
	if err = r.runHook(ctx, "commit", "application", "before_dispatch"); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCommit, Cause: err}
	}
	if err = r.Store.CommitMulti(ctx, plan); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCommit, Cause: err}
	}
	result := multiResult(plan)
	committed, loadErr := r.Store.LoadMultiGeneration(ctx, plan.InstallationID, plan.Generation)
	if loadErr != nil {
		return result, UnknownOutcomeError{Phase: PhaseAfterCommit, Cause: loadErr}
	}
	for _, component := range committed.Components {
		result.Components[component.ID] = component.ContainerID
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterCommit, "confirmed", nil); err != nil {
		return result, UnknownOutcomeError{Phase: PhaseAfterCommit, Cause: err}
	}
	result.CleanupDeferred = r.cleanupOlder(ctx, plan)
	return result, nil
}

func validateMultiPlan(plan MultiPlan) error {
	if len(plan.Components) < 2 || len(plan.Components) > 16 || plan.TopologyHash == "" || plan.PlanHash == "" {
		return errors.New("invalid multi-component generation plan")
	}
	seen := map[string]bool{}
	for ordinal, component := range plan.Components {
		if component.ID == "" || seen[component.ID] || component.StartOrdinal != ordinal || component.ContainerName == "" || component.Image == "" {
			return errors.New("invalid normalized component plan")
		}
		for _, dependency := range component.DependsOn {
			if !seen[dependency] {
				return errors.New("component plan is not dependency-first")
			}
		}
		seen[component.ID] = true
	}
	return nil
}

func (r MultiRunner) loadActive(ctx context.Context, plan MultiPlan) (MultiGeneration, error) {
	active, err := r.Store.MultiCurrent(ctx, plan.InstallationID)
	if errors.Is(err, sql.ErrNoRows) {
		if plan.ExpectedGeneration != 0 {
			return MultiGeneration{}, errors.New("active generation is missing")
		}
		return MultiGeneration{}, nil
	}
	if err != nil {
		return MultiGeneration{}, err
	}
	if len(active.Components) < 2 {
		return MultiGeneration{}, errors.New("stored active generation is not multi-component")
	}
	if active.Status == "removed" {
		return active, nil
	}
	if err = r.verifyGeneration(ctx, active, containers.RuntimeRunning); err != nil {
		if plan.AllowMissingActive && active.Status == "active" {
			if _, repairErr := r.observeRepairableGeneration(ctx, active); repairErr == nil {
				return active, nil
			}
		}
		return MultiGeneration{}, fmt.Errorf("active runtime ownership is ambiguous: %w", err)
	}
	if active.Status == "verification_required" {
		network, networkErr := r.Runtime.ObserveNetwork(ctx, active.NetworkName)
		if networkErr != nil || !network.Exists {
			return MultiGeneration{}, errors.New("migrated multi-component network cannot be verified")
		}
		observations := map[string]containers.ContainerObservation{}
		for _, component := range active.Components {
			observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
			if observeErr != nil {
				return MultiGeneration{}, observeErr
			}
			observations[component.ID] = observed
		}
		if err = r.Store.PromoteMigratedMulti(ctx, active, network.ID, observations, "running", plan.OperationID, plan.FencingToken); err != nil {
			return MultiGeneration{}, err
		}
		active.Status, active.NetworkID = "active", network.ID
		for index := range active.Components {
			active.Components[index].ImageID = observations[active.Components[index].ID].ImageID
			active.Components[index].ConfigurationHash = observationHash(observations[active.Components[index].ID])
			active.Components[index].State = "running"
		}
	}
	return active, nil
}

func (r MultiRunner) observeRepairableGeneration(ctx context.Context, generation MultiGeneration) (map[string]containers.ContainerObservation, error) {
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil {
		return nil, err
	}
	if network.Exists && generation.NetworkID != "" && network.ID != generation.NetworkID {
		return nil, errors.New("active network identity changed")
	}
	observations := make(map[string]containers.ContainerObservation, len(generation.Components))
	for _, component := range generation.Components {
		observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil {
			return nil, observeErr
		}
		if !observed.Exists {
			byName, nameErr := r.Runtime.ObserveContainer(ctx, component.ContainerName)
			if nameErr != nil {
				return nil, nameErr
			}
			if byName.Exists {
				return nil, fmt.Errorf("component %s expected name is occupied", component.ID)
			}
			observations[component.ID] = observed
			continue
		}
		if observed.ID != component.ContainerID || observed.Name != component.ContainerName || observed.ImageReference != component.Image || observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
			return nil, fmt.Errorf("component %s identity changed", component.ID)
		}
		if component.ImageID != "" && observed.ImageID != component.ImageID {
			return nil, fmt.Errorf("component %s image identity changed", component.ID)
		}
		detached := detachedStoppedConfigurationMatches(observed, component.ConfigurationHash, generation.NetworkName, network.ID)
		if component.ConfigurationHash != "" && observationHash(observed) != component.ConfigurationHash && !detached {
			return nil, fmt.Errorf("component %s configuration changed", component.ID)
		}
		if observed.State != containers.RuntimeRunning && observed.State != containers.RuntimeStopped {
			return nil, fmt.Errorf("component %s state is not repairable", component.ID)
		}
		if !network.Exists {
			return nil, errors.New("active network is missing while a component still exists")
		}
		attachment, ok := observed.Networks[generation.NetworkName]
		if (!ok || attachment.NetworkID != network.ID) && !detached {
			return nil, fmt.Errorf("component %s network changed", component.ID)
		}
		observations[component.ID] = observed
	}
	return observations, nil
}

func (r MultiRunner) verifyGeneration(ctx context.Context, generation MultiGeneration, want containers.RuntimeState) error {
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil || !network.Exists || (generation.NetworkID != "" && network.ID != generation.NetworkID) {
		return errors.New("generation network identity mismatch")
	}
	for _, component := range generation.Components {
		observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil || !observed.Exists || observed.ID != component.ContainerID || observed.Name != component.ContainerName || observed.ImageReference != component.Image || observed.State != want {
			return fmt.Errorf("component %s identity or state mismatch", component.ID)
		}
		if observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
			return fmt.Errorf("component %s ownership mismatch", component.ID)
		}
		attachment, ok := observed.Networks[generation.NetworkName]
		if !ok || attachment.NetworkID != network.ID {
			return fmt.Errorf("component %s network mismatch", component.ID)
		}
		if component.ConfigurationHash != "" && observationHash(observed) != component.ConfigurationHash {
			return fmt.Errorf("component %s configuration mismatch", component.ID)
		}
	}
	return nil
}

func (r MultiRunner) verifyPrepared(ctx context.Context, plan MultiPlan, hashes map[string]string) error {
	network, err := r.Runtime.ObserveNetwork(ctx, plan.NetworkName)
	if err != nil {
		return err
	}
	stored, err := r.Store.LoadMultiGeneration(ctx, plan.InstallationID, plan.Generation)
	if err != nil {
		return err
	}
	for _, planned := range plan.Components {
		component := componentByID(stored.Components, planned.ID)
		if component == nil {
			return errors.New("prepared component evidence missing")
		}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil {
			return observeErr
		}
		if err = verifyMultiCandidate(plan, planned, network, observed, containers.RuntimeStopped); err != nil || observationHash(observed) != hashes[planned.ID] {
			return fmt.Errorf("prepared component %s drifted", planned.ID)
		}
	}
	return nil
}

func (r MultiRunner) mutateExisting(ctx context.Context, plan MultiPlan, component Component, action string, want containers.RuntimeState) error {
	before, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
	if observeErr != nil || !before.Exists || before.ID != component.ContainerID || before.Name != component.ContainerName || before.ImageReference != component.Image || before.Labels[ownership.LabelManaged] != "true" || before.Labels[ownership.LabelInstance] != plan.InstallationID || before.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(plan.Generation) || (component.ConfigurationHash != "" && observationHash(before) != component.ConfigurationHash) {
		return errors.New("component ownership is ambiguous before mutation")
	}
	if err := r.stepIntent(ctx, plan, component.ID, action, component.ContainerID); err != nil {
		return err
	}
	if err := r.runHook(ctx, action, component.ID, "before_dispatch"); err != nil {
		return err
	}
	if err := r.Store.DispatchComponentStep(ctx, plan, component.ID, action, 1); err != nil {
		return err
	}
	var mutationErr error
	switch action {
	case "start":
		mutationErr = r.Runtime.StartContainer(ctx, component.ContainerID)
	case "stop":
		mutationErr = r.Runtime.StopContainer(ctx, component.ContainerID)
	case "remove":
		mutationErr = r.Runtime.RemoveContainer(ctx, component.ContainerID)
	default:
		return errors.New("unsupported component mutation")
	}
	if hookErr := r.runHook(ctx, action, component.ID, "after_dispatch"); mutationErr == nil && hookErr != nil {
		mutationErr = hookErr
	}
	if mutationErr == nil {
		mutationErr = r.runHook(ctx, action, component.ID, "after_mutation_success")
	}
	if hookErr := r.runHook(ctx, action, component.ID, "after_mutation_transport_error"); mutationErr == nil && hookErr != nil {
		mutationErr = hookErr
	}
	observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
	if observeErr != nil {
		_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, "", "unknown", stableError(mutationErr, observeErr))
		return UnknownOutcomeError{Phase: phaseForAction(action), Cause: firstError(mutationErr, observeErr)}
	}
	applied := observed.State == want
	if action == "remove" {
		applied = !observed.Exists || observed.State == containers.RuntimeMissing
	}
	if !applied {
		detail := stableError(mutationErr, errors.New("observed postcondition not achieved"))
		_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "confirmed_absent", detail)
		return errors.New(detail)
	}
	if mutationErr != nil {
		// The runtime response was lost, but fresh observation proved the exact
		// requested postcondition. Record that classification and continue.
		_ = r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "confirmed_applied", mutationErr.Error())
	} else if err := r.Store.FinishComponentStep(ctx, plan, component.ID, action, 1, observed.ID, "confirmed_applied", ""); err != nil {
		return err
	}
	return nil
}

func (r MultiRunner) afterCutoverFailure(ctx context.Context, plan MultiPlan, active MultiGeneration, created, stoppedOld, started []string, phase Phase, cause error) error {
	stored, _ := r.Store.LoadMultiGeneration(ctx, plan.InstallationID, plan.Generation)
	for _, component := range reverseComponents(stored.Components) {
		if !containsID(started, component.ID) {
			continue
		}
		_ = r.mutateExisting(ctx, plan, component, "stop", containers.RuntimeStopped)
	}
	if active.Status == "active" && plan.RollbackSafe {
		oldPlan := plan
		oldPlan.Generation = active.Generation
		for _, component := range active.Components {
			if !containsID(stoppedOld, component.ID) {
				continue
			}
			if err := r.mutateExisting(ctx, oldPlan, component, "start", containers.RuntimeRunning); err != nil {
				_ = r.Store.MarkMultiFailed(ctx, plan, "pending")
				return UnknownOutcomeError{Phase: phase, Cause: cause}
			}
			if err := r.Store.RecordMultiContainer(ctx, oldPlan, component.ID, component.ContainerID, "", "", "running", true); err != nil {
				_ = r.Store.MarkMultiFailed(ctx, plan, "pending")
				return UnknownOutcomeError{Phase: phase, Cause: cause}
			}
		}
		cleanup := "clean"
		var cleanupErr error
		if cleanupErr = r.cleanupCandidate(ctx, plan); cleanupErr != nil {
			cleanup = "pending"
		}
		markErr := r.Store.MarkMultiFailed(ctx, plan, cleanup)
		if cleanupErr != nil {
			return UnknownOutcomeError{Phase: PhaseCleanup, Cause: errors.Join(cause, cleanupErr, markErr)}
		}
		if markErr != nil {
			return UnknownOutcomeError{Phase: PhaseCleanup, Cause: errors.Join(cause, markErr)}
		}
		return cause
	}
	_ = r.Store.MarkMultiFailed(ctx, plan, "pending")
	return UnknownOutcomeError{Phase: phase, Cause: cause}
}

func (r MultiRunner) failBeforeCutover(ctx context.Context, plan MultiPlan, cause error) error {
	cleanup := "clean"
	cleanupErr := r.cleanupCandidate(ctx, plan)
	if cleanupErr != nil {
		cleanup = "pending"
	}
	markErr := r.Store.MarkMultiFailed(ctx, plan, cleanup)
	if cleanupErr != nil {
		return UnknownOutcomeError{Phase: PhaseCleanup, Cause: errors.Join(cause, cleanupErr, markErr)}
	}
	if markErr != nil {
		return UnknownOutcomeError{Phase: PhaseCleanup, Cause: errors.Join(cause, markErr)}
	}
	return cause
}

func (r MultiRunner) cleanupCandidate(ctx context.Context, plan MultiPlan) error {
	generation, err := r.Store.MultiByOperation(ctx, plan.OperationID)
	if err != nil {
		return err
	}
	for _, component := range reverseComponents(generation.Components) {
		if component.ContainerID == "" {
			continue
		}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil || (observed.Exists && observed.ID != component.ContainerID) {
			return errors.New("candidate ownership changed during cleanup")
		}
		if observed.Exists && observed.State == containers.RuntimeRunning {
			if err = r.mutateExisting(ctx, plan, component, "stop", containers.RuntimeStopped); err != nil {
				return err
			}
		}
		if observed.Exists {
			if err = r.mutateExisting(ctx, plan, component, "remove", containers.RuntimeMissing); err != nil {
				return err
			}
		}
	}
	if generation.NetworkName != "" {
		network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if observeErr != nil || (network.Exists && generation.NetworkID != "" && network.ID != generation.NetworkID) {
			return errors.New("candidate network ownership changed during cleanup")
		}
		if network.Exists {
			if err = r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName); err != nil {
				return err
			}
			removed, removeObserveErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
			if removeObserveErr != nil || removed.Exists {
				return errors.New("candidate network removal could not be verified")
			}
		}
	}
	return nil
}

func (r MultiRunner) cleanupOlder(ctx context.Context, plan MultiPlan) bool {
	older, err := r.Store.MultiOlderRetained(ctx, plan.InstallationID, plan.ExpectedGeneration)
	if err != nil {
		return true
	}
	deferred := false
	for _, generation := range older {
		if hookErr := r.runHook(ctx, "cleanup", "application", "before_dispatch"); hookErr != nil {
			_ = r.Store.MarkCleanupPending(ctx, Generation{InstallationID: generation.InstallationID, Generation: generation.Generation})
			deferred = true
			continue
		}
		failed := false
		if verifyErr := r.verifyGeneration(ctx, generation, containers.RuntimeStopped); verifyErr != nil {
			_ = r.Store.MarkCleanupPending(ctx, Generation{InstallationID: generation.InstallationID, Generation: generation.Generation})
			deferred = true
			continue
		}
		for _, component := range reverseComponents(generation.Components) {
			observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
			if observeErr != nil || (observed.Exists && (observed.ID != component.ContainerID || observed.Name != component.ContainerName)) {
				failed = true
				break
			}
			if observed.Exists && observed.State == containers.RuntimeRunning {
				failed = true
				break
			}
			if observed.Exists && r.Runtime.RemoveContainer(ctx, observed.ID) != nil {
				failed = true
				break
			}
		}
		if !failed {
			network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
			if observeErr != nil || (network.Exists && network.ID != generation.NetworkID) {
				failed = true
			} else if network.Exists && r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName) != nil {
				failed = true
			}
		}
		if failed {
			_ = r.Store.MarkCleanupPending(ctx, Generation{InstallationID: generation.InstallationID, Generation: generation.Generation})
			deferred = true
			continue
		}
		if err = r.Store.MarkRemoved(ctx, Generation{InstallationID: generation.InstallationID, Generation: generation.Generation}); err != nil {
			deferred = true
		}
	}
	return deferred
}

func (r MultiRunner) checkpoint(ctx context.Context, plan MultiPlan, phase Phase, outcome string, facts map[string]string) error {
	return r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, string(phase), outcome, facts)
}
func (r MultiRunner) stepIntent(ctx context.Context, plan MultiPlan, component, action, expected string) error {
	return r.Store.BeginComponentStep(ctx, plan, component, action, expected, 1)
}
func (r MultiRunner) runHook(ctx context.Context, action, component, point string) error {
	if r.ComponentHook == nil {
		return nil
	}
	return r.ComponentHook(ctx, action, component, point)
}

func verifyMultiCandidate(plan MultiPlan, component MultiComponentPlan, network containers.NetworkObservation, observed containers.ContainerObservation, want containers.RuntimeState) error {
	single := Plan{InstallationID: plan.InstallationID, Generation: plan.Generation, Image: component.Image, NetworkName: plan.NetworkName, ContainerName: component.ContainerName, Container: component.Container}
	return verifyCandidate(single, network, observed, want)
}

func reverseComponents(components []Component) []Component {
	out := append([]Component(nil), components...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartOrdinal > out[j].StartOrdinal })
	return out
}
func componentByID(components []Component, id string) *Component {
	for index := range components {
		if components[index].ID == id {
			return &components[index]
		}
	}
	return nil
}
func containsID(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}
func phaseForAction(action string) Phase {
	if action == "stop" {
		return PhaseAfterOldStop
	}
	return PhaseAfterCandidateStart
}
func stableError(first, second error) string {
	if first != nil {
		return first.Error()
	}
	if second != nil {
		return second.Error()
	}
	return "runtime postcondition not achieved"
}
func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
func multiResult(plan MultiPlan) Result {
	components := map[string]string{}
	for _, component := range plan.Components {
		components[component.ID] = component.ContainerName
	}
	return Result{Generation: plan.Generation, ReleaseID: plan.ReleaseID, RuntimeState: "running", NetworkName: plan.NetworkName, Bindings: exposure.Normalize(plan.Bindings), Components: components}
}
