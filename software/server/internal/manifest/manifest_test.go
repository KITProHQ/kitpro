package manifest

import (
	"strings"
	"testing"
)

const valid = `{"schema_version":1,"id":"busybox","name":"BusyBox","releases":[{"version":"1.0","registry":"docker.io","repository":"library/busybox","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"storage":[{"id":"data","container_path":"/data","persistent":true,"read_only":false}],"restart":"unless-stopped"}`

func validV7Manifest() string {
	data := strings.Replace(valid, `"schema_version":1`, `"schema_version":7`, 1)
	data = strings.Replace(data, `"name":"BusyBox"`, `"name":"BusyBox","category":"Developer Tools","kind":"application","catalog_status":"standard","website_url":"https://example.com","source_url":"https://github.com/example/project","documentation_url":"https://docs.example.com/project","logo":"busybox","limitations":["No automatic public access."],"lifecycle_notice":{"install":"Review the application settings before installation.","stop":"Stopping interrupts service.","remove":"Stored data is retained.","require_acknowledgement":true}`, 1)
	return strings.Replace(data, `"restart":"unless-stopped"`, `"restart":"unless-stopped","backup":{"strategy":"cold-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, 1)
}

func TestParseAndResolve(t *testing.T) {
	m, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(m, "1.0", "inst-12345678", "kitpro-net-inst-12345678", "/srv/kitpro/apps/busybox/inst-12345678/data")
	if err != nil || p.ImageDigest == "" {
		t.Fatalf("resolve: %v", err)
	}
	if p.Hash() == "" {
		t.Fatal("empty hash")
	}
}

func TestTrustedRegistryPolicyIncludesExactCodebergHost(t *testing.T) {
	for _, registry := range []string{"docker.io", "ghcr.io", "codeberg.org"} {
		data := strings.Replace(valid, `"registry":"docker.io"`, `"registry":"`+registry+`"`, 1)
		if _, err := Parse([]byte(data)); err != nil {
			t.Fatalf("trusted registry %s rejected: %v", registry, err)
		}
	}
	// Registry policy is host-scoped today. Catalog review and helper-side exact
	// plan comparison provide the narrower repository trust boundary.
	arbitraryCodebergRepository := strings.Replace(valid, `"registry":"docker.io","repository":"library/busybox"`, `"registry":"codeberg.org","repository":"unrelated/project"`, 1)
	if _, err := Parse([]byte(arbitraryCodebergRepository)); err != nil {
		t.Fatalf("host-scoped Codeberg policy changed: %v", err)
	}
	for _, registry := range []string{"evil.example", "evil.codeberg.org", "codeberg.org.evil.example"} {
		data := strings.Replace(valid, `"registry":"docker.io"`, `"registry":"`+registry+`"`, 1)
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("untrusted registry %s accepted", registry)
		}
	}
}

func TestSchemaVersionsOneThroughSevenRemainParseable(t *testing.T) {
	versions := map[string]string{
		"v1": valid,
		"v2": `{"schema_version":2,"id":"multi-app","name":"Multi app","releases":[{"version":"1","registry":"docker.io","repository":"example/app","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux/amd64"}],"components":[{"id":"broker","release":"1"},{"id":"web","release":"1","depends_on":["broker"]}]}`,
		"v3": strings.Replace(strings.Replace(valid, `"schema_version":1`, `"schema_version":3`, 1), `"restart":"unless-stopped"`, `"hardware":[{"class":"gpu.nvidia","optional":true,"cpu_fallback":true}],"restart":"unless-stopped"`, 1),
		"v4": strings.Replace(strings.Replace(valid, `"schema_version":1`, `"schema_version":4`, 1), `"restart":"unless-stopped"`, `"external_storage":[{"id":"media","container_path":"/media","mode":"read-only","purpose":"Media library"}],"restart":"unless-stopped"`, 1),
		"v5": strings.Replace(strings.Replace(valid, `"schema_version":1`, `"schema_version":5`, 1), `"restart":"unless-stopped"`, `"run_as":{"uid":1000,"gid":1000},"restart":"unless-stopped"`, 1),
		"v6": strings.Replace(strings.Replace(valid, `"schema_version":1`, `"schema_version":6`, 1), `"restart":"unless-stopped"`, `"restart":"unless-stopped","backup":{"strategy":"cold-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, 1),
		"v7": validV7Manifest(),
	}
	for name, data := range versions {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err != nil {
				t.Fatalf("schema %s no longer parses: %v", name, err)
			}
		})
	}
}

func TestNetworkBindingSchemaValidation(t *testing.T) {
	base := strings.Replace(validV7Manifest(), `"schema_version":7`, `"schema_version":8`, 1)
	base = strings.Replace(base, `"restart":"unless-stopped"`, `"services":[{"id":"dns-tcp","name":"DNS TCP","protocol":"tcp","container_port":53,"fixed_host_port":53},{"id":"dns-udp","name":"DNS UDP","protocol":"udp","container_port":53,"fixed_host_port":53}],"restart":"unless-stopped"`, 1)
	manifest, err := Parse([]byte(base))
	if err != nil {
		t.Fatalf("valid TCP and UDP fixed bindings rejected: %v", err)
	}
	if len(manifest.Services) != 2 || manifest.Services[1].Protocol != "udp" {
		t.Fatalf("network services not parsed: %#v", manifest.Services)
	}
	if _, err = Parse([]byte(strings.Replace(base, `"schema_version":8`, `"schema_version":7`, 1))); err == nil {
		t.Fatal("schema v7 accepted fixed host port")
	}
	if _, err = Parse([]byte(strings.Replace(base, `"protocol":"udp"`, `"protocol":"quic"`, 1))); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	if _, err = Parse([]byte(strings.Replace(base, `"fixed_host_port":53`, `"fixed_host_port":0`, 1))); err != nil {
		t.Fatalf("zero fixed-port default rejected: %v", err)
	}
	defaults := strings.Replace(base, `"id":"dns-tcp","name":"DNS TCP","protocol":"tcp","container_port":53,"fixed_host_port":53`, `"id":"dns-tcp","name":"DNS TCP","protocol":"tcp","container_port":53,"default_exposure":"lan","fixed_host_port":53`, 1)
	defaults = strings.Replace(defaults, `"id":"dns-udp","name":"DNS UDP","protocol":"udp","container_port":53,"fixed_host_port":53`, `"id":"dns-udp","name":"DNS UDP","protocol":"udp","container_port":53,"default_exposure":"lan","fixed_host_port":53`, 1)
	if parsed, parseErr := Parse([]byte(defaults)); parseErr != nil || parsed.Services[0].DefaultExposure != "lan" {
		t.Fatalf("trusted default exposure rejected: %v %#v", parseErr, parsed.Services)
	}
	if _, err = Parse([]byte(strings.Replace(defaults, `"default_exposure":"lan","fixed_host_port":53`, `"default_exposure":"public","fixed_host_port":53`, 1))); err == nil {
		t.Fatal("unconstrained default exposure accepted")
	}
	if _, err = Parse([]byte(strings.Replace(defaults, `"default_exposure":"lan","fixed_host_port":53`, `"default_exposure":"lan"`, 1))); err == nil {
		t.Fatal("default LAN exposure without fixed port accepted")
	}
}

func TestSchemaEightManagedSystemConfigurationAndEnvironmentNames(t *testing.T) {
	base := strings.Replace(validV7Manifest(), `"schema_version":7`, `"schema_version":8`, 1)
	base = strings.Replace(base, `"container_path":"/data","persistent":true,"read_only":false`, `"container_path":"/etc/pihole","persistent":true,"read_only":false,"system_config":true`, 1)
	base = strings.Replace(base, `"restart":"unless-stopped"`, `"environment":[{"name":"FTLCONF_dns_listeningMode","value":"ALL"}],"restart":"unless-stopped"`, 1)
	m, err := Parse([]byte(base))
	if err != nil || !m.Storage[0].SystemConfig || m.Environment[0].Name != "FTLCONF_dns_listeningMode" {
		t.Fatalf("schema-v8 system configuration rejected: %v %#v", err, m)
	}
	for name, invalid := range map[string]string{
		"missing opt-in": strings.Replace(base, `,"system_config":true`, ``, 1),
		"older schema":   strings.Replace(base, `"schema_version":8`, `"schema_version":7`, 1),
		"read-only":      strings.Replace(base, `"read_only":false`, `"read_only":true`, 1),
		"broad etc":      strings.Replace(base, `"container_path":"/etc/pihole"`, `"container_path":"/etc"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, parseErr := Parse([]byte(invalid)); parseErr == nil {
				t.Fatal("unsafe system configuration accepted")
			}
		})
	}
	legacyMixedCase := strings.Replace(validV7Manifest(), `"restart":"unless-stopped"`, `"environment":[{"name":"FTLCONF_dns_listeningMode","value":"ALL"}],"restart":"unless-stopped"`, 1)
	if _, err = Parse([]byte(legacyMixedCase)); err == nil {
		t.Fatal("schema v7 accepted schema-v8 mixed-case environment names")
	}
}

func TestSchemaEightCredentialPresentationIsExplicitAndBounded(t *testing.T) {
	base := strings.Replace(validV7Manifest(), `"schema_version":7`, `"schema_version":8`, 1)
	internalOnly := strings.Replace(base, `"restart":"unless-stopped"`, `"environment":[{"name":"INTERNAL_KEY","secret":true,"required":true,"generate":"random-hex-32"}],"restart":"unless-stopped"`, 1)
	m, err := Parse([]byte(internalOnly))
	if err != nil || len(PresentedCredentials(m)) != 0 {
		t.Fatalf("internal generated secret became revealable: %v %#v", err, PresentedCredentials(m))
	}
	presented := strings.Replace(internalOnly, `"generate":"random-hex-32"`, `"generate":"random-hex-32","credential":{"id":"admin-password","label":"Admin password"}`, 1)
	m, err = Parse([]byte(presented))
	credential, found := FindPresentedCredential(m, "admin-password")
	if err != nil || !found || credential.Label != "Admin password" || credential.EnvironmentName != "INTERNAL_KEY" {
		t.Fatalf("credential presentation rejected: %v %#v", err, credential)
	}
	invalid := map[string]string{
		"older schema":           strings.Replace(presented, `"schema_version":8`, `"schema_version":7`, 1),
		"non-generated value":    strings.Replace(presented, `"secret":true,"required":true,"generate":"random-hex-32"`, `"value":"public"`, 1),
		"invalid public ID":      strings.Replace(presented, `"id":"admin-password"`, `"id":"INTERNAL_KEY"`, 1),
		"empty label":            strings.Replace(presented, `"label":"Admin password"`, `"label":""`, 1),
		"untrimmed label":        strings.Replace(presented, `"label":"Admin password"`, `"label":" Admin password"`, 1),
		"long label":             strings.Replace(presented, `"label":"Admin password"`, `"label":"`+strings.Repeat("a", 65)+`"`, 1),
		"long username":          strings.Replace(presented, `"label":"Admin password"`, `"label":"Admin password","username":"`+strings.Repeat("a", 65)+`"`, 1),
		"duplicate presentation": strings.Replace(presented, `}],"restart"`, `},{"name":"SECOND_KEY","secret":true,"required":true,"generate":"random-hex-32","credential":{"id":"admin-password","label":"Second"}}],"restart"`, 1),
	}
	for name, data := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, parseErr := Parse([]byte(data)); parseErr == nil {
				t.Fatal("invalid credential presentation accepted")
			}
		})
	}
}

func TestSchemaEightStructuredConfigurationPolicyIsNarrow(t *testing.T) {
	base := strings.Replace(validV7Manifest(), `"schema_version":7`, `"schema_version":8`, 1)
	base = strings.Replace(base, `"storage":[{"id":"data","container_path":"/data","persistent":true,"read_only":false}]`, `"storage":[{"id":"config","container_path":"/var/syncthing","persistent":true,"read_only":false}]`, 1)
	base = strings.Replace(base, `"restart":"unless-stopped"`, `"configuration":{"type":"syncthing-tcp-only-v1","storage_id":"config"},"restart":"unless-stopped"`, 1)
	base = strings.Replace(base, `"id":"data","disposition":"include"`, `"id":"config","disposition":"include"`, 1)
	m, err := Parse([]byte(base))
	if err != nil || m.Configuration == nil || m.Configuration.Type != "syncthing-tcp-only-v1" || m.Configuration.StorageID != "config" {
		t.Fatalf("structured configuration policy rejected: %v %#v", err, m.Configuration)
	}
	invalid := map[string]string{
		"older schema":      strings.Replace(base, `"schema_version":8`, `"schema_version":7`, 1),
		"unknown policy":    strings.Replace(base, `syncthing-tcp-only-v1`, `arbitrary-template`, 1),
		"missing storage":   strings.Replace(base, `"storage_id":"config"`, `"storage_id":"missing"`, 1),
		"read-only storage": strings.Replace(base, `"persistent":true`, `"persistent":true,"read_only":true`, 1),
		"wrong mount":       strings.Replace(base, `/var/syncthing`, `/config`, 1),
	}
	for name, data := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, parseErr := Parse([]byte(data)); parseErr == nil {
				t.Fatal("unsafe structured configuration policy accepted")
			}
		})
	}
}

func TestCatalogMetadataSchemaValidation(t *testing.T) {
	m, err := Parse([]byte(validV7Manifest()))
	if err != nil {
		t.Fatal(err)
	}
	if m.Category != "Developer Tools" || m.Kind != "application" || m.CatalogStatus != "standard" || m.Logo != "busybox" || len(m.Limitations) != 1 || m.LifecycleNotice == nil || !m.LifecycleNotice.RequireAcknowledgement {
		t.Fatalf("metadata was not parsed: %#v", m)
	}
	networkService := strings.Replace(validV7Manifest(), `"category":"Developer Tools"`, `"category":"Networking"`, 1)
	networkService = strings.Replace(networkService, `"kind":"application"`, `"kind":"network-service"`, 1)
	networkService = strings.Replace(networkService, `"catalog_status":"standard"`, `"catalog_status":"experimental"`, 1)
	if _, err := Parse([]byte(networkService)); err != nil {
		t.Fatalf("valid experimental network service metadata: %v", err)
	}
	productivity := strings.Replace(validV7Manifest(), `"category":"Developer Tools"`, `"category":"Productivity"`, 1)
	if _, err := Parse([]byte(productivity)); err != nil {
		t.Fatalf("valid Productivity category: %v", err)
	}

	tests := map[string]string{
		"category":        strings.Replace(validV7Manifest(), `"category":"Developer Tools"`, `"category":"Unreviewed"`, 1),
		"kind":            strings.Replace(validV7Manifest(), `"kind":"application"`, `"kind":"container"`, 1),
		"status":          strings.Replace(validV7Manifest(), `"catalog_status":"standard"`, `"catalog_status":"preview"`, 1),
		"http URL":        strings.Replace(validV7Manifest(), `https://example.com`, `http://example.com`, 1),
		"malformed URL":   strings.Replace(validV7Manifest(), `https://example.com`, `https://`, 1),
		"URL credentials": strings.Replace(validV7Manifest(), `https://example.com`, `https://user@example.com`, 1),
		"logo path":       strings.Replace(validV7Manifest(), `"logo":"busybox"`, `"logo":"../busybox"`, 1),
		"empty limitation": strings.Replace(validV7Manifest(),
			`"limitations":["No automatic public access."]`, `"limitations":[""]`, 1),
		"oversized limitation": strings.Replace(validV7Manifest(),
			`No automatic public access.`, strings.Repeat("x", 281), 1),
		"empty lifecycle notice": strings.Replace(validV7Manifest(),
			`"lifecycle_notice":{"install":"Review the application settings before installation.","stop":"Stopping interrupts service.","remove":"Stored data is retained.","require_acknowledgement":true}`, `"lifecycle_notice":{}`, 1),
		"missing backup": strings.Replace(validV7Manifest(),
			`,"backup":{"strategy":"cold-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, ``, 1),
	}
	tooMany := make([]string, 9)
	for i := range tooMany {
		tooMany[i] = `"limitation-` + string(rune('a'+i)) + `"`
	}
	tests["too many limitations"] = strings.Replace(validV7Manifest(), `"limitations":["No automatic public access."]`, `"limitations":[`+strings.Join(tooMany, ",")+`]`, 1)

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Fatalf("accepted invalid v7 metadata: %s", data)
			}
		})
	}
}

func TestCatalogMetadataRequiresSchemaSeven(t *testing.T) {
	data := strings.Replace(valid, `"name":"BusyBox"`, `"name":"BusyBox","category":"Developer Tools"`, 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("accepted catalog metadata in schema version 1")
	}
}

func TestExternalStorageIsTypedAndContainsNoHostPath(t *testing.T) {
	valid := []byte(`{"schema_version":4,"id":"media-app","name":"Media","releases":[{"version":"1","registry":"docker.io","repository":"example/media","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux/amd64"}],"external_storage":[{"id":"media","container_path":"/media","mode":"read-only","required":true,"purpose":"Media library"}]}`)
	if _, err := Parse(valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		[]byte(`{"schema_version":4,"id":"media-app","name":"Media","releases":[{"version":"1","registry":"docker.io","repository":"example/media","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux/amd64"}],"external_storage":[{"id":"media","container_path":"/media","mode":"raw","purpose":"Media"}]}`),
		[]byte(`{"schema_version":4,"id":"media-app","name":"Media","releases":[{"version":"1","registry":"docker.io","repository":"example/media","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux/amd64"}],"external_storage":[{"id":"media","container_path":"/media","mode":"read-only","purpose":"Media","host_path":"/etc"}]}`),
		[]byte(`{"schema_version":4,"id":"media-app","name":"Media","releases":[{"version":"1","registry":"docker.io","repository":"example/media","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux/amd64"}],"storage":[{"id":"config","container_path":"/data","persistent":true}],"external_storage":[{"id":"media","container_path":"/data","mode":"read-only","purpose":"Media"}]}`),
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("accepted unbounded external storage")
		}
	}
}

func TestMultiContainerManifestIsTypedAndDependencyChecked(t *testing.T) {
	data := `{"schema_version":2,"id":"paperless","name":"Paperless","releases":[{"version":"1","registry":"ghcr.io","repository":"paperless-ngx/paperless-ngx","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"components":[{"id":"db","release":"1","storage":[{"id":"data","container_path":"/var/lib/postgresql/data","persistent":true}],"services":[{"id":"postgres","name":"Postgres","protocol":"tcp","container_port":5432}]},{"id":"web","release":"1","depends_on":["db"],"storage":[{"id":"data","container_path":"/usr/src/paperless/data","persistent":true}],"services":[{"id":"web","name":"Web","protocol":"http","container_port":8000}]}]}`
	m, err := Parse([]byte(data))
	if err != nil || len(m.Components) != 2 {
		t.Fatalf("parse multi-container manifest: %v", err)
	}
	p, err := Resolve(m, "1", "inst-paperless01", "kitpro-net-inst-paperless01", "/srv/kitpro/apps/paperless/inst-paperless01/data")
	if err != nil || len(p.Components) != 2 {
		t.Fatalf("resolve multi-container plan: %v", err)
	}
	bad := strings.Replace(data, `"depends_on":["db"]`, `"depends_on":["missing"]`, 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("accepted dependency on unknown component")
	}
}

func TestDescriptionIsBoundedAndSingleLine(t *testing.T) {
	for _, description := range []string{strings.Repeat("x", 281), `line one\nline two`} {
		candidate := strings.Replace(valid, `"name":"BusyBox"`, `"name":"BusyBox","description":"`+description+`"`, 1)
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Fatalf("accepted invalid description of length %d", len(description))
		}
	}
}

func TestGeneratedSecretDeclaration(t *testing.T) {
	data := strings.Replace(valid, `"restart":"unless-stopped"`, `"environment":[{"name":"APP_SECRET","secret":true,"required":true,"generate":"random-hex-32"}],"restart":"unless-stopped"`, 1)
	m, err := Parse([]byte(data))
	if err != nil || len(m.Environment) != 1 || m.Environment[0].Generate != "random-hex-32" {
		t.Fatalf("generated secret declaration: %v %#v", err, m.Environment)
	}
	for _, invalid := range []string{
		`{"name":"APP_SECRET","secret":true,"required":true,"generate":"weak"}`,
		`{"name":"APP_SECRET","secret":true,"generate":"random-hex-32"}`,
		`{"name":"APP_SECRET","required":true,"generate":"random-hex-32"}`,
	} {
		candidate := strings.Replace(valid, `"restart":"unless-stopped"`, `"environment":[`+invalid+`],"restart":"unless-stopped"`, 1)
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Fatalf("accepted invalid generated secret: %s", invalid)
		}
	}
}
func TestRuntimeIdentityAndManagedStorageOwnership(t *testing.T) {
	data := `{"schema_version":5,"id":"media-app","name":"Media app","releases":[{"version":"1","registry":"ghcr.io","repository":"example/media","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"run_as":{"uid":1000,"gid":1000},"storage":[{"id":"data","container_path":"/data","persistent":true,"owner_uid":1000,"owner_gid":1000}],"services":[{"id":"web","name":"Web","protocol":"http","container_port":8080}]}`
	m, err := Parse([]byte(data))
	if err != nil || m.RunAs == nil || m.RunAs.UID != 1000 || m.Storage[0].OwnerGID != 1000 {
		t.Fatalf("runtime identity: %v %#v", err, m)
	}
	for _, bad := range []string{
		strings.Replace(data, `"uid":1000`, `"uid":0`, 1),
		strings.Replace(data, `,"owner_gid":1000`, `,"owner_gid":0`, 1),
		strings.Replace(data, `"schema_version":5`, `"schema_version":4`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("unsafe runtime identity accepted")
		}
	}
}
func TestBackupPolicyClassifiesEveryPersistentStorage(t *testing.T) {
	data := strings.Replace(valid, `"schema_version":1`, `"schema_version":6`, 1)
	data = strings.Replace(data, `"restart":"unless-stopped"`, `"restart":"unless-stopped","backup":{"strategy":"cold-sqlite-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, 1)
	m, err := Parse([]byte(data))
	if err != nil || m.Backup == nil || m.Backup.Strategy != "cold-sqlite-filesystem" {
		t.Fatalf("backup policy: %v %#v", err, m.Backup)
	}
	p, err := Resolve(m, "1.0", "inst-12345678", "kitpro-net-inst-12345678", "/srv/kitpro/apps/busybox/inst-12345678/data")
	if err != nil || p.Backup == nil || len(p.Backup.Storage) != 1 {
		t.Fatalf("resolved backup policy: %v %#v", err, p.Backup)
	}
	p.Backup.Storage[0].ID = "changed"
	if m.Backup.Storage[0].ID != "data" {
		t.Fatal("resolved plan aliases manifest backup policy")
	}

	for _, candidate := range []string{
		strings.Replace(data, `"backup":{"strategy":"cold-sqlite-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, `"backup":{"strategy":"shell","storage":[{"component":"app","id":"data","disposition":"include"}]}`, 1),
		strings.Replace(data, `"id":"data","disposition":"include"`, `"id":"missing","disposition":"include"`, 1),
		strings.Replace(data, `"disposition":"include"`, `"disposition":"skip"`, 1),
		strings.Replace(data, `,"backup":{"strategy":"cold-sqlite-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, ``, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"backup":{"strategy":"cold-filesystem","storage":[{"component":"app","id":"data","disposition":"include"}]}`, 1),
	} {
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Fatalf("accepted invalid backup policy: %s", candidate)
		}
	}
}

func TestMetadataOnlyBackupRejectsPersistentStorage(t *testing.T) {
	data := strings.Replace(valid, `"schema_version":1`, `"schema_version":6`, 1)
	data = strings.Replace(data, `"restart":"unless-stopped"`, `"restart":"unless-stopped","backup":{"strategy":"metadata-only"}`, 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("accepted metadata-only policy with persistent storage")
	}
	data = strings.Replace(data, `,"storage":[{"id":"data","container_path":"/data","persistent":true,"read_only":false}]`, ``, 1)
	if _, err := Parse([]byte(data)); err != nil {
		t.Fatalf("metadata-only policy: %v", err)
	}
}
func TestHardwareSchemaIsTypedAndBounded(t *testing.T) {
	data := strings.Replace(valid, `"schema_version":1`, `"schema_version":3`, 1)
	data = strings.Replace(data, `"restart":"unless-stopped"`, `"hardware":[{"class":"gpu.nvidia","optional":true,"cpu_fallback":true}],"restart":"unless-stopped"`, 1)
	m, err := Parse([]byte(data))
	if err != nil || len(m.Hardware) != 1 {
		t.Fatalf("hardware manifest: %v %#v", err, m.Hardware)
	}
	for _, class := range []string{"/dev/sda", "/dev/mem", "/dev/kvm", "usb", "gpu.unknown"} {
		candidate := strings.Replace(data, `gpu.nvidia`, class, 1)
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Fatalf("accepted unsafe hardware class %q", class)
		}
	}
	badFallback := strings.Replace(data, `"optional":true,`, `"optional":false,`, 1)
	if _, err := Parse([]byte(badFallback)); err == nil {
		t.Fatal("accepted fallback for required hardware")
	}
}
func TestManifestRejectsUnsafeVariants(t *testing.T) {
	cases := []string{`{"schema_version":2}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[],"unexpected":1}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[{"version":"1","registry":"docker.io","repository":"x","digest":"sha256:bad","platform":"linux/amd64"}]}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[{"version":"1","registry":"docker.io","repository":"x","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"storage":[{"id":"x","container_path":"/etc"}]}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[{"version":"1","registry":"docker.io","repository":"x","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"storage":[{"id":"x","container_path":"/data"},{"id":"x","container_path":"/other"}]}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[{"version":"1","registry":"docker.io","repository":"x","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05adfab0","platform":"linux/amd64"}],"command":["/bin/sh -c unsafe"]}`, `{"schema_version":1,"id":"busybox","name":"x","releases":[],"releases":[]}`}
	for _, c := range cases {
		if _, err := Parse([]byte(c)); err == nil {
			t.Errorf("accepted unsafe manifest: %s", c)
		}
	}
	for _, replacement := range []string{
		`"platform":"linux/arm64"`,
		`"restart":"always"`,
		`"restart":"no","privileged":true`,
		`"restart":"no","network_mode":"host","sysctls":{"x":"y"}`,
		`"restart":"no","secret":true,"value":"embedded"`,
	} {
		candidate := strings.Replace(valid, `"platform":"linux/amd64"`, replacement, 1)
		if replacement != `"platform":"linux/arm64"` {
			candidate = strings.Replace(valid, `"restart":"unless-stopped"`, replacement, 1)
		}
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Errorf("accepted unsafe variant: %s", replacement)
		}
	}
	for _, socket := range []string{"docker.sock", "podman.sock", "containerd.sock"} {
		candidate := strings.Replace(valid, `"container_path":"/data"`, `"container_path":"/var/run/`+socket+`"`, 1)
		if _, err := Parse([]byte(candidate)); err == nil {
			t.Errorf("accepted runtime socket storage: %s", socket)
		}
	}
	oversized := make([]byte, 65*1024)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if _, err := Parse(oversized); err == nil {
		t.Error("accepted oversized manifest")
	}
}
