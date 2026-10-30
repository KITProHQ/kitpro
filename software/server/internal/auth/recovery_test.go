package auth

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/state"
)

func TestResetPasswordRevokesEverySessionAndAuditsWithoutPlaintext(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if err = Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	const oldPassword = "old-password-123"
	const newPassword = "new-password-456"
	if err = Setup(ctx, db, "admin", oldPassword); err != nil {
		t.Fatal(err)
	}
	var administratorID int
	if err = db.QueryRow(`SELECT id FROM administrator WHERE username='admin'`).Scan(&administratorID); err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, 0, 2)
	for range 2 {
		token, _, sessionErr := NewSession(ctx, db, administratorID)
		if sessionErr != nil {
			t.Fatal(sessionErr)
		}
		tokens = append(tokens, token)
	}
	result, err := ResetPassword(ctx, db, "admin", newPassword)
	if err != nil || result.SessionsRevoked != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if Verify(ctx, db, "admin", oldPassword) || !Verify(ctx, db, "admin", newPassword) {
		t.Fatal("password replacement did not take effect")
	}
	for _, token := range tokens {
		request := &http.Request{Header: http.Header{}}
		request.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		if _, ok := Session(ctx, db, request); ok {
			t.Fatal("pre-recovery session remained active")
		}
	}
	var detail string
	if err = db.QueryRow(`SELECT detail_json FROM audit_events WHERE event_type='administrator-password-reset'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "{}" {
		t.Fatalf("audit detail contains unexpected data: %q", detail)
	}
	var leaked int
	for _, secret := range []string{oldPassword, newPassword} {
		if err = db.QueryRow(`SELECT count(*) FROM audit_events WHERE detail_json LIKE '%'||?||'%'`, secret).Scan(&leaked); err != nil || leaked != 0 {
			t.Fatalf("plaintext password leaked into audit: count=%d err=%v", leaked, err)
		}
	}
}

func TestResetPasswordRejectsUnknownUserAndWeakPassword(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = state.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if err = Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = ResetPassword(ctx, db, "missing", "long-enough-password"); err == nil {
		t.Fatal("unknown administrator accepted")
	}
	if _, err = ResetPassword(ctx, db, "admin", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
}
