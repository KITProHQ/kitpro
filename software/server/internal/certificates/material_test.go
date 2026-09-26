package certificates

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testMaterial(t *testing.T, hostname string, now time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(60 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, key)})
	return certificate, privateKey
}

func mustPKCS8(t *testing.T, key any) []byte {
	t.Helper()
	value, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestInstallValidatesAndProtectsPrivateKey(t *testing.T) {
	now := time.Now().UTC()
	certificate, privateKey := testMaterial(t, "app.example.test", now)
	root := t.TempDir()
	metadata, err := (MaterialStore{Root: root}).Install("cert-0123456789abcdef", "app.example.test", certificate, privateKey, now)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.NotAfter.Before(now.Add(59*24*time.Hour)) || metadata.CertificateSHA256 == "" || metadata.PublicKeySHA256 == "" {
		t.Fatalf("metadata=%+v", metadata)
	}
	info, err := os.Stat(filepath.Join(root, "cert-0123456789abcdef", "privkey.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private key mode=%o", info.Mode().Perm())
	}
}

func TestInstallRejectsWrongHostnameAndKey(t *testing.T) {
	now := time.Now().UTC()
	certificate, privateKey := testMaterial(t, "app.example.test", now)
	if _, err := (MaterialStore{Root: t.TempDir()}).Install("cert-0123456789abcdef", "other.example.test", certificate, privateKey, now); err == nil {
		t.Fatal("wrong hostname accepted")
	}
	_, otherKey := testMaterial(t, "app.example.test", now)
	if _, err := (MaterialStore{Root: t.TempDir()}).Install("cert-0123456789abcdef", "app.example.test", certificate, otherKey, now); err == nil {
		t.Fatal("mismatched key accepted")
	}
}

func TestPrivateKeyIsNeverReturnedInMetadata(t *testing.T) {
	now := time.Now().UTC()
	certificate, privateKey := testMaterial(t, "app.example.test", now)
	metadata, err := (MaterialStore{Root: t.TempDir()}).Install("cert-0123456789abcdef", "app.example.test", certificate, privateKey, now)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.CertificateID == string(privateKey) || metadata.Hostname == string(privateKey) {
		t.Fatal("private key disclosed")
	}
}
