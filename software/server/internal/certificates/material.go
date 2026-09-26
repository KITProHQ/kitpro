package certificates

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Metadata struct {
	CertificateID      string    `json:"certificate_id"`
	Hostname           string    `json:"hostname"`
	CertificateSHA256  string    `json:"certificate_sha256"`
	PublicKeySHA256    string    `json:"public_key_sha256"`
	NotBefore          time.Time `json:"not_before"`
	NotAfter           time.Time `json:"not_after"`
	RenewalRecommended bool      `json:"renewal_recommended"`
}

type MaterialStore struct{ Root string }

func (s MaterialStore) Install(certificateID, hostname string, certificatePEM, privateKeyPEM []byte, now time.Time) (Metadata, error) {
	if !validID(certificateID, "cert-") || !validHostname(hostname) {
		return Metadata{}, fmt.Errorf("invalid certificate identity")
	}
	leaf, chain, key, err := validateMaterial(hostname, certificatePEM, privateKeyPEM, now)
	if err != nil {
		return Metadata{}, err
	}
	root := s.Root
	if root == "" {
		root = "/var/lib/kitpro-helper/ingress/certificates"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Metadata{}, fmt.Errorf("invalid certificate store")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return Metadata{}, fmt.Errorf("create certificate store: %w", err)
	}
	staging, err := os.MkdirTemp(root, "."+certificateID+"-")
	if err != nil {
		return Metadata{}, fmt.Errorf("stage certificate material: %w", err)
	}
	defer os.RemoveAll(staging)
	if err = os.Chmod(staging, 0700); err != nil {
		return Metadata{}, err
	}
	if err = writeExclusive(filepath.Join(staging, "fullchain.pem"), certificatePEM, 0644); err != nil {
		return Metadata{}, err
	}
	if err = writeExclusive(filepath.Join(staging, "privkey.pem"), privateKeyPEM, 0600); err != nil {
		return Metadata{}, err
	}
	target := filepath.Join(root, certificateID)
	previous := target + ".previous"
	_ = os.RemoveAll(previous)
	if _, statErr := os.Stat(target); statErr == nil {
		if err = os.Rename(target, previous); err != nil {
			return Metadata{}, fmt.Errorf("preserve previous certificate: %w", err)
		}
	}
	if err = os.Rename(staging, target); err != nil {
		_ = os.Rename(previous, target)
		return Metadata{}, fmt.Errorf("activate certificate: %w", err)
	}
	if err = syncDirectory(root); err != nil {
		return Metadata{}, err
	}
	certDigest := sha256.Sum256(chain)
	publicDigest := sha256.Sum256(key)
	return Metadata{CertificateID: certificateID, Hostname: hostname, CertificateSHA256: hex.EncodeToString(certDigest[:]), PublicKeySHA256: hex.EncodeToString(publicDigest[:]), NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(), RenewalRecommended: leaf.NotAfter.Before(now.Add(30 * 24 * time.Hour))}, nil
}

func validateMaterial(hostname string, certificatePEM, privateKeyPEM []byte, now time.Time) (*x509.Certificate, []byte, []byte, error) {
	if len(certificatePEM) == 0 || len(certificatePEM) > 256*1024 || len(privateKeyPEM) == 0 || len(privateKeyPEM) > 64*1024 {
		return nil, nil, nil, fmt.Errorf("invalid certificate material size")
	}
	var certificates []*x509.Certificate
	rest := certificatePEM
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, nil, nil, fmt.Errorf("certificate bundle contains unsupported PEM data")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("parse certificate: %w", err)
		}
		certificates = append(certificates, certificate)
		rest = remaining
	}
	if len(certificates) == 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, nil, fmt.Errorf("invalid certificate PEM")
	}
	leaf := certificates[0]
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.VerifyHostname(hostname) != nil {
		return nil, nil, nil, fmt.Errorf("certificate is not currently valid for hostname")
	}
	keyBlock, keyRest := pem.Decode(privateKeyPEM)
	if keyBlock == nil || len(bytes.TrimSpace(keyRest)) != 0 || !strings.Contains(keyBlock.Type, "PRIVATE KEY") {
		return nil, nil, nil, fmt.Errorf("invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		if rsaKey, rsaErr := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); rsaErr == nil {
			key = rsaKey
		} else if ecKey, ecErr := x509.ParseECPrivateKey(keyBlock.Bytes); ecErr == nil {
			key = ecKey
		} else {
			return nil, nil, nil, fmt.Errorf("parse private key")
		}
	}
	public, err := x509.MarshalPKIXPublicKey(publicKey(key))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode public key")
	}
	leafPublic, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil || !bytes.Equal(public, leafPublic) {
		return nil, nil, nil, fmt.Errorf("certificate and private key do not match")
	}
	return leaf, certificatePEM, public, nil
}

func publicKey(key any) any {
	if value, ok := key.(crypto.Signer); ok {
		return value.Public()
	}
	return nil
}

func writeExclusive(path string, value []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(value); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+16 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func validHostname(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || strings.ContainsAny(value, " /\\:\r\n\t") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
