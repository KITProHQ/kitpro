package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const generatedSyncthingConfig = `<?xml version="1.0"?><configuration version="52">
<folder id="shared" path="/sync"><device id="REMOTE-ID"></device></folder>
<device id="LOCAL-IDENTITY"><address>dynamic</address></device>
<gui enabled="true" tls="false"><address>0.0.0.0:8384</address><apikey>keep-this-api-key</apikey></gui>
<options>
<listenAddress>default</listenAddress><listenAddress>quic://0.0.0.0:22000</listenAddress>
<globalAnnounceEnabled>true</globalAnnounceEnabled><localAnnounceEnabled>true</localAnnounceEnabled>
<relaysEnabled>true</relaysEnabled><natEnabled>true</natEnabled><startBrowser>true</startBrowser>
<urAccepted>0</urAccepted><autoUpgradeIntervalH>12</autoUpgradeIntervalH><crashReportingEnabled>true</crashReportingEnabled>
</options><defaults><folder id=""></folder></defaults></configuration>`

func TestSyncthingTCPOnlyPolicyPreservesIdentityAndUserConfiguration(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.xml")
	if err := os.WriteFile(path, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root); err != nil {
		t.Fatal(err)
	}
	if err := Verify(SyncthingTCPOnlyV1, root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, retained := range []string{"LOCAL-IDENTITY", "REMOTE-ID", `path="/sync"`, "keep-this-api-key"} {
		if !strings.Contains(text, retained) {
			t.Fatalf("policy rewrite lost application-managed value %q", retained)
		}
	}
	if strings.Count(text, "<listenAddress>") != 1 || !strings.Contains(text, "tcp://0.0.0.0:22000") || strings.Contains(text, "quic://") || strings.Contains(text, ">default</listenAddress>") {
		t.Fatalf("listen policy was not reduced to TCP only: %s", text)
	}
	for _, disabled := range []string{"globalAnnounceEnabled", "localAnnounceEnabled", "relaysEnabled", "natEnabled", "startBrowser", "crashReportingEnabled"} {
		if !strings.Contains(text, "<"+disabled+">false</"+disabled+">") {
			t.Fatalf("%s was not disabled: %s", disabled, text)
		}
	}
}

func TestSyncthingPolicyFailsClosedOnUnknownOrIncompleteInput(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.xml")
	if err := os.WriteFile(path, []byte(strings.Replace(generatedSyncthingConfig, "<natEnabled>true</natEnabled>", "", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root); err == nil {
		t.Fatal("incomplete upstream configuration was accepted")
	}
	if err := Apply("arbitrary-template", root); err == nil {
		t.Fatal("unknown configuration policy was accepted")
	}
}

func TestSyncthingPolicyRejectsSymlinkedConfiguration(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.xml")
	if err := os.WriteFile(target, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(configDir, "config.xml")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root); err == nil {
		t.Fatal("symlinked application configuration was accepted")
	}
}

func TestSyncthingPolicyRejectsSymlinkedConfigurationDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "config.xml"), []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "config")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root); err == nil {
		t.Fatal("symlinked application configuration directory was accepted")
	}
}
