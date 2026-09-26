package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	acmelifecycle "github.com/kitpro/kitpro/software/server/internal/acme"
	"github.com/kitpro/kitpro/software/server/internal/certificates"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

func helperTestCertificate(t *testing.T, hostname string, serial int64) acmelifecycle.IssuedMaterial {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(60 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return acmelifecycle.IssuedMaterial{CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
}

func TestACMERenewalRetainsWorkingCertificateOnActivationFailure(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	originalStore, originalIssue, originalReload := certificateStore, acmeWorkerIssue, reloadIngressCertificate
	defer func() {
		certificateStore, acmeWorkerIssue, reloadIngressCertificate = originalStore, originalIssue, originalReload
	}()
	certificateStore = func() certificates.MaterialStore { return certificates.MaterialStore{Root: root} }
	issued := []acmelifecycle.IssuedMaterial{helperTestCertificate(t, "app.example.test", 1), helperTestCertificate(t, "app.example.test", 2), helperTestCertificate(t, "app.example.test", 3)}
	call := 0
	acmeWorkerIssue = func(_ context.Context, request acmelifecycle.WorkerRequest) (acmelifecycle.IssuedMaterial, error) {
		if request.Hostname != "app.example.test" || request.Email != "admin@example.test" || request.Directory != acmelifecycle.DirectoryStaging || !request.AcceptTerms {
			t.Fatalf("unexpected worker request: %#v", request)
		}
		value := issued[call]
		call++
		return value, nil
	}
	request := protocol.Request{CertificateID: "cert-0123456789abcdef", Hostname: "app.example.test", ACMEEmail: "admin@example.test", ACMEDirectory: acmelifecycle.DirectoryStaging, AcceptACMETerms: true}
	if _, err = issueACMECertificate(context.Background(), db, request, false); err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(root, request.CertificateID, "fullchain.pem")
	first, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ingress_generations(generation,operation_id,configuration_sha256,status,generation_path,created_at) VALUES(1,'op-test','digest','active','/test','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ingress_generation_routes(generation,route_id,installation_id,service_id,hostname,backend_address,backend_port,exposure_mode,tls_policy,certificate_id) VALUES(1,'route-0123456789abcdef','inst-test','web','app.example.test','127.0.0.1',20001,'managed-https','acme',?)`, request.CertificateID); err != nil {
		t.Fatal(err)
	}
	reloadIngressCertificate = func(context.Context) error { return errors.New("synthetic reload failure") }
	if _, err = issueACMECertificate(context.Background(), db, protocol.Request{CertificateID: request.CertificateID, Hostname: request.Hostname}, true); err == nil {
		t.Fatal("renewal activation unexpectedly succeeded")
	}
	retained, err := os.ReadFile(certificatePath)
	if err != nil || string(retained) != string(first) {
		t.Fatal("failed renewal replaced the working certificate")
	}
	reloadIngressCertificate = func(context.Context) error { return nil }
	if _, err = issueACMECertificate(context.Background(), db, protocol.Request{CertificateID: request.CertificateID, Hostname: request.Hostname}, true); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(certificatePath)
	if err != nil || string(replaced) == string(first) {
		t.Fatal("successful renewal did not replace the certificate")
	}
	if _, err = os.Stat(filepath.Join(root, request.CertificateID) + ".previous"); !os.IsNotExist(err) {
		t.Fatal("successful renewal retained predecessor indefinitely")
	}
}
