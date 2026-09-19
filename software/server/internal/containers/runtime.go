// Package containers defines the runtime operations that KITPro's privileged
// helper is allowed to perform. It is intentionally smaller than either the
// Docker or Podman API.
package containers

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
	HostIP   string
	HostPort string
}

type StorageMount struct {
	ContainerPath string
	HostPath      string
	ReadOnly      bool
}

type DeviceMapping struct {
	PathOnHost, PathInContainer, CgroupPermissions string
}

type DeviceRequest struct {
	Driver       string
	Count        int
	Capabilities [][]string
}
