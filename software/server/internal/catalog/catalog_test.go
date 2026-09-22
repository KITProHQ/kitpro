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
	wantIDs := []string{"actual-budget", "audiobookshelf", "busybox", "forgejo", "freshrss", "home-assistant", "it-tools", "jellyfin", "mealie", "memos", "navidrome", "ollama", "open-webui", "paperless-ngx", "plex", "sftpgo", "uptime-kuma", "vaultwarden"}
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
		{"forgejo", "16.0.5", "codeberg.org/forgejo/forgejo@sha256:523de0217475297d05786d7551c1c1d6b5c8b90d6fee7189e88a234260ec0e74", "/data", 3000, map[string]string{"USER_UID": "1000", "USER_GID": "1000"}},
		{"uptime-kuma", "2.3.1", "docker.io/louislam/uptime-kuma@sha256:92fd01c488771d1bcb0b299770255c06994ab7e4f079b7c7fcf52b8e08789a67", "/app/data", 3001, nil},
		{"mealie", "3.24.0", "ghcr.io/mealie-recipes/mealie@sha256:3d2384661634e954c12ec27bb5b25a0263832f9e39044f145d726d388e9f8268", "/app/data", 9000, map[string]string{"ALLOW_SIGNUP": "false", "TZ": "UTC"}},
		{"memos", "0.30.0", "docker.io/neosmemo/memos@sha256:51a4cef418b1f173ac37139ad99de08da5b8662136007231d3ac8a0498a3095a", "/var/opt/memos", 5230, nil},
		{"actual-budget", "26.9.0", "docker.io/actualbudget/actual-server@sha256:06080cca505895fffd5736089920001e979bf9595f757d1d5b9ebfc99722c410", "/data", 5006, nil},
		{"vaultwarden", "1.37.2", "docker.io/vaultwarden/server@sha256:5d326778c22f063d093d6b0c9c766a28249561632266776f2c93132ab0ad3a80", "/data", 80, nil},
		{"home-assistant", "stable", "ghcr.io/home-assistant/home-assistant@sha256:542890f4a7ef9269b7a5ac23ada303b327537c62fa0f866e49daebc61cb44caa", "/config", 8123, nil},
		{"paperless-ngx", "2.20.15", "docker.io/paperlessngx/paperless-ngx@sha256:6c86cad803970ea782683a8e80e7403444c5bf3cf70de63b4d3c8e87500db92f", "/usr/src/paperless/data", 8000, nil},
		{"plex", "1.43.4.10903-e5521bd8c", "docker.io/plexinc/pms-docker@sha256:dbb879bf58c3fc56635f21ac48c32aa6853aaa23d4a57b102033b6dc6d2d9cee", "/config", 32400, nil},
		{"open-webui", "0.11.3", "ghcr.io/open-webui/open-webui@sha256:9cd136effce6bb12a6a1988a35ab3b82cb40c48a6768fceeb17c83baf7cfac9c", "/app/backend/data", 8080, nil},
		{"it-tools", "2024.10.22-7ca5933", "docker.io/corentinth/it-tools@sha256:6f177c156b9466610e0f2093e24668b78da501c66f0054f98bccb582b74ab26b", "", 80, nil},
		{"ollama", "0.34.0", "docker.io/ollama/ollama@sha256:aa6f86f01fee264c81f1edd9083ebfb07c8116d95d8bedd1ad470874b66a40b4", "/root/.ollama", 11434, nil},
		{"jellyfin", "12.1", "docker.io/jellyfin/jellyfin@sha256:326be1010b16c92e492f6c7dd6fd105943db84ce723c73183279a1ab357b8f9b", "/config", 8096, nil},
		{"navidrome", "0.64.0", "docker.io/deluan/navidrome@sha256:1a64cbb2603cec5d2615c3a27e91442436b2229583408a55cdc8d85705b95e65", "/data", 4533, map[string]string{"ND_LOGLEVEL": "info", "ND_SCANSCHEDULE": "1h"}},
		{"audiobookshelf", "2.36.0", "ghcr.io/advplyr/audiobookshelf@sha256:e388e90e381ae3fa8660346612b2955f2c555ede81c9c286e2218bdf966b4de8", "/config", 13378, map[string]string{"TZ": "UTC", "PORT": "13378"}},
		{"sftpgo", "2.7.5", "ghcr.io/drakkan/sftpgo@sha256:d819bcea946470940416b63604f820aee965a02127b07126785e279fa311258e", "/var/lib/sftpgo", 8080, nil},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			m := c[tt.id].Manifest
			if m.Description == "" || len(m.Services) == 0 || m.Backup == nil {
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

func TestVisibleCatalogMetadataIsManifestBacked(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	wantCategories := map[string]string{
		"actual-budget": "Finance", "audiobookshelf": "Media",
		"forgejo": "Developer Tools", "freshrss": "Reading", "home-assistant": "Home automation",
		"it-tools": "Developer Tools", "jellyfin": "Media",
		"mealie": "Food and recipes", "memos": "Notes",
		"navidrome": "Music", "ollama": "AI", "open-webui": "AI",
		"paperless-ngx": "Documents", "plex": "Media", "sftpgo": "Files",
		"uptime-kuma": "Monitoring", "vaultwarden": "Security",
	}
	if len(wantCategories) != 17 {
		t.Fatal("visible catalog metadata fixture must cover all 17 applications")
	}
	for id, category := range wantCategories {
		entry, ok := c[id]
		if !ok {
			t.Fatalf("visible application %s is missing", id)
		}
		m := entry.Manifest
		if m.SchemaVersion != manifest.CatalogMetadataSchemaVersion || m.Category != category || m.Kind != "application" || m.CatalogStatus != "standard" {
			t.Fatalf("incomplete metadata for %s: %#v", id, m)
		}
		if m.WebsiteURL == "" && m.SourceURL == "" && m.DocumentationURL == "" {
			t.Fatalf("%s has no reviewed project link", id)
		}
		if strings.Contains(m.Logo, "://") || strings.Contains(m.Logo, "/") {
			t.Fatalf("%s has a remote or path-based logo reference %q", id, m.Logo)
		}
	}
	if c["busybox"].Manifest.SchemaVersion != manifest.BackupSchemaVersion {
		t.Fatal("busybox must remain a schema-v6 compatibility fixture")
	}
}

func TestPlexProfileIsConstrained(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m := c["plex"].Manifest
	if m.SchemaVersion != manifest.CatalogMetadataSchemaVersion || m.ID != "plex" || m.Name != "Plex" || m.Category != "Media" || m.Kind != "application" || m.CatalogStatus != "standard" {
		t.Fatalf("unexpected Plex identity or metadata: %#v", m)
	}
	if m.WebsiteURL != "https://www.plex.tv/" || m.SourceURL != "https://github.com/plexinc/pms-docker" || m.DocumentationURL != "https://support.plex.tv/articles/200288586-installation/" || m.Logo != "" || len(m.Limitations) != 4 {
		t.Fatalf("unexpected Plex presentation metadata: %#v", m)
	}
	for _, required := range []string{"CPU-only", "GPU", "Remote Access", "32400/TCP", "discovery"} {
		found := false
		for _, limitation := range m.Limitations {
			found = found || strings.Contains(limitation, required)
		}
		if !found {
			t.Fatalf("Plex limitations do not mention %s: %#v", required, m.Limitations)
		}
	}
	if len(m.Releases) != 1 || m.Releases[0].Version != "1.43.4.10903-e5521bd8c" || m.Releases[0].Registry != "docker.io" || m.Releases[0].Repository != "plexinc/pms-docker" || m.Releases[0].Digest != "sha256:dbb879bf58c3fc56635f21ac48c32aa6853aaa23d4a57b102033b6dc6d2d9cee" || m.Releases[0].Platform != "linux/amd64" {
		t.Fatalf("unexpected Plex release: %#v", m.Releases)
	}
	if len(m.Components) != 0 || len(m.Hardware) != 0 || len(m.Command) != 0 || len(m.Environment) != 0 || m.RunAs != nil {
		t.Fatalf("Plex acquired unexpected runtime authority: %#v", m)
	}
	if len(m.Storage) != 1 || m.Storage[0].ID != "config" || m.Storage[0].ContainerPath != "/config" || !m.Storage[0].Persistent || m.Storage[0].ReadOnly || m.Storage[0].OwnerUID != 0 || m.Storage[0].OwnerGID != 0 {
		t.Fatalf("unexpected Plex managed storage: %#v", m.Storage)
	}
	if len(m.ExternalStorage) != 1 || m.ExternalStorage[0] != (manifest.ExternalStorage{ID: "media", ContainerPath: "/data", Mode: "read-only", Required: true, Purpose: "Media library"}) {
		t.Fatalf("unexpected Plex external storage: %#v", m.ExternalStorage)
	}
	if len(m.Services) != 1 || m.Services[0].ID != "web" || m.Services[0].Protocol != "http" || m.Services[0].ContainerPort != 32400 {
		t.Fatalf("unexpected Plex services: %#v", m.Services)
	}
	if m.Restart != "unless-stopped" || m.Backup == nil || m.Backup.Strategy != "cold-sqlite-filesystem" || len(m.Backup.Storage) != 1 || m.Backup.Storage[0] != (manifest.BackupStorage{Component: "app", ID: "config", Disposition: "include"}) {
		t.Fatalf("unexpected Plex lifecycle policy: restart=%q backup=%#v", m.Restart, m.Backup)
	}
}

func TestForgejoProfileIsConstrained(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m := c["forgejo"].Manifest
	if m.SchemaVersion != manifest.CatalogMetadataSchemaVersion || m.ID != "forgejo" || m.Name != "Forgejo" || m.Category != "Developer Tools" || m.Kind != "application" || m.CatalogStatus != "standard" {
		t.Fatalf("unexpected Forgejo identity or metadata: %#v", m)
	}
	if m.WebsiteURL != "https://forgejo.org/" || m.SourceURL != "https://codeberg.org/forgejo/forgejo" || m.DocumentationURL != "https://forgejo.org/docs/latest/" || m.Logo != "" || len(m.Limitations) != 3 {
		t.Fatalf("unexpected Forgejo presentation metadata: %#v", m)
	}
	for _, required := range []string{"Git-over-SSH", "SQLite", "HTTPS"} {
		found := false
		for _, limitation := range m.Limitations {
			found = found || strings.Contains(limitation, required)
		}
		if !found {
			t.Fatalf("Forgejo limitations do not mention %s: %#v", required, m.Limitations)
		}
	}
	if len(m.Releases) != 1 || m.Releases[0].Version != "16.0.5" || m.Releases[0].Registry != "codeberg.org" || m.Releases[0].Repository != "forgejo/forgejo" || m.Releases[0].Digest != "sha256:523de0217475297d05786d7551c1c1d6b5c8b90d6fee7189e88a234260ec0e74" || m.Releases[0].Platform != "linux/amd64" {
		t.Fatalf("unexpected Forgejo release: %#v", m.Releases)
	}
	if len(m.Components) != 0 || len(m.ExternalStorage) != 0 || m.RunAs != nil || len(m.Hardware) != 0 || len(m.Command) != 0 {
		t.Fatalf("Forgejo acquired unexpected runtime authority: %#v", m)
	}
	if len(m.Storage) != 1 || m.Storage[0].ID != "data" || m.Storage[0].ContainerPath != "/data" || !m.Storage[0].Persistent || m.Storage[0].ReadOnly || m.Storage[0].OwnerUID != 1000 || m.Storage[0].OwnerGID != 1000 {
		t.Fatalf("unexpected Forgejo storage: %#v", m.Storage)
	}
	if len(m.Services) != 1 || m.Services[0].ID != "web" || m.Services[0].Protocol != "http" || m.Services[0].ContainerPort != 3000 {
		t.Fatalf("unexpected Forgejo services: %#v", m.Services)
	}
	if m.Restart != "unless-stopped" || m.Backup == nil || m.Backup.Strategy != "cold-sqlite-filesystem" || len(m.Backup.Storage) != 1 || m.Backup.Storage[0] != (manifest.BackupStorage{Component: "app", ID: "data", Disposition: "include"}) {
		t.Fatalf("unexpected Forgejo lifecycle policy: restart=%q backup=%#v", m.Restart, m.Backup)
	}
}
