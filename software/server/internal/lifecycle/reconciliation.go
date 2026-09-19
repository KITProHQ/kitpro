package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

type ReconciliationState string

const (
	ReconciliationConsistent     ReconciliationState = "consistent"
	ReconciliationRepairable     ReconciliationState = "repairable"
	ReconciliationDegraded       ReconciliationState = "degraded"
	ReconciliationActionRequired ReconciliationState = "action_required"
	ReconciliationRuntimeMissing ReconciliationState = "runtime_missing"
	ReconciliationRuntimeUnknown ReconciliationState = "runtime_unknown"
	ReconciliationCleanupPending ReconciliationState = "cleanup_pending"
)

type MismatchCode string

const (
	MismatchActiveContainerMissing    MismatchCode = "active_container_missing"
	MismatchActiveContainerStopped    MismatchCode = "active_container_stopped"
	MismatchActiveContainerRunning    MismatchCode = "active_container_unexpectedly_running"
	MismatchActiveContainerUnstable   MismatchCode = "active_container_unstable"
	MismatchImage                     MismatchCode = "image_mismatch"
	MismatchConfiguration             MismatchCode = "configuration_mismatch"
	MismatchNetwork                   MismatchCode = "network_mismatch"
	MismatchComponentMissing          MismatchCode = "component_missing"
	MismatchComponentStateMixed       MismatchCode = "component_state_mixed"
	MismatchDependencyState           MismatchCode = "dependency_state_inconsistent"
	MismatchRetainedGenerationMissing MismatchCode = "retained_generation_missing"
	MismatchRetainedGenerationRunning MismatchCode = "retained_generation_running"
	MismatchCandidateOrphaned         MismatchCode = "candidate_orphaned"
	MismatchCleanupPending            MismatchCode = "cleanup_pending"
	MismatchControlProjectionStale    MismatchCode = "control_projection_stale"
	MismatchRuntimeUnreachable        MismatchCode = "runtime_unreachable"
	MismatchRuntimeStateUnknown       MismatchCode = "runtime_state_unclassified"
	MismatchOwnershipAmbiguous        MismatchCode = "ownership_ambiguous"
)

const (
	RepairNone                    = "none"
	RepairStartActive             = "start_active"
	RepairRecreateGeneration      = "recreate_generation"
	RepairCleanupResources        = "cleanup_resources"
	RepairAcknowledgeRetainedLoss = "acknowledge_retained_missing"
)

type ComponentFinding struct {
	ComponentID       string         `json:"component_id"`
	Generation        int            `json:"generation"`
	Role              string         `json:"role"`
	ExpectedRuntime   string         `json:"expected_runtime_state"`
	ObservedRuntime   string         `json:"observed_runtime_state"`
	ExpectedRuntimeID string         `json:"expected_runtime_id,omitempty"`
	ObservedRuntimeID string         `json:"observed_runtime_id,omitempty"`
	MismatchCodes     []MismatchCode `json:"mismatch_codes,omitempty"`
}

type CommittedProjection struct {
	Generation   int    `json:"runtime_generation"`
	ReleaseID    string `json:"release_id"`
	RuntimeState string `json:"runtime_state"`
	ExposureMode string `json:"exposure_mode"`
	ServiceID    string `json:"service_id,omitempty"`
	HostAddress  string `json:"host_address,omitempty"`
	HostPort     int    `json:"host_port,omitempty"`
}

type ReconciliationResult struct {
	InstallationID       string               `json:"installation_id"`
	CheckedGeneration    int                  `json:"checked_generation"`
	State                ReconciliationState  `json:"reconciliation_state"`
	RuntimeState         string               `json:"runtime_state"`
	RuntimeIdentity      string               `json:"runtime_identity,omitempty"`
	ObservedAt           string               `json:"observed_at"`
	MismatchCodes        []MismatchCode       `json:"mismatch_codes,omitempty"`
	RecommendedAction    string               `json:"recommended_action"`
	OriginatingOperation string               `json:"originating_operation_id,omitempty"`
	Summary              string               `json:"summary"`
	Components           []ComponentFinding   `json:"components,omitempty"`
	Projection           *CommittedProjection `json:"committed_projection,omitempty"`
}

type Reconciler struct {
	Runtime containers.LifecycleRuntime
	Store   Store
	Now     func() time.Time
}

func (r Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r Reconciler) Reconcile(ctx context.Context, installation string) (ReconciliationResult, error) {
	if r.Runtime == nil || r.Store.DB == nil || installation == "" {
		return ReconciliationResult{}, errors.New("reconciliation dependencies unavailable")
	}
	result := ReconciliationResult{InstallationID: installation, State: ReconciliationConsistent, RuntimeState: "unknown", ObservedAt: r.now().Format(time.RFC3339Nano), RecommendedAction: RepairNone}
	if provider, ok := r.Runtime.(containers.RuntimeIdentityProvider); ok {
		identity, err := provider.ObserveRuntimeIdentity(ctx)
		if err != nil {
			result.State = ReconciliationRuntimeUnknown
			result.MismatchCodes = []MismatchCode{MismatchRuntimeUnreachable}
			result.Summary = "container runtime unavailable"
			if saveErr := r.Store.SaveReconciliation(ctx, result); saveErr != nil {
				return result, saveErr
			}
			return result, nil
		}
		result.RuntimeIdentity = strings.Join([]string{identity.Name, identity.ID, identity.Version}, ":")
	}

	generations, err := r.Store.ReconciliationGenerations(ctx, installation)
	if err != nil {
		return result, err
	}
	if len(generations) == 0 {
		return result, sql.ErrNoRows
	}
	var active *MultiGeneration
	for i := range generations {
		if generations[i].Status == "active" || generations[i].Status == "verification_required" {
			active = &generations[i]
			break
		}
	}
	if active == nil {
		latest := generations[0]
		result.CheckedGeneration = latest.Generation
		result.RuntimeState = "runtime_removed"
		result.Summary = "no active runtime generation"
		if err = r.classifyNonActive(ctx, &result, generations); err != nil {
			return r.runtimeUnknown(ctx, result, err)
		}
		result.finish()
		return result, r.Store.SaveReconciliation(ctx, result)
	}

	result.CheckedGeneration = active.Generation
	result.OriginatingOperation = active.CreatingOperationID
	result.Projection = &CommittedProjection{Generation: active.Generation, ReleaseID: active.ReleaseID, ExposureMode: active.ExposureMode, ServiceID: active.ServiceID, HostAddress: active.HostAddress, HostPort: active.HostPort}
	findings, runtimeState, observeErr := r.observeGeneration(ctx, *active, "active")
	if observeErr != nil {
		return r.runtimeUnknown(ctx, result, observeErr)
	}
	result.Components = append(result.Components, findings...)
	result.RuntimeState = runtimeState
	result.Projection.RuntimeState = runtimeState
	for _, finding := range findings {
		result.MismatchCodes = append(result.MismatchCodes, finding.MismatchCodes...)
	}
	if err = r.classifyNonActive(ctx, &result, generations); err != nil {
		return r.runtimeUnknown(ctx, result, err)
	}
	result.finish()
	return result, r.Store.SaveReconciliation(ctx, result)
}

func (r Reconciler) runtimeUnknown(ctx context.Context, result ReconciliationResult, cause error) (ReconciliationResult, error) {
	result.State = ReconciliationRuntimeUnknown
	result.RuntimeState = "unknown"
	result.MismatchCodes = append(result.MismatchCodes, MismatchRuntimeUnreachable)
	result.MismatchCodes = uniqueCodes(result.MismatchCodes)
	result.RecommendedAction = RepairNone
	result.Summary = "container runtime observation failed"
	if err := r.Store.SaveReconciliation(ctx, result); err != nil {
		return result, err
	}
	return result, nil
}

func (r Reconciler) observeGeneration(ctx context.Context, generation MultiGeneration, role string) ([]ComponentFinding, string, error) {
	network, err := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
	if err != nil {
		return nil, "unknown", err
	}
	findings := make([]ComponentFinding, 0, len(generation.Components))
	states := map[string]int{}
	observedByID := map[string]string{}
	for _, component := range generation.Components {
		expected := component.State
		if role == "retained" {
			expected = "stopped"
		} else if expected != "running" && expected != "stopped" {
			if role == "cleanup" || role == "candidate" {
				expected = "stopped"
			} else {
				expected = "running"
			}
		}
		finding := ComponentFinding{ComponentID: component.ID, Generation: generation.Generation, Role: role, ExpectedRuntime: expected, ExpectedRuntimeID: component.ContainerID}
		observed, observeErr := r.Runtime.ObserveContainer(ctx, component.ContainerID)
		if observeErr != nil {
			return nil, "unknown", observeErr
		}
		if !observed.Exists {
			byName, nameErr := r.Runtime.ObserveContainer(ctx, component.ContainerName)
			if nameErr != nil {
				return nil, "unknown", nameErr
			}
			finding.ObservedRuntime = "missing"
			states["missing"]++
			if byName.Exists {
				finding.ObservedRuntimeID = byName.ID
				finding.MismatchCodes = append(finding.MismatchCodes, MismatchOwnershipAmbiguous)
			} else if role == "active" {
				if len(generation.Components) == 1 {
					finding.MismatchCodes = append(finding.MismatchCodes, MismatchActiveContainerMissing)
				} else {
					finding.MismatchCodes = append(finding.MismatchCodes, MismatchComponentMissing)
				}
			} else if role == "retained" {
				finding.MismatchCodes = append(finding.MismatchCodes, MismatchRetainedGenerationMissing)
			}
			findings = append(findings, finding)
			continue
		}
		finding.ObservedRuntimeID = observed.ID
		finding.ObservedRuntime = string(observed.State)
		states[finding.ObservedRuntime]++
		if observed.ID != component.ContainerID || observed.Name != component.ContainerName || observed.Labels[ownership.LabelManaged] != "true" || observed.Labels[ownership.LabelInstance] != generation.InstallationID || observed.Labels["com.kitpro.runtime-generation"] != fmt.Sprint(generation.Generation) {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchOwnershipAmbiguous)
		}
		if observed.ImageReference != component.Image || (component.ImageID != "" && observed.ImageID != component.ImageID) {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchImage)
		}
		attachment, attached := observed.Networks[generation.NetworkName]
		stoppedBeforeFirstStart := (role == "candidate" || role == "cleanup") && observed.State == containers.RuntimeStopped && attachment.NetworkID == "" && observed.NetworkMode == generation.NetworkName
		if !network.Exists || (generation.NetworkID != "" && network.ID != generation.NetworkID) || !attached || (network.ID != "" && attachment.NetworkID != network.ID && !stoppedBeforeFirstStart) {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchNetwork)
		}
		if generation.Status == "verification_required" && generation.TopologyHash == "" && len(generation.Components) == 1 && component.ConfigurationHash == "" {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchConfiguration)
		} else if component.ConfigurationHash != "" && observationHash(observed) != component.ConfigurationHash {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchConfiguration)
		}
		if role == "active" && expected == "running" && observed.State == containers.RuntimeStopped {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchActiveContainerStopped)
		}
		if role == "active" && expected == "stopped" && observed.State == containers.RuntimeRunning {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchActiveContainerRunning)
		}
		if role == "active" && (observed.State == containers.RuntimeRestarting || observed.State == containers.RuntimePaused) {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchActiveContainerUnstable)
		}
		if role == "active" && observed.State == containers.RuntimeUnknown {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchRuntimeStateUnknown)
		}
		if role == "retained" && observed.State == containers.RuntimeRunning {
			finding.MismatchCodes = append(finding.MismatchCodes, MismatchRetainedGenerationRunning)
		}
		observedByID[component.ID] = finding.ObservedRuntime
		findings = append(findings, finding)
	}
	if role == "active" && len(states) > 1 {
		for i := range findings {
			findings[i].MismatchCodes = append(findings[i].MismatchCodes, MismatchComponentStateMixed)
		}
	}
	if role == "active" {
		for i := range generation.Components {
			component := generation.Components[i]
			if observedByID[component.ID] != "running" {
				continue
			}
			for _, dependency := range component.DependsOn {
				if observedByID[dependency] != "running" {
					findings[i].MismatchCodes = append(findings[i].MismatchCodes, MismatchDependencyState)
				}
			}
		}
	}
	runtimeState := "unknown"
	switch {
	case states["running"] == len(generation.Components):
		runtimeState = "running"
	case states["stopped"] == len(generation.Components):
		runtimeState = "stopped"
	case states["missing"] == len(generation.Components):
		runtimeState = "missing"
	case states[string(containers.RuntimeRestarting)] == len(generation.Components):
		runtimeState = string(containers.RuntimeRestarting)
	case states[string(containers.RuntimePaused)] == len(generation.Components):
		runtimeState = string(containers.RuntimePaused)
	case states[string(containers.RuntimeUnknown)] == len(generation.Components):
		runtimeState = string(containers.RuntimeUnknown)
	default:
		runtimeState = "degraded"
	}
	return findings, runtimeState, nil
}

func (r Reconciler) classifyNonActive(ctx context.Context, result *ReconciliationResult, generations []MultiGeneration) error {
	for _, generation := range generations {
		role := ""
		switch generation.Status {
		case "retained":
			role = "retained"
		case "cleanup_pending":
			role = "cleanup"
		case "candidate", "prepared", "failed":
			role = "candidate"
		default:
			continue
		}
		findings, _, err := r.observeGeneration(ctx, generation, role)
		if err != nil {
			return err
		}
		allMissing := len(findings) > 0
		anyExact := false
		for _, finding := range findings {
			result.Components = append(result.Components, finding)
			result.MismatchCodes = append(result.MismatchCodes, finding.MismatchCodes...)
			if finding.ObservedRuntime != "missing" {
				allMissing = false
				if len(finding.MismatchCodes) == 0 {
					anyExact = true
				}
			}
		}
		if generation.CleanupState == "pending" {
			if allMissing {
				network, networkErr := r.Runtime.ObserveNetwork(ctx, generation.NetworkName)
				if networkErr != nil {
					return networkErr
				}
				if !network.Exists {
					if err = r.Store.MarkReconciledAbsent(ctx, generation); err != nil {
						return err
					}
				} else {
					result.MismatchCodes = append(result.MismatchCodes, MismatchCleanupPending)
				}
			} else {
				result.MismatchCodes = append(result.MismatchCodes, MismatchCleanupPending)
			}
		}
		if role == "candidate" && anyExact {
			result.MismatchCodes = append(result.MismatchCodes, MismatchCandidateOrphaned)
			if result.OriginatingOperation == "" {
				result.OriginatingOperation = generation.CreatingOperationID
			}
		}
	}
	return nil
}

func (r *ReconciliationResult) finish() {
	r.MismatchCodes = uniqueCodes(r.MismatchCodes)
	has := func(code MismatchCode) bool {
		for _, candidate := range r.MismatchCodes {
			if candidate == code {
				return true
			}
		}
		return false
	}
	switch {
	case has(MismatchRuntimeUnreachable):
		r.State, r.RecommendedAction, r.Summary = ReconciliationRuntimeUnknown, RepairNone, "container runtime unavailable"
	case has(MismatchRuntimeStateUnknown):
		r.State, r.RecommendedAction, r.Summary = ReconciliationActionRequired, RepairNone, "runtime state cannot be safely classified"
	case has(MismatchOwnershipAmbiguous) || has(MismatchImage) || has(MismatchConfiguration) || has(MismatchNetwork) || has(MismatchRetainedGenerationRunning) || has(MismatchActiveContainerRunning):
		r.State, r.RecommendedAction, r.Summary = ReconciliationActionRequired, RepairNone, "runtime identity or configuration differs from helper authority"
	case has(MismatchActiveContainerMissing) || has(MismatchComponentMissing):
		r.State, r.RecommendedAction, r.Summary = ReconciliationRuntimeMissing, RepairRecreateGeneration, "active runtime resources are missing"
	case has(MismatchDependencyState) || has(MismatchComponentStateMixed):
		r.State, r.RecommendedAction, r.Summary = ReconciliationDegraded, RepairStartActive, "active components are in a mixed dependency state"
	case has(MismatchActiveContainerUnstable):
		r.State, r.RecommendedAction, r.Summary = ReconciliationDegraded, RepairNone, "exact active runtime is unstable; bounded lifecycle actions remain available"
	case has(MismatchActiveContainerStopped):
		r.State, r.RecommendedAction, r.Summary = ReconciliationRepairable, RepairStartActive, "exact active runtime is stopped"
	case has(MismatchCandidateOrphaned) || has(MismatchCleanupPending):
		r.State, r.RecommendedAction, r.Summary = ReconciliationCleanupPending, RepairCleanupResources, "exact non-active runtime resources require cleanup"
	case has(MismatchRetainedGenerationMissing):
		r.State, r.RecommendedAction, r.Summary = ReconciliationDegraded, RepairAcknowledgeRetainedLoss, "retained recovery generation is no longer available"
	default:
		r.State, r.RecommendedAction, r.Summary = ReconciliationConsistent, RepairNone, "helper authority and runtime observation agree"
	}
}

func uniqueCodes(values []MismatchCode) []MismatchCode {
	seen := map[MismatchCode]bool{}
	result := make([]MismatchCode, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s Store) ReconciliationGenerations(ctx context.Context, installation string) ([]MultiGeneration, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND status<>'removed' ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'verification_required' THEN 1 WHEN 'retained' THEN 2 WHEN 'cleanup_pending' THEN 3 ELSE 4 END,runtime_generation DESC`, installation)
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
	result := make([]MultiGeneration, 0, len(generations))
	for _, generation := range generations {
		item, loadErr := s.LoadMultiGeneration(ctx, installation, generation)
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, item)
	}
	return result, nil
}

func (s Store) SaveReconciliation(ctx context.Context, result ReconciliationResult) error {
	codes, err := json.Marshal(result.MismatchCodes)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(result.Components)
	if err != nil {
		return err
	}
	if len(evidence) > 32*1024 {
		return errors.New("reconciliation evidence exceeds bounded record")
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO reconciliation(instance_id,classification,observed_at,summary,checked_generation,runtime_state,runtime_identity,mismatch_codes_json,recommended_action,originating_operation_id,evidence_json) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET classification=excluded.classification,observed_at=excluded.observed_at,summary=excluded.summary,checked_generation=excluded.checked_generation,runtime_state=excluded.runtime_state,runtime_identity=excluded.runtime_identity,mismatch_codes_json=excluded.mismatch_codes_json,recommended_action=excluded.recommended_action,originating_operation_id=excluded.originating_operation_id,evidence_json=excluded.evidence_json`, result.InstallationID, result.State, result.ObservedAt, result.Summary, result.CheckedGeneration, result.RuntimeState, result.RuntimeIdentity, string(codes), result.RecommendedAction, result.OriginatingOperation, string(evidence))
	return err
}

func (s Store) LoadReconciliation(ctx context.Context, installation string) (ReconciliationResult, error) {
	var result ReconciliationResult
	var codes, evidence string
	err := s.DB.QueryRowContext(ctx, `SELECT instance_id,classification,checked_generation,runtime_state,runtime_identity,observed_at,mismatch_codes_json,recommended_action,originating_operation_id,summary,evidence_json FROM reconciliation WHERE instance_id=?`, installation).Scan(&result.InstallationID, &result.State, &result.CheckedGeneration, &result.RuntimeState, &result.RuntimeIdentity, &result.ObservedAt, &codes, &result.RecommendedAction, &result.OriginatingOperation, &result.Summary, &evidence)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(codes), &result.MismatchCodes); err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(evidence), &result.Components); err != nil {
		return result, err
	}
	if result.CheckedGeneration > 0 {
		generation, loadErr := s.LoadMultiGeneration(ctx, installation, result.CheckedGeneration)
		if loadErr == nil && generation.Status == "active" {
			result.Projection = &CommittedProjection{Generation: generation.Generation, ReleaseID: generation.ReleaseID, RuntimeState: result.RuntimeState, ExposureMode: generation.ExposureMode, ServiceID: generation.ServiceID, HostAddress: generation.HostAddress, HostPort: generation.HostPort}
		}
	}
	return result, nil
}

func (s Store) MarkReconciledAbsent(ctx context.Context, generation MultiGeneration) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE runtime_generations SET status='removed',cleanup_state='clean' WHERE installation_id=? AND runtime_generation=? AND status IN ('cleanup_pending','failed')`, generation.InstallationID, generation.Generation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_components SET state='removed' WHERE installation_id=? AND runtime_generation=?`, generation.InstallationID, generation.Generation); err != nil {
		return err
	}
	return tx.Commit()
}

func ReconcileAll(ctx context.Context, store Store, runtime containers.LifecycleRuntime) (int, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT DISTINCT installation_id FROM runtime_generations WHERE status<>'removed' ORDER BY installation_id`)
	if err != nil {
		return 0, err
	}
	var installations []string
	for rows.Next() {
		var installation string
		if err = rows.Scan(&installation); err != nil {
			rows.Close()
			return 0, err
		}
		installations = append(installations, installation)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, installation := range installations {
		observationCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		_, reconcileErr := (Reconciler{Runtime: runtime, Store: store}).Reconcile(observationCtx, installation)
		cancel()
		if reconcileErr != nil {
			return 0, reconcileErr
		}
	}
	return len(installations), nil
}
