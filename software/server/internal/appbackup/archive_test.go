package appbackup

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	return Manifest{
		Format:            FormatName,
		FormatVersion:     FormatVersion,
		KITProVersion:     "test",
		CreatedAt:         time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC),
		ApplicationID:     "fixture",
		InstallationID:    "inst-fixture01",
		ReleaseID:         "1",
		RuntimeGeneration: 2,
		Strategy:          "cold-filesystem",
		Components:        []Component{{ID: "app", ImageDigest: "docker.io/library/busybox@sha256:" + strings.Repeat("a", 64)}},
		Storage:           []Storage{{Component: "app", ID: "data", Disposition: "include", ArchivePath: "application/app/data", OwnerUID: os.Getuid(), OwnerGID: os.Getgid()}},
	}
}

func TestArchiveRoundTripAndChecksums(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "record.txt"), []byte("known state\n"), 0640); err != nil {
		t.Fatal(err)
	}
	readOnly := filepath.Join(source, "read-only")
	if err := os.Mkdir(readOnly, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readOnly, "nested.txt"), []byte("nested state\n"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnly, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0755) })
	archive := filepath.Join(root, "fixture.kitpro-backup.tar.gz")
	if _, err := Create(archive, testManifest(), []Source{{ArchivePath: "application/app/data", HostPath: source}}, []InlineFile{{ArchivePath: "metadata/installation.json", Contents: []byte("{}\n"), Mode: 0600}}); err != nil {
		t.Fatal(err)
	}
	measured, entries, err := Measure(archive, Limits{})
	if err != nil || measured <= int64(len("known state\n")) || entries != 7 {
		t.Fatalf("archive measurement: bytes=%d entries=%d err=%v", measured, entries, err)
	}
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("archive mode: %v %v", info, err)
	}
	destination := filepath.Join(root, "restore")
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(destination, "application", "app", "data", "read-only"), 0755)
	})
	result, err := Extract(archive, destination, ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.InstallationID != "inst-fixture01" || len(result.Checksums.Files) != 4 {
		t.Fatalf("unexpected result: %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(destination, "application", "app", "data", "record.txt"))
	if err != nil || string(data) != "known state\n" {
		t.Fatalf("restored data: %q %v", data, err)
	}
	nested, err := os.ReadFile(filepath.Join(destination, "application", "app", "data", "read-only", "nested.txt"))
	if err != nil || string(nested) != "nested state\n" {
		t.Fatalf("restored nested data: %q %v", nested, err)
	}
	info, err = os.Stat(filepath.Join(destination, "application", "app", "data", "read-only"))
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("restored nested mode: %v %v", info, err)
	}
}

func TestArchiveRejectsSourceSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(root, "bad.tar.gz"), testManifest(), []Source{{ArchivePath: "application/app/data", HostPath: source}}, nil); err == nil {
		t.Fatal("accepted symbolic link")
	}
	if _, err := os.Stat(filepath.Join(root, "bad.tar.gz.partial")); !os.IsNotExist(err) {
		t.Fatal("partial archive was not cleaned")
	}
}

func TestExtractRejectsUnsafeArchiveEntries(t *testing.T) {
	for name, typeflag := range map[string]byte{
		"../escape":                   tar.TypeReg,
		"kitpro-backup/../../escape":  tar.TypeReg,
		"kitpro-backup/application/x": tar.TypeSymlink,
	} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "bad.tar.gz")
			writeRawArchive(t, archive, []tar.Header{{Name: name, Typeflag: typeflag, Linkname: "/etc/passwd"}})
			if _, err := Extract(archive, filepath.Join(root, "restore"), ExtractOptions{}); err == nil {
				t.Fatalf("accepted unsafe entry %q", name)
			}
			if _, err := os.Stat(filepath.Join(root, "restore")); !os.IsNotExist(err) {
				t.Fatal("failed extraction destination was not cleaned")
			}
		})
	}
}

func TestExtractRejectsChecksumMismatchAndFormatVersion(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest()
	manifest.FormatVersion = 99
	if _, err := Create(filepath.Join(root, "unsupported.tar.gz"), manifest, nil, nil); err == nil {
		t.Fatal("created unsupported format")
	}

	archive := filepath.Join(root, "valid.tar.gz")
	if _, err := Create(archive, testManifest(), []Source{{ArchivePath: "application/app/data", HostPath: source}}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	tampered := filepath.Join(root, "tampered.tar.gz")
	if err := os.WriteFile(tampered, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(tampered, filepath.Join(root, "restore"), ExtractOptions{}); err == nil {
		t.Fatal("accepted tampered archive")
	}
}

func TestExtractEnforcesLimits(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "limit.tar.gz")
	writeRawArchive(t, archive, []tar.Header{{Name: "kitpro-backup/large", Typeflag: tar.TypeReg, Size: 10}})
	if _, err := Extract(archive, filepath.Join(root, "restore"), ExtractOptions{Limits: Limits{MaxEntries: 1, MaxFileBytes: 5, MaxTotalBytes: 5}}); err == nil {
		t.Fatal("accepted oversized entry")
	}
}

func writeRawArchive(t *testing.T, destination string, headers []tar.Header) {
	t.Helper()
	f, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := range headers {
		if err := tw.WriteHeader(&headers[i]); err != nil {
			t.Fatal(err)
		}
		if headers[i].Size > 0 {
			if _, err := tw.Write(make([]byte, headers[i].Size)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
