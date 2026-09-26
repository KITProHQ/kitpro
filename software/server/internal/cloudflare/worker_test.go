package cloudflare

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkerNeverProjectsTokensAndRequiresRemoteConfirmation(t *testing.T) {
	manager, config, _, _ := configuredManager(t)
	worker := &Worker{Manager: manager}
	response := worker.execute(context.Background(), WorkerRequest{Operation: "health", ProviderID: config.ProviderID})
	encoded, _ := json.Marshal(response)
	if !response.OK || strings.Contains(string(encoded), "api-token") || strings.Contains(string(encoded), "tunnel-token") || strings.Contains(string(encoded), testToken) {
		t.Fatalf("unsafe response: %s", encoded)
	}
	response = worker.execute(context.Background(), WorkerRequest{Operation: "delete-remote-tunnel", ProviderID: config.ProviderID})
	if response.OK || response.ErrorCode != "remote_confirmation_required" {
		t.Fatalf("remote delete did not require confirmation: %+v", response)
	}
	response = worker.execute(context.Background(), WorkerRequest{Operation: "not-an-operation", ProviderID: config.ProviderID, APIToken: testToken})
	encoded, _ = json.Marshal(response)
	if response.OK || response.ErrorCode != "operation_not_permitted" || strings.Contains(string(encoded), testToken) {
		t.Fatalf("unsafe rejected response: %s", encoded)
	}
}
