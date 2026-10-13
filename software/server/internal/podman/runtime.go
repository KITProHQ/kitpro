package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

// A clean host may need to pull a large pinned image before systemctl start
// returns. Keep the adapter deadline just beyond the generated Quadlet unit's
// 30-minute TimeoutStartSec so systemd remains the authoritative timeout.
const commandTimeout = 31 * time.Minute

var resourceName = regexp.MustCompile(`^kitpro-[a-z0-9-]{3,220}$`)

type Config struct {
	UnitDir        string
	EnvironmentDir string
	PodmanPath     string
	SystemctlPath  string
}

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 16*1024 {
			message = message[:16*1024]
		}
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", filepath.Base(command), message)
	}
	return output, nil
}

type Runtime struct {
	config Config
	runner Runner
}

func New() *Runtime {
	return NewWith(Config{
		UnitDir:        env("KITPRO_QUADLET_DIR", "/etc/containers/systemd"),
		EnvironmentDir: env("KITPRO_RUNTIME_CONFIG_DIR", "/etc/kitpro-server/runtime"),
		PodmanPath:     env("KITPRO_PODMAN_PATH", "/usr/bin/podman"),
		SystemctlPath:  env("KITPRO_SYSTEMCTL_PATH", "/usr/bin/systemctl"),
	}, execRunner{})
}

func NewWith(config Config, runner Runner) *Runtime {
	return &Runtime{config: config, runner: runner}
}

func (r *Runtime) Name() string { return "podman" }

func (r *Runtime) Version() (map[string]any, error) {
	output, err := r.run(r.config.PodmanPath, "version", "--format", "json")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("decode podman version: %w", err)
	}
	return result, nil
}

func (r *Runtime) Info() (map[string]any, error) {
	output, err := r.run(r.config.PodmanPath, "info", "--format", "json")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("decode podman info: %w", err)
	}
	// Hardware discovery consumes the runtime-neutral map through a legacy key.
	// Rocky GPU support remains unavailable until a CDI path is certified.
	result["Runtimes"] = map[string]any{}
	return result, nil
}

// Pull is intentionally deferred to the Quadlet-generated service. Pulling
// through systemd keeps registry networking outside the confined helper.
func (r *Runtime) Pull(image string) error {
	if !validImage(image) {
		return fmt.Errorf("invalid OCI image reference")
	}
	return nil
}

func (r *Runtime) CreateNetwork(name string, labels map[string]string) (map[string]any, error) {
	if !validName(name) {
		return nil, fmt.Errorf("invalid network name")
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=KITPro application network ")
	b.WriteString(name)
	b.WriteString("\n\n[Network]\nDriver=bridge\nNetworkName=")
	b.WriteString(name)
	b.WriteByte('\n')
	writeLabels(&b, labels)
	b.WriteString("NetworkDeleteOnStop=true\n\n[Install]\nWantedBy=multi-user.target\n")
	if err := r.write(filepath.Join(r.config.UnitDir, name+".network"), []byte(b.String()), 0644); err != nil {
		return nil, err
	}
	if err := r.reload(); err != nil {
		return nil, err
	}
	if _, err := r.run(r.config.SystemctlPath, "start", networkService(name)); err != nil {
		return nil, err
	}
	return map[string]any{"Id": name}, nil
}

func (r *Runtime) CreateContainerPlan(plan containers.ContainerPlan) (string, error) {
	if err := validatePlan(plan); err != nil {
		return "", err
	}
	if len(plan.DeviceRequests) > 0 {
		return "", fmt.Errorf("Podman GPU device requests are not certified on Rocky Linux")
	}
	if err := os.MkdirAll(r.config.EnvironmentDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(r.config.EnvironmentDir, 0700); err != nil {
		return "", err
	}
	environmentPath := filepath.Join(r.config.EnvironmentDir, plan.Name+".env")
	if len(plan.Environment) > 0 {
		if err := r.write(environmentPath, []byte(strings.Join(plan.Environment, "\n")+"\n"), 0600); err != nil {
			return "", err
		}
	} else if err := os.Remove(environmentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	var b strings.Builder
	b.WriteString("[Unit]\nDescription=KITPro application container ")
	b.WriteString(plan.Name)
	b.WriteByte('\n')
	for _, dependency := range plan.Dependencies {
		b.WriteString("Requires=")
		b.WriteString(containerService(dependency))
		b.WriteString("\nAfter=")
		b.WriteString(containerService(dependency))
		b.WriteByte('\n')
	}
	b.WriteString("\n[Container]\nImage=")
	b.WriteString(plan.Image)
	b.WriteString("\nContainerName=")
	b.WriteString(plan.Name)
	b.WriteString("\nNetwork=")
	b.WriteString(plan.Network)
	b.WriteString(".network\nNoNewPrivileges=true\nPull=missing\n")
	writeLabels(&b, plan.Labels)
	if plan.User != "" {
		parts := strings.Split(plan.User, ":")
		b.WriteString("User=")
		b.WriteString(parts[0])
		b.WriteString("\nGroup=")
		b.WriteString(parts[1])
		b.WriteByte('\n')
	}
	if len(plan.Environment) > 0 {
		b.WriteString("EnvironmentFile=")
		b.WriteString(quote(environmentPath))
		b.WriteByte('\n')
	}
	for _, mount := range plan.Storage {
		options := []string{}
		if mount.ReadOnly {
			options = append(options, "ro")
		}
		if strings.HasPrefix(mount.HostPath, "/srv/kitpro/apps/") {
			options = append(options, "Z")
		}
		value := mount.HostPath + ":" + mount.ContainerPath
		if len(options) > 0 {
			value += ":" + strings.Join(options, ",")
		}
		b.WriteString("Volume=")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	for key, bindings := range plan.PortBindings {
		parts := strings.Split(key, "/")
		for _, binding := range bindings {
			b.WriteString("PublishPort=")
			b.WriteString(publishAddress(binding.HostIP))
			b.WriteByte(':')
			b.WriteString(binding.HostPort)
			b.WriteByte(':')
			b.WriteString(parts[0])
			if parts[1] != "tcp" {
				b.WriteByte('/')
				b.WriteString(parts[1])
			}
			b.WriteByte('\n')
		}
	}
	for _, alias := range plan.NetworkAliases {
		b.WriteString("NetworkAlias=")
		b.WriteString(alias)
		b.WriteByte('\n')
	}
	for _, device := range plan.Devices {
		b.WriteString("AddDevice=")
		b.WriteString(quote(device.PathOnHost + ":" + device.PathInContainer + ":" + device.CgroupPermissions))
		b.WriteByte('\n')
	}
	if len(plan.Command) > 0 {
		b.WriteString("Exec=")
		for index, argument := range plan.Command {
			if index > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(quote(argument))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n[Service]\nTimeoutStartSec=1800\nTimeoutStopSec=90\n")
	if plan.RestartPolicy == "unless-stopped" {
		b.WriteString("Restart=always\nRestartSec=5s\n")
	} else {
		b.WriteString("Restart=no\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")

	if err := r.write(filepath.Join(r.config.UnitDir, plan.Name+".container"), []byte(b.String()), 0644); err != nil {
		return "", err
	}
	metadata := plan
	metadata.Environment = nil
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	if err := r.write(r.metadataPath(plan.Name), encoded, 0600); err != nil {
		return "", err
	}
	if err := r.reload(); err != nil {
		return "", err
	}
	return plan.Name, nil
}

func (r *Runtime) Start(id string) error {
	if !validName(id) {
		return fmt.Errorf("invalid container name")
	}
	_, err := r.run(r.config.SystemctlPath, "start", containerService(id))
	return err
}

func (r *Runtime) Stop(id string) error {
	if !validName(id) {
		return fmt.Errorf("invalid container name")
	}
	_, err := r.run(r.config.SystemctlPath, "stop", containerService(id))
	return err
}

func (r *Runtime) Remove(id string) error {
	if !validName(id) {
		return fmt.Errorf("invalid container name")
	}
	for _, path := range []string{
		filepath.Join(r.config.UnitDir, id+".container"),
		filepath.Join(r.config.EnvironmentDir, id+".env"),
		r.metadataPath(id),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return r.reload()
}

func (r *Runtime) RemoveNetwork(name string) error {
	if !validName(name) {
		return fmt.Errorf("invalid network name")
	}
	if _, err := r.run(r.config.SystemctlPath, "stop", networkService(name)); err != nil && !strings.Contains(err.Error(), "not loaded") {
		return err
	}
	if err := os.Remove(filepath.Join(r.config.UnitDir, name+".network")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return r.reload()
}

func (r *Runtime) Inspect(id string) (map[string]any, error) {
	if !validName(id) {
		return nil, fmt.Errorf("invalid container name")
	}
	output, err := r.run(r.config.PodmanPath, "inspect", "--format", "json", id)
	if err == nil {
		var items []map[string]any
		if decodeErr := json.Unmarshal(output, &items); decodeErr != nil || len(items) != 1 {
			return nil, fmt.Errorf("decode podman inspect")
		}
		return items[0], nil
	}
	return r.syntheticInspect(id)
}

func (r *Runtime) ListContainers() ([]containers.ContainerSummary, error) {
	output, err := r.run(r.config.PodmanPath, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("decode podman container list: %w", err)
	}
	result := make([]containers.ContainerSummary, 0, len(raw))
	for _, item := range raw {
		id, _ := item["Id"].(string)
		if id == "" {
			id, _ = item["ID"].(string)
		}
		names := stringSlice(item["Names"])
		if len(names) == 0 {
			if name, _ := item["Names"].(string); name != "" {
				names = []string{name}
			} else if name, _ := item["Name"].(string); name != "" {
				names = []string{name}
			}
		}
		result = append(result, containers.ContainerSummary{ID: id, Names: names})
	}
	return result, nil
}

func (r *Runtime) HasForeignNetworkMember(network, ownedContainerID string) (bool, error) {
	return r.HasForeignNetworkMembers(network, map[string]bool{ownedContainerID: true})
}

func (r *Runtime) HasForeignNetworkMembers(network string, owned map[string]bool) (bool, error) {
	if !validName(network) {
		return false, fmt.Errorf("invalid network name")
	}
	output, err := r.run(r.config.PodmanPath, "ps", "--all", "--filter", "network="+network, "--format", "json")
	if err != nil {
		return false, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(output, &raw); err != nil {
		return false, fmt.Errorf("decode podman network members: %w", err)
	}
	for _, item := range raw {
		id, _ := item["Id"].(string)
		if id == "" {
			id, _ = item["ID"].(string)
		}
		if owned[id] {
			continue
		}
		matched := false
		for _, name := range append(stringSlice(item["Names"]), stringValue(item["Name"])) {
			if owned[strings.TrimPrefix(name, "/")] {
				matched = true
				break
			}
		}
		if !matched {
			return true, nil
		}
	}
	return false, nil
}

func (r *Runtime) syntheticInspect(id string) (map[string]any, error) {
	if _, err := os.Stat(filepath.Join(r.config.UnitDir, id+".container")); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(r.metadataPath(id))
	if err != nil {
		return nil, err
	}
	var plan containers.ContainerPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	binds := make([]string, 0, len(plan.Storage))
	for _, mount := range plan.Storage {
		value := mount.HostPath + ":" + mount.ContainerPath
		if mount.ReadOnly {
			value += ":ro"
		}
		binds = append(binds, value)
	}
	ports := map[string]any{}
	for key, values := range plan.PortBindings {
		items := make([]any, 0, len(values))
		for _, binding := range values {
			items = append(items, map[string]any{"HostIp": binding.HostIP, "HostPort": binding.HostPort})
		}
		ports[key] = items
	}
	return map[string]any{
		"Id":              id,
		"Name":            id,
		"Config":          map[string]any{"Image": plan.Image, "Labels": stringMapAny(plan.Labels), "User": plan.User},
		"HostConfig":      map[string]any{"Binds": stringSliceAny(binds), "PortBindings": ports, "NetworkMode": plan.Network},
		"NetworkSettings": map[string]any{"Networks": map[string]any{plan.Network: map[string]any{}}},
		"State":           map[string]any{"Status": "stopped", "Running": false},
	}, nil
}

func (r *Runtime) metadataPath(name string) string {
	return filepath.Join(r.config.EnvironmentDir, name+".json")
}

func (r *Runtime) reload() error {
	_, err := r.run(r.config.SystemctlPath, "daemon-reload")
	return err
}

func (r *Runtime) run(command string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return r.runner.Run(ctx, command, args...)
}

func (r *Runtime) write(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".kitpro-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func validatePlan(plan containers.ContainerPlan) error {
	if !validName(plan.Name) || !validName(plan.Network) || !validImage(plan.Image) {
		return fmt.Errorf("invalid Quadlet plan identity")
	}
	if plan.User != "" {
		parts := strings.Split(plan.User, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid runtime user")
		}
		if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
			return fmt.Errorf("invalid runtime user")
		}
		if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
			return fmt.Errorf("invalid runtime group")
		}
	}
	for _, dependency := range plan.Dependencies {
		if !validName(dependency) {
			return fmt.Errorf("invalid dependency")
		}
	}
	for _, entry := range plan.Environment {
		if !strings.Contains(entry, "=") || strings.ContainsAny(entry, "\x00\r\n") {
			return fmt.Errorf("invalid environment entry")
		}
	}
	for _, mount := range plan.Storage {
		if !filepath.IsAbs(mount.HostPath) || !filepath.IsAbs(mount.ContainerPath) || strings.ContainsAny(mount.HostPath+mount.ContainerPath, "\x00\r\n") {
			return fmt.Errorf("invalid storage mount")
		}
	}
	for key, bindings := range plan.PortBindings {
		parts := strings.Split(key, "/")
		if len(parts) != 2 || (parts[1] != "tcp" && parts[1] != "udp") {
			return fmt.Errorf("invalid port binding")
		}
		for _, binding := range bindings {
			if binding.HostIP == "" || binding.HostPort == "" {
				return fmt.Errorf("wildcard port binding denied")
			}
		}
	}
	return nil
}

func validName(value string) bool { return resourceName.MatchString(value) }

func publishAddress(value string) string {
	if strings.Contains(value, ":") && !strings.HasPrefix(value, "[") {
		return "[" + value + "]"
	}
	return value
}

func validImage(value string) bool {
	parts := strings.Split(value, "@sha256:")
	if len(parts) != 2 || len(parts[1]) != 64 || (!strings.HasPrefix(parts[0], "docker.io/") && !strings.HasPrefix(parts[0], "ghcr.io/") && !strings.HasPrefix(parts[0], "codeberg.org/")) {
		return false
	}
	for _, ch := range parts[1] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func writeLabels(b *strings.Builder, labels map[string]string) {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString("Label=")
		b.WriteString(quote(key + "=" + labels[key]))
		b.WriteByte('\n')
	}
}

func quote(value string) string { return strconv.Quote(value) }

func containerService(name string) string { return name + ".service" }
func networkService(name string) string   { return name + "-network.service" }

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringMapAny(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func stringSliceAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
