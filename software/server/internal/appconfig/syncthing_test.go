package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.xml")
	if err := os.WriteFile(path, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root, runtimeOwnerForPath(t, configDir)); err != nil {
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
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.xml")
	original := []byte(strings.Replace(generatedSyncthingConfig, "<natEnabled>true</natEnabled>", "", 1))
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root, runtimeOwnerForPath(t, configDir)); err == nil {
		t.Fatal("incomplete upstream configuration was accepted")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("failed apply changed original configuration: err=%v", err)
	}
	if err := Apply("arbitrary-template", root, runtimeOwnerForPath(t, configDir)); err == nil {
		t.Fatal("unknown configuration policy was accepted")
	}
}

func TestSyncthingPolicyRejectsSymlinkedConfiguration(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.xml")
	if err := os.WriteFile(target, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(configDir, "config.xml")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root, runtimeOwnerForPath(t, configDir)); err == nil {
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
	if err := Apply(SyncthingTCPOnlyV1, root, RuntimeOwner{UID: os.Getuid(), GID: os.Getgid()}); err == nil {
		t.Fatal("symlinked application configuration directory was accepted")
	}
}

func TestSyncthingPolicyRestoresRuntimeOwnershipAndLeavesIdentityUntouched(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.xml")
	identityPath := filepath.Join(configDir, "key.pem")
	if err := os.WriteFile(configPath, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte("identity-key"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := runtimeOwnerForPath(t, configDir)
	if err := Apply(SyncthingTCPOnlyV1, root, owner); err != nil {
		t.Fatal(err)
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtimeOwnerForPath(t, configPath); got != owner {
		t.Fatalf("config owner = %#v, want %#v", got, owner)
	}
	if got := runtimeOwnerForPath(t, configDir); got != owner {
		t.Fatalf("config directory owner = %#v, want %#v", got, owner)
	}
	if configInfo.Mode().Perm() != 0600 || dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("permissions changed: file=%o dir=%o", configInfo.Mode().Perm(), dirInfo.Mode().Perm())
	}
	identity, err := os.ReadFile(identityPath)
	if err != nil || string(identity) != "identity-key" {
		t.Fatalf("identity file changed: %q, err=%v", identity, err)
	}
}

func TestSyncthingPolicyRejectsUnexpectedHardLink(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.xml")
	if err := os.WriteFile(configPath, []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(configPath, filepath.Join(root, "config-copy.xml")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root, runtimeOwnerForPath(t, configDir)); err == nil {
		t.Fatal("hard-linked configuration was accepted")
	}
}

func TestSyncthingPolicyRejectsUnexpectedOwner(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.xml"), []byte(generatedSyncthingConfig), 0600); err != nil {
		t.Fatal(err)
	}
	owner := runtimeOwnerForPath(t, configDir)
	if err := Apply(SyncthingTCPOnlyV1, root, RuntimeOwner{UID: owner.UID + 1, GID: owner.GID}); err == nil {
		t.Fatal("unexpected configuration owner was accepted")
	}
}

func TestSyncthingPolicyRejectsUnexpectedPermissions(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.xml"), []byte(generatedSyncthingConfig), 0640); err != nil {
		t.Fatal(err)
	}
	if err := Apply(SyncthingTCPOnlyV1, root, runtimeOwnerForPath(t, configDir)); err == nil {
		t.Fatal("unexpected configuration permissions were accepted")
	}
}

func runtimeOwnerForPath(t *testing.T, path string) RuntimeOwner {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("missing filesystem ownership metadata")
	}
	return RuntimeOwner{UID: int(stat.Uid), GID: int(stat.Gid)}
}
