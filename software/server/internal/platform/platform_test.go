package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRelease(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectFileClassifiesPlatformsWithoutIDLikeFallback(t *testing.T) {
	tests := []struct {
		name, body, id, runtime, orchestration, mac string
		support                                     Support
		installable                                 bool
	}{
		{"rocky10", "ID=rocky\nVERSION_ID=10.1\nPRETTY_NAME=\"Rocky Linux 10.1\"\n", "rocky", "podman", "quadlet", "selinux", Experimental, true},
		{"rhel10", "ID=rhel\nVERSION_ID=10.0\n", "rhel", "podman", "quadlet", "selinux", Experimental, false},
		{"alma10", "ID=almalinux\nVERSION_ID=10\n", "almalinux", "podman", "quadlet", "selinux", Experimental, false},
		{"debian13", "ID=debian\nVERSION_ID=\"13\"\n", "debian", "docker", "compose", "apparmor", Supported, true},
		{"ubuntu2604", "ID=ubuntu\nVERSION_ID=26.04\n", "ubuntu", "docker", "compose", "apparmor", Supported, true},
		{"arch", "ID=arch\nID_LIKE=unknown\n", "arch", "docker", "compose", "apparmor", Supported, true},
		{"unknown-like-rocky", "ID=custom\nID_LIKE=\"rhel fedora\"\nVERSION_ID=10\n", "custom", "", "", "", Unsupported, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DetectFile(writeRelease(t, test.body))
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != test.id || got.ContainerRuntime != test.runtime || got.Orchestration != test.orchestration || got.MAC != test.mac || got.Support != test.support || got.Installable != test.installable {
				t.Fatalf("unexpected classification: %#v", got)
			}
		})
	}
}

func TestDetectFileRejectsMalformedInput(t *testing.T) {
	for _, body := range []string{"ID rocky\n", "bad-key=value\n", "ID=\"unterminated\n"} {
		if _, err := DetectFile(writeRelease(t, body)); err == nil {
			t.Fatalf("malformed os-release accepted: %q", body)
		}
	}
}
