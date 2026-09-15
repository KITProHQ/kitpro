package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/hardware"
	"regexp"
	"strings"
)

const SchemaVersion = 1
const MultiContainerSchemaVersion = 2
const HardwareSchemaVersion = 3
const ExternalStorageSchemaVersion = 4
const RuntimeIdentitySchemaVersion = 5

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Manifest struct {
	SchemaVersion   int               `json:"schema_version"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	Releases        []Release         `json:"releases"`
	Storage         []Storage         `json:"storage,omitempty"`
	Environment     []Env             `json:"environment,omitempty"`
	Command         []string          `json:"command,omitempty"`
	Restart         string            `json:"restart,omitempty"`
	Services        []Service         `json:"services,omitempty"`
	Components      []Component       `json:"components,omitempty"`
	Hardware        []Accelerator     `json:"hardware,omitempty"`
	ExternalStorage []ExternalStorage `json:"external_storage,omitempty"`
	RunAs           *RuntimeIdentity  `json:"run_as,omitempty"`
}
type Release struct {
	Version    string `json:"version"`
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
	Platform   string `json:"platform"`
}
type Storage struct {
	ID            string `json:"id"`
	ContainerPath string `json:"container_path"`
	Persistent    bool   `json:"persistent"`
	ReadOnly      bool   `json:"read_only"`
	OwnerUID      int    `json:"owner_uid,omitempty"`
	OwnerGID      int    `json:"owner_gid,omitempty"`
}

// RuntimeIdentity is a trusted numeric primary identity. Supplementary groups,
// host-name lookup, and user-supplied identities are intentionally absent.
type RuntimeIdentity struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}

// ExternalStorage declares a logical imported-data slot. Host paths and bind
// options are deliberately absent and are resolved by the privileged helper.
type ExternalStorage struct {
	ID            string `json:"id"`
	ContainerPath string `json:"container_path"`
	Mode          string `json:"mode"`
	Required      bool   `json:"required,omitempty"`
	Purpose       string `json:"purpose"`
}
type Env struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Generate string `json:"generate,omitempty"`
	Required bool   `json:"required,omitempty"`
}
type Service struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	ContainerPort int    `json:"container_port"`
}
type Component struct {
	ID              string            `json:"id"`
	Release         string            `json:"release"`
	Storage         []Storage         `json:"storage,omitempty"`
	Environment     []Env             `json:"environment,omitempty"`
	Command         []string          `json:"command,omitempty"`
	Services        []Service         `json:"services,omitempty"`
	DependsOn       []string          `json:"depends_on,omitempty"`
	Restart         string            `json:"restart,omitempty"`
	Hardware        []Accelerator     `json:"hardware,omitempty"`
	ExternalStorage []ExternalStorage `json:"external_storage,omitempty"`
	RunAs           *RuntimeIdentity  `json:"run_as,omitempty"`
}

// Accelerator names a helper-owned device class. Host paths, groups, Docker
// runtime arguments, and capabilities are deliberately not representable.
type Accelerator struct {
	Class       string `json:"class"`
	Optional    bool   `json:"optional,omitempty"`
	CPUFallback bool   `json:"cpu_fallback,omitempty"`
}
type Plan struct {
	ApplicationID, ReleaseID, InstanceID, ImageDigest, NetworkName, DataPath, Restart string
	Command                                                                           []string
	Environment                                                                       []Env
	Storage                                                                           []Storage
	Services                                                                          []Service
	Components                                                                        []Component
	ResolvedComponents                                                                []ResolvedComponent
	Hardware                                                                          []Accelerator
	ExternalStorage                                                                   []ExternalStorage
	RunAs                                                                             *RuntimeIdentity
}

// ResolvedComponent is the helper-facing, digest-pinned component plan. It is
// intentionally explicit so a catalog cannot smuggle arbitrary Docker fields.
type ResolvedComponent struct {
	ID              string
	ImageDigest     string
	Command         []string
	Environment     []Env
	Storage         []Storage
	Services        []Service
	DependsOn       []string
	Restart         string
	Hardware        []Accelerator
	ExternalStorage []ExternalStorage
	RunAs           *RuntimeIdentity
}

func Parse(data []byte) (Manifest, error) {
	if len(data) > 64*1024 {
		return Manifest{}, fmt.Errorf("manifest exceeds 64 KiB")
	}
	if err := uniqueJSON(data); err != nil {
		return Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest JSON: %w", err)
	}
	if err := Validate(m); err != nil {
		return m, err
	}
	return m, nil
}
func Validate(m Manifest) error {
	if m.SchemaVersion != SchemaVersion && m.SchemaVersion != MultiContainerSchemaVersion && m.SchemaVersion != HardwareSchemaVersion && m.SchemaVersion != ExternalStorageSchemaVersion && m.SchemaVersion != RuntimeIdentitySchemaVersion {
		return fmt.Errorf("unsupported manifest schema version %d", m.SchemaVersion)
	}
	if m.SchemaVersion == SchemaVersion && len(m.Components) != 0 {
		return fmt.Errorf("components require schema version 2")
	}
	if m.SchemaVersion == MultiContainerSchemaVersion && len(m.Components) < 2 {
		return fmt.Errorf("multi-container manifest requires at least two components")
	}
	if m.SchemaVersion == HardwareSchemaVersion && len(m.Components) == 1 {
		return fmt.Errorf("multi-container manifest requires at least two components")
	}
	if m.SchemaVersion < HardwareSchemaVersion && (len(m.Hardware) != 0 || componentsHaveHardware(m.Components)) {
		return fmt.Errorf("hardware requires schema version 3")
	}
	if m.SchemaVersion < ExternalStorageSchemaVersion && (len(m.ExternalStorage) != 0 || componentsHaveExternalStorage(m.Components)) {
		return fmt.Errorf("external storage requires schema version 4")
	}
	if m.SchemaVersion < RuntimeIdentitySchemaVersion && (m.RunAs != nil || storageHasOwnership(m.Storage) || componentsHaveRuntimeIdentity(m.Components)) {
		return fmt.Errorf("runtime identity requires schema version 5")
	}
	if err := validateRuntimeIdentity(m.RunAs); err != nil {
		return err
	}
	if err := validateExternalStorage(m.ExternalStorage, m.Storage); err != nil {
		return err
	}
	if err := validateHardware(m.Hardware); err != nil {
		return err
	}
	if !idPattern.MatchString(m.ID) || len(m.Name) == 0 || len(m.Name) > 128 || len(m.Description) > 280 || strings.ContainsAny(m.Description, "\r\n") {
		return fmt.Errorf("invalid application identity")
	}
	if len(m.Releases) == 0 || len(m.Releases) > 16 {
		return fmt.Errorf("release list invalid")
	}
	seen := map[string]bool{}
	for _, r := range m.Releases {
		if r.Version == "" || seen[r.Version] {
			return fmt.Errorf("duplicate/invalid release version")
		}
		seen[r.Version] = true
		if r.Registry != "docker.io" && r.Registry != "ghcr.io" {
			return fmt.Errorf("registry not allowed")
		}
		if strings.ContainsAny(r.Repository, " \t\n") || !digestPattern.MatchString(r.Digest) || r.Platform != "linux/amd64" {
			return fmt.Errorf("invalid release image")
		}
	}
	seen = map[string]bool{}
	for _, s := range m.Storage {
		if !idPattern.MatchString(s.ID) || seen[s.ID] || !strings.HasPrefix(s.ContainerPath, "/") || strings.Contains(s.ContainerPath, "..") || s.ContainerPath == "/" || strings.HasPrefix(s.ContainerPath, "/proc") || strings.HasPrefix(s.ContainerPath, "/sys") || strings.HasPrefix(s.ContainerPath, "/dev") || strings.HasPrefix(s.ContainerPath, "/etc") || strings.Contains(s.ContainerPath, "docker.sock") {
			return fmt.Errorf("invalid storage declaration")
		}
		seen[s.ID] = true
		if err := validateStorageOwnership(s); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, e := range m.Environment {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`).MatchString(e.Name) || seen[e.Name] || (e.Required && e.Value != "") {
			return fmt.Errorf("invalid environment declaration")
		}
		seen[e.Name] = true
		if e.Secret && e.Value != "" {
			return fmt.Errorf("secret values may not be embedded")
		}
		if (e.Generate != "" && (!e.Secret || !e.Required)) || (e.Generate != "" && e.Generate != "random-hex-32") {
			return fmt.Errorf("invalid generated secret declaration")
		}
	}
	if m.Restart != "" && m.Restart != "no" && m.Restart != "unless-stopped" {
		return fmt.Errorf("invalid restart policy")
	}
	if len(m.Command) > 16 {
		return fmt.Errorf("command too long")
	}
	for _, c := range m.Command {
		if c == "" || len(c) > 256 || strings.ContainsAny(c, "\r\n") {
			return fmt.Errorf("invalid command")
		}
	}
	if len(m.Services) > 8 {
		return fmt.Errorf("too many services")
	}
	seen = map[string]bool{}
	seenPort := map[string]bool{}
	for _, s := range m.Services {
		portProtocol := fmt.Sprintf("%d/%s", s.ContainerPort, s.Protocol)
		if !idPattern.MatchString(s.ID) || s.Name == "" || len(s.Name) > 128 || seen[s.ID] || seenPort[portProtocol] || s.ContainerPort < 1 || s.ContainerPort > 65535 || (s.Protocol != "http" && s.Protocol != "https" && s.Protocol != "tcp") {
			return fmt.Errorf("invalid port")
		}
		seen[s.ID] = true
		seenPort[portProtocol] = true
	}
	if len(m.Components) > 16 {
		return fmt.Errorf("too many components")
	}
	componentIDs := map[string]bool{}
	releaseIDs := map[string]bool{}
	for _, release := range m.Releases {
		releaseIDs[release.Version] = true
	}
	for _, c := range m.Components {
		if !idPattern.MatchString(c.ID) || componentIDs[c.ID] || c.Release == "" || len(c.Release) > 64 || !releaseIDs[c.Release] {
			return fmt.Errorf("invalid component declaration")
		}
		componentIDs[c.ID] = true
		if c.Restart != "" && c.Restart != "no" && c.Restart != "unless-stopped" {
			return fmt.Errorf("invalid component restart policy")
		}
		if err := validateComponentFields(c); err != nil {
			return fmt.Errorf("component %s: %w", c.ID, err)
		}
		if err := validateHardware(c.Hardware); err != nil {
			return fmt.Errorf("component %s: %w", c.ID, err)
		}
		if err := validateExternalStorage(c.ExternalStorage, c.Storage); err != nil {
			return fmt.Errorf("component %s: %w", c.ID, err)
		}
		if err := validateRuntimeIdentity(c.RunAs); err != nil {
			return fmt.Errorf("component %s: %w", c.ID, err)
		}
	}
	for _, c := range m.Components {
		for _, dep := range c.DependsOn {
			if !componentIDs[dep] {
				return fmt.Errorf("component dependency not found")
			}
		}
	}
	return nil
}

func validateExternalStorage(items []ExternalStorage, managed []Storage) error {
	if len(items) > 8 {
		return fmt.Errorf("too many external storage slots")
	}
	seen := map[string]bool{}
	targets := map[string]bool{}
	for _, item := range managed {
		seen[item.ID] = true
		targets[item.ContainerPath] = true
	}
	for _, item := range items {
		if !idPattern.MatchString(item.ID) || seen[item.ID] || targets[item.ContainerPath] || (item.Mode != "read-only" && item.Mode != "read-write") || !safeContainerPath(item.ContainerPath) || item.Purpose == "" || len(item.Purpose) > 80 || strings.ContainsAny(item.Purpose, "\r\n") {
			return fmt.Errorf("invalid external storage declaration")
		}
		seen[item.ID] = true
		targets[item.ContainerPath] = true
	}
	return nil
}

func safeContainerPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.Contains(path, "..") && path != "/" && !strings.HasPrefix(path, "/proc") && !strings.HasPrefix(path, "/sys") && !strings.HasPrefix(path, "/dev") && !strings.HasPrefix(path, "/etc") && !strings.Contains(path, "docker.sock")
}

func componentsHaveExternalStorage(components []Component) bool {
	for _, component := range components {
		if len(component.ExternalStorage) != 0 {
			return true
		}
	}
	return false
}

func validateHardware(items []Accelerator) error {
	if len(items) > 4 {
		return fmt.Errorf("too many hardware requirements")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !hardware.ValidClass(item.Class) || seen[item.Class] {
			return fmt.Errorf("unknown or duplicate device class %q", item.Class)
		}
		if item.CPUFallback && !item.Optional {
			return fmt.Errorf("CPU fallback requires optional acceleration")
		}
		seen[item.Class] = true
	}
	return nil
}

func componentsHaveHardware(components []Component) bool {
	for _, component := range components {
		if len(component.Hardware) != 0 {
			return true
		}
	}
	return false
}

func validateComponentFields(c Component) error {
	seen := map[string]bool{}
	for _, s := range c.Storage {
		if !idPattern.MatchString(s.ID) || seen[s.ID] || !strings.HasPrefix(s.ContainerPath, "/") || strings.Contains(s.ContainerPath, "..") || s.ContainerPath == "/" || strings.HasPrefix(s.ContainerPath, "/proc") || strings.HasPrefix(s.ContainerPath, "/sys") || strings.HasPrefix(s.ContainerPath, "/dev") || strings.HasPrefix(s.ContainerPath, "/etc") || strings.Contains(s.ContainerPath, "docker.sock") {
			return fmt.Errorf("invalid storage declaration")
		}
		seen[s.ID] = true
		if err := validateStorageOwnership(s); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, e := range c.Environment {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`).MatchString(e.Name) || seen[e.Name] || (e.Required && e.Value != "") || (e.Secret && e.Value != "") {
			return fmt.Errorf("invalid environment declaration")
		}
		if (e.Generate != "" && (!e.Secret || !e.Required)) || (e.Generate != "" && e.Generate != "random-hex-32") {
			return fmt.Errorf("invalid generated secret declaration")
		}
		seen[e.Name] = true
	}
	if len(c.Command) > 16 || len(c.Services) > 8 {
		return fmt.Errorf("component declaration too large")
	}
	for _, arg := range c.Command {
		if arg == "" || len(arg) > 256 || strings.ContainsAny(arg, "\r\n") || arg == "/bin/sh" || arg == "/bin/bash" || arg == "-c" {
			return fmt.Errorf("invalid command")
		}
	}
	seen = map[string]bool{}
	ports := map[string]bool{}
	for _, s := range c.Services {
		key := fmt.Sprintf("%d/%s", s.ContainerPort, s.Protocol)
		if !idPattern.MatchString(s.ID) || s.Name == "" || len(s.Name) > 128 || seen[s.ID] || ports[key] || s.ContainerPort < 1 || s.ContainerPort > 65535 || (s.Protocol != "http" && s.Protocol != "https" && s.Protocol != "tcp") {
			return fmt.Errorf("invalid service")
		}
		seen[s.ID], ports[key] = true, true
	}
	return nil
}

func validateRuntimeIdentity(identity *RuntimeIdentity) error {
	if identity == nil {
		return nil
	}
	if identity.UID < 1 || identity.UID > 65535 || identity.GID < 1 || identity.GID > 65535 {
		return fmt.Errorf("runtime identity outside policy")
	}
	return nil
}

func validateStorageOwnership(storage Storage) error {
	if (storage.OwnerUID == 0) != (storage.OwnerGID == 0) {
		return fmt.Errorf("managed storage owner requires uid and gid")
	}
	if storage.OwnerUID < 0 || storage.OwnerUID > 65535 || storage.OwnerGID < 0 || storage.OwnerGID > 65535 {
		return fmt.Errorf("managed storage owner outside policy")
	}
	return nil
}

func storageHasOwnership(items []Storage) bool {
	for _, item := range items {
		if item.OwnerUID != 0 {
			return true
		}
	}
	return false
}

func componentsHaveRuntimeIdentity(components []Component) bool {
	for _, component := range components {
		if component.RunAs != nil || storageHasOwnership(component.Storage) {
			return true
		}
	}
	return false
}
func Resolve(m Manifest, release string, instance string, network string, dataPath string) (Plan, error) {
	if err := Validate(m); err != nil {
		return Plan{}, err
	}
	var r Release
	for _, x := range m.Releases {
		if x.Version == release {
			r = x
		}
	}
	if r.Version == "" {
		return Plan{}, fmt.Errorf("release not found")
	}
	if !idPattern.MatchString(instance) || network == "" || strings.ContainsAny(network, "/.\\") || !strings.HasPrefix(dataPath, "/srv/kitpro/apps/") || strings.Contains(dataPath, "..") || strings.ContainsAny(dataPath, "\\\r\n") {
		return Plan{}, fmt.Errorf("invalid instance paths")
	}
	p := Plan{ApplicationID: m.ID, ReleaseID: r.Version, InstanceID: instance, ImageDigest: r.Registry + "/" + r.Repository + "@" + r.Digest, NetworkName: network, DataPath: dataPath, Restart: m.Restart, Command: append([]string(nil), m.Command...), Environment: append([]Env(nil), m.Environment...), Storage: append([]Storage(nil), m.Storage...), Services: append([]Service(nil), m.Services...), Components: append([]Component(nil), m.Components...), Hardware: append([]Accelerator(nil), m.Hardware...), ExternalStorage: append([]ExternalStorage(nil), m.ExternalStorage...), RunAs: m.RunAs}
	for _, c := range m.Components {
		var cr Release
		for _, candidate := range m.Releases {
			if candidate.Version == c.Release {
				cr = candidate
				break
			}
		}
		resolved := ResolvedComponent{ID: c.ID, ImageDigest: cr.Registry + "/" + cr.Repository + "@" + cr.Digest, Command: append([]string(nil), c.Command...), Environment: append([]Env(nil), c.Environment...), Storage: append([]Storage(nil), c.Storage...), Services: append([]Service(nil), c.Services...), DependsOn: append([]string(nil), c.DependsOn...), Restart: c.Restart, Hardware: append([]Accelerator(nil), c.Hardware...), ExternalStorage: append([]ExternalStorage(nil), c.ExternalStorage...), RunAs: c.RunAs}
		p.ResolvedComponents = append(p.ResolvedComponents, resolved)
	}
	return p, nil
}
func (p Plan) Hash() string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func uniqueJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSON(dec); err != nil {
		return err
	}
	var extra any
	if dec.Decode(&extra) == nil {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := tok.(type) {
	case json.Delim:
		if d == '{' {
			seen := map[string]bool{}
			for dec.More() {
				k, e := dec.Token()
				if e != nil {
					return e
				}
				key := k.(string)
				if seen[key] {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[key] = true
				if e = walkJSON(dec); e != nil {
					return e
				}
			}
			_, err = dec.Token()
			return err
		}
		if d == '[' {
			for dec.More() {
				if err := walkJSON(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
	}
	return nil
}
