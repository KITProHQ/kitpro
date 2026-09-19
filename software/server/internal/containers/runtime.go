// Package containers defines the runtime operations that KITPro's privileged
// helper is allowed to perform. It is intentionally smaller than either the
// Docker or Podman API.
package containers

import (
	"context"
	"time"
)

type Runtime interface {
	Name() string
	Version() (map[string]any, error)
	Info() (map[string]any, error)
	Pull(image string) error
	CreateNetwork(name string, labels map[string]string) (map[string]any, error)
	CreateContainerPlan(ContainerPlan) (string, error)
	Start(id string) error
	Stop(id string) error
	Remove(id string) error
	RemoveNetwork(name string) error
	Inspect(id string) (map[string]any, error)
	ListContainers() ([]ContainerSummary, error)
	HasForeignNetworkMember(network, ownedContainerID string) (bool, error)
	HasForeignNetworkMembers(network string, owned map[string]bool) (bool, error)
}

// LifecycleRuntime is the narrow, typed contract required by staged
// single-component replacement. It is intentionally separate from Runtime so
// the experimental Podman adapter does not define the supported Docker path.
type LifecycleRuntime interface {
	PullImage(context.Context, string) error
	ObserveImage(context.Context, string) (ImageObservation, error)
	CreateLifecycleNetwork(context.Context, string, map[string]string) (NetworkObservation, error)
	ObserveNetwork(context.Context, string) (NetworkObservation, error)
	CreateLifecycleContainer(context.Context, ContainerPlan) (string, error)
	ObserveContainer(context.Context, string) (ContainerObservation, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	RemoveLifecycleNetwork(context.Context, string) error
	WaitContainer(context.Context, string, RuntimeState, time.Duration) (ContainerObservation, error)
}

// RuntimeIdentityProvider is optional because reconciliation needs daemon
// identity evidence while ordinary lifecycle mutations do not. Runtime
// adapters may implement it without widening the mutation contract.
type RuntimeIdentityProvider interface {
	ObserveRuntimeIdentity(context.Context) (RuntimeIdentityObservation, error)
}

type RuntimeState string

const (
	RuntimeUnknown RuntimeState = "unknown"
	RuntimeMissing RuntimeState = "missing"
	RuntimeStopped RuntimeState = "stopped"
	RuntimeRunning RuntimeState = "running"
)

type ImageObservation struct {
	Exists      bool
	ID          string
	RepoDigests []string
}

type RuntimeIdentityObservation struct {
	Name, ID, Version string
}

type NetworkObservation struct {
	Exists bool
	ID     string
	Name   string
	Labels map[string]string
}

type MountObservation struct {
	Source, Destination string
	ReadOnly            bool
}

type NetworkAttachment struct {
	NetworkID string
	Aliases   []string
}

type ContainerObservation struct {
	Exists         bool
	ID             string
	Name           string
	ImageID        string
	ImageReference string
	State          RuntimeState
	Health         string
	Labels         map[string]string
	User           string
	Command        []string
	Environment    []string
	RestartPolicy  string
	Mounts         []MountObservation
	Networks       map[string]NetworkAttachment
	NetworkMode    string
	PortBindings   map[string][]PortBinding
	Devices        []DeviceMapping
	DeviceRequests []DeviceRequest
}

type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

type ContainerPlan struct {
	Image, Name, Network string
	User                 string
	Labels               map[string]string
	Command              []string
	Environment          []string
	DataPath             string
	ReadOnly             bool
	Storage              []StorageMount
	PortBindings         map[string][]PortBinding
	RestartPolicy        string
	NetworkAliases       []string
	Dependencies         []string
	Devices              []DeviceMapping
	DeviceRequests       []DeviceRequest
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type StorageMount struct {
	ContainerPath string
	HostPath      string
	ReadOnly      bool
}

type DeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type DeviceRequest struct {
	Driver       string     `json:"Driver"`
	Count        int        `json:"Count"`
	Capabilities [][]string `json:"Capabilities"`
}
