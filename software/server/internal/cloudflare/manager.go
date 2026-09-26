package cloudflare

import (
	"context"
	"errors"
)

type API interface {
	ValidateCredential(context.Context, string, string, string, string) (Tunnel, error)
	TunnelToken(context.Context, string, string, string) (string, error)
	AddRoute(context.Context, string, string, string, string, string) (DNSRecord, error)
	RemoveRoute(context.Context, string, string, string, string, string, string) error
	DeleteTunnel(context.Context, string, string, string) error
}

type Manager struct {
	Store   CredentialStore
	API     API
	Runtime Runtime
}

func (m Manager) Configure(ctx context.Context, config ProviderConfig, apiToken string) (ProviderConfig, error) {
	if m.API == nil || m.Runtime == nil || validateProviderConfig(config) != nil {
		return ProviderConfig{}, &Error{Code: "not_configured"}
	}
	if _, err := m.API.ValidateCredential(ctx, apiToken, config.AccountID, config.ZoneID, config.TunnelID); err != nil {
		return ProviderConfig{}, err
	}
	tunnelToken, err := m.API.TunnelToken(ctx, apiToken, config.AccountID, config.TunnelID)
	if err != nil {
		return ProviderConfig{}, err
	}
	existing, _, _, loadErr := m.Store.Load(config.ProviderID)
	replacing := loadErr == nil
	if replacing {
		config.Enabled, config.Routes = existing.Enabled, existing.Routes
	}
	if err = m.Store.Install(config, apiToken, tunnelToken); err != nil {
		return ProviderConfig{}, err
	}
	if config.Enabled {
		if restartable, ok := m.Runtime.(interface{ Restart(string) error }); ok {
			err = restartable.Restart(config.ProviderID)
		} else {
			err = m.Runtime.Stop()
			if err == nil {
				err = m.Runtime.Start(config.ProviderID)
			}
		}
		if err != nil {
			if replacing && m.Store.Rollback(config.ProviderID) == nil {
				_ = m.Runtime.Start(config.ProviderID)
			}
			return ProviderConfig{}, &Error{Code: "runtime_restart_failed"}
		}
	}
	if err = m.Store.Commit(config.ProviderID); err != nil {
		return ProviderConfig{}, err
	}
	stored, _, _, err := m.Store.Load(config.ProviderID)
	return stored, err
}

func (m Manager) Enable(providerID string) (ProviderConfig, error) {
	config, _, _, err := m.Store.Load(providerID)
	if err != nil {
		return ProviderConfig{}, err
	}
	if err = m.Runtime.Start(providerID); err != nil {
		return ProviderConfig{}, err
	}
	config.Enabled = true
	if err = m.Store.SaveConfig(config); err != nil {
		_ = m.Runtime.Stop()
		return ProviderConfig{}, err
	}
	return config, nil
}

func (m Manager) Disable(providerID string) (ProviderConfig, error) {
	config, _, _, err := m.Store.Load(providerID)
	if err != nil {
		return ProviderConfig{}, err
	}
	if err = m.Runtime.Stop(); err != nil {
		return ProviderConfig{}, err
	}
	config.Enabled = false
	if err = m.Store.SaveConfig(config); err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}

func (m Manager) AddRoute(ctx context.Context, providerID, hostname string) (ProviderConfig, error) {
	config, apiToken, _, err := m.Store.Load(providerID)
	if err != nil {
		return ProviderConfig{}, err
	}
	if !config.Enabled {
		return ProviderConfig{}, &Error{Code: "provider_disabled"}
	}
	if _, exists := config.Routes[hostname]; exists {
		return ProviderConfig{}, &Error{Code: "route_conflict"}
	}
	record, err := m.API.AddRoute(ctx, apiToken, config.AccountID, config.ZoneID, config.TunnelID, hostname)
	if err != nil {
		return ProviderConfig{}, err
	}
	config.Routes[hostname] = record.ID
	if err = m.Store.SaveConfig(config); err != nil {
		_ = m.API.RemoveRoute(ctx, apiToken, config.AccountID, config.ZoneID, config.TunnelID, hostname, record.ID)
		return ProviderConfig{}, err
	}
	return config, nil
}

func (m Manager) RemoveRemoteRoute(ctx context.Context, providerID, hostname string) (ProviderConfig, error) {
	config, apiToken, _, err := m.Store.Load(providerID)
	if err != nil {
		return ProviderConfig{}, err
	}
	recordID, exists := config.Routes[hostname]
	if !exists {
		return ProviderConfig{}, &Error{Code: "route_not_found"}
	}
	if err = m.API.RemoveRoute(ctx, apiToken, config.AccountID, config.ZoneID, config.TunnelID, hostname, recordID); err != nil {
		return ProviderConfig{}, err
	}
	delete(config.Routes, hostname)
	if err = m.Store.SaveConfig(config); err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}

func (m Manager) Unlink(providerID string) error {
	if _, _, _, err := m.Store.Load(providerID); err != nil {
		return err
	}
	if err := m.Runtime.Stop(); err != nil {
		return err
	}
	return m.Store.Unlink(providerID)
}

func (m Manager) DeleteRemoteTunnel(ctx context.Context, providerID string) error {
	config, apiToken, _, err := m.Store.Load(providerID)
	if err != nil {
		return err
	}
	if len(config.Routes) != 0 {
		return &Error{Code: "remote_routes_remain"}
	}
	if config.Enabled {
		return &Error{Code: "provider_must_be_disabled"}
	}
	if err = m.API.DeleteTunnel(ctx, apiToken, config.AccountID, config.TunnelID); err != nil {
		return err
	}
	return m.Store.Unlink(providerID)
}

func (m Manager) Health(ctx context.Context, providerID string) (ProviderConfig, RuntimeStatus, error) {
	config, apiToken, _, err := m.Store.Load(providerID)
	if err != nil {
		return ProviderConfig{}, RuntimeStatus{}, err
	}
	status := m.Runtime.Status(ctx)
	if _, err = m.API.ValidateCredential(ctx, apiToken, config.AccountID, config.ZoneID, config.TunnelID); err != nil {
		var providerError *Error
		if errors.As(err, &providerError) {
			return config, status, providerError
		}
		return config, status, &Error{Code: "provider_unavailable"}
	}
	return config, status, nil
}

func (m Manager) Recover() error {
	configs, err := m.Store.List()
	if err != nil {
		return err
	}
	for _, config := range configs {
		if config.Enabled {
			return m.Runtime.Start(config.ProviderID)
		}
	}
	return nil
}
