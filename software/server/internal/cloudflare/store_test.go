package cloudflare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testProviderConfig() ProviderConfig {
	return ProviderConfig{ProviderID: "provider-0123456789abcdef", AccountID: testAccount, ZoneID: testZone, TunnelID: testTunnel, Routes: map[string]string{}}
}

func TestCredentialStoreProtectsAndReplacesCredentialsAtomically(t *testing.T) {
	root := t.TempDir()
	store := CredentialStore{Root: root}
	config := testProviderConfig()
	firstAPI := "first-api-token-0123456789012345"
	firstTunnel := "first-tunnel-token-012345678901"
	if err := store.Install(config, firstAPI, firstTunnel); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api-token", "tunnel-token", "config.json"} {
		info, err := os.Stat(filepath.Join(root, config.ProviderID, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode=%v", name, info.Mode().Perm())
		}
	}
	secondAPI := "second-api-token-01234567890123"
	secondTunnel := "second-tunnel-token-0123456789"
	if err := store.Install(config, secondAPI, secondTunnel); err != nil {
		t.Fatal(err)
	}
	_, apiToken, tunnelToken, err := store.Load(config.ProviderID)
	if err != nil || apiToken != secondAPI || tunnelToken != secondTunnel {
		t.Fatal("replacement credentials were not active")
	}
	if err = store.Rollback(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	_, apiToken, tunnelToken, err = store.Load(config.ProviderID)
	if err != nil || apiToken != firstAPI || tunnelToken != firstTunnel {
		t.Fatal("credential rollback did not restore prior values")
	}
	if err = store.Install(config, secondAPI, secondTunnel); err != nil {
		t.Fatal(err)
	}
	if err = store.Commit(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, config.ProviderID) + ".previous"); !os.IsNotExist(err) {
		t.Fatal("committed credential retained predecessor")
	}
}

func TestCredentialStoreRejectsPathInjectionAndUnlinksOnlyLocalState(t *testing.T) {
	store := CredentialStore{Root: t.TempDir()}
	config := testProviderConfig()
	if err := store.Install(config, "api-token-01234567890123456789", "tunnel-token-0123456789012345"); err != nil {
		t.Fatal(err)
	}
	for _, providerID := range []string{"../provider", "provider-0123", strings.Repeat("a", 80)} {
		if err := store.Unlink(providerID); err == nil {
			t.Fatalf("accepted provider path %q", providerID)
		}
	}
	if err := store.Unlink(config.ProviderID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Load(config.ProviderID); err == nil {
		t.Fatal("unlinked credentials remained readable")
	}
}
