package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const MaxFrame = 64 * 1024

type Request struct {
	Version           int    `json:"version"`
	ID                string `json:"request_id"`
	OperationID       string `json:"operation_id,omitempty"`
	Operation         string `json:"operation"`
	OperationRevision int    `json:"operation_revision,omitempty"`
	Deadline          string `json:"deadline,omitempty"`
	// InstanceID is the stable installed-application identity; runtime
	// incarnations are distinguished by RuntimeGeneration.
	InstanceID        string           `json:"instance_id"`
	RuntimeGeneration int              `json:"runtime_generation,omitempty"`
	Image             string           `json:"image,omitempty"`
	ApplicationID     string           `json:"application_id,omitempty"`
	ReleaseID         string           `json:"release_id,omitempty"`
	NetworkName       string           `json:"network_name,omitempty"`
	DataPath          string           `json:"data_path,omitempty"`
	RestartPolicy     string           `json:"restart_policy,omitempty"`
	Command           []string         `json:"command,omitempty"`
	Environment       []EnvVar         `json:"environment,omitempty"`
	Storage           []StorageMount   `json:"storage,omitempty"`
	Bindings          []ServiceBinding `json:"bindings,omitempty"`
	// Deprecated scalar exposure fields are accepted only so the helper can
	// reject mixed/legacy mutation requests explicitly. Bindings is the sole
	// authority for protocol-v2 application plans.
	ExposureMode    string                   `json:"exposure_mode,omitempty"`
	HostAddress     string                   `json:"host_address,omitempty"`
	HostPort        int                      `json:"host_port,omitempty"`
	ServiceID       string                   `json:"service_id,omitempty"`
	ContainerPort   int                      `json:"container_port,omitempty"`
	ServiceProtocol string                   `json:"service_protocol,omitempty"`
	Services        []Service                `json:"services,omitempty"`
	Components      []Component              `json:"components,omitempty"`
	Hardware        []HardwareRequirement    `json:"hardware,omitempty"`
	ExternalStorage []ExternalStorageBinding `json:"external_storage,omitempty"`
	RootID          string                   `json:"root_id,omitempty"`
	RootName        string                   `json:"root_name,omitempty"`
	RootPath        string                   `json:"root_path,omitempty"`
	RootMode        string                   `json:"root_mode,omitempty"`
	RunAs           *RuntimeIdentity         `json:"run_as,omitempty"`
	BackupID        string                   `json:"backup_id,omitempty"`
	RepairAction    string                   `json:"repair_action,omitempty"`
	CredentialID    string                   `json:"credential_id,omitempty"`
}
type EnvVar struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Generate string `json:"generate,omitempty"`
}
type StorageMount struct {
	ID            string `json:"id"`
	ContainerPath string `json:"container_path"`
	HostPath      string `json:"host_path"`
	ReadOnly      bool   `json:"read_only,omitempty"`
	OwnerUID      int    `json:"owner_uid,omitempty"`
	OwnerGID      int    `json:"owner_gid,omitempty"`
}
type RuntimeIdentity struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}
type ExternalStorageBinding struct {
	SlotID string `json:"slot_id"`
	RootID string `json:"root_id"`
}
type Service struct {
	ID            string `json:"id"`
	Protocol      string `json:"protocol"`
	ContainerPort int    `json:"container_port"`
}
type ServiceBinding struct {
	ServiceID     string `json:"service_id"`
	Transport     string `json:"transport"`
	ContainerPort int    `json:"container_port"`
	Mode          string `json:"mode"`
	HostAddress   string `json:"host_address,omitempty"`
	HostPort      int    `json:"host_port,omitempty"`
}
type Component struct {
	ID              string                   `json:"id"`
	Image           string                   `json:"image"`
	Command         []string                 `json:"command,omitempty"`
	Environment     []EnvVar                 `json:"environment,omitempty"`
	Storage         []StorageMount           `json:"storage,omitempty"`
	Services        []Service                `json:"services,omitempty"`
	DependsOn       []string                 `json:"depends_on,omitempty"`
	Restart         string                   `json:"restart,omitempty"`
	Hardware        []HardwareRequirement    `json:"hardware,omitempty"`
	ExternalStorage []ExternalStorageBinding `json:"external_storage,omitempty"`
	RunAs           *RuntimeIdentity         `json:"run_as,omitempty"`
}
type HardwareRequirement struct {
	Class       string `json:"class"`
	Optional    bool   `json:"optional,omitempty"`
	CPUFallback bool   `json:"cpu_fallback,omitempty"`
}
type Response struct {
	OK                bool   `json:"ok"`
	RequestID         string `json:"request_id,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	State             string `json:"state,omitempty"`
	Phase             string `json:"phase,omitempty"`
	ActiveOperationID string `json:"active_operation_id,omitempty"`
	Result            any    `json:"result,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	Error             string `json:"error,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
}

// CredentialDisclosure is returned only by the explicit reveal operation.
// It must never be persisted in operation state, logged, or embedded in normal
// application resources.
type CredentialDisclosure struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Username string `json:"username,omitempty"`
	Value    string `json:"value"`
}

// SemanticOperationID returns the durable mutation identity. The request ID
// fallback keeps protocol-v1 read paths and focused handler tests compatible;
// protocol-v2 mutations are required to provide OperationID explicitly.
func (r Request) SemanticOperationID() string {
	if r.OperationID != "" {
		return r.OperationID
	}
	return r.ID
}

// Canonical returns the bounded semantic request representation used for
// idempotency. Transport request IDs, semantic operation IDs, deadlines, and
// generated secret values cannot change the hash. JSON struct encoding fixes
// field order, while omitempty makes nil and empty collections equivalent.
// Slice order is retained because command, component, dependency, and manifest
// order participate in the existing trusted-plan contract.
func Canonical(r Request) ([]byte, error) {
	r.ID = ""
	r.OperationID = ""
	r.Deadline = ""
	for i := range r.Environment {
		if r.Environment[i].Secret {
			r.Environment[i].Value = ""
		}
	}
	for i := range r.Components {
		for j := range r.Components[i].Environment {
			if r.Components[i].Environment[j].Secret {
				r.Components[i].Environment[j].Value = ""
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFrame {
		return nil, errors.New("canonical request too large")
	}
	return b, nil
}

func Hash(r Request) (string, error) {
	b, e := Canonical(r)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func Read(r io.Reader) (Request, error) {
	var n uint32
	if e := binary.Read(r, binary.BigEndian, &n); e != nil {
		return Request{}, e
	}
	if n == 0 || n > MaxFrame {
		return Request{}, errors.New("invalid frame length")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(r, b); e != nil {
		return Request{}, e
	}
	if err := rejectDuplicateKeys(b); err != nil {
		return Request{}, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var q Request
	if e := d.Decode(&q); e != nil {
		return q, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return q, errors.New("trailing JSON")
	}
	return q, nil
}

func rejectDuplicateKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := readUniqueValue(d); err != nil {
		return err
	}
	if token, err := d.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing JSON token %v", token)
	}
	return nil
}

func readUniqueValue(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := readUniqueValue(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid object termination")
		}
	case '[':
		for d.More() {
			if err := readUniqueValue(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid array termination")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func ReadResponse(r io.Reader) (Response, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return Response{}, err
	}
	if n == 0 || n > MaxFrame {
		return Response{}, errors.New("invalid frame length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Response{}, err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	var v Response
	if err := d.Decode(&v); err != nil {
		return v, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return v, errors.New("trailing JSON")
	}
	return v, nil
}
func Write(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > MaxFrame {
		return errors.New("response too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, e = w.Write(h[:]); e != nil {
		return e
	}
	_, e = w.Write(b)
	return e
}
