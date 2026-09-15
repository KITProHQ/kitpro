package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct{ HTTP *http.Client }

const imagePullTimeout = 30 * time.Minute

type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}
type ContainerPlan struct {
	Image, Name, Network string
	Labels               map[string]string
	Command              []string
	Environment          []string
	DataPath             string
	ReadOnly             bool
	Storage              []StorageMount
	PortBindings         map[string][]PortBinding
	RestartPolicy        string
	NetworkAliases       []string
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

func New() *Client {
	return &Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}, Timeout: 120 * time.Second}}
}
func (c *Client) do(method, path string, body io.Reader) (map[string]any, error) {
	q, err := http.NewRequest(method, "http://docker"+path, body)
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
func (c *Client) Pull(image string) error {
	u := "http://docker/images/create?fromImage=" + url.QueryEscape(image)
	q, err := http.NewRequest("POST", u, nil)
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
	b, _ := json.Marshal(map[string]any{"Name": name, "Driver": "bridge", "Labels": labels})
	return c.do("POST", "/networks/create", strings.NewReader(string(b)))
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
	endpoint := map[string]any{}
	if len(p.NetworkAliases) > 0 {
		endpoint["Aliases"] = p.NetworkAliases
	}
	body := map[string]any{"Image": p.Image, "Labels": p.Labels, "HostConfig": host, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{p.Network: endpoint}}}
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
	out, e := c.do("POST", "/containers/create?name="+url.QueryEscape(p.Name), strings.NewReader(string(b)))
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
	_, e := c.do("POST", "/containers/"+id+"/start", nil)
	return e
}
func (c *Client) Stop(id string) error {
	_, e := c.do("POST", "/containers/"+id+"/stop?t=5", nil)
	return e
}
func (c *Client) Remove(id string) error {
	_, e := c.do("DELETE", "/containers/"+id+"?v=false", nil)
	return e
}
func (c *Client) RemoveNetwork(name string) error {
	_, e := c.do("DELETE", "/networks/"+url.PathEscape(name), nil)
	return e
}
func (c *Client) Inspect(id string) (map[string]any, error) {
	return c.do("GET", "/containers/"+id+"/json", nil)
}
func (c *Client) InspectNetwork(name string) (map[string]any, error) {
	return c.do("GET", "/networks/"+url.PathEscape(name), nil)
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
