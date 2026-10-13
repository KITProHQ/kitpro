package acmelifecycle

import (
	"context"
	"crypto"
	"errors"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string) ([]string, error)

func (f resolverFunc) LookupHost(ctx context.Context, host string) ([]string, error) {
	return f(ctx, host)
}

type authorityFunc func(context.Context, Request, Publisher) (IssuedMaterial, error)

func (f authorityFunc) Issue(ctx context.Context, request Request, publisher Publisher) (IssuedMaterial, error) {
	return f(ctx, request, publisher)
}

type publisherStub struct{}

func (publisherStub) Present(string, string, string) error { return nil }
func (publisherStub) Clear(string, string)                 {}

func validRequest(t *testing.T) Request {
	t.Helper()
	key, _, err := NewAccountKey()
	if err != nil {
		t.Fatal(err)
	}
	return Request{Hostname: "app.example.test", Email: "admin@example.test", Directory: DirectoryStaging, AcceptTerms: true, AccountKey: key}
}

func TestManagerValidIssuancePath(t *testing.T) {
	manager := Manager{
		Resolver: resolverFunc(func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }),
		Authority: authorityFunc(func(_ context.Context, request Request, _ Publisher) (IssuedMaterial, error) {
			if request.Hostname != "app.example.test" {
				t.Fatalf("hostname=%q", request.Hostname)
			}
			return IssuedMaterial{CertificatePEM: []byte("certificate"), PrivateKeyPEM: []byte("private")}, nil
		}),
	}
	material, err := manager.Issue(context.Background(), validRequest(t), publisherStub{})
	if err != nil || len(material.PrivateKeyPEM) == 0 {
		t.Fatalf("material=%+v err=%v", material, err)
	}
}

func TestManagerRejectsInvalidAndUnresolvedHostnames(t *testing.T) {
	manager := Manager{Resolver: resolverFunc(func(context.Context, string) ([]string, error) { return nil, errors.New("unresolved") }), Authority: authorityFunc(func(context.Context, Request, Publisher) (IssuedMaterial, error) {
		t.Fatal("authority called")
		return IssuedMaterial{}, nil
	})}
	for _, hostname := range []string{"bad host.example", "*.example.test", "unresolved.example.test"} {
		request := validRequest(t)
		request.Hostname = hostname
		if _, err := manager.Issue(context.Background(), request, publisherStub{}); err == nil {
			t.Fatalf("hostname %q accepted", hostname)
		}
	}
}

func TestManagerReportsPrerequisiteAndIssuanceFailuresWithoutSecrets(t *testing.T) {
	request := validRequest(t)
	request.AccountKey = crypto.Signer(nil)
	manager := Manager{Resolver: resolverFunc(func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }), Authority: authorityFunc(func(context.Context, Request, Publisher) (IssuedMaterial, error) {
		return IssuedMaterial{}, errors.New("secret-ca-detail")
	})}
	if _, err := manager.Issue(context.Background(), request, publisherStub{}); err == nil {
		t.Fatal("missing key accepted")
	}
	request = validRequest(t)
	_, err := manager.Issue(context.Background(), request, publisherStub{})
	if err == nil || err.Error() == "secret-ca-detail" {
		t.Fatalf("unsafe issuance error=%v", err)
	}
}

func TestRenewalStateWarnsBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	if state, _ := RenewalState(now.Add(60*24*time.Hour), now); state != "active" {
		t.Fatalf("state=%s", state)
	}
	if state, _ := RenewalState(now.Add(20*24*time.Hour), now); state != "expiring" {
		t.Fatalf("state=%s", state)
	}
	if state, _ := RenewalState(now.Add(-time.Hour), now); state != "invalid" {
		t.Fatalf("state=%s", state)
	}
}
