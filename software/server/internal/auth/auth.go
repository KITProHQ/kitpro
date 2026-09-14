package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
)

const CookieName = "kitpro_session"
const CSRFCookieName = "kitpro_csrf"
const PasswordHashCost = 10
const MaxPasswordBytes = 72

type Config struct {
	IdleTimeout       time.Duration
	AbsoluteLifetime  time.Duration
	SeenWriteInterval time.Duration
	ThrottleWindow    time.Duration
	ThrottleBaseDelay time.Duration
	ThrottleMaxDelay  time.Duration
}

var DefaultConfig = Config{30 * time.Minute, 12 * time.Hour, 5 * time.Minute, 15 * time.Minute, 250 * time.Millisecond, 8 * time.Second}

func Init(ctx context.Context, db *sql.DB) error {
	_, e := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS administrator (id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, administrator_id INTEGER NOT NULL, csrf_hash TEXT NOT NULL, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT); CREATE TABLE IF NOT EXISTS login_throttle (key_hash TEXT PRIMARY KEY, failures INTEGER NOT NULL, next_allowed_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	return e
}
func validPassword(password string) bool {
	return len(password) >= 12 && len([]byte(password)) <= MaxPasswordBytes
}
func Setup(ctx context.Context, db *sql.DB, user, password string) error {
	if strings.TrimSpace(user) == "" || !validPassword(password) {
		return fmt.Errorf("invalid credentials")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), PasswordHashCost)
	if e != nil {
		return e
	}
	_, e = db.ExecContext(ctx, "INSERT INTO administrator(username,password_hash,created_at,updated_at) VALUES(?,?,?,?)", user, string(h), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func Verify(ctx context.Context, db *sql.DB, user, password string) bool {
	var h string
	e := db.QueryRowContext(ctx, "SELECT password_hash FROM administrator WHERE username=?", user).Scan(&h)
	if e != nil {
		h = dummyHash
	}
	candidate := []byte(password)
	if len(candidate) > MaxPasswordBytes {
		candidate = []byte("invalid-password")
	}
	return validPassword(password) && bcrypt.CompareHashAndPassword([]byte(h), candidate) == nil
}

const dummyHash = "$2b$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func HasAdmin(ctx context.Context, db *sql.DB) bool {
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM administrator").Scan(&n)
	return n > 0
}
func NewSession(ctx context.Context, db *sql.DB, admin int) (string, string, error) {
	return NewSessionWithConfig(ctx, db, admin, DefaultConfig)
}
func NewSessionWithConfig(ctx context.Context, db *sql.DB, admin int, cfg Config) (string, string, error) {
	raw := make([]byte, 32)
	csrf := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return "", "", e
	}
	if _, e := rand.Read(csrf); e != nil {
		return "", "", e
	}
	now := time.Now().UTC()
	_, e := db.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?,?,?,NULL)", hash(raw), admin, hash(csrf), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Add(cfg.AbsoluteLifetime).Format(time.RFC3339Nano))
	return hex.EncodeToString(raw), hex.EncodeToString(csrf), e
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Session(ctx context.Context, db *sql.DB, r *http.Request) (int, bool) {
	return SessionWithConfig(ctx, db, r, DefaultConfig)
}
func SessionWithConfig(ctx context.Context, db *sql.DB, r *http.Request, cfg Config) (int, bool) {
	c, e := r.Cookie(CookieName)
	if e != nil {
		return 0, false
	}
	raw, e := hex.DecodeString(c.Value)
	if e != nil {
		return 0, false
	}
	var id int
	var created, last, exp string
	e = db.QueryRowContext(ctx, "SELECT administrator_id,created_at,last_seen_at,expires_at FROM sessions WHERE token_hash=? AND revoked_at IS NULL", hash(raw)).Scan(&id, &created, &last, &exp)
	if e != nil {
		return 0, false
	}
	now := time.Now()
	ct, _ := time.Parse(time.RFC3339Nano, created)
	lt, _ := time.Parse(time.RFC3339Nano, last)
	et, _ := time.Parse(time.RFC3339Nano, exp)
	if now.After(et) || now.Sub(lt) > cfg.IdleTimeout || now.Sub(ct) > cfg.AbsoluteLifetime {
		return 0, false
	}
	if now.Sub(lt) > cfg.SeenWriteInterval {
		_, _ = db.ExecContext(ctx, "UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now.UTC().Format(time.RFC3339Nano), hash(raw))
	}
	return id, true
}
func CSRF(ctx context.Context, db *sql.DB, r *http.Request) bool {
	c, e := r.Cookie(CookieName)
	if e != nil {
		return false
	}
	tok := r.Header.Get("X-CSRF-Token")
	if tok == "" {
		tok = r.FormValue("csrf_token")
	}
	if tok == "" {
		return false
	}
	raw, e := hex.DecodeString(c.Value)
	if e != nil {
		return false
	}
	var want string
	e = db.QueryRowContext(ctx, "SELECT csrf_hash FROM sessions WHERE token_hash=? AND revoked_at IS NULL", hash(raw)).Scan(&want)
	if e != nil {
		return false
	}
	decoded, err := hex.DecodeString(tok)
	if err != nil {
		return false
	}
	got := hash(decoded)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
func Revoke(ctx context.Context, db *sql.DB, r *http.Request) {
	if c, e := r.Cookie(CookieName); e == nil {
		if b, e := hex.DecodeString(c.Value); e == nil {
			_, _ = db.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE token_hash=?", time.Now().UTC().Format(time.RFC3339Nano), hash(b))
		}
	}
}

func ChangePassword(ctx context.Context, db *sql.DB, admin int, current, next string) (string, string, error) {
	if !validPassword(next) {
		return "", "", fmt.Errorf("invalid password")
	}
	var old string
	if e := db.QueryRowContext(ctx, "SELECT password_hash FROM administrator WHERE id=?", admin).Scan(&old); e != nil || bcrypt.CompareHashAndPassword([]byte(old), []byte(current)) != nil {
		return "", "", fmt.Errorf("invalid credentials")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(next), PasswordHashCost)
	if e != nil {
		return "", "", e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return "", "", e
	}
	_, e = tx.ExecContext(ctx, "UPDATE administrator SET password_hash=?,updated_at=? WHERE id=?", string(h), time.Now().UTC().Format(time.RFC3339Nano), admin)
	if e == nil {
		_, e = tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE administrator_id=?", time.Now().UTC().Format(time.RFC3339Nano), admin)
	}
	if e != nil {
		tx.Rollback()
		return "", "", e
	}
	if e = tx.Commit(); e != nil {
		return "", "", e
	}
	return NewSession(ctx, db, admin)
}

func Cleanup(ctx context.Context, db *sql.DB, now time.Time) {
	_, _ = db.ExecContext(ctx, "DELETE FROM sessions WHERE revoked_at IS NOT NULL OR expires_at < ?", now.UTC().Format(time.RFC3339Nano))
}

func throttleKey(remote, username string) string {
	return hash([]byte(remote + "\x00" + strings.ToLower(strings.TrimSpace(username))))
}

// Login performs comparable bcrypt work for unknown users and applies bounded, expiring throttling.
func Login(ctx context.Context, db *sql.DB, username, password, remote string, cfg Config) (int, time.Duration, error) {
	key := throttleKey(remote, username)
	var failures int
	var next, updated string
	_ = db.QueryRowContext(ctx, "SELECT failures,next_allowed_at,updated_at FROM login_throttle WHERE key_hash=?", key).Scan(&failures, &next, &updated)
	now := time.Now().UTC()
	if t, e := time.Parse(time.RFC3339Nano, next); e == nil && now.Before(t) {
		return 0, t.Sub(now), fmt.Errorf("login throttled")
	}
	var id int
	var stored string
	e := db.QueryRowContext(ctx, "SELECT id,password_hash FROM administrator WHERE username=?", username).Scan(&id, &stored)
	if e != nil {
		stored = dummyHash
	}
	candidate := []byte(password)
	if len(candidate) > MaxPasswordBytes {
		candidate = []byte("invalid-password")
	}
	valid := validPassword(password) && bcrypt.CompareHashAndPassword([]byte(stored), candidate) == nil && e == nil
	if !valid {
		failures++
		if failures > 8 {
			failures = 8
		}
		delay := cfg.ThrottleBaseDelay * time.Duration(1<<(failures-1))
		if delay > cfg.ThrottleMaxDelay {
			delay = cfg.ThrottleMaxDelay
		}
		until := now.Add(delay)
		_, _ = db.ExecContext(ctx, `INSERT INTO login_throttle(key_hash,failures,next_allowed_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(key_hash) DO UPDATE SET failures=excluded.failures,next_allowed_at=excluded.next_allowed_at,updated_at=excluded.updated_at`, key, failures, until.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		_, _ = db.ExecContext(ctx, "DELETE FROM login_throttle WHERE updated_at < ?", now.Add(-cfg.ThrottleWindow).Format(time.RFC3339Nano))
		return 0, delay, fmt.Errorf("invalid credentials")
	}
	_, _ = db.ExecContext(ctx, "DELETE FROM login_throttle WHERE key_hash=?", key)
	return id, 0, nil
}
