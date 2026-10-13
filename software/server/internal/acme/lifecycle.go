package acmelifecycle

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
	xacme "golang.org/x/crypto/acme"
)

const (
	DirectoryProduction = "letsencrypt-production"
	DirectoryStaging    = "letsencrypt-staging"
	stagingURL          = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

type Request struct {
	Hostname    string
	Email       string
	Directory   string
	AcceptTerms bool
	AccountKey  crypto.Signer
}

type IssuedMaterial struct {
	CertificatePEM []byte
	PrivateKeyPEM  []byte
}

type Resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type Publisher interface {
	Present(hostname, token, keyAuthorization string) error
	Clear(hostname, token string)
}

type Authority interface {
	Issue(context.Context, Request, Publisher) (IssuedMaterial, error)
}

type Manager struct {
	Resolver  Resolver
	Authority Authority
}

type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func (m Manager) Issue(ctx context.Context, request Request, publisher Publisher) (IssuedMaterial, error) {
	hostname, err := ingress.NormalizeHostname(request.Hostname)
	if err != nil || hostname != request.Hostname || strings.HasPrefix(hostname, "*.") {
		return IssuedMaterial{}, &Error{Code: "invalid_hostname", Err: errors.New("ACME hostname is invalid")}
	}
	address, err := mail.ParseAddress(request.Email)
	if err != nil || address.Address != request.Email || request.AccountKey == nil || !request.AcceptTerms {
		return IssuedMaterial{}, &Error{Code: "invalid_request", Err: errors.New("ACME contact and terms acceptance are required")}
	}
	if request.Directory != DirectoryProduction && request.Directory != DirectoryStaging {
		return IssuedMaterial{}, &Error{Code: "invalid_directory", Err: errors.New("ACME directory is not allowlisted")}
	}
	if m.Resolver == nil || m.Authority == nil || publisher == nil {
		return IssuedMaterial{}, &Error{Code: "not_configured", Err: errors.New("ACME lifecycle is not configured")}
	}
	addresses, err := m.Resolver.LookupHost(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return IssuedMaterial{}, &Error{Code: "hostname_unresolved", Err: errors.New("hostname does not resolve")}
	}
	for _, value := range addresses {
		if net.ParseIP(value) == nil {
			return IssuedMaterial{}, &Error{Code: "hostname_unresolved", Err: errors.New("hostname resolution returned invalid data")}
		}
	}
	material, err := m.Authority.Issue(ctx, request, publisher)
	if err != nil {
		var lifecycleError *Error
		if errors.As(err, &lifecycleError) {
			return IssuedMaterial{}, err
		}
		return IssuedMaterial{}, &Error{Code: "issuance_failed", Err: errors.New("certificate authority rejected issuance")}
	}
	if len(material.CertificatePEM) == 0 || len(material.PrivateKeyPEM) == 0 {
		return IssuedMaterial{}, &Error{Code: "issuance_failed", Err: errors.New("certificate authority returned empty material")}
	}
	return material, nil
}

type XCryptoAuthority struct{}

func (XCryptoAuthority) Issue(ctx context.Context, request Request, publisher Publisher) (IssuedMaterial, error) {
	directoryURL := xacme.LetsEncryptURL
	if request.Directory == DirectoryStaging {
		directoryURL = stagingURL
	}
	client := &xacme.Client{Key: request.AccountKey, DirectoryURL: directoryURL, UserAgent: "KITPro-Server/alpha.14"}
	if _, err := client.Register(ctx, &xacme.Account{Contact: []string{"mailto:" + request.Email}}, xacme.AcceptTOS); err != nil {
		if !errors.Is(err, xacme.ErrAccountAlreadyExists) {
			return IssuedMaterial{}, &Error{Code: "account_registration_failed", Err: errors.New("ACME account registration failed")}
		}
	}
	order, err := client.AuthorizeOrder(ctx, xacme.DomainIDs(request.Hostname))
	if err != nil {
		return IssuedMaterial{}, &Error{Code: "order_failed", Err: errors.New("ACME order failed")}
	}
	for _, authorizationURL := range order.AuthzURLs {
		authorization, fetchErr := client.GetAuthorization(ctx, authorizationURL)
		if fetchErr != nil {
			return IssuedMaterial{}, &Error{Code: "authorization_failed", Err: errors.New("ACME authorization unavailable")}
		}
		if authorization.Status == xacme.StatusValid {
			continue
		}
		var challenge *xacme.Challenge
		for _, candidate := range authorization.Challenges {
			if candidate.Type == "http-01" {
				challenge = candidate
				break
			}
		}
		if challenge == nil {
			return IssuedMaterial{}, &Error{Code: "challenge_unavailable", Err: errors.New("ACME server did not offer HTTP-01")}
		}
		keyAuthorization, responseErr := client.HTTP01ChallengeResponse(challenge.Token)
		if responseErr != nil {
			return IssuedMaterial{}, &Error{Code: "challenge_failed", Err: errors.New("ACME challenge response failed")}
		}
		if err = publisher.Present(request.Hostname, challenge.Token, keyAuthorization); err != nil {
			return IssuedMaterial{}, &Error{Code: "challenge_publish_failed", Err: errors.New("ACME challenge could not be published")}
		}
		if _, err = client.Accept(ctx, challenge); err == nil {
			_, err = client.WaitAuthorization(ctx, authorization.URI)
		}
		publisher.Clear(request.Hostname, challenge.Token)
		if err != nil {
			return IssuedMaterial{}, &Error{Code: "challenge_failed", Err: errors.New("ACME challenge validation failed")}
		}
	}
	certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return IssuedMaterial{}, &Error{Code: "key_generation_failed", Err: errors.New("certificate key generation failed")}
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{request.Hostname}}, certificateKey)
	if err != nil {
		return IssuedMaterial{}, &Error{Code: "csr_failed", Err: errors.New("certificate request generation failed")}
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil || len(chain) == 0 {
		return IssuedMaterial{}, &Error{Code: "issuance_failed", Err: errors.New("ACME certificate issuance failed")}
	}
	var certificatePEM []byte
	for _, certificate := range chain {
		certificatePEM = append(certificatePEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(certificateKey)
	if err != nil {
		return IssuedMaterial{}, &Error{Code: "key_encoding_failed", Err: errors.New("certificate key encoding failed")}
	}
	return IssuedMaterial{CertificatePEM: certificatePEM, PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}, nil
}

func NewAccountKey() (crypto.Signer, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), nil
}

func ParseAccountKey(value []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(value)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("invalid ACME account key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid ACME account key")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("invalid ACME account key")
	}
	return signer, nil
}

func RenewalState(notAfter, now time.Time) (string, time.Time) {
	renewAt := notAfter.Add(-30 * 24 * time.Hour)
	switch {
	case !now.Before(notAfter):
		return "invalid", renewAt
	case !now.Before(renewAt):
		return "expiring", renewAt
	default:
		return "active", renewAt
	}
}
