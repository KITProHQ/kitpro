package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

type Phase string

const (
	PhaseBeforePull           Phase = "before_pull"
	PhaseAfterPull            Phase = "after_pull"
	PhaseBeforeNetwork        Phase = "before_candidate_network"
	PhaseAfterNetwork         Phase = "after_candidate_network"
	PhaseBeforeContainer      Phase = "before_candidate_container"
	PhaseAfterContainer       Phase = "after_candidate_container"
	PhaseBeforeOldStop        Phase = "before_old_stop"
	PhaseAfterOldStop         Phase = "after_old_stop"
	PhaseBeforeCandidateStart Phase = "before_candidate_start"
	PhaseAfterCandidateStart  Phase = "after_candidate_start"
	PhaseBeforeVerification   Phase = "before_runtime_verification"
	PhaseAfterVerification    Phase = "after_runtime_verification"
	PhaseBeforeCommit         Phase = "before_generation_commit"
	PhaseAfterCommit          Phase = "after_generation_commit"
	PhaseCleanup              Phase = "old_generation_cleanup"
)

var ErrSimulatedInterruption = errors.New("simulated lifecycle interruption")

type Hook func(context.Context, Phase) error

type Evidence interface {
	RecordPhase(context.Context, string, int64, string, string, map[string]string) error
}

type Plan struct {
	OperationID, InstallationID, ApplicationID, ReleaseID string
	Generation, ExpectedGeneration                        int
	FencingToken                                          int64
	Image, NetworkName, ContainerName, PlanHash           string
	DataPath                                              string
	ExposureMode, ServiceID, HostAddress, ServiceProtocol string
	HostPort, ContainerPort                               int
	Container                                             containers.ContainerPlan
	RollbackSafe                                          bool
	AllowMissingActive                                    bool
}

type Generation struct {
	InstallationID, CreatingOperationID, ApplicationID, ReleaseID string
	Generation                                                    int
	Status, NetworkName, NetworkID, PlanHash, DataPath            string
	ExposureMode, ServiceID, HostAddress, ServiceProtocol         string
	HostPort, ContainerPort                                       int
	CleanupState                                                  string
	Component                                                     Component
}

type Component struct {
	ID, ContainerName, ContainerID, Image, ImageID, ConfigurationHash string
	State                                                             string
	DependsOn                                                         []string
	StartOrdinal                                                      int
}

// MultiPlan is one accepted application generation. Components are stored in
// deterministic dependency-first order and committed as a single unit.
type MultiPlan struct {
	OperationID, InstallationID, ApplicationID, ReleaseID string
	Generation, ExpectedGeneration                        int
	FencingToken                                          int64
	NetworkName, PlanHash, TopologyHash, DataPath         string
	ExposureMode, ServiceID, HostAddress, ServiceProtocol string
	HostPort, ContainerPort                               int
	Components                                            []MultiComponentPlan
	RollbackSafe                                          bool
	AllowMissingActive                                    bool
}

type MultiComponentPlan struct {
	ID, Image, ContainerName string
	DependsOn                []string
	StartOrdinal             int
	Container                containers.ContainerPlan
}

type MultiGeneration struct {
	InstallationID, CreatingOperationID, ApplicationID, ReleaseID    string
	Generation                                                       int
	Status, NetworkName, NetworkID, PlanHash, TopologyHash, DataPath string
	ExposureMode, ServiceID, HostAddress, ServiceProtocol            string
	HostPort, ContainerPort                                          int
	CleanupState                                                     string
	Components                                                       []Component
}

type ComponentStep struct {
	OperationID, InstallationID, ComponentID, Action string
	Generation, Attempt                              int
	ExpectedRuntimeID, ObservedRuntimeID             string
	IntentAt, DispatchedAt, VerifiedAt               string
	OutcomeConfidence, ErrorDetail                   string
}

type Result struct {
	Generation      int               `json:"runtime_generation"`
	ReleaseID       string            `json:"release_id"`
	RuntimeState    string            `json:"runtime_state"`
	ContainerName   string            `json:"container"`
	ContainerID     string            `json:"container_id"`
	NetworkName     string            `json:"network"`
	ExposureMode    string            `json:"exposure_mode"`
	ServiceID       string            `json:"service_id,omitempty"`
	HostAddress     string            `json:"host_address,omitempty"`
	HostPort        int               `json:"host_port,omitempty"`
	CleanupDeferred bool              `json:"cleanup_deferred,omitempty"`
	Components      map[string]string `json:"components,omitempty"`
}

type UnknownOutcomeError struct {
	Phase Phase
	Cause error
}

func (e UnknownOutcomeError) Error() string {
	if e.Cause == nil {
		return "runtime outcome requires reconciliation at " + string(e.Phase)
	}
	return "runtime outcome requires reconciliation at " + string(e.Phase) + ": " + e.Cause.Error()
}
func (e UnknownOutcomeError) Unwrap() error { return e.Cause }

const observationTimeout = 15 * time.Second
