package catalog

import (
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

func TestBuiltInCatalogLoads(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"actual-budget", "busybox", "freshrss", "home-assistant", "it-tools", "jellyfin", "mealie", "memos", "ollama", "open-webui", "paperless-ngx", "uptime-kuma", "vaultwarden"}
	got := IDs(c)
	if len(c) != len(wantIDs) || len(got) != len(wantIDs) {
		t.Fatalf("unexpected catalog: %#v", got)
	}
	for i := range wantIDs {
		if got[i] != wantIDs[i] {
			t.Fatalf("unexpected catalog: %#v", got)
		}
	}

	tests := []struct {
		id, version, image, storagePath string
		port                            int
		environment                     map[string]string
	}{
		{"freshrss", "1.29.1", "docker.io/freshrss/freshrss@sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21", "/var/www/FreshRSS/data", 80, map[string]string{"TZ": "UTC"}},
		{"uptime-kuma", "2.3.1", "docker.io/louislam/uptime-kuma@sha256:92fd01c488771d1bcb0b299770255c06994ab7e4f079b7c7fcf52b8e08789a67", "/app/data", 3001, nil},
		{"mealie", "3.24.0", "ghcr.io/mealie-recipes/mealie@sha256:3d2384661634e954c12ec27bb5b25a0263832f9e39044f145d726d388e9f8268", "/app/data", 9000, map[string]string{"ALLOW_SIGNUP": "false", "TZ": "UTC"}},
		{"memos", "0.30.0", "docker.io/neosmemo/memos@sha256:51a4cef418b1f173ac37139ad99de08da5b8662136007231d3ac8a0498a3095a", "/var/opt/memos", 5230, nil},
		{"actual-budget", "26.9.0", "docker.io/actualbudget/actual-server@sha256:06080cca505895fffd5736089920001e979bf9595f757d1d5b9ebfc99722c410", "/data", 5006, nil},
		{"vaultwarden", "1.37.2", "docker.io/vaultwarden/server@sha256:5d326778c22f063d093d6b0c9c766a28249561632266776f2c93132ab0ad3a80", "/data", 80, nil},
		{"home-assistant", "stable", "ghcr.io/home-assistant/home-assistant@sha256:542890f4a7ef9269b7a5ac23ada303b327537c62fa0f866e49daebc61cb44caa", "/config", 8123, nil},
		{"paperless-ngx", "2.20.15", "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", "/usr/src/paperless/data", 8000, nil},
		{"open-webui", "0.11.3", "ghcr.io/open-webui/open-webui@sha256:9cd136effce6bb12a6a1988a35ab3b82cb40c48a6768fceeb17c83baf7cfac9c", "/app/backend/data", 8080, nil},
		{"it-tools", "2024.10.22-7ca5933", "docker.io/corentinth/it-tools@sha256:6f177c156b9466610e0f2093e24668b78da501c66f0054f98bccb582b74ab26b", "", 80, nil},
		{"ollama", "0.34.0", "docker.io/ollama/ollama@sha256:aa6f86f01fee264c81f1edd9083ebfb07c8116d95d8bedd1ad470874b66a40b4", "/root/.ollama", 11434, nil},
		{"jellyfin", "12.1", "docker.io/jellyfin/jellyfin@sha256:326be1010b16c92e492f6c7dd6fd105943db84ce723c73183279a1ab357b8f9b", "/config", 8096, nil},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			m := c[tt.id].Manifest
			if m.Description == "" || len(m.Services) == 0 {
				t.Fatalf("incomplete manifest: %#v", m)
			}
			if len(m.Components) == 0 && ((tt.storagePath != "" && (len(m.Storage) == 0 || m.Storage[0].ContainerPath != tt.storagePath)) || (tt.storagePath == "" && len(m.Storage) != 0) || m.Services[0].ContainerPort != tt.port) {
				t.Fatalf("unexpected storage/service: %#v %#v", m.Storage, m.Services)
			}
			gotEnv := map[string]string{}
			for _, variable := range m.Environment {
				if variable.Secret {
					if variable.Value != "" || variable.Generate != "random-hex-32" || !variable.Required {
						t.Fatal("catalog has an unsafe secret environment declaration")
					}
					continue
				}
				gotEnv[variable.Name] = variable.Value
			}
			for name, value := range tt.environment {
				if gotEnv[name] != value {
					t.Fatalf("environment %s = %q, want %q", name, gotEnv[name], value)
				}
			}
			instance := "inst-" + strings.ReplaceAll(tt.id, "-", "") + "01"
			plan, resolveErr := manifest.Resolve(m, tt.version, instance, "kitpro-net-"+instance, "/srv/kitpro/apps/"+tt.id+"/"+instance+"/data")
			if resolveErr != nil || plan.ImageDigest != tt.image {
				t.Fatalf("resolve: %v %s", resolveErr, plan.ImageDigest)
			}
			if tt.id == "paperless-ngx" && len(plan.ResolvedComponents) != 2 {
				t.Fatalf("components not resolved: %#v", plan.ResolvedComponents)
			}
		})
	}
	if _, present := c["linkding"]; present {
		t.Fatal("Linkding must not be catalogued before secure bootstrap secrets are supported")
	}
}
