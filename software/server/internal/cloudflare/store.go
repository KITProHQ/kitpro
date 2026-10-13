package cloudflare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var providerIDPattern = regexp.MustCompile(`^provider-[a-f0-9]{16}$`)

type ProviderConfig struct {
	ProviderID   string            `json:"provider_id"`
	AccountID    string            `json:"account_id"`
	ZoneID       string            `json:"zone_id"`
	TunnelID     string            `json:"tunnel_id"`
	Enabled      bool              `json:"enabled"`
	Routes       map[string]string `json:"routes"`
	APITokenHash string            `json:"api_token_sha256"`
	UpdatedAt    string            `json:"updated_at"`
}

type CredentialStore struct{ Root string }

func (s CredentialStore) Install(config ProviderConfig, apiToken, tunnelToken string) error {
	if err := validateProviderConfig(config); err != nil || !validToken(apiToken) || !validToken(tunnelToken) {
		return &Error{Code: "invalid_configuration"}
	}
	root, err := s.root()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("create provider store: %w", err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(root, "."+config.ProviderID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err = os.Chmod(staging, 0700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(apiToken))
	config.APITokenHash = hex.EncodeToString(digest[:])
	config.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if config.Routes == nil {
		config.Routes = map[string]string{}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	for name, value := range map[string][]byte{"api-token": []byte(apiToken), "tunnel-token": []byte(tunnelToken), "config.json": encoded} {
		if err = writeCredentialFile(filepath.Join(staging, name), value); err != nil {
			return err
		}
	}
	target := filepath.Join(root, config.ProviderID)
	previous := target + ".previous"
	if err = os.RemoveAll(previous); err != nil {
		return err
	}
	if _, statErr := os.Stat(target); statErr == nil {
		if err = os.Rename(target, previous); err != nil {
			return err
		}
	}
	if err = os.Rename(staging, target); err != nil {
		_ = os.Rename(previous, target)
		return err
	}
	return syncProviderDirectory(root)
}

func (s CredentialStore) Load(providerID string) (ProviderConfig, string, string, error) {
	root, err := s.root()
	if err != nil || !providerIDPattern.MatchString(providerID) {
		return ProviderConfig{}, "", "", &Error{Code: "invalid_configuration"}
	}
	directory := filepath.Join(root, providerID)
	encoded, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		return ProviderConfig{}, "", "", &Error{Code: "provider_not_configured"}
	}
	var config ProviderConfig
	if json.Unmarshal(encoded, &config) != nil || validateProviderConfig(config) != nil || config.ProviderID != providerID {
		return ProviderConfig{}, "", "", &Error{Code: "provider_state_invalid"}
	}
	apiToken, err := os.ReadFile(filepath.Join(directory, "api-token"))
	if err != nil || !validToken(string(apiToken)) {
		return ProviderConfig{}, "", "", &Error{Code: "provider_state_invalid"}
	}
	tunnelToken, err := os.ReadFile(filepath.Join(directory, "tunnel-token"))
	if err != nil || !validToken(string(tunnelToken)) {
		return ProviderConfig{}, "", "", &Error{Code: "provider_state_invalid"}
	}
	return config, string(apiToken), string(tunnelToken), nil
}

func (s CredentialStore) SaveConfig(config ProviderConfig) error {
	root, err := s.root()
	if err != nil || validateProviderConfig(config) != nil {
		return &Error{Code: "invalid_configuration"}
	}
	config.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, config.ProviderID)
	temporary := filepath.Join(directory, ".config.json.new")
	if err = os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = writeCredentialFile(temporary, encoded); err != nil {
		return err
	}
	if err = os.Rename(temporary, filepath.Join(directory, "config.json")); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncProviderDirectory(directory)
}

func (s CredentialStore) Commit(providerID string) error {
	root, err := s.root()
	if err != nil || !providerIDPattern.MatchString(providerID) {
		return &Error{Code: "invalid_configuration"}
	}
	if err = os.RemoveAll(filepath.Join(root, providerID) + ".previous"); err != nil {
		return err
	}
	return syncProviderDirectory(root)
}

func (s CredentialStore) Rollback(providerID string) error {
	root, err := s.root()
	if err != nil || !providerIDPattern.MatchString(providerID) {
		return &Error{Code: "invalid_configuration"}
	}
	target := filepath.Join(root, providerID)
	previous := target + ".previous"
	if _, err = os.Stat(previous); err != nil {
		return &Error{Code: "previous_credential_unavailable"}
	}
	rejected := target + ".rejected"
	_ = os.RemoveAll(rejected)
	if err = os.Rename(target, rejected); err != nil {
		return err
	}
	if err = os.Rename(previous, target); err != nil {
		_ = os.Rename(rejected, target)
		return err
	}
	_ = os.RemoveAll(rejected)
	return syncProviderDirectory(root)
}

func (s CredentialStore) Unlink(providerID string) error {
	root, err := s.root()
	if err != nil || !providerIDPattern.MatchString(providerID) {
		return &Error{Code: "invalid_configuration"}
	}
	if err = os.RemoveAll(filepath.Join(root, providerID)); err != nil {
		return err
	}
	if err = os.RemoveAll(filepath.Join(root, providerID) + ".previous"); err != nil {
		return err
	}
	return syncProviderDirectory(root)
}

func (s CredentialStore) TunnelTokenFile(providerID string) (*os.File, error) {
	root, err := s.root()
	if err != nil || !providerIDPattern.MatchString(providerID) {
		return nil, &Error{Code: "invalid_configuration"}
	}
	return os.Open(filepath.Join(root, providerID, "tunnel-token"))
}

func (s CredentialStore) List() ([]ProviderConfig, error) {
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []ProviderConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	configs := []ProviderConfig{}
	for _, entry := range entries {
		if !entry.IsDir() || !providerIDPattern.MatchString(entry.Name()) {
			continue
		}
		config, _, _, loadErr := s.Load(entry.Name())
		if loadErr != nil {
			return nil, loadErr
		}
		configs = append(configs, config)
	}
	return configs, nil
}

func (s CredentialStore) root() (string, error) {
	root := s.Root
	if root == "" {
		root = "/var/lib/kitpro-helper/providers/cloudflare"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", &Error{Code: "invalid_configuration"}
	}
	return root, nil
}

func validateProviderConfig(config ProviderConfig) error {
	if !providerIDPattern.MatchString(config.ProviderID) || !hexIDPattern.MatchString(config.AccountID) || !hexIDPattern.MatchString(config.ZoneID) || !uuidPattern.MatchString(config.TunnelID) || len(config.Routes) > 128 {
		return &Error{Code: "invalid_configuration"}
	}
	for hostname, recordID := range config.Routes {
		if _, err := validatedHostname(hostname); err != nil || !hexIDPattern.MatchString(recordID) {
			return &Error{Code: "invalid_configuration"}
		}
	}
	if config.APITokenHash != "" {
		if len(config.APITokenHash) != 64 || strings.Trim(config.APITokenHash, "0123456789abcdef") != "" {
			return &Error{Code: "invalid_configuration"}
		}
	}
	return nil
}

func writeCredentialFile(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(value); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func syncProviderDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
