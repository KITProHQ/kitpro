package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/cloudflare"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

var cloudflareWorkerCall = func(ctx context.Context, request cloudflare.WorkerRequest) (cloudflare.WorkerResponse, error) {
	return (cloudflare.WorkerClient{}).Do(ctx, request)
}

func cloudflareOperation(ctx context.Context, db *sql.DB, request protocol.Request) (map[string]any, error) {
	action := map[string]string{
		"ConfigureCloudflareProvider":  "configure",
		"EnableCloudflareProvider":     "enable",
		"DisableCloudflareProvider":    "disable",
		"AddCloudflareRoute":           "add-route",
		"RemoveCloudflareRemoteRoute":  "remove-remote-route",
		"UnlinkCloudflareProvider":     "unlink",
		"DeleteCloudflareRemoteTunnel": "delete-remote-tunnel",
	}[request.Operation]
	if action == "" {
		return nil, fmt.Errorf("unsupported Cloudflare operation")
	}
	workerRequest := cloudflare.WorkerRequest{Operation: action, ProviderID: request.ProviderID, AccountID: request.ProviderAccount, ZoneID: request.ProviderZone, TunnelID: request.ProviderTunnel, APIToken: request.ProviderToken, Hostname: request.Hostname, ConfirmRemote: request.ConfirmRemote}
	tokenDigest := sha256.Sum256([]byte(request.ProviderToken))
	response, err := cloudflareWorkerCall(ctx, workerRequest)
	workerRequest.APIToken, request.ProviderToken = "", ""
	if err != nil || !response.OK {
		code := response.ErrorCode
		if code == "" {
			code = "provider_operation_failed"
		}
		return nil, fmt.Errorf("Cloudflare provider operation failed: %s", code)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch action {
	case "configure":
		if response.Config == nil {
			return nil, fmt.Errorf("Cloudflare worker returned no provider state")
		}
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			return nil, txErr
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `INSERT INTO cloudflare_provider_state(provider_id,account_id,zone_id,tunnel_id,desired_state,runtime_state,last_error_code,updated_at) VALUES(?,?,?,?,'disabled','offline','',?) ON CONFLICT(provider_id) DO UPDATE SET account_id=excluded.account_id,zone_id=excluded.zone_id,tunnel_id=excluded.tunnel_id,last_error_code='',updated_at=excluded.updated_at`, request.ProviderID, request.ProviderAccount, request.ProviderZone, request.ProviderTunnel, now)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO exposure_provider_credentials(provider_id,provider_type,credential_sha256,credential_path,created_at,replaced_at) VALUES(?,'cloudflare-tunnel',?,?,?,NULL) ON CONFLICT(provider_id) DO UPDATE SET credential_sha256=excluded.credential_sha256,credential_path=excluded.credential_path,replaced_at=excluded.created_at`, request.ProviderID, hex.EncodeToString(tokenDigest[:]), "/var/lib/kitpro-helper/providers/cloudflare/"+request.ProviderID+"/api-token", now)
		}
		if err == nil {
			err = tx.Commit()
		}
	case "enable", "disable":
		desired, runtimeState := "enabled", "connecting"
		if action == "disable" {
			desired, runtimeState = "disabled", "offline"
		}
		_, err = db.ExecContext(ctx, `UPDATE cloudflare_provider_state SET desired_state=?,runtime_state=?,last_error_code='',updated_at=? WHERE provider_id=?`, desired, runtimeState, now, request.ProviderID)
		if err != nil && action == "enable" {
			_, _ = cloudflareWorkerCall(ctx, cloudflare.WorkerRequest{Operation: "disable", ProviderID: request.ProviderID})
		}
	case "add-route":
		if response.Config == nil || response.Config.Routes[request.Hostname] == "" {
			return nil, fmt.Errorf("Cloudflare worker returned no route state")
		}
		_, err = db.ExecContext(ctx, `INSERT INTO cloudflare_provider_routes(provider_id,hostname,dns_record_id,created_at) VALUES(?,?,?,?)`, request.ProviderID, request.Hostname, response.Config.Routes[request.Hostname], now)
		if err != nil {
			_, _ = cloudflareWorkerCall(ctx, cloudflare.WorkerRequest{Operation: "remove-remote-route", ProviderID: request.ProviderID, Hostname: request.Hostname, ConfirmRemote: true})
		}
	case "remove-remote-route":
		_, err = db.ExecContext(ctx, `DELETE FROM cloudflare_provider_routes WHERE provider_id=? AND hostname=?`, request.ProviderID, request.Hostname)
	case "unlink", "delete-remote-tunnel":
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			return nil, txErr
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `DELETE FROM cloudflare_provider_state WHERE provider_id=?`, request.ProviderID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM exposure_provider_credentials WHERE provider_id=?`, request.ProviderID)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("Cloudflare provider state could not be committed")
	}
	result := map[string]any{"provider_id": request.ProviderID, "action": action}
	if response.Config != nil {
		result["enabled"] = response.Config.Enabled
	}
	return result, nil
}

func inspectCloudflareProvider(ctx context.Context, db *sql.DB, request protocol.Request) (map[string]any, error) {
	response, err := cloudflareWorkerCall(ctx, cloudflare.WorkerRequest{Operation: "health", ProviderID: request.ProviderID})
	runtimeState, errorCode := "failed", "provider_unavailable"
	if response.Runtime != nil {
		runtimeState = response.Runtime.State
	}
	if err != nil {
		if response.ErrorCode != "" {
			errorCode = response.ErrorCode
		}
	} else {
		errorCode = ""
	}
	_, _ = db.ExecContext(ctx, `UPDATE cloudflare_provider_state SET runtime_state=?,last_error_code=?,updated_at=? WHERE provider_id=?`, runtimeState, errorCode, time.Now().UTC().Format(time.RFC3339Nano), request.ProviderID)
	return map[string]any{"provider_id": request.ProviderID, "runtime_state": runtimeState, "last_error_code": errorCode}, err
}
