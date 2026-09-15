package manifest

import (
	"strings"
	"testing"
)

const valid = `{"schema_version":1,"id":"busybox","name":"BusyBox","releases":[{"version":"1.0","registry":"docker.io","repository":"library/busybox","digest":"sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0","platform":"linux/amd64"}],"storage":[{"id":"data","container_path":"/data","persistent":true,"read_only":false}],"restart":"unless-stopped"}`

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
	if _, err := Parse([]byte(strings.Replace(valid, `"container_path":"/data"`, `"container_path":"/var/run/docker.sock"`, 1))); err == nil {
		t.Error("accepted Docker socket storage")
	}
	oversized := make([]byte, 65*1024)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if _, err := Parse(oversized); err == nil {
		t.Error("accepted oversized manifest")
	}
}
