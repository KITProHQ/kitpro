package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/cloudflare"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func TestCloudflareHelperStoresOnlyCredentialReferenceAndSafeProjection(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	original := cloudflareWorkerCall
	defer func() { cloudflareWorkerCall = original }()
	const token = "known-cloudflare-api-token-0123456789"
	config := cloudflare.ProviderConfig{ProviderID: "provider-0123456789abcdef", AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ZoneID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TunnelID: "12345678-1234-4123-8123-123456789abc", Routes: map[string]string{}}
	cloudflareWorkerCall = func(_ context.Context, request cloudflare.WorkerRequest) (cloudflare.WorkerResponse, error) {
		if request.Operation == "configure" && request.APIToken != token {
			t.Fatal("provider token was not passed to the isolated worker")
		}
		return cloudflare.WorkerResponse{OK: true, Config: &config}, nil
	}
	request := protocol.Request{Operation: "ConfigureCloudflareProvider", ProviderID: config.ProviderID, ProviderAccount: config.AccountID, ProviderZone: config.ZoneID, ProviderTunnel: config.TunnelID, ProviderToken: token}
	result, err := cloudflareOperation(context.Background(), db, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), token) {
		t.Fatal("provider token leaked into helper result")
	}
	var digest, path string
	if err = db.QueryRow(`SELECT credential_sha256,credential_path FROM exposure_provider_credentials WHERE provider_id=?`, config.ProviderID).Scan(&digest, &path); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 || strings.Contains(digest, token) || !strings.HasSuffix(path, "/"+config.ProviderID+"/api-token") {
		t.Fatalf("unsafe credential projection digest=%q path=%q", digest, path)
	}
}

func TestCloudflareHelperCompensatesFailedRouteProjection(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	original := cloudflareWorkerCall
	defer func() { cloudflareWorkerCall = original }()
	const providerID = "provider-0123456789abcdef"
	config := cloudflare.ProviderConfig{ProviderID: providerID, AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ZoneID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TunnelID: "12345678-1234-4123-8123-123456789abc", Routes: map[string]string{"app.example.test": "cccccccccccccccccccccccccccccccc"}}
	compensated := false
	cloudflareWorkerCall = func(_ context.Context, request cloudflare.WorkerRequest) (cloudflare.WorkerResponse, error) {
		if request.Operation == "remove-remote-route" && request.ConfirmRemote {
			compensated = true
		}
		return cloudflare.WorkerResponse{OK: true, Config: &config}, nil
	}
	// No provider parent row exists, so the route projection must fail its
	// foreign key and trigger remote compensation.
	_, err = cloudflareOperation(context.Background(), db, protocol.Request{Operation: "AddCloudflareRoute", ProviderID: providerID, Hostname: "app.example.test"})
	if err == nil || !compensated {
		t.Fatalf("err=%v compensated=%v", err, compensated)
	}
}
