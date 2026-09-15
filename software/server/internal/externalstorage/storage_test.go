package externalstorage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectRejectsProtectedAndSymlinkPaths(t *testing.T) {
	for _, path := range []string{"/", "/etc", "/dev", "/proc", "/var/lib/docker", "/var/lib/containerd", "/var/lib/containers", "/var/log", "/opt/data", "/tmp", "/mnt", "/media", "/data", "/srv", "/srv/kitpro", "/run/docker.sock", "relative", "/tmp/../etc"} {
		if _, err := Inspect(path); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	base := t.TempDir()
	real := filepath.Join(base, "media")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(link); err == nil {
		t.Fatal("accepted symbolic-link root")
	}
}

func TestInspectAndIdentityDrift(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	parents := []string{filepath.Dir(root)}
	id, err := InspectWithin(root, parents)
	if err != nil {
		t.Fatal(err)
	}
	if id.CanonicalPath != root || id.MountPoint == "" || id.Filesystem == "" {
		t.Fatalf("incomplete identity: %#v", id)
	}
	if _, err = ValidateWithin(id, root, parents); err != nil {
		t.Fatal(err)
	}
	id.Inode++
	if _, err = ValidateWithin(id, root, parents); err == nil {
		t.Fatal("accepted changed identity")
	}
}

func TestModesAreBounded(t *testing.T) {
	if !ValidMode(ReadOnly) || !ValidMode(ReadWrite) || ValidMode("rw") {
		t.Fatal("mode registry is not bounded")
	}
}
