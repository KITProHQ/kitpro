package ingress

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

type ExposureMode string

const (
	LAN              ExposureMode = "lan"
	ManagedHTTPS     ExposureMode = "managed-https"
	CloudflareTunnel ExposureMode = "cloudflare-tunnel"
)

type TLSPolicy string

const (
	TLSNone       TLSPolicy = "none"
	TLSProvided   TLSPolicy = "provided"
	TLSACME       TLSPolicy = "acme"
	TLSCloudflare TLSPolicy = "cloudflare"
)

type DesiredState string

const (
	Disabled DesiredState = "disabled"
	Enabled  DesiredState = "enabled"
)

type Route struct {
	ID                string       `json:"route_id"`
	InstallationID    string       `json:"installation_id"`
	ServiceID         string       `json:"service_id"`
	Hostname          string       `json:"hostname"`
	ExposureMode      ExposureMode `json:"exposure_mode"`
	UpstreamProtocol  string       `json:"upstream_protocol"`
	TLSPolicy         TLSPolicy    `json:"tls_policy"`
	CertificateID     string       `json:"certificate_id,omitempty"`
	ProviderID        string       `json:"provider_id,omitempty"`
	DesiredState      DesiredState `json:"desired_state"`
	AppliedGeneration int64        `json:"applied_generation"`
	CreatedAt         string       `json:"created_at"`
	UpdatedAt         string       `json:"updated_at"`
}

type Backend struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type AppliedRoute struct {
	Route
	Backend Backend `json:"backend"`
}

var (
	identityPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	routePattern       = regexp.MustCompile(`^route-[a-f0-9]{16}$`)
	certificatePattern = regexp.MustCompile(`^cert-[a-f0-9]{16}$`)
	providerPattern    = regexp.MustCompile(`^provider-[a-f0-9]{16}$`)
	labelPattern       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

func NewID(prefix string) (string, error) {
	if prefix != "route" && prefix != "cert" && prefix != "provider" && prefix != "bundle" {
		return "", fmt.Errorf("unsupported identity prefix")
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(raw), nil
}

// NormalizeHostname accepts DNS ASCII, including already encoded IDNA labels.
// Unicode input is rejected so the comparison form never differs from storage.
func NormalizeHostname(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil || strings.ContainsAny(value, "_/:\\\x00\r\n\t ") {
		return "", fmt.Errorf("invalid hostname")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("hostname must contain at least two labels")
	}
	for _, label := range labels {
		if !labelPattern.MatchString(label) {
			return "", fmt.Errorf("invalid hostname")
		}
	}
	return value, nil
}

func ValidateRoute(route Route, allowedModes []string, externalAllowed bool) error {
	hostname, err := NormalizeHostname(route.Hostname)
	if err != nil || hostname != route.Hostname || !routePattern.MatchString(route.ID) || !identityPattern.MatchString(route.InstallationID) || !identityPattern.MatchString(route.ServiceID) {
		return fmt.Errorf("invalid route identity")
	}
	if route.UpstreamProtocol != "http" && route.UpstreamProtocol != "https" {
		return fmt.Errorf("invalid upstream protocol")
	}
	allowed := false
	for _, mode := range allowedModes {
		allowed = allowed || string(route.ExposureMode) == mode
	}
	if !allowed {
		return fmt.Errorf("exposure mode is not allowed by the catalog")
	}
	if route.DesiredState != Disabled && route.DesiredState != Enabled {
		return fmt.Errorf("invalid desired state")
	}
	switch route.ExposureMode {
	case LAN:
		if route.TLSPolicy != TLSNone && route.TLSPolicy != TLSProvided {
			return fmt.Errorf("invalid LAN TLS policy")
		}
	case ManagedHTTPS:
		if route.TLSPolicy != TLSProvided && route.TLSPolicy != TLSACME {
			return fmt.Errorf("managed HTTPS requires a certificate")
		}
	case CloudflareTunnel:
		if !externalAllowed || route.TLSPolicy != TLSCloudflare || !providerPattern.MatchString(route.ProviderID) {
			return fmt.Errorf("invalid Cloudflare route")
		}
	default:
		return fmt.Errorf("invalid exposure mode")
	}
	if route.TLSPolicy == TLSProvided || route.TLSPolicy == TLSACME {
		if !certificatePattern.MatchString(route.CertificateID) {
			return fmt.Errorf("certificate is required")
		}
	} else if route.CertificateID != "" {
		return fmt.Errorf("certificate is not allowed")
	}
	if route.ExposureMode != CloudflareTunnel && route.ProviderID != "" {
		return fmt.Errorf("provider is not allowed")
	}
	return nil
}

func ValidateBackend(backend Backend) error {
	ip := net.ParseIP(backend.Address)
	if ip == nil || !ip.IsLoopback() || backend.Port < 1 || backend.Port > 65535 {
		return fmt.Errorf("ingress backend must be an exact loopback binding")
	}
	return nil
}

func NormalizeRoutes(routes []AppliedRoute) ([]AppliedRoute, error) {
	out := append([]AppliedRoute(nil), routes...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hostname == out[j].Hostname {
			return out[i].ID < out[j].ID
		}
		return out[i].Hostname < out[j].Hostname
	})
	seenHosts := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, route := range out {
		if route.DesiredState != Enabled {
			return nil, fmt.Errorf("disabled route included in active generation")
		}
		if err := ValidateRoute(route.Route, []string{string(LAN), string(ManagedHTTPS), string(CloudflareTunnel)}, true); err != nil {
			return nil, err
		}
		if err := ValidateBackend(route.Backend); err != nil {
			return nil, err
		}
		if seenHosts[route.Hostname] || seenIDs[route.ID] {
			return nil, fmt.Errorf("duplicate ingress route")
		}
		seenHosts[route.Hostname] = true
		seenIDs[route.ID] = true
	}
	return out, nil
}
