package hostbackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlbackup "github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func fixture(t *testing.T) (string, string, Result) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	for _, path := range []string{"ingress/bootstrap", "ingress/generations", "ingress/certificates/cert-0123456789abcdef", "acme", "providers/cloudflare/provider-0123456789abcdef", "backups"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ingress/bootstrap/nginx.conf"), []byte("bootstrap"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ingress/bootstrap"), filepath.Join(root, "ingress/current")); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{
		"ingress/certificates/cert-0123456789abcdef/key.pem":  "certificate-private-key",
		"ingress/certificates/cert-0123456789abcdef/cert.pem": "certificate-chain",
		"acme/account.key": "acme-account-private-key",
		"providers/cloudflare/provider-0123456789abcdef/api-token":    "cloudflare-api-token-0123456789",
		"providers/cloudflare/provider-0123456789abcdef/tunnel-token": "cloudflare-tunnel-token-012345",
	}
	for path, value := range secrets {
		if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := state.Open(filepath.Join(root, "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,generation_path,created_at) VALUES(1,'op-0123456789abcdef0123456789abcdef',?,'active','/var/lib/kitpro-helper/ingress/generations/1','now')`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), CreateOptions{DB: db, Destination: filepath.Join(root, "backups"), Name: "op-0123456789abcdef0123456789abcdef.kitpro-host-state.tar.gz", StateRoot: root, Now: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "helper.db"), result
}

func TestCreateAndRestoreSensitiveHostStateFailClosed(t *testing.T) {
	root, databasePath, result := fixture(t)
	if len(result.SHA256) != 64 || !result.Manifest.CertificatePrivateKeysIncluded || result.Manifest.CloudflareCredentialsIncluded || result.Manifest.RemoteCloudflareStateIncluded || result.Manifest.GeneratedIngressRuntimeIncluded {
		t.Fatalf("unsafe manifest: %+v", result.Manifest)
	}
	if info, err := os.Stat(result.Path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("archive mode=%v err=%v", info, err)
	}
	if err := os.WriteFile(filepath.Join(root, "acme/account.key"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "providers/cloudflare/provider-0123456789abcdef/api-token"), []byte("local-provider-state"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range databaseSidecarSuffixes {
		if err := os.WriteFile(databasePath+suffix, []byte("stale-sidecar"+suffix), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Restore(context.Background(), RestoreOptions{Archive: result.Path, StateRoot: root}); err != nil {
		t.Fatal(err)
	}
	if err := controlbackup.Verify(context.Background(), databasePath); err != nil {
		t.Fatalf("restored helper database is not independently valid: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(databasePath + suffix); !os.IsNotExist(err) {
			t.Fatalf("restored helper database retained sidecar %s: %v", suffix, err)
		}
	}
	if value, err := os.ReadFile(filepath.Join(root, "acme/account.key")); err != nil || string(value) != "acme-account-private-key" {
		t.Fatalf("restored ACME key=%q err=%v", value, err)
	}
	if value, err := os.ReadFile(filepath.Join(root, "providers/cloudflare/provider-0123456789abcdef/api-token")); err != nil || string(value) != "local-provider-state" {
		t.Fatalf("retired provider state was unexpectedly restored: value=%q err=%v", value, err)
	}
	for _, path := range []string{"acme/account.key", "ingress/certificates/cert-0123456789abcdef/key.pem"} {
		if info, err := os.Stat(filepath.Join(root, path)); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("restored mode for %s: %v err=%v", path, info, err)
		}
	}
	db, err := state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, failure string
	if err = db.QueryRow(`SELECT status,failure_code FROM ingress_generations WHERE generation=1`).Scan(&status, &failure); err != nil || status != "rolled_back" || failure != "restore_requires_reactivation" {
		t.Fatalf("generation status=%q failure=%q err=%v", status, failure, err)
	}
	target, err := os.Readlink(filepath.Join(root, "ingress/current"))
	if err != nil || target != filepath.Join(root, "ingress/bootstrap") {
		t.Fatalf("ingress current=%q err=%v", target, err)
	}
}

func TestCreateCompletesItsOwnOperationInRestoredSnapshot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	for _, path := range []string{"backups", "ingress/bootstrap"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ingress/bootstrap/nginx.conf"), []byte("bootstrap"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ingress/bootstrap"), filepath.Join(root, "ingress/current")); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "helper.db")
	db, err := state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	operationID := "op-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = db.Exec(`INSERT INTO helper_operations(operation_id,request_hash,request_json,protocol_version,operation_revision,operation_kind,installation_id,caller_uid,state,current_phase,outcome_confidence,recovery_disposition,fencing_token,accepted_at,started_at,updated_at)
		VALUES(?,?,'{}',2,1,'BackupHelperState','helper-state',1000,'executing','dispatching','not_dispatched','retry_after_validation',1,'now','now','now')`, operationID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO installation_leases(installation_id,operation_id,fencing_token,state,acquired_at,updated_at,recovery_required) VALUES('helper-state',?,1,'held','now','now',0)`, operationID); err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), CreateOptions{DB: db, Destination: filepath.Join(root, "backups"), Name: operationID + ".kitpro-host-state.tar.gz", OperationID: operationID, StateRoot: root, Now: time.Unix(2, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = Restore(context.Background(), RestoreOptions{Archive: result.Path, StateRoot: root}); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var operationState, phase, confidence, disposition, leaseState string
	var recoveryRequired int
	if err = db.QueryRow(`SELECT state,current_phase,outcome_confidence,recovery_disposition FROM helper_operations WHERE operation_id=?`, operationID).Scan(&operationState, &phase, &confidence, &disposition); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" || phase != "completed" || confidence != "confirmed_applied" || disposition != "completed" {
		t.Fatalf("restored backup operation = %q %q %q %q", operationState, phase, confidence, disposition)
	}
	if err = db.QueryRow(`SELECT state,recovery_required FROM installation_leases WHERE installation_id='helper-state'`).Scan(&leaseState, &recoveryRequired); err != nil {
		t.Fatal(err)
	}
	if leaseState != "released" || recoveryRequired != 0 {
		t.Fatalf("restored backup lease = %q recovery=%d", leaseState, recoveryRequired)
	}
}

func TestInterruptedRestoreRecoversPreRestoreState(t *testing.T) {
	root, databasePath, result := fixture(t)
	db, err := state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cloudflare_provider_state(provider_id,account_id,zone_id,tunnel_id,desired_state,runtime_state,updated_at) VALUES('provider-fedcba9876543210',?,?,?,'disabled','offline','now')`, strings.Repeat("a", 32), strings.Repeat("b", 32), "12345678-1234-4123-8123-123456789abc"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.WriteFile(filepath.Join(root, "acme/account.key"), []byte("pre-restore-current"), 0600); err != nil {
		t.Fatal(err)
	}
	sidecars := map[string][]byte{"-wal": []byte("pre-restore-wal"), "-shm": []byte("pre-restore-shm")}
	for suffix, contents := range sidecars {
		if err = os.WriteFile(databasePath+suffix, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = Restore(context.Background(), RestoreOptions{Archive: result.Path, StateRoot: root, Fault: func(point string) error {
		if point == "after-helper.db" {
			return ErrInterrupted
		}
		return nil
	}})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("restore error=%v", err)
	}
	if err = Recover(root); err != nil {
		t.Fatal(err)
	}
	for suffix, expected := range sidecars {
		actual, readErr := os.ReadFile(databasePath + suffix)
		if readErr != nil || string(actual) != string(expected) {
			t.Fatalf("pre-restore sidecar %s not recovered: %q err=%v", suffix, actual, readErr)
		}
		if err = os.Remove(databasePath + suffix); err != nil {
			t.Fatal(err)
		}
	}
	db, err = state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM cloudflare_provider_state WHERE provider_id='provider-fedcba9876543210'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pre-restore database not recovered count=%d err=%v", count, err)
	}
	if value, err := os.ReadFile(filepath.Join(root, "acme/account.key")); err != nil || string(value) != "pre-restore-current" {
		t.Fatalf("pre-restore secret not retained: %q err=%v", value, err)
	}
}

func TestExtractRejectsTraversalBeforeWritingOutsideStaging(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "malicious.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	payload := []byte("escape")
	if err = tarWriter.WriteHeader(&tar.Header{Name: archiveRoot + "/nested/../../escaped", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(payload))}); err == nil {
		_, err = tarWriter.Write(payload)
	}
	if closeErr := tarWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gzipWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "staging")
	if err = os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = extract(archive, destination); err == nil {
		t.Fatal("archive traversal was accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside staging: %v", err)
	}
}
