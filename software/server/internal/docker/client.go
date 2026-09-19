package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

type Client struct{ HTTP *http.Client }

const imagePullTimeout = 30 * time.Minute

type ContainerSummary = containers.ContainerSummary
type ContainerPlan = containers.ContainerPlan
type PortBinding = containers.PortBinding
type StorageMount = containers.StorageMount
type DeviceMapping = containers.DeviceMapping
type DeviceRequest = containers.DeviceRequest

func New() *Client {
	return &Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}, Timeout: 120 * time.Second}}
}

func (c *Client) Name() string { return "docker" }
func (c *Client) do(method, path string, body io.Reader) (map[string]any, error) {
	return c.doContext(context.Background(), method, path, body)
}
func (c *Client) doContext(ctx context.Context, method, path string, body io.Reader) (map[string]any, error) {
	q, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return nil, err
	}
	q.Header.Set("Content-Type", "application/json")
	r, e := c.HTTP.Do(q)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || (r.StatusCode >= 300 && r.StatusCode != http.StatusNotModified) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &detail)
		msg := strings.TrimSpace(detail.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		if msg == "" {
			msg = "no response body"
		}
		return nil, fmt.Errorf("docker operation %s: HTTP %s: %s", method+" "+path, r.Status, msg)
	}
	var out map[string]any
	e = json.NewDecoder(io.LimitReader(r.Body, 128*1024)).Decode(&out)
	if e == io.EOF {
		return map[string]any{}, nil
	}
	return out, e
}
func (c *Client) Version() (map[string]any, error) { return c.do("GET", "/version", nil) }
func (c *Client) Info() (map[string]any, error)    { return c.do("GET", "/info", nil) }
func (c *Client) Pull(image string) error {
	return c.PullImage(context.Background(), image)
}
func (c *Client) PullImage(ctx context.Context, image string) error {
	u := "http://docker/images/create?fromImage=" + url.QueryEscape(image)
	q, err := http.NewRequestWithContext(ctx, "POST", u, nil)
	if err != nil {
		return err
	}
	pullHTTP := *c.HTTP
	pullHTTP.Timeout = imagePullTimeout
	r, err := pullHTTP.Do(q)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		return fmt.Errorf("docker pull: HTTP %s: %s", r.Status, strings.TrimSpace(string(b)))
	}
	// Docker's progress stream grows with image size and layer count. Drain it
	// completely so closing the response does not cancel an otherwise healthy
	// pull; the dedicated timeout above remains the hard upper bound.
	_, err = io.Copy(io.Discard, r.Body)
	return err
}
func (c *Client) CreateNetwork(name string, labels map[string]string) (map[string]any, error) {
	observation, err := c.CreateLifecycleNetwork(context.Background(), name, labels)
	return map[string]any{"Id": observation.ID}, err
}
func (c *Client) CreateLifecycleNetwork(ctx context.Context, name string, labels map[string]string) (containers.NetworkObservation, error) {
	b, _ := json.Marshal(map[string]any{"Name": name, "Driver": "bridge", "Labels": labels})
	out, err := c.doContext(ctx, "POST", "/networks/create", strings.NewReader(string(b)))
	if err != nil {
		return containers.NetworkObservation{}, err
	}
	id, _ := out["Id"].(string)
	return containers.NetworkObservation{Exists: true, ID: id, Name: name, Labels: labels}, nil
}
func (c *Client) CreateContainer(name, image, network string, labels map[string]string) (string, error) {
	b, _ := json.Marshal(map[string]any{"Image": image, "Labels": labels, "HostConfig": map[string]any{"NetworkMode": network, "Privileged": false, "ReadonlyRootfs": false}, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network: map[string]any{}}}})
	out, e := c.do("POST", "/containers/create?name="+url.QueryEscape(name), strings.NewReader(string(b)))
	if e != nil {
		return "", e
	}
	id, _ := out["Id"].(string)
	return id, nil
}
func (c *Client) CreateContainerPlan(p ContainerPlan) (string, error) {
	return c.CreateLifecycleContainer(context.Background(), p)
}
func (c *Client) CreateLifecycleContainer(ctx context.Context, p ContainerPlan) (string, error) {
	host := map[string]any{"NetworkMode": p.Network, "Privileged": false, "ReadonlyRootfs": false}
	if p.RestartPolicy != "" && p.RestartPolicy != "no" {
		host["RestartPolicy"] = map[string]any{"Name": p.RestartPolicy, "MaximumRetryCount": 0}
	}
	if len(p.Storage) > 0 {
		binds := make([]string, 0, len(p.Storage))
		for _, m := range p.Storage {
			mode := ""
			if m.ReadOnly {
				mode = ":ro"
			}
			binds = append(binds, m.HostPath+":"+m.ContainerPath+mode)
		}
		host["Binds"] = binds
	} else if p.DataPath != "" {
		host["Binds"] = []string{p.DataPath + ":/data" + func() string {
			if p.ReadOnly {
				return ":ro"
			}
			return ""
		}()}
	}
	if len(p.Devices) > 0 {
		host["Devices"] = p.Devices
	}
	if len(p.DeviceRequests) > 0 {
		host["DeviceRequests"] = p.DeviceRequests
	}
	endpoint := map[string]any{}
	if len(p.NetworkAliases) > 0 {
		endpoint["Aliases"] = p.NetworkAliases
	}
	body := map[string]any{"Image": p.Image, "Labels": p.Labels, "HostConfig": host, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{p.Network: endpoint}}}
	if p.User != "" {
		body["User"] = p.User
	}
	if len(p.PortBindings) > 0 {
		host["PortBindings"] = p.PortBindings
	}
	if len(p.Command) > 0 {
		body["Cmd"] = p.Command
	}
	if len(p.Environment) > 0 {
		body["Env"] = p.Environment
	}
	b, _ := json.Marshal(body)
	out, e := c.doContext(ctx, "POST", "/containers/create?name="+url.QueryEscape(p.Name), strings.NewReader(string(b)))
	if e != nil {
		return "", e
	}
	id, _ := out["Id"].(string)
	if id == "" {
		return "", fmt.Errorf("docker returned no container id")
	}
	return id, nil
}
func (c *Client) Start(id string) error {
	return c.StartContainer(context.Background(), id)
}
func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, e := c.doContext(ctx, "POST", "/containers/"+id+"/start", nil)
	return e
}
func (c *Client) Stop(id string) error {
	return c.StopContainer(context.Background(), id)
}
func (c *Client) StopContainer(ctx context.Context, id string) error {
	_, e := c.doContext(ctx, "POST", "/containers/"+id+"/stop?t=5", nil)
	return e
}
func (c *Client) Remove(id string) error {
	return c.RemoveContainer(context.Background(), id)
}
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	_, e := c.doContext(ctx, "DELETE", "/containers/"+id+"?v=false", nil)
	return e
}
func (c *Client) RemoveNetwork(name string) error {
	return c.RemoveLifecycleNetwork(context.Background(), name)
}
func (c *Client) RemoveLifecycleNetwork(ctx context.Context, name string) error {
	_, e := c.doContext(ctx, "DELETE", "/networks/"+url.PathEscape(name), nil)
	return e
}
func (c *Client) Inspect(id string) (map[string]any, error) {
	return c.do("GET", "/containers/"+id+"/json", nil)
}
func (c *Client) InspectNetwork(name string) (map[string]any, error) {
	return c.do("GET", "/networks/"+url.PathEscape(name), nil)
}

func (c *Client) ObserveRuntimeIdentity(ctx context.Context) (containers.RuntimeIdentityObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/info", nil)
	if err != nil {
		return containers.RuntimeIdentityObservation{}, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return containers.RuntimeIdentityObservation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return containers.RuntimeIdentityObservation{}, fmt.Errorf("docker info: HTTP %s", response.Status)
	}
	var observed struct {
		ID            string `json:"ID"`
		ServerVersion string `json:"ServerVersion"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&observed); err != nil {
		return containers.RuntimeIdentityObservation{}, err
	}
	if observed.ID == "" {
		return containers.RuntimeIdentityObservation{}, errors.New("docker daemon identity unavailable")
	}
	return containers.RuntimeIdentityObservation{Name: "docker", ID: observed.ID, Version: observed.ServerVersion}, nil
}

func (c *Client) ObserveImage(ctx context.Context, image string) (containers.ImageObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/images/"+url.PathEscape(image)+"/json", nil)
	if err != nil {
		return containers.ImageObservation{}, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return containers.ImageObservation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return containers.ImageObservation{}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return containers.ImageObservation{}, fmt.Errorf("docker image inspect: HTTP %s", response.Status)
	}
	var observed struct {
		ID          string   `json:"Id"`
		RepoDigests []string `json:"RepoDigests"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(&observed); err != nil {
		return containers.ImageObservation{}, err
	}
	return containers.ImageObservation{Exists: true, ID: observed.ID, RepoDigests: observed.RepoDigests}, nil
}

func (c *Client) ObserveNetwork(ctx context.Context, name string) (containers.NetworkObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/networks/"+url.PathEscape(name), nil)
	if err != nil {
		return containers.NetworkObservation{}, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return containers.NetworkObservation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return containers.NetworkObservation{Name: name}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return containers.NetworkObservation{}, fmt.Errorf("docker network inspect: HTTP %s", response.Status)
	}
	var observed struct {
		ID     string            `json:"Id"`
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(&observed); err != nil {
		return containers.NetworkObservation{}, err
	}
	return containers.NetworkObservation{Exists: true, ID: observed.ID, Name: observed.Name, Labels: observed.Labels}, nil
}

func (c *Client) ObserveContainer(ctx context.Context, id string) (containers.ContainerObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return containers.ContainerObservation{}, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return containers.ContainerObservation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return containers.ContainerObservation{State: containers.RuntimeMissing}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return containers.ContainerObservation{}, fmt.Errorf("docker container inspect: HTTP %s", response.Status)
	}
	var raw struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		Image string `json:"Image"`
		State struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
			Health  *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
			User   string            `json:"User"`
			Cmd    []string          `json:"Cmd"`
			Env    []string          `json:"Env"`
		} `json:"Config"`
		HostConfig struct {
			NetworkMode   string `json:"NetworkMode"`
			RestartPolicy struct {
				Name string `json:"Name"`
			} `json:"RestartPolicy"`
			PortBindings   map[string][]containers.PortBinding `json:"PortBindings"`
			Devices        []containers.DeviceMapping          `json:"Devices"`
			DeviceRequests []containers.DeviceRequest          `json:"DeviceRequests"`
		} `json:"HostConfig"`
		Mounts []struct {
			Source, Destination string
			RW                  bool `json:"RW"`
		} `json:"Mounts"`
		NetworkSettings struct {
			Networks map[string]struct {
				NetworkID string   `json:"NetworkID"`
				Aliases   []string `json:"Aliases"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&raw); err != nil {
		return containers.ContainerObservation{}, err
	}
	state := containers.RuntimeUnknown
	if raw.State.Running && raw.State.Status == "running" {
		state = containers.RuntimeRunning
	} else if !raw.State.Running && (raw.State.Status == "created" || raw.State.Status == "exited") {
		state = containers.RuntimeStopped
	}
	health := ""
	if raw.State.Health != nil {
		health = raw.State.Health.Status
	}
	observed := containers.ContainerObservation{Exists: true, ID: raw.ID, Name: strings.TrimPrefix(raw.Name, "/"), ImageID: raw.Image, ImageReference: raw.Config.Image, State: state, Health: health, Labels: raw.Config.Labels, User: raw.Config.User, Command: raw.Config.Cmd, Environment: raw.Config.Env, RestartPolicy: raw.HostConfig.RestartPolicy.Name, NetworkMode: raw.HostConfig.NetworkMode, PortBindings: raw.HostConfig.PortBindings, Devices: raw.HostConfig.Devices, DeviceRequests: raw.HostConfig.DeviceRequests, Networks: map[string]containers.NetworkAttachment{}}
	for _, mount := range raw.Mounts {
		observed.Mounts = append(observed.Mounts, containers.MountObservation{Source: mount.Source, Destination: mount.Destination, ReadOnly: !mount.RW})
	}
	for name, network := range raw.NetworkSettings.Networks {
		observed.Networks[name] = containers.NetworkAttachment{NetworkID: network.NetworkID, Aliases: network.Aliases}
	}
	return observed, nil
}

func (c *Client) WaitContainer(ctx context.Context, id string, want containers.RuntimeState, timeout time.Duration) (containers.ContainerObservation, error) {
	deadline := time.Now().Add(timeout)
	for {
		observed, err := c.ObserveContainer(ctx, id)
		if err != nil {
			return containers.ContainerObservation{}, err
		}
		if observed.State == want {
			return observed, nil
		}
		if time.Now().After(deadline) {
			return observed, fmt.Errorf("container did not reach %s state", want)
		}
		select {
		case <-ctx.Done():
			return containers.ContainerObservation{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (c *Client) ListContainers() ([]ContainerSummary, error) {
	q, e := http.NewRequest("GET", "http://docker/containers/json?all=true", nil)
	if e != nil {
		return nil, e
	}
	r, e := c.HTTP.Do(q)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, fmt.Errorf("docker list: %s", r.Status)
	}
	var out []ContainerSummary
	e = json.NewDecoder(io.LimitReader(r.Body, 128*1024)).Decode(&out)
	return out, e
}

// HasForeignNetworkMember observes both the network endpoint map and every
// container's configured networks. Docker can omit stopped endpoints from a
// network inspection, so destructive lifecycle checks must use both views.
func (c *Client) HasForeignNetworkMember(network, ownedContainerID string) (bool, error) {
	networkObject, err := c.InspectNetwork(network)
	if err != nil {
		return false, err
	}
	if endpoints, ok := networkObject["Containers"].(map[string]any); ok {
		for id := range endpoints {
			if id != ownedContainerID {
				return true, nil
			}
		}
	}
	containers, err := c.ListContainers()
	if err != nil {
		return false, err
	}
	for _, container := range containers {
		if container.ID == ownedContainerID {
			continue
		}
		observed, inspectErr := c.Inspect(container.ID)
		if inspectErr != nil {
			return false, inspectErr
		}
		settings, _ := observed["NetworkSettings"].(map[string]any)
		networks, _ := settings["Networks"].(map[string]any)
		if _, member := networks[network]; member {
			return true, nil
		}
	}
	return false, nil
}

// HasForeignNetworkMembers is the multi-component equivalent of
// HasForeignNetworkMember. The caller supplies the complete trusted runtime
// set; no Docker label is treated as authority.
func (c *Client) HasForeignNetworkMembers(network string, owned map[string]bool) (bool, error) {
	networkObject, err := c.InspectNetwork(network)
	if err != nil {
		return false, err
	}
	if endpoints, ok := networkObject["Containers"].(map[string]any); ok {
		for id := range endpoints {
			if !owned[id] {
				return true, nil
			}
		}
	}
	containers, err := c.ListContainers()
	if err != nil {
		return false, err
	}
	for _, container := range containers {
		if owned[container.ID] {
			continue
		}
		observed, inspectErr := c.Inspect(container.ID)
		if inspectErr != nil {
			return false, inspectErr
		}
		settings, _ := observed["NetworkSettings"].(map[string]any)
		networks, _ := settings["Networks"].(map[string]any)
		if _, member := networks[network]; member {
			return true, nil
		}
	}
	return false, nil
}
