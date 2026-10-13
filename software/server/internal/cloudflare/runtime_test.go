package cloudflare

import (
	"strings"
	"testing"
)

func TestRuntimeCommandUsesInheritedTokenFile(t *testing.T) {
	command := runtimeCommand("/usr/bin/cloudflared")
	joined := strings.Join(command.Args, " ")
	if joined != "/usr/bin/cloudflared tunnel --no-autoupdate --metrics 127.0.0.1:20241 run --token-file /proc/self/fd/3" {
		t.Fatalf("unexpected constrained runtime command: %q", joined)
	}
	if strings.Contains(joined, "secret") || strings.Contains(joined, "--token ") {
		t.Fatalf("runtime command exposes credential: %q", joined)
	}
}
