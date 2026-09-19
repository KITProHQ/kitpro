package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

type Runner struct {
	Runtime  containers.LifecycleRuntime
	Store    Store
	Evidence Evidence
	Hook     Hook
}

func (r Runner) Replace(ctx context.Context, plan Plan) (Result, error) {
	if r.Runtime == nil || r.Store.DB == nil || r.Evidence == nil {
		return Result{}, errors.New("staged lifecycle dependencies unavailable")
	}
	active, err := r.loadAndVerifyActive(ctx, plan)
	if err != nil {
		return Result{}, err
	}
	if active.Generation != plan.ExpectedGeneration || plan.Generation != plan.ExpectedGeneration+1 {
		return Result{}, fmt.Errorf("stale runtime generation: expected %d active %d target %d", plan.ExpectedGeneration, active.Generation, plan.Generation)
	}
	if err = r.checkpoint(ctx, plan, PhaseBeforePull, "intent", nil); err != nil {
		return Result{}, err
	}
	pullCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	err = r.Runtime.PullImage(pullCtx, plan.Image)
	cancel()
	if err != nil {
		return Result{}, fmt.Errorf("pull target image: %w", err)
	}
	image, err := r.Runtime.ObserveImage(ctx, plan.Image)
	if err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterPull, Cause: err}
	}
	if !imageContainsDigest(image, plan.Image) {
		return Result{}, errors.New("pulled target digest could not be verified")
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterPull, "confirmed", map[string]string{"image_id": image.ID}); err != nil {
		return Result{}, err
	}
	if err = r.Store.Prepare(ctx, plan); err != nil {
		return Result{}, err
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeNetwork, "intent", nil); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}
	network, err := r.Runtime.CreateLifecycleNetwork(ctx, plan.NetworkName, plan.Container.Labels)
	if err != nil {
		if observedNetwork, observeErr := r.Runtime.ObserveNetwork(ctx, plan.NetworkName); observeErr != nil || observedNetwork.Exists {
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeNetwork, Cause: err}
		}
		return Result{}, r.failBeforeCutover(ctx, plan, active, fmt.Errorf("create candidate network: %w", err))
	}
	if err = r.Store.RecordNetwork(ctx, plan, network.ID); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterNetwork, Cause: err}
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterNetwork, "confirmed", map[string]string{"network_id": network.ID}); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterNetwork, Cause: err}
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeContainer, "intent", nil); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}
	containerID, err := r.Runtime.CreateLifecycleContainer(ctx, plan.Container)
	if err != nil {
		if observedContainer, observeErr := r.Runtime.ObserveContainer(ctx, plan.ContainerName); observeErr != nil || observedContainer.Exists {
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeContainer, Cause: err}
		}
		return Result{}, r.failBeforeCutover(ctx, plan, active, fmt.Errorf("create stopped candidate: %w", err))
	}
	observed, err := r.Runtime.ObserveContainer(ctx, containerID)
	if err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
	}
	if err = verifyCandidate(plan, network, observed, containers.RuntimeStopped); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}
	configHash := observationHash(observed)
	if err = r.Store.RecordContainer(ctx, plan, observed.ID, observed.ImageID, configHash, "stopped"); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterContainer, "confirmed", map[string]string{"container_id": observed.ID, "configuration_hash": configHash}); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterContainer, Cause: err}
	}

	// Pre-cutover observation is intentionally fresh. No old resource has been
	// stopped or removed before both generations pass exact identity checks.
	var activeObserved containers.ContainerObservation
	if active.Status == "active" {
		if plan.AllowMissingActive {
			activeObserved, err = r.observeRepairableActive(ctx, active)
		} else {
			activeObserved, err = r.verifyStoredGeneration(ctx, active, containers.RuntimeRunning)
		}
		if err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, active, err)
		}
	}
	network, err = r.Runtime.ObserveNetwork(ctx, plan.NetworkName)
	if err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}
	observed, err = r.Runtime.ObserveContainer(ctx, containerID)
	if err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}
	if err = verifyCandidate(plan, network, observed, containers.RuntimeStopped); err != nil {
		return Result{}, r.failBeforeCutover(ctx, plan, active, err)
	}

	if active.Status == "active" && activeObserved.Exists && activeObserved.State == containers.RuntimeRunning {
		if err = r.checkpoint(ctx, plan, PhaseBeforeOldStop, "intent", map[string]string{"rollback_policy": rollbackPolicy(plan)}); err != nil {
			return Result{}, r.failBeforeCutover(ctx, plan, active, err)
		}
		if err = r.Runtime.StopContainer(ctx, active.Component.ContainerID); err != nil {
			oldObserved, observeErr := r.Runtime.ObserveContainer(ctx, active.Component.ContainerID)
			if observeErr == nil && oldObserved.State == containers.RuntimeRunning {
				return Result{}, r.failBeforeCutover(ctx, plan, active, fmt.Errorf("old generation stop failed: %w", err))
			}
			return Result{}, UnknownOutcomeError{Phase: PhaseBeforeOldStop, Cause: err}
		}
		if _, err = r.Runtime.WaitContainer(ctx, active.Component.ContainerID, containers.RuntimeStopped, observationTimeout); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterOldStop, Cause: err}
		}
		if err = r.checkpoint(ctx, plan, PhaseAfterOldStop, "confirmed", nil); err != nil {
			return Result{}, UnknownOutcomeError{Phase: PhaseAfterOldStop, Cause: err}
		}
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeCandidateStart, "intent", nil); err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeCandidateStart, err)
	}
	if err = r.Runtime.StartContainer(ctx, containerID); err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeCandidateStart, err)
	}
	if _, err = r.Runtime.WaitContainer(ctx, containerID, containers.RuntimeRunning, observationTimeout); err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseAfterCandidateStart, err)
	}
	if err = r.Store.RecordState(ctx, plan, "running", false); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterCandidateStart, Cause: err}
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterCandidateStart, "confirmed", nil); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterCandidateStart, Cause: err}
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeVerification, "intent", nil); err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeVerification, err)
	}
	network, err = r.Runtime.ObserveNetwork(ctx, plan.NetworkName)
	if err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeVerification, err)
	}
	observed, err = r.Runtime.ObserveContainer(ctx, containerID)
	if err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeVerification, err)
	}
	if err = verifyCandidate(plan, network, observed, containers.RuntimeRunning); err != nil {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeVerification, err)
	}
	if observationHash(observed) != configHash {
		return Result{}, r.afterCutoverFailure(ctx, plan, active, containerID, PhaseBeforeVerification, errors.New("candidate configuration changed before commit"))
	}
	if err = r.Store.RecordState(ctx, plan, "running", true); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterVerification, Cause: err}
	}
	if err = r.checkpoint(ctx, plan, PhaseAfterVerification, "confirmed", map[string]string{"runtime_state": "running", "health": observed.Health}); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseAfterVerification, Cause: err}
	}

	if err = r.checkpoint(ctx, plan, PhaseBeforeCommit, "intent", nil); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCommit, Cause: err}
	}
	if err = r.Store.Commit(ctx, plan); err != nil {
		return Result{}, UnknownOutcomeError{Phase: PhaseBeforeCommit, Cause: err}
	}
	result := Result{Generation: plan.Generation, ReleaseID: plan.ReleaseID, RuntimeState: "running", ContainerName: plan.ContainerName, ContainerID: observed.ID, NetworkName: plan.NetworkName, ExposureMode: plan.ExposureMode, ServiceID: plan.ServiceID, HostAddress: plan.HostAddress, HostPort: plan.HostPort}
	if err = r.checkpoint(ctx, plan, PhaseAfterCommit, "confirmed", nil); err != nil {
		return result, UnknownOutcomeError{Phase: PhaseAfterCommit, Cause: err}
	}

	result.CleanupDeferred = r.cleanupOlder(ctx, plan)
	return result, nil
}

func (r Runner) loadAndVerifyActive(ctx context.Context, plan Plan) (Generation, error) {
	if err := r.Store.BackfillInstallation(ctx, plan.InstallationID); err != nil {
		return Generation{}, err
	}
	active, err := r.Store.Current(ctx, plan.InstallationID)
	if errors.Is(err, sql.ErrNoRows) {
		if plan.ExpectedGeneration != 0 {
			return Generation{}, errors.New("active generation is missing")
		}
		return Generation{}, nil
	}
	if err != nil {
		return Generation{}, err
	}
	if active.Status == "removed" {
		return active, nil
	}
	observed, err := r.verifyStoredGeneration(ctx, active, containers.RuntimeRunning)
	if err != nil {
		if plan.AllowMissingActive && active.Status == "active" {
			if _, repairErr := r.observeRepairableActive(ctx, active); repairErr == nil {
				return active, nil
			}
		}
		return Generation{}, fmt.Errorf("active runtime ownership is ambiguous: %w", err)
	}
	if active.Status == "verification_required" {
		network, networkErr := r.Runtime.ObserveNetwork(ctx, active.NetworkName)
		if networkErr != nil || !network.Exists {
			return Generation{}, errors.New("migrated network cannot be verified")
		}
		if err = r.Store.PromoteMigrated(ctx, active, network.ID, observed.ImageID, observationHash(observed)); err != nil {
			return Generation{}, err
		}
		active.Status, active.NetworkID, active.Component.ImageID, active.Component.ConfigurationHash = "active", network.ID, observed.ImageID, observationHash(observed)
	}
	return active, nil
}

func (r Runner) observeRepairableActive(ctx context.Context, generation Generation) (containers.ContainerObservation, error) {
	observed, err := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerID)
	if err != nil {
		return observed, err
	}
	if !observed.Exists {
		byName, nameErr := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerName)
		if nameErr != nil {
			return observed, nameErr
		}
		if byName.Exists {
			return byName, errors.New("expected container name is occupied by an untrusted runtime object")
		}
		return observed, nil
	}
	if observed.ID != generation.Component.ContainerID || observed.Name != generation.Component.ContainerName || observed.ImageReference != generation.Component.Image || observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
		return observed, errors.New("active container identity changed")
	}
	if generation.Component.ConfigurationHash != "" && observationHash(observed) != generation.Component.ConfigurationHash {
		return observed, errors.New("active container configuration changed")
	}
	if observed.State != containers.RuntimeRunning && observed.State != containers.RuntimeStopped {
		return observed, errors.New("active container state is not repairable")
	}
	return observed, nil
}

func (r Runner) verifyStoredGeneration(ctx context.Context, generation Generation, want containers.RuntimeState) (containers.ContainerObservation, error) {
	observed, err := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerID)
	if err != nil {
		return observed, err
	}
	if !observed.Exists || observed.ID != generation.Component.ContainerID || observed.Name != generation.Component.ContainerName || observed.ImageReference != generation.Component.Image || observed.State != want {
		return observed, errors.New("container identity or state mismatch")
	}
	if observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
		return observed, errors.New("container ownership evidence mismatch")
	}
	if _, ok := observed.Networks[generation.NetworkName]; !ok {
		return observed, errors.New("container network mismatch")
	}
	if generation.Status == "verification_required" && generation.Component.ConfigurationHash == "" {
		return observed, errors.New("migrated container configuration is not verified")
	}
	if generation.Component.ConfigurationHash != "" && observationHash(observed) != generation.Component.ConfigurationHash {
		return observed, errors.New("container configuration hash mismatch")
	}
	return observed, nil
}

func (r Runner) checkpoint(ctx context.Context, plan Plan, phase Phase, outcome string, facts map[string]string) error {
	if err := r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, string(phase), outcome, facts); err != nil {
		return err
	}
	if r.Hook != nil {
		if err := r.Hook(ctx, phase); err != nil {
			return err
		}
	}
	return nil
}

func (r Runner) failBeforeCutover(ctx context.Context, plan Plan, active Generation, cause error) error {
	cleanupState := "clean"
	if cleanupErr := r.cleanupCandidate(ctx, plan); cleanupErr != nil {
		cleanupState = "pending"
	}
	_ = r.Store.MarkFailed(ctx, plan, cleanupState)
	return cause
}

func (r Runner) afterCutoverFailure(ctx context.Context, plan Plan, active Generation, containerID string, phase Phase, cause error) error {
	observed, observeErr := r.Runtime.ObserveContainer(ctx, containerID)
	if observeErr != nil {
		return UnknownOutcomeError{Phase: phase, Cause: cause}
	}
	if observed.State == containers.RuntimeRunning {
		if stopErr := r.Runtime.StopContainer(ctx, containerID); stopErr != nil {
			return UnknownOutcomeError{Phase: phase, Cause: cause}
		}
	}
	if active.Status == "active" && plan.RollbackSafe {
		if startErr := r.Runtime.StartContainer(ctx, active.Component.ContainerID); startErr != nil {
			return UnknownOutcomeError{Phase: phase, Cause: cause}
		}
		if _, waitErr := r.Runtime.WaitContainer(ctx, active.Component.ContainerID, containers.RuntimeRunning, observationTimeout); waitErr != nil {
			return UnknownOutcomeError{Phase: phase, Cause: cause}
		}
		cleanupState := "clean"
		if cleanupErr := r.cleanupCandidate(ctx, plan); cleanupErr != nil {
			cleanupState = "pending"
		}
		_ = r.Store.MarkFailed(ctx, plan, cleanupState)
		return cause
	}
	return UnknownOutcomeError{Phase: phase, Cause: cause}
}

func (r Runner) cleanupCandidate(ctx context.Context, plan Plan) error {
	generation, err := r.Store.ByOperation(ctx, plan.OperationID)
	if err != nil {
		return err
	}
	if err = r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, "candidate_cleanup", "intent", map[string]string{"generation": fmt.Sprint(plan.Generation)}); err != nil {
		return err
	}
	if generation.Component.ContainerID != "" {
		observed, observeErr := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerID)
		if observeErr != nil || (observed.Exists && observed.ID != generation.Component.ContainerID) {
			return errors.New("candidate ownership changed during cleanup")
		}
		if observed.Exists {
			if observed.State == containers.RuntimeRunning {
				_ = r.Runtime.StopContainer(ctx, observed.ID)
			}
			if err = r.Runtime.RemoveContainer(ctx, observed.ID); err != nil {
				return err
			}
			removed, removeObserveErr := r.Runtime.ObserveContainer(ctx, observed.ID)
			if removeObserveErr != nil || removed.State != containers.RuntimeMissing {
				return errors.New("candidate container removal could not be verified")
			}
		}
	}
	if generation.NetworkName != "" {
		network, observeErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if observeErr != nil {
			return observeErr
		}
		if network.Exists && network.ID != generation.NetworkID {
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
	return r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, "candidate_cleanup", "confirmed", map[string]string{"generation": fmt.Sprint(plan.Generation)})
}

func (r Runner) cleanupOlder(ctx context.Context, plan Plan) bool {
	older, err := r.Store.OlderRetained(ctx, plan.InstallationID, plan.ExpectedGeneration)
	if err != nil {
		return true
	}
	deferred := false
	for _, generation := range older {
		if checkpointErr := r.checkpoint(ctx, plan, PhaseCleanup, "intent", map[string]string{"generation": fmt.Sprint(generation.Generation)}); checkpointErr != nil {
			_ = r.Store.MarkCleanupPending(ctx, generation)
			deferred = true
			continue
		}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, generation.Component.ContainerID)
		if observeErr != nil {
			_ = r.Store.MarkCleanupPending(ctx, generation)
			deferred = true
			continue
		}
		if observed.Exists {
			if _, observeErr = r.verifyStoredGeneration(ctx, generation, containers.RuntimeStopped); observeErr != nil {
				_ = r.Store.MarkCleanupPending(ctx, generation)
				deferred = true
				continue
			}
		}
		if observed.Exists {
			if r.Runtime.RemoveContainer(ctx, observed.ID) != nil {
				_ = r.Store.MarkCleanupPending(ctx, generation)
				deferred = true
				continue
			}
			removed, removeObserveErr := r.Runtime.ObserveContainer(ctx, observed.ID)
			if removeObserveErr != nil || removed.State != containers.RuntimeMissing {
				_ = r.Store.MarkCleanupPending(ctx, generation)
				deferred = true
				continue
			}
		}
		network, networkErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
		if networkErr != nil || (network.Exists && network.ID != generation.NetworkID) {
			_ = r.Store.MarkCleanupPending(ctx, generation)
			deferred = true
			continue
		}
		if network.Exists {
			if r.Runtime.RemoveLifecycleNetwork(ctx, generation.NetworkName) != nil {
				_ = r.Store.MarkCleanupPending(ctx, generation)
				deferred = true
				continue
			}
			removed, removeObserveErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
			if removeObserveErr != nil || removed.Exists {
				_ = r.Store.MarkCleanupPending(ctx, generation)
				deferred = true
				continue
			}
		}
		if evidenceErr := r.Evidence.RecordPhase(ctx, plan.OperationID, plan.FencingToken, string(PhaseCleanup), "confirmed", map[string]string{"generation": fmt.Sprint(generation.Generation)}); evidenceErr != nil {
			_ = r.Store.MarkCleanupPending(ctx, generation)
			deferred = true
			continue
		}
		if markErr := r.Store.MarkRemoved(ctx, generation); markErr != nil {
			deferred = true
		}
	}
	return deferred
}

func verifyCandidate(plan Plan, network containers.NetworkObservation, observed containers.ContainerObservation, want containers.RuntimeState) error {
	if !network.Exists || network.Name != plan.NetworkName || observed.State != want || !observed.Exists || observed.Name != plan.ContainerName || observed.ImageReference != plan.Image {
		return errors.New("candidate runtime identity mismatch")
	}
	if observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != plan.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(plan.Generation) {
		return errors.New("candidate ownership evidence mismatch")
	}
	attachment, ok := observed.Networks[plan.NetworkName]
	if !ok || (attachment.NetworkID != network.ID && !(want == containers.RuntimeStopped && attachment.NetworkID == "" && observed.NetworkMode == plan.NetworkName)) {
		return errors.New("candidate network identity mismatch")
	}
	if observed.User != plan.Container.User || normalizeRestart(observed.RestartPolicy) != normalizeRestart(plan.Container.RestartPolicy) || (len(plan.Container.Command) > 0 && !equalJSON(observed.Command, plan.Container.Command)) || !containsStrings(observed.Environment, plan.Container.Environment) {
		return errors.New("candidate runtime configuration mismatch")
	}
	if !equalJSON(normalizeMounts(observed.Mounts), normalizeExpectedMounts(plan.Container)) || !equalJSON(normalizePortBindings(observed.PortBindings), normalizePortBindings(plan.Container.PortBindings)) || !equalJSON(normalizeDevices(observed.Devices), normalizeDevices(plan.Container.Devices)) || !equalJSON(normalizeDeviceRequests(observed.DeviceRequests), normalizeDeviceRequests(plan.Container.DeviceRequests)) {
		return errors.New("candidate mounts, ports, or devices mismatch")
	}
	return nil
}

// VerifyMigratedConfiguration compares a legacy runtime observation with a
// plan reconstructed exclusively from helper-owned state and the trusted
// catalog. Image defaults may add environment entries, so required values are
// matched as a subset while all helper-controlled fields remain exact.
func VerifyMigratedConfiguration(plan containers.ContainerPlan, observed containers.ContainerObservation) error {
	if observed.User != plan.User || observed.NetworkMode != plan.Network || !equalJSON(observed.Labels, plan.Labels) || normalizeRestart(observed.RestartPolicy) != normalizeRestart(plan.RestartPolicy) || (len(plan.Command) > 0 && !equalJSON(observed.Command, plan.Command)) || !containsStrings(observed.Environment, plan.Environment) {
		return errors.New("migrated runtime configuration mismatch")
	}
	if !equalJSON(normalizeMounts(observed.Mounts), normalizeExpectedMounts(plan)) || !equalJSON(normalizePortBindings(observed.PortBindings), normalizePortBindings(plan.PortBindings)) || !equalJSON(normalizeDevices(observed.Devices), normalizeDevices(plan.Devices)) || !equalJSON(normalizeDeviceRequests(observed.DeviceRequests), normalizeDeviceRequests(plan.DeviceRequests)) {
		return errors.New("migrated runtime mounts, ports, or devices mismatch")
	}
	return nil
}

// ConfigurationHash records the exact post-verification runtime
// configuration. Callers must first prove the observation against trusted
// configuration; this function does not confer trust by itself.
func ConfigurationHash(observed containers.ContainerObservation) string {
	return observationHash(observed)
}
func normalizePortBindings(values map[string][]containers.PortBinding) map[string][]containers.PortBinding {
	if len(values) == 0 {
		return map[string][]containers.PortBinding{}
	}
	return values
}
func normalizeDevices(values []containers.DeviceMapping) []containers.DeviceMapping {
	if len(values) == 0 {
		return []containers.DeviceMapping{}
	}
	return values
}
func normalizeDeviceRequests(values []containers.DeviceRequest) []containers.DeviceRequest {
	if len(values) == 0 {
		return []containers.DeviceRequest{}
	}
	return values
}

func imageContainsDigest(observed containers.ImageObservation, image string) bool {
	if !observed.Exists {
		return false
	}
	wanted := canonicalDigestReference(image)
	for _, digest := range observed.RepoDigests {
		if canonicalDigestReference(digest) == wanted {
			return true
		}
	}
	return false
}

func canonicalDigestReference(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "index.docker.io/")
	image = strings.TrimPrefix(image, "library/")
	return image
}

func observationHash(observed containers.ContainerObservation) string {
	value := struct {
		Image, User, Restart string
		Command              []string
		Environment          []string
		Labels               map[string]string
		Mounts               []containers.MountObservation
		Networks             []string
		NetworkMode          string
		Ports                map[string][]containers.PortBinding
		Devices              []containers.DeviceMapping
		Requests             []containers.DeviceRequest
	}{observed.ImageReference, observed.User, normalizeRestart(observed.RestartPolicy), observed.Command, sortedStrings(observed.Environment), observed.Labels, normalizeMounts(observed.Mounts), networkNames(observed.Networks), observed.NetworkMode, observed.PortBindings, observed.Devices, observed.DeviceRequests}
	encoded, _ := json.Marshal(value)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}
func networkNames(networks map[string]containers.NetworkAttachment) []string {
	result := make([]string, 0, len(networks))
	for name := range networks {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func normalizeMounts(mounts []containers.MountObservation) []containers.MountObservation {
	out := append([]containers.MountObservation(nil), mounts...)
	sort.Slice(out, func(i, j int) bool { return out[i].Destination < out[j].Destination })
	return out
}
func normalizeExpectedMounts(plan containers.ContainerPlan) []containers.MountObservation {
	out := make([]containers.MountObservation, 0, len(plan.Storage))
	for _, mount := range plan.Storage {
		out = append(out, containers.MountObservation{Source: mount.HostPath, Destination: mount.ContainerPath, ReadOnly: mount.ReadOnly})
	}
	return normalizeMounts(out)
}
func normalizeRestart(value string) string {
	if value == "" {
		return "no"
	}
	return value
}
func equalJSON(a, b any) bool {
	first, _ := json.Marshal(a)
	second, _ := json.Marshal(b)
	return string(first) == string(second)
}
func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
func containsStrings(have, required []string) bool {
	set := make(map[string]bool, len(have))
	for _, value := range have {
		set[value] = true
	}
	for _, value := range required {
		if !set[value] {
			return false
		}
	}
	return true
}
func rollbackPolicy(plan Plan) string {
	if plan.RollbackSafe {
		return "same_image_restart_allowed"
	}
	return "shared_data_conservative"
}
