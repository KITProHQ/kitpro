package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const MaxFrame = 64 * 1024

type Request struct {
	Version   int    `json:"version"`
	ID        string `json:"request_id"`
	Operation string `json:"operation"`
	// InstanceID is the stable installed-application identity; runtime
	// incarnations are distinguished by RuntimeGeneration.
	InstanceID        string                   `json:"instance_id"`
	RuntimeGeneration int                      `json:"runtime_generation,omitempty"`
	Image             string                   `json:"image,omitempty"`
	ApplicationID     string                   `json:"application_id,omitempty"`
	ReleaseID         string                   `json:"release_id,omitempty"`
	NetworkName       string                   `json:"network_name,omitempty"`
	DataPath          string                   `json:"data_path,omitempty"`
	RestartPolicy     string                   `json:"restart_policy,omitempty"`
	Command           []string                 `json:"command,omitempty"`
	Environment       []EnvVar                 `json:"environment,omitempty"`
	Storage           []StorageMount           `json:"storage,omitempty"`
	ExposureMode      string                   `json:"exposure_mode,omitempty"`
	HostAddress       string                   `json:"host_address,omitempty"`
	HostPort          int                      `json:"host_port,omitempty"`
	ServiceID         string                   `json:"service_id,omitempty"`
	ContainerPort     int                      `json:"container_port,omitempty"`
	ServiceProtocol   string                   `json:"service_protocol,omitempty"`
	Services          []Service                `json:"services,omitempty"`
	Components        []Component              `json:"components,omitempty"`
	Hardware          []HardwareRequirement    `json:"hardware,omitempty"`
	ExternalStorage   []ExternalStorageBinding `json:"external_storage,omitempty"`
	RootID            string                   `json:"root_id,omitempty"`
	RootName          string                   `json:"root_name,omitempty"`
	RootPath          string                   `json:"root_path,omitempty"`
	RootMode          string                   `json:"root_mode,omitempty"`
	RunAs             *RuntimeIdentity         `json:"run_as,omitempty"`
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
	OK        bool   `json:"ok"`
	RequestID string `json:"request_id,omitempty"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

func Hash(r Request) (string, error) {
	b, e := json.Marshal(r)
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
	d := json.NewDecoder(strings.NewReader(string(b)))
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
