package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
)

const (
	apiBaseURL    = "https://api.cloudflare.com/client/v4"
	originService = "http://127.0.0.1:18080"
)

var (
	hexIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	uuidPattern  = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-5][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
)

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type Tunnel struct {
	ID        string `json:"id"`
	AccountID string `json:"account_tag"`
	ConfigSrc string `json:"config_src"`
	Status    string `json:"status"`
}

type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Comment string `json:"comment"`
}

func NewClient() Client {
	return Client{BaseURL: apiBaseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c Client) ValidateCredential(ctx context.Context, token, accountID, zoneID, tunnelID string) (Tunnel, error) {
	if !validToken(token) || !hexIDPattern.MatchString(accountID) || !hexIDPattern.MatchString(zoneID) || !uuidPattern.MatchString(tunnelID) {
		return Tunnel{}, &Error{Code: "invalid_configuration"}
	}
	var verification struct {
		Status string `json:"status"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/user/tokens/verify", nil, &verification); err != nil {
		return Tunnel{}, err
	}
	if verification.Status != "active" {
		return Tunnel{}, &Error{Code: "invalid_token"}
	}
	var tunnel Tunnel
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, &tunnel); err != nil {
		return Tunnel{}, permissionError(err)
	}
	if tunnel.ID != tunnelID || tunnel.AccountID != accountID || tunnel.ConfigSrc != "cloudflare" {
		return Tunnel{}, &Error{Code: "tunnel_mismatch"}
	}
	path := "/zones/" + zoneID + "/dns_records?per_page=1"
	var records []DNSRecord
	if err := c.call(ctx, token, http.MethodGet, path, nil, &records); err != nil {
		return Tunnel{}, permissionError(err)
	}
	return tunnel, nil
}

func (c Client) TunnelToken(ctx context.Context, token, accountID, tunnelID string) (string, error) {
	if !validToken(token) || !hexIDPattern.MatchString(accountID) || !uuidPattern.MatchString(tunnelID) {
		return "", &Error{Code: "invalid_configuration"}
	}
	var value string
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/token", nil, &value); err != nil {
		return "", permissionError(err)
	}
	if !validToken(value) {
		return "", &Error{Code: "invalid_tunnel_token"}
	}
	return value, nil
}

func (c Client) AddRoute(ctx context.Context, token, accountID, zoneID, tunnelID, hostname string) (DNSRecord, error) {
	hostname, err := validatedHostname(hostname)
	if err != nil {
		return DNSRecord{}, err
	}
	rules, err := c.configuration(ctx, token, accountID, tunnelID)
	if err != nil {
		return DNSRecord{}, err
	}
	for _, rule := range rules {
		if rule.Hostname == hostname {
			return DNSRecord{}, &Error{Code: "route_conflict"}
		}
	}
	records, err := c.records(ctx, token, zoneID, hostname)
	if err != nil {
		return DNSRecord{}, err
	}
	if len(records) != 0 {
		return DNSRecord{}, &Error{Code: "route_conflict"}
	}
	catchall := IngressRule{Service: "http_status:404"}
	if len(rules) > 0 && rules[len(rules)-1].Hostname == "" {
		catchall = rules[len(rules)-1]
		rules = rules[:len(rules)-1]
	}
	rules = append(rules, IngressRule{Hostname: hostname, Service: originService}, catchall)
	if err = c.putConfiguration(ctx, token, accountID, tunnelID, rules); err != nil {
		return DNSRecord{}, err
	}
	record := DNSRecord{Name: hostname, Type: "CNAME", Content: tunnelID + ".cfargotunnel.com", Comment: "Managed by KITPro Server"}
	if err = c.call(ctx, token, http.MethodPost, "/zones/"+zoneID+"/dns_records", map[string]any{"type": record.Type, "name": record.Name, "content": record.Content, "proxied": true, "ttl": 1, "comment": "Managed by KITPro Server"}, &record); err != nil {
		_ = c.putConfiguration(ctx, token, accountID, tunnelID, removeRule(rules, hostname))
		return DNSRecord{}, err
	}
	if !hexIDPattern.MatchString(record.ID) || record.Name != hostname || record.Type != "CNAME" || record.Content != tunnelID+".cfargotunnel.com" || record.Comment != "Managed by KITPro Server" {
		if hexIDPattern.MatchString(record.ID) {
			_ = c.call(ctx, token, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+record.ID, nil, &struct{}{})
		}
		_ = c.putConfiguration(ctx, token, accountID, tunnelID, removeRule(rules, hostname))
		return DNSRecord{}, &Error{Code: "invalid_provider_response"}
	}
	return record, nil
}

func (c Client) RemoveRoute(ctx context.Context, token, accountID, zoneID, tunnelID, hostname, recordID string) error {
	hostname, err := validatedHostname(hostname)
	if err != nil || !hexIDPattern.MatchString(recordID) {
		return &Error{Code: "invalid_configuration"}
	}
	rules, err := c.configuration(ctx, token, accountID, tunnelID)
	if err != nil {
		return err
	}
	found := false
	for _, rule := range rules {
		if rule.Hostname == hostname {
			if rule.Service != originService {
				return &Error{Code: "route_ownership_conflict"}
			}
			found = true
		}
	}
	if !found {
		return &Error{Code: "route_not_found"}
	}
	if err = c.call(ctx, token, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, &struct{}{}); err != nil {
		return err
	}
	return c.putConfiguration(ctx, token, accountID, tunnelID, removeRule(rules, hostname))
}

func (c Client) DeleteTunnel(ctx context.Context, token, accountID, tunnelID string) error {
	if !validToken(token) || !hexIDPattern.MatchString(accountID) || !uuidPattern.MatchString(tunnelID) {
		return &Error{Code: "invalid_configuration"}
	}
	return c.call(ctx, token, http.MethodDelete, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, &struct{}{})
}

func (c Client) configuration(ctx context.Context, token, accountID, tunnelID string) ([]IngressRule, error) {
	var result struct {
		Config struct {
			Ingress []IngressRule `json:"ingress"`
		} `json:"config"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", nil, &result); err != nil {
		return nil, permissionError(err)
	}
	return result.Config.Ingress, nil
}

func (c Client) putConfiguration(ctx context.Context, token, accountID, tunnelID string, rules []IngressRule) error {
	if len(rules) == 0 || rules[len(rules)-1].Hostname != "" {
		return &Error{Code: "invalid_tunnel_configuration"}
	}
	return c.call(ctx, token, http.MethodPut, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", map[string]any{"config": map[string]any{"ingress": rules}}, &struct{}{})
}

func (c Client) records(ctx context.Context, token, zoneID, hostname string) ([]DNSRecord, error) {
	if !hexIDPattern.MatchString(zoneID) {
		return nil, &Error{Code: "invalid_configuration"}
	}
	query := url.Values{"name": {hostname}, "type": {"CNAME"}, "per_page": {"100"}}
	var records []DNSRecord
	if err := c.call(ctx, token, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+query.Encode(), nil, &records); err != nil {
		return nil, permissionError(err)
	}
	return records, nil
}

func (c Client) call(ctx context.Context, token, method, path string, body any, result any) error {
	if !validToken(token) || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n") {
		return &Error{Code: "invalid_configuration"}
	}
	base := c.BaseURL
	if base == "" {
		base = apiBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return &Error{Code: "invalid_configuration"}
	}
	var reader io.Reader
	if body != nil {
		encoded, encodeErr := json.Marshal(body)
		if encodeErr != nil || len(encoded) > 128*1024 {
			return &Error{Code: "invalid_configuration"}
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, reader)
	if err != nil {
		return &Error{Code: "request_failed"}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return &Error{Code: "api_unavailable"}
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 512*1024)
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err = json.NewDecoder(limited).Decode(&envelope); err != nil {
		return &Error{Code: "invalid_provider_response"}
	}
	if response.StatusCode == http.StatusUnauthorized {
		return &Error{Code: "invalid_token"}
	}
	if response.StatusCode == http.StatusForbidden {
		return &Error{Code: "insufficient_permissions"}
	}
	if response.StatusCode >= 500 {
		return &Error{Code: "api_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return &Error{Code: "provider_rejected"}
	}
	if result != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err = json.Unmarshal(envelope.Result, result); err != nil {
			return &Error{Code: "invalid_provider_response"}
		}
	}
	return nil
}

func validToken(value string) bool {
	return len(value) >= 20 && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func validatedHostname(value string) (string, error) {
	hostname, err := ingress.NormalizeHostname(value)
	if err != nil || hostname != value {
		return "", &Error{Code: "invalid_hostname"}
	}
	return hostname, nil
}

func permissionError(err error) error {
	var providerError *Error
	if errors.As(err, &providerError) && providerError.Code == "invalid_token" {
		return err
	}
	return err
}

func removeRule(rules []IngressRule, hostname string) []IngressRule {
	out := make([]IngressRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Hostname != hostname {
			out = append(out, rule)
		}
	}
	return out
}

func OriginService() string { return originService }
