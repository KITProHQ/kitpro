package auth

import (
	"context"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupAndSession(t *testing.T) {
	db, e := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Init(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if HasAdmin(context.Background(), db) {
		t.Fatal("unexpected admin")
	}
	if e = Setup(context.Background(), db, "admin", "correct horse battery staple"); e != nil {
		t.Fatal(e)
	}
	if !Verify(context.Background(), db, "admin", "correct horse battery staple") {
		t.Fatal("password failed")
	}
	tok, _, e := NewSession(context.Background(), db, 1)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	if _, ok := Session(context.Background(), db, r); !ok {
		t.Fatal("session invalid")
	}
}

func TestCSRFCookieAloneIsNotProof(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Init(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = Setup(context.Background(), db, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	token, csrf, err := NewSession(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mutation", nil)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	request.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: csrf})
	if CSRF(context.Background(), db, request) {
		t.Fatal("CSRF cookie alone authorized a mutation")
	}
	request.Header.Set("X-CSRF-Token", csrf)
	if !CSRF(context.Background(), db, request) {
		t.Fatal("explicit CSRF token was rejected")
	}
}

func TestPasswordByteLimit(t *testing.T) {
	db, e := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Init(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if e = Setup(context.Background(), db, "admin", string(make([]byte, 72))); e != nil {
		t.Fatal(e)
	}
	if Verify(context.Background(), db, "admin", string(make([]byte, 73))) {
		t.Fatal("73-byte password unexpectedly accepted")
	}
	if validPassword(string(make([]byte, 72))) == false || validPassword(string(make([]byte, 73))) {
		t.Fatal("byte limit incorrect")
	}
	if !validPassword("pässword-123") {
		t.Fatal("multibyte password below limit rejected")
	}
	if validPassword(strings.Repeat("界", 25)) {
		t.Fatal("multibyte password above byte limit accepted")
	}
}

func TestSessionConfigExpiryAndThrottle(t *testing.T) {
	db, e := state.Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_ = Init(context.Background(), db)
	if e = Setup(context.Background(), db, "admin", "correct horse battery staple"); e != nil {
		t.Fatal(e)
	}
	cfg := DefaultConfig
	cfg.IdleTimeout = 20 * time.Millisecond
	cfg.AbsoluteLifetime = time.Hour
	cfg.SeenWriteInterval = 0
	cfg.ThrottleBaseDelay = time.Millisecond
	cfg.ThrottleMaxDelay = 4 * time.Millisecond
	tok, _, e := NewSessionWithConfig(context.Background(), db, 1, cfg)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	time.Sleep(30 * time.Millisecond)
	if _, ok := SessionWithConfig(context.Background(), db, r, cfg); ok {
		t.Fatal("expired idle session accepted")
	}
	if _, _, e = Login(context.Background(), db, "admin", "wrong password", "127.0.0.1:1", cfg); e == nil {
		t.Fatal("wrong password accepted")
	}
	if _, delay, e := Login(context.Background(), db, "admin", "wrong password", "127.0.0.1:1", cfg); e == nil || delay <= 0 {
		t.Fatal("throttle not applied")
	}
	time.Sleep(5 * time.Millisecond)
	if _, _, e = Login(context.Background(), db, "admin", "correct horse battery staple", "127.0.0.1:1", cfg); e != nil {
		t.Fatalf("correct login after cooldown failed: %v", e)
	}
	if _, _, e = Login(context.Background(), db, "nobody", "wrong password", "127.0.0.2:1", cfg); e == nil {
		t.Fatal("unknown user accepted")
	}
	if _, delay, e := Login(context.Background(), db, "nobody", "wrong password", "127.0.0.2:1", cfg); e == nil || delay <= 0 {
		t.Fatal("unknown-user throttle not applied")
	}
}
