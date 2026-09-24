package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/maintenance"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	hostplatform "github.com/kitpro/kitpro/software/server/internal/platform"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed web.html
var page string

//go:embed auth.html
var authPage string

//go:embed web.css
var webCSS string

//go:embed web.js
var webJS string

//go:embed catalog-assets
var catalogAssets embed.FS

type app struct {
	db            *sql.DB
	helper        string
	tmpl          *template.Template
	authTmpl      *template.Template
	authCfg       auth.Config
	secureCookies bool
	allowedHosts  map[string]bool
	catalog       map[string]catalog.Entry
	helperCall    func(protocol.Request) (protocol.Response, error)
	allocatePort  func(map[int]bool, string) (int, error)
	hostListeners func() ([]exposure.ServiceBinding, error)
	// beforeControlProjection is a deterministic fault-injection seam. A nil
	// hook has no production effect.
	beforeControlProjection func() error
}

type internalDispatchKey struct{}

type serviceStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Transport     string `json:"transport"`
	ContainerPort int    `json:"container_port"`
	Mode          string `json:"mode"`
	HostAddress   string `json:"host_address,omitempty"`
	HostPort      int    `json:"host_port,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	OpenEndpoint  string `json:"open_endpoint,omitempty"`
	ModeLabel     string `json:"-"`
}
type trustedRootView struct {
	ID, Name, Path, Mode, Filesystem string
	Available, NetworkBacked         bool
}
type storageSlotView struct {
	ID, Purpose, Mode string
	Roots             []trustedRootView
}
type installedStorageView struct {
	Purpose, RootName, Mode, Path string
	Available                     bool
}

// committedLifecycleResult is the control-plane projection of the helper's
// authoritative application-generation commit. The API must not project
// request intent as active state.
type committedLifecycleResult struct {
	Generation   int                       `json:"runtime_generation"`
	ReleaseID    string                    `json:"release_id"`
	RuntimeState string                    `json:"runtime_state"`
	Bindings     []exposure.ServiceBinding `json:"bindings"`
}

type reconciliationResult struct {
	InstallationID    string                    `json:"installation_id"`
	CheckedGeneration int                       `json:"checked_generation"`
	State             string                    `json:"reconciliation_state"`
	RuntimeState      string                    `json:"runtime_state"`
	ObservedAt        string                    `json:"observed_at"`
	MismatchCodes     []string                  `json:"mismatch_codes"`
	RecommendedAction string                    `json:"recommended_action"`
	Projection        *committedLifecycleResult `json:"committed_projection"`
}

func decodeReconciliationResult(value any) (reconciliationResult, error) {
	var result reconciliationResult
	encoded, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(encoded, &result)
	}
	if err != nil || result.InstallationID == "" || result.State == "" || result.ObservedAt == "" {
		return reconciliationResult{}, errors.New("helper returned no reconciliation result")
	}
	return result, nil
}

func decodeCommittedLifecycleResult(value any) (committedLifecycleResult, error) {
	var result committedLifecycleResult
	encoded, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(encoded, &result)
	}
	validState := result.RuntimeState == "running" || result.RuntimeState == "stopped" || result.RuntimeState == "runtime_removed"
	if err != nil || result.Generation < 1 || result.ReleaseID == "" || !validState || len(result.Bindings) > exposure.MaxBindings {
		return committedLifecycleResult{}, errors.New("helper returned no committed lifecycle result")
	}
	for _, binding := range result.Bindings {
		if binding.ServiceID == "" || binding.ContainerPort < 1 || (binding.Transport != exposure.TCP && binding.Transport != exposure.UDP) {
			return committedLifecycleResult{}, errors.New("helper returned invalid committed bindings")
		}
	}
	return result, nil
}

func main() {
	if handleMaintenance(os.Args[1:]) {
		return
	}
	db, e := state.Open(env("KITPRO_CONTROL_DB", "/var/lib/kitpro-api/control.db"))
	if e != nil {
		panic(e)
	}
	defer db.Close()
	if e = state.Migrate(context.Background(), db, false); e != nil {
		panic(e)
	}
	if e = auth.Init(context.Background(), db); e != nil {
		panic(e)
	}
	if _, e = operations.RecoverAccepted(context.Background(), db); e != nil {
		panic(e)
	}
	allowed := map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true}
	for _, h := range strings.Split(env("KITPRO_ALLOWED_HOSTS", ""), ",") {
		if h = strings.TrimSpace(h); h != "" {
			allowed[h] = true
		}
	}
	a := &app{db: db, helper: env("KITPRO_HELPER_SOCKET", "/run/kitpro/helper.sock"), tmpl: template.Must(template.New("page").Parse(page)), authTmpl: template.Must(template.New("auth").Parse(authPage)), authCfg: auth.DefaultConfig, secureCookies: env("KITPRO_SECURE_COOKIES", "") == "1", allowedHosts: allowed}
	a.catalog, e = catalog.Load()
	if e != nil {
		panic(e)
	}
	if v := os.Getenv("KITPRO_AUTH_IDLE"); v != "" {
		if d, e := time.ParseDuration(v); e == nil {
			a.authCfg.IdleTimeout = d
		}
	}
	if v := os.Getenv("KITPRO_AUTH_ABSOLUTE"); v != "" {
		if d, e := time.ParseDuration(v); e == nil {
			a.authCfg.AbsoluteLifetime = d
		}
	}
	auth.Cleanup(context.Background(), db, time.Now())
	http.HandleFunc("/assets/kitpro.css", a.asset("text/css; charset=utf-8", webCSS))
	http.HandleFunc("/assets/kitpro.js", a.asset("text/javascript; charset=utf-8", webJS))
	http.HandleFunc("/assets/catalog/", a.catalogAsset)
	http.HandleFunc("/setup", a.setup)
	http.HandleFunc("/login", a.login)
	http.HandleFunc("/logout", a.guard(a.logout))
	http.HandleFunc("/account/password", a.guard(a.password))
	http.HandleFunc("/", a.guard(a.home))
	http.HandleFunc("/api/v1/operations", a.guard(a.ops))
	http.HandleFunc("/api/v1/operations/", a.guard(a.ops))
	http.HandleFunc("/api/v1/apps", a.guard(a.apps))
	http.HandleFunc("/api/v1/apps/", a.guard(a.apps))
	http.HandleFunc("/api/v1/installations/", a.guard(a.installations))
	http.HandleFunc("/api/v1/host", a.guard(a.host))
	http.HandleFunc("/api/v1/hardware", a.guard(a.hardware))
	http.HandleFunc("/api/v1/storage-roots", a.guard(a.storageRoots))
	http.HandleFunc("/api/v1/storage-roots/", a.guard(a.storageRoots))
	http.HandleFunc("/api/v1/backup", a.guard(a.backup))
	http.HandleFunc("/api/v1/version", a.guard(a.version))
	http.ListenAndServe(env("KITPRO_API_ADDR", "127.0.0.1:8080"), nil)
}

type authPageData struct {
	Title, Eyebrow, Heading, Description string
	NeedsCurrentPassword, NeedsUsername  bool
	PasswordLabel, PasswordName          string
	PasswordAutocomplete                 string
	MinimumLength                        bool
	CSRF, SubmitLabel, Footer, Error     string
}

func (a *app) securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (a *app) asset(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.allowedHosts[r.Host] {
			http.Error(w, "host denied", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		a.securityHeaders(w)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, body)
		}
	}
}

func (a *app) catalogAsset(w http.ResponseWriter, r *http.Request) {
	if !a.allowedHosts[r.Host] {
		http.Error(w, "host denied", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/assets/catalog/")
	if !validCatalogLogoFilename(name) {
		http.NotFound(w, r)
		return
	}
	body, err := catalogAssets.ReadFile("catalog-assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.securityHeaders(w)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func validCatalogLogoFilename(name string) bool {
	if len(name) < len("a.svg") || len(name) > 68 || !strings.HasSuffix(name, ".svg") {
		return false
	}
	key := strings.TrimSuffix(name, ".svg")
	for i, r := range key {
		if i == 0 && (r < 'a' || r > 'z') {
			return false
		}
		if i > 0 && !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func (a *app) renderAuth(w http.ResponseWriter, status int, data authPageData) {
	a.securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := a.authTmpl.Execute(w, data); err != nil {
		slog.Default().Error("auth page render failed", "event", "ui_render_failed", "error", err)
	}
}

func handleMaintenance(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version":
		fmt.Println(buildinfo.String("kitpro-api"))
		return true
	case "--migrate-only":
		if len(args) != 1 {
			fatalMaintenance("--migrate-only accepts no arguments")
		}
		db, err := state.Open(env("KITPRO_CONTROL_DB", "/var/lib/kitpro-api/control.db"))
		if err == nil {
			defer db.Close()
			err = state.Migrate(context.Background(), db, false)
		}
		if err == nil {
			err = auth.Init(context.Background(), db)
		}
		if err != nil {
			fatalMaintenance(err.Error())
		}
		slog.Info("database migration completed", "component", "api", "event", "migration_completed", "result", "success")
		return true
	case "--prepare-upgrade":
		if len(args) != 2 {
			fatalMaintenance("--prepare-upgrade requires target version")
		}
		dbPath := env("KITPRO_CONTROL_DB", "/var/lib/kitpro-api/control.db")
		db, err := state.Open(dbPath)
		if err == nil {
			defer db.Close()
			var result maintenance.Result
			result, err = maintenance.PrepareUpgrade(context.Background(), db, env("KITPRO_CONTROL_BACKUP_DIR", "/var/lib/kitpro-api/backups"), "control", args[1], time.Now())
			if err == nil {
				slog.Info("upgrade backup completed", "component", "api", "event", "upgrade_backup_completed", "result", "success", "schema_version", result.SchemaVersion, "path", result.BackupPath)
			}
		}
		if err != nil {
			fatalMaintenance(err.Error())
		}
		return true
	case "--verify-database":
		if len(args) != 1 {
			fatalMaintenance("--verify-database accepts no arguments")
		}
		if err := backup.Verify(context.Background(), env("KITPRO_CONTROL_DB", "/var/lib/kitpro-api/control.db")); err != nil {
			fatalMaintenance(err.Error())
		}
		slog.Info("database verification completed", "component", "api", "event", "database_verification_completed", "result", "success")
		return true
	default:
		fatalMaintenance("unknown argument")
	}
	return true
}

func fatalMaintenance(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
func (a *app) password(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		csrf := ""
		if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
			csrf = cookie.Value
		}
		a.renderAuth(w, http.StatusOK, passwordPage(csrf, ""))
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	id, ok := auth.SessionWithConfig(r.Context(), a.db, r, a.authCfg)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	tok, csrf, e := auth.ChangePassword(r.Context(), a.db, id, r.FormValue("current_password"), r.FormValue("new_password"))
	if e != nil {
		csrfToken := ""
		if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
			csrfToken = cookie.Value
		}
		a.renderAuth(w, http.StatusBadRequest, passwordPage(csrfToken, "The password could not be changed. Check your current password and try again."))
		return
	}
	a.setCookies(w, tok, csrf)
	http.Redirect(w, r, "/", 303)
}

func passwordPage(csrf, message string) authPageData {
	return authPageData{Title: "Change password · KITPro Server", Eyebrow: "Account settings", Heading: "Change your password", Description: "Update the password for your local administrator account.", NeedsCurrentPassword: true, PasswordLabel: "New password", PasswordName: "new_password", PasswordAutocomplete: "new-password", MinimumLength: true, CSRF: csrf, SubmitLabel: "Change password", Footer: "Your account and application data stay on this server.", Error: message}
}
func (a *app) setCookies(w http.ResponseWriter, tok, csrf string) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: tok, Path: "/", HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(a.authCfg.AbsoluteLifetime.Seconds())})
	http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: csrf, Path: "/", Secure: a.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(a.authCfg.AbsoluteLifetime.Seconds())})
}
func (a *app) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.allowedHosts[r.Host] {
			slog.Default().Warn("host rejected", "event", "host_rejected", "host", r.Host)
			http.Error(w, "host denied", 400)
			return
		}
		if _, ok := auth.SessionWithConfig(r.Context(), a.db, r, a.authCfg); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", 401)
			} else {
				http.Redirect(w, r, "/login", 302)
			}
			return
		}
		a.securityHeaders(w)
		if r.Method != "GET" && r.Method != "HEAD" {
			if o := r.Header.Get("Origin"); o == "" || !a.allowedOrigin(o) {
				slog.Default().Warn("origin rejected", "event", "origin_rejected", "origin", o)
				http.Error(w, "origin denied", 403)
				return
			}
			if !auth.CSRF(r.Context(), a.db, r) {
				slog.Default().Warn("csrf rejected", "event", "csrf_rejected", "path", r.URL.Path)
				http.Error(w, "csrf denied", 403)
				return
			}
		}
		next(w, r)
	}
}
func (a *app) allowedOrigin(origin string) bool {
	u := strings.TrimRight(origin, "/")
	return u == "http://"+rHost(a.allowedHosts) || u == "https://"+rHost(a.allowedHosts) || u == "http://127.0.0.1:8080" || u == "http://localhost:8080"
}
func rHost(m map[string]bool) string {
	for h := range m {
		if h != "localhost:8080" && h != "127.0.0.1:8080" {
			return h
		}
	}
	return "127.0.0.1:8080"
}
func (a *app) setup(w http.ResponseWriter, r *http.Request) {
	if !a.allowedHosts[r.Host] {
		http.Error(w, "host denied", 400)
		return
	}
	if auth.HasAdmin(r.Context(), a.db) {
		http.NotFound(w, r)
		return
	}
	if r.Method == "POST" {
		if e := auth.Setup(r.Context(), a.db, r.FormValue("username"), r.FormValue("password")); e != nil {
			a.renderAuth(w, http.StatusBadRequest, setupPage("Setup could not be completed. Choose a username and a password of at least 12 characters."))
			return
		}
		slog.Default().Info("setup completed", "event", "setup_completed", "username", r.FormValue("username"))
		http.Redirect(w, r, "/login", 303)
		return
	}
	a.renderAuth(w, http.StatusOK, setupPage(""))
}

func setupPage(message string) authPageData {
	return authPageData{Title: "Set up KITPro Server", Eyebrow: "Welcome to your server", Heading: "Create your local administrator", Description: "This account manages KITPro on this server. No cloud account is required.", NeedsUsername: true, PasswordLabel: "Password", PasswordName: "password", PasswordAutocomplete: "new-password", MinimumLength: true, SubmitLabel: "Create administrator", Footer: "Next, you will sign in and choose your first application. Your data stays on your server.", Error: message}
}
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if !a.allowedHosts[r.Host] {
		http.Error(w, "host denied", 400)
		return
	}
	if r.Method == "POST" {
		id, delay, e := auth.Login(r.Context(), a.db, r.FormValue("username"), r.FormValue("password"), r.RemoteAddr, a.authCfg)
		if e != nil {
			slog.Default().Warn("login failed", "event", "login_failure", "username", r.FormValue("username"))
			if delay > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(delay.Seconds()+1)))
				slog.Default().Warn("login throttled", "event", "login_throttled", "remote", r.RemoteAddr)
			}
			a.renderAuth(w, http.StatusUnauthorized, loginPage("The username or password was not recognized. Check your details and try again."))
			return
		}
		slog.Default().Info("login succeeded", "event", "login_success", "username", r.FormValue("username"))
		tok, csrf, e := auth.NewSessionWithConfig(r.Context(), a.db, id, a.authCfg)
		if e != nil {
			http.Error(w, "login failed", 500)
			return
		}
		a.setCookies(w, tok, csrf)
		http.Redirect(w, r, "/", 303)
		_ = csrf
		return
	}
	a.renderAuth(w, http.StatusOK, loginPage(""))
}

func loginPage(message string) authPageData {
	return authPageData{Title: "Sign in · KITPro Server", Eyebrow: "Local administration", Heading: "Welcome back", Description: "Sign in to manage the applications and services on this server.", NeedsUsername: true, PasswordLabel: "Password", PasswordName: "password", PasswordAutocomplete: "current-password", SubmitLabel: "Sign in", Footer: "KITPro uses a local account. Your credentials are not sent to a KITPro cloud service.", Error: message}
}
func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if !a.allowedHosts[r.Host] {
		http.Error(w, "host denied", 400)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	auth.Revoke(r.Context(), a.db, r)
	slog.Default().Info("logout", "event", "logout")
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: "", Path: "/", MaxAge: -1, Secure: a.secureCookies, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", 303)
}
func (a *app) backup(w http.ResponseWriter, r *http.Request) {
	id := operations.NewID()
	cp, e := backup.Vacuum(r.Context(), a.db, "/var/lib/kitpro-api/backups", id+".db")
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	resp, e := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: id, Operation: "BackupHelperState", OperationRevision: 1})
	if e != nil || !resp.OK {
		http.Error(w, "helper backup failed", 503)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"control": cp, "helper": "completed"})
}

// version exposes non-sensitive build and schema metadata to authenticated
// operators.  Filesystem paths and builder details intentionally stay out of
// this response.
func (a *app) version(w http.ResponseWriter, r *http.Request) {
	var schema int
	_ = a.db.QueryRowContext(r.Context(), "SELECT version FROM schema_version LIMIT 1").Scan(&schema)
	json.NewEncoder(w).Encode(map[string]any{
		"component":              "kitpro-server",
		"version":                buildinfo.Version,
		"source_commit":          buildinfo.SourceCommit,
		"build_date":             buildinfo.BuildDate,
		"architecture":           "linux/" + runtime.GOARCH,
		"package_family":         packageFamily(),
		"platform":               platformName(),
		"schema_version":         schema,
		"catalog_schema_version": manifest.CatalogMetadataSchemaVersion,
	})
}
func (a *app) hardware(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	resp, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "GetHardwareInventory"})
	if err != nil || !resp.OK {
		http.Error(w, "hardware inventory unavailable", http.StatusServiceUnavailable)
		return
	}
	json.NewEncoder(w).Encode(resp.Result)
}
func (a *app) storageRoots(w http.ResponseWriter, r *http.Request) {
	request := protocol.Request{Version: 1, ID: operations.NewID()}
	switch r.Method {
	case http.MethodGet:
		request.Operation = "ListStorageRoots"
	case http.MethodPost:
		if strings.TrimPrefix(r.URL.Path, "/api/v1/storage-roots/") != r.URL.Path && strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/storage-roots/"), "/") != "" {
			http.Error(w, "method denied", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid storage registration", 400)
			return
		}
		request.Operation, request.RootName, request.RootPath, request.RootMode = "RegisterStorageRoot", strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("path")), r.FormValue("mode")
		request.Version, request.ID, request.OperationID, request.OperationRevision = 2, operations.NewRequestID(), operations.NewID(), 1
	case http.MethodDelete:
		request.Operation = "RemoveStorageRoot"
		request.RootID = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/storage-roots/"), "/")
		request.Version, request.ID, request.OperationID, request.OperationRevision = 2, operations.NewRequestID(), operations.NewID(), 1
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	var response protocol.Response
	var err error
	if request.OperationID != "" {
		response, err = a.callHelperOperation(request)
	} else {
		response, err = a.callHelper(request)
	}
	if err != nil {
		http.Error(w, "storage helper unavailable", 503)
		return
	}
	if !response.OK {
		http.Error(w, response.Error, 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response.Result)
}
func (a *app) home(w http.ResponseWriter, r *http.Request) {
	type appView struct {
		ID, Name, Description, Release, Category, Initials, Hardware string
		WebsiteURL, SourceURL, DocumentationURL, LogoURL             string
		RecoverInstallationID                                        string
		InstalledCount                                               int
		HardwareUnavailable, Experimental, NetworkService            bool
		StorageSlots                                                 []storageSlotView
		Limitations, NetworkRequirements                             []string
		InstallNotice                                                string
	}
	type installationView struct {
		ID, Name, Description, Category, Initials, State, StateLabel, StateClass, AppID, Release string
		AccessLabel, OpenEndpoint, AvailableRelease, HardwareLabel, HardwareClass, HardwareID    string
		LogoURL                                                                                  string
		Generation, InstalledCount                                                               int
		UpdateAvailable, Experimental, NetworkService                                            bool
		Services                                                                                 []serviceStatus
		Components                                                                               []string
		Storage                                                                                  []installedStorageView
		Credentials                                                                              []manifest.CredentialPresentation
		LifecycleNotice                                                                          *manifest.LifecycleNotice
	}
	type operationView struct {
		Title, Initial, StatusLabel, StatusClass, Summary, RequestedAt, TimeLabel string
	}
	type healthView struct {
		Label, Headline, Description, Runtime, Helper, Class string
	}

	storageRoots := a.trustedStorageRoots()
	installations := []installationView{}
	installedCountByApp := map[string]int{}
	recoverableInstallationByApp := map[string]string{}
	recoverableInstallationCount := map[string]int{}
	runningCount, privateCount, attentionCount, updateCount := 0, 0, 0, 0
	installedRows, _ := a.db.QueryContext(r.Context(), "SELECT installation_id,application_id,desired_state,runtime_generation FROM installations ORDER BY created_at")
	if installedRows != nil {
		defer installedRows.Close()
		for installedRows.Next() {
			var id, appID, desired string
			var generation int
			if installedRows.Scan(&id, &appID, &desired, &generation) != nil {
				continue
			}
			installedCountByApp[appID]++
			if desired == "runtime_removed" {
				recoverableInstallationCount[appID]++
				recoverableInstallationByApp[appID] = id
			}
			view := installationView{ID: id, Name: appID, AppID: appID, State: desired, Generation: generation, AccessLabel: "Private"}
			services, _ := a.serviceStatuses(r.Context(), id)
			view.Services = services
			_ = a.db.QueryRowContext(r.Context(), "SELECT release_id FROM installations WHERE installation_id=?", id).Scan(&view.Release)
			if entry, ok := a.catalog[appID]; ok {
				m := entry.Manifest
				view.Name, view.Description, view.Category, view.Initials = m.Name, m.Description, m.Category, appInitials(m.Name)
				view.LogoURL, view.Experimental = catalogLogoURL(m.Logo), m.CatalogStatus == "experimental"
				view.NetworkService, view.LifecycleNotice = m.Kind == "network-service", m.LifecycleNotice
				for _, credential := range manifest.PresentedCredentials(m) {
					view.Credentials = append(view.Credentials, manifest.CredentialPresentation{ID: credential.ID, Label: credential.Label, Username: credential.Username})
				}
				if len(m.Components) == 0 && len(m.Releases) > 1 {
					candidate := m.Releases[len(m.Releases)-1].Version
					if candidate != view.Release {
						view.AvailableRelease, view.UpdateAvailable = candidate, true
						updateCount++
					}
				}
				if len(m.Components) == 0 {
					view.Components = []string{"Application"}
				} else {
					for _, component := range m.Components {
						view.Components = append(view.Components, componentLabel(component.ID))
					}
				}
			}
			view.HardwareLabel, view.HardwareClass, view.HardwareID = a.installationHardware(id, appID)
			if entry, ok := a.catalog[appID]; ok {
				view.Storage = a.installationStorage(id, entry.Manifest)
			}
			view.StateLabel, view.StateClass = statePresentation(desired)
			if desired == "running" {
				runningCount++
			} else if desired != "stopped" && desired != "runtime_removed" {
				attentionCount++
			}
			if len(services) == 0 {
				privateCount++
			}
			for i := range services {
				services[i].ModeLabel = exposureLabel(services[i].Mode)
				if services[i].Mode == string(exposure.Internal) {
					privateCount++
				}
				if services[i].OpenEndpoint != "" && view.OpenEndpoint == "" {
					view.OpenEndpoint = services[i].OpenEndpoint
				}
				if services[i].Mode != "" {
					view.AccessLabel = services[i].ModeLabel
				}
			}
			view.Services = services
			installations = append(installations, view)
		}
	}
	hardwareAvailable, hardwareVendor := a.hardwareAvailability()
	apps := []appView{}
	for _, id := range catalog.IDs(a.catalog) {
		m := a.catalog[id].Manifest
		if !catalogVisible(m.ID) {
			continue
		}
		release := ""
		if len(m.Releases) > 0 {
			release = m.Releases[0].Version
		}
		hardwareLabel := "CPU supported"
		hardwareUnavailable := false
		for _, requirement := range m.Hardware {
			if requirement.Optional {
				hardwareLabel = "CPU supported · GPU optional"
			} else {
				hardwareLabel = "GPU required"
			}
			if requirement.Class == "gpu.nvidia" {
				hardwareLabel += " · NVIDIA certified"
				if hardwareAvailable && hardwareVendor == "nvidia" {
					hardwareLabel += " · available"
				} else if !requirement.Optional {
					hardwareLabel += " · unavailable"
					hardwareUnavailable = true
				}
			}
		}
		recoverID := ""
		if recoverableInstallationCount[m.ID] == 1 {
			recoverID = recoverableInstallationByApp[m.ID]
		}
		app := appView{ID: m.ID, Name: m.Name, Description: m.Description, Release: release, Category: m.Category, Initials: appInitials(m.Name), Hardware: hardwareLabel, HardwareUnavailable: hardwareUnavailable, InstalledCount: installedCountByApp[m.ID], RecoverInstallationID: recoverID, WebsiteURL: m.WebsiteURL, SourceURL: m.SourceURL, DocumentationURL: m.DocumentationURL, LogoURL: catalogLogoURL(m.Logo), Limitations: append([]string(nil), m.Limitations...), Experimental: m.CatalogStatus == "experimental", NetworkService: m.Kind == "network-service"}
		if m.LifecycleNotice != nil {
			app.InstallNotice = m.LifecycleNotice.Install
		}
		for _, service := range m.Services {
			transport, _ := exposure.TransportFor(service.Protocol)
			if service.FixedHostPort != 0 {
				app.NetworkRequirements = append(app.NetworkRequirements, fmt.Sprintf("%s requires the configured LAN address on port %d/%s", service.Name, service.FixedHostPort, transport))
			} else if service.DefaultExposure == string(exposure.Loopback) {
				app.NetworkRequirements = append(app.NetworkRequirements, service.Name+" uses a dynamic port on this server")
			}
		}
		for _, slot := range m.ExternalStorage {
			item := storageSlotView{ID: slot.ID, Purpose: slot.Purpose, Mode: slot.Mode}
			for _, root := range storageRoots {
				if root.Available && (slot.Mode == "read-only" || root.Mode == "read-write") {
					item.Roots = append(item.Roots, root)
				}
			}
			app.StorageSlots = append(app.StorageSlots, item)
		}
		apps = append(apps, app)
	}
	records, _ := operations.List(r.Context(), a.db)
	rows := make([]operationView, 0, len(records))
	for _, record := range records {
		title := operationTitle(record.Type)
		label, class := operationStatus(record.Status)
		summary := operationSummary(record)
		when := record.RequestedAt
		if parsed, err := time.Parse(time.RFC3339Nano, record.RequestedAt); err == nil {
			when = parsed.Local().Format("Jan 2, 3:04 PM")
		}
		rows = append(rows, operationView{Title: title, Initial: strings.ToUpper(title[:1]), StatusLabel: label, StatusClass: class, Summary: summary, RequestedAt: record.RequestedAt, TimeLabel: when})
	}
	health := healthView{Label: "Healthy", Headline: "Your server is ready.", Description: "KITPro is connected to its protected helper and ready to manage trusted applications.", Runtime: "Connected", Helper: "Protected", Class: "is-healthy"}
	if response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"}); err != nil || !response.OK {
		health = healthView{Label: "Attention needed", Headline: "Container services need attention.", Description: "KITPro cannot reach the container runtime through its protected helper. Existing data remains in place.", Runtime: "Unavailable", Helper: "Check service", Class: "needs-attention"}
		attentionCount++
	}
	csrf := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
		csrf = cookie.Value
	}
	a.securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var schema int
	_ = a.db.QueryRowContext(r.Context(), "SELECT version FROM schema_version LIMIT 1").Scan(&schema)
	hardwareSummary := "No supported accelerator detected. CPU workloads remain available where supported."
	if response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "GetHardwareInventory"}); err == nil && response.OK {
		if result, ok := response.Result.(map[string]any); ok {
			if items, ok := result["accelerators"].([]any); ok && len(items) > 0 {
				if accelerator, ok := items[0].(map[string]any); ok {
					model, _ := accelerator["model"].(string)
					vendor, _ := accelerator["vendor"].(string)
					hardwareSummary = model + " — detected"
					if vendor == "nvidia" {
						if runtimeReady, _ := result["nvidia_runtime"].(bool); runtimeReady {
							hardwareSummary = model + " — available; NVIDIA Container Toolkit detected"
						} else {
							hardwareSummary = model + " — GPU detected, but the container GPU runtime is unavailable"
						}
					}
				}
			}
		}
	}
	if err := a.tmpl.Execute(w, map[string]any{"Rows": rows, "Apps": apps, "Installations": installations, "StorageRoots": storageRoots, "CSRF": csrf, "Version": buildinfo.Version, "SourceCommit": buildinfo.SourceCommit, "Platform": platformName(), "Architecture": runtime.GOARCH, "PackageFamily": packageFamily(), "SchemaVersion": schema, "CatalogSchemaVersion": manifest.CatalogMetadataSchemaVersion, "Health": health, "InstalledCount": len(installations), "RunningCount": runningCount, "PrivateCount": privateCount, "AttentionCount": attentionCount, "UpdateCount": updateCount, "HardwareSummary": hardwareSummary}); err != nil {
		slog.Default().Error("dashboard render failed", "event", "ui_render_failed", "error", err)
	}
}

func (a *app) hardwareAvailability() (bool, string) {
	response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "GetHardwareInventory"})
	if err != nil || !response.OK {
		return false, ""
	}
	result, _ := response.Result.(map[string]any)
	items, _ := result["accelerators"].([]any)
	if len(items) != 1 {
		return false, ""
	}
	accelerator, _ := items[0].(map[string]any)
	vendor, _ := accelerator["vendor"].(string)
	if vendor == "nvidia" {
		ready, _ := result["nvidia_runtime"].(bool)
		return ready, vendor
	}
	return true, vendor
}

func (a *app) trustedStorageRoots() []trustedRootView {
	response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "ListStorageRoots"})
	if err != nil || !response.OK {
		return nil
	}
	result, _ := response.Result.(map[string]any)
	items, _ := result["roots"].([]any)
	views := []trustedRootView{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		view := trustedRootView{}
		view.ID, _ = item["id"].(string)
		view.Name, _ = item["name"].(string)
		view.Path, _ = item["path"].(string)
		view.Mode, _ = item["mode"].(string)
		view.Filesystem, _ = item["filesystem"].(string)
		view.Available, _ = item["available"].(bool)
		view.NetworkBacked, _ = item["network_backed"].(bool)
		views = append(views, view)
	}
	return views
}

func (a *app) installationStorage(installation string, m manifest.Manifest) []installedStorageView {
	response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "GetStorageAssignments", InstanceID: installation})
	if err != nil || !response.OK {
		return nil
	}
	purposes := map[string]string{}
	for _, slot := range m.ExternalStorage {
		purposes[slot.ID] = slot.Purpose
	}
	for _, component := range m.Components {
		for _, slot := range component.ExternalStorage {
			purposes[component.ID+"/"+slot.ID] = slot.Purpose
		}
	}
	result, _ := response.Result.(map[string]any)
	items, _ := result["assignments"].([]any)
	views := []installedStorageView{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		component, _ := item["component"].(string)
		slot, _ := item["slot_id"].(string)
		key := slot
		if component != "" {
			key = component + "/" + slot
		}
		view := installedStorageView{Purpose: purposes[key]}
		view.RootName, _ = item["root_name"].(string)
		view.Mode, _ = item["mode"].(string)
		view.Path, _ = item["path"].(string)
		view.Available, _ = item["available"].(bool)
		views = append(views, view)
	}
	return views
}

func (a *app) installationHardware(installation, appID string) (label, class, stableID string) {
	entry, ok := a.catalog[appID]
	if !ok || len(entry.Manifest.Hardware) == 0 {
		return "CPU", "", ""
	}
	response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "GetHardwareAssignment", InstanceID: installation})
	if err == nil && response.OK {
		result, _ := response.Result.(map[string]any)
		items, _ := result["assignments"].([]any)
		if len(items) > 0 {
			assignment, _ := items[0].(map[string]any)
			mode, _ := assignment["mode"].(string)
			class, _ = assignment["class"].(string)
			stableID, _ = assignment["stable_id"].(string)
			if mode == "cpu" {
				return "CPU fallback", class, stableID
			}
			available, _ := assignment["available"].(bool)
			if !available {
				if !entry.Manifest.Hardware[0].Optional {
					return "Required GPU unavailable", class, stableID
				}
				return "Assigned GPU unavailable — recreate for CPU fallback", class, stableID
			}
			model, _ := assignment["model"].(string)
			if model == "" {
				model = "NVIDIA GPU"
			}
			return model, class, stableID
		}
	}
	if !entry.Manifest.Hardware[0].Optional {
		return "Required GPU unavailable", entry.Manifest.Hardware[0].Class, ""
	}
	return "CPU fallback", entry.Manifest.Hardware[0].Class, ""
}

func platformName() string {
	p, err := hostplatform.Detect()
	if err != nil || p.PrettyName == "" {
		return "Linux"
	}
	return p.PrettyName
}

func packageFamily() string {
	p, err := hostplatform.Detect()
	if err != nil || p.PackageManager == "" {
		return "unknown"
	}
	return p.PackageManager
}

func catalogVisible(id string) bool { return id != "busybox" }

func catalogLogoURL(key string) string {
	if key == "" {
		return ""
	}
	return "/assets/catalog/" + key + ".svg"
}

func appInitials(name string) string {
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(name))
	if len(words) == 0 {
		return "AP"
	}
	if len(words) == 1 {
		runes := []rune(words[0])
		if len(runes) == 1 {
			return strings.ToUpper(string(runes[0]))
		}
		return strings.ToUpper(string(runes[:2]))
	}
	return strings.ToUpper(string([]rune(words[0])[0]) + string([]rune(words[1])[0]))
}

func componentLabel(id string) string {
	if id == "broker" {
		return "Background service"
	}
	if id == "web" {
		return "Web application"
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

func statePresentation(state string) (string, string) {
	switch state {
	case "running":
		return "Running", "badge-success"
	case "stopped":
		return "Stopped", "badge-warning"
	case "runtime_removed":
		return "Runtime removed", "badge-warning"
	default:
		return "Needs attention", "badge-danger"
	}
}

func exposureLabel(mode string) string {
	switch mode {
	case string(exposure.Loopback):
		return "This server only"
	case string(exposure.LAN):
		return "Local network"
	default:
		return "Private"
	}
}

func operationTitle(operationType string) string {
	titles := map[string]string{
		"InstallApplication": "Application installed", "UpdateApplication": "Application updated",
		"ConfigureServiceExposure": "Access changed", "RecreateApplication": "Runtime recreated",
		"StartApplication": "Application started", "StopApplication": "Application stopped", "RestartApplication": "Application restarted",
		"RemoveApplication": "Runtime removed",
	}
	if title := titles[operationType]; title != "" {
		return title
	}
	return "Server operation"
}

func operationStatus(status string) (string, string) {
	switch status {
	case "succeeded":
		return "Completed", "badge-success"
	case "failed":
		return "Needs attention", "badge-danger"
	default:
		return "In progress", "badge-info"
	}
}

func operationSummary(record operations.Record) string {
	if record.Status == "failed" {
		return "KITPro could not complete this operation. Review the application and retry when ready."
	}
	if record.Status != "succeeded" {
		return "KITPro is applying the requested change."
	}
	return "The requested change completed successfully."
}

func (a *app) ops(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && strings.Trim(r.URL.Path, "/") != "api/v1/operations" {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/operations/")
		x, e := operations.Get(r.Context(), a.db, id)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if x.Status == "accepted" || x.Status == "helper_succeeded_projection_pending" {
			if helper, helperErr := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: x.ID, Operation: "GetOperation"}); helperErr == nil && helper.ErrorCode != "OperationNotFound" {
				switch helper.State {
				case "succeeded":
					x.Status, x.Summary = "succeeded", "privileged operation completed"
					if isCommittedLifecycleOperation(x.Type) {
						if projectionErr := a.repairCommittedProjection(r.Context(), x.InstanceID, x.ID, helper.Result, true); projectionErr != nil {
							x.Status, x.Summary = "helper_succeeded_projection_pending", "privileged operation completed; control projection repair pending"
						}
					}
					if x.Type == "ReconcileInstallation" || x.Type == "RepairInstallation" {
						if _, projectionErr := a.projectReconciliation(r.Context(), x.ID, helper.Result); projectionErr != nil {
							x.Status, x.Summary = "accepted", "privileged operation completed; reconciliation projection pending"
						}
					}
				case "failed", "action_required", "cancelled", "superseded":
					x.Status, x.Summary = "failed", "privileged operation requires review"
				default:
					x.Summary = "privileged operation continues in helper"
				}
				_ = operations.Update(r.Context(), a.db, x.ID, x.Status, x.Summary)
			}
		}
		json.NewEncoder(w).Encode(x)
		return
	}
	if r.Method == "GET" {
		x, _ := operations.List(r.Context(), a.db)
		json.NewEncoder(w).Encode(x)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/install") {
		http.Error(w, "use /api/v1/apps/{id}/install", http.StatusBadRequest)
		return
	}
	if trusted, _ := r.Context().Value(internalDispatchKey{}).(bool); !trusted {
		http.Error(w, "use a constrained application lifecycle endpoint", http.StatusBadRequest)
		return
	}
	id, inst := operations.NewID(), operations.NewInstallation()
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		appID = "busybox"
	}
	gen := 1
	release := ""
	existing := r.URL.Query().Get("installation_id")
	if existing != "" {
		inst = existing
		if e := a.db.QueryRowContext(r.Context(), "SELECT application_id,release_id,runtime_generation FROM installations WHERE installation_id=?", inst).Scan(&appID, &release, &gen); e != nil {
			http.Error(w, "installation not found", 404)
			return
		}
		gen++
	}
	entry, ok := a.catalog[appID]
	if !ok {
		http.Error(w, "catalog unavailable", 500)
		return
	}
	if release == "" {
		releaseIndex := len(entry.Manifest.Releases) - 1
		if len(entry.Manifest.Components) > 0 {
			releaseIndex = 0
		}
		release = entry.Manifest.Releases[releaseIndex].Version
	}
	if target := r.URL.Query().Get("target_release"); target != "" {
		release = target
	}
	network := "kitpro-net-" + inst + "-g" + strconv.Itoa(gen)
	dataPath := "/srv/kitpro/apps/" + entry.Manifest.ID + "/" + inst + "/data"
	plan, pe := manifest.Resolve(entry.Manifest, release, inst, network, dataPath)
	if pe != nil {
		http.Error(w, pe.Error(), 500)
		return
	}
	_ = r.ParseForm()
	opType := r.URL.Query().Get("operation_type")
	if opType == "" {
		opType = "InstallApplication"
	}
	if opType != "InstallApplication" && opType != "ConfigureServiceExposure" && opType != "UpdateApplication" && opType != "RecreateApplication" {
		http.Error(w, "invalid operation type", 400)
		return
	}
	if opType == "UpdateApplication" && existing == "" {
		http.Error(w, "existing installation required", 400)
		return
	}
	if e := operations.Insert(r.Context(), a.db, id, opType, inst); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if existing == "" {
		_, e := a.db.ExecContext(r.Context(), "INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", inst, plan.ApplicationID, plan.ReleaseID, "runtime_removed", 0, now, now)
		if e != nil {
			operations.Update(r.Context(), a.db, id, "failed", e.Error())
			http.Error(w, "installation state failed", 500)
			return
		}
	}
	var e error
	bindingFailureSummary := ""
	helperRejected := false
	helperPending := false
	var helperRequest protocol.Request
	var helperResponse protocol.Response
	{
		helperOperation := "InstallApplication"
		if opType == "ConfigureServiceExposure" {
			helperOperation = opType
		} else if opType == "UpdateApplication" {
			helperOperation = "UpdateApplication"
		} else if opType == "RecreateApplication" {
			helperOperation = "InstallApplication"
		}
		q := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: id, Operation: helperOperation, OperationRevision: 1, InstanceID: inst, RuntimeGeneration: gen, Image: plan.ImageDigest, ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID, NetworkName: plan.NetworkName, DataPath: plan.DataPath, RestartPolicy: plan.Restart}
		if plan.Configuration != nil {
			q.Configuration = &protocol.ConfigurationPolicy{Type: plan.Configuration.Type, StorageID: plan.Configuration.StorageID}
		}
		q.RepairAction = r.URL.Query().Get("repair_action")
		helperRequest = q
		for _, item := range plan.Hardware {
			q.Hardware = append(q.Hardware, protocol.HardwareRequirement{Class: item.Class, Optional: item.Optional, CPUFallback: item.CPUFallback})
		}
		for _, x := range plan.Command {
			q.Command = append(q.Command, x)
		}
		for _, x := range plan.Environment {
			q.Environment = append(q.Environment, protocol.EnvVar{Name: x.Name, Value: x.Value, Secret: x.Secret, Generate: x.Generate})
		}
		for _, x := range plan.Storage {
			q.Storage = append(q.Storage, protocol.StorageMount{ID: x.ID, ContainerPath: x.ContainerPath, HostPath: "/srv/kitpro/apps/" + plan.ApplicationID + "/" + inst + "/" + x.ID, ReadOnly: x.ReadOnly, OwnerUID: x.OwnerUID, OwnerGID: x.OwnerGID})
		}
		if plan.RunAs != nil {
			q.RunAs = &protocol.RuntimeIdentity{UID: plan.RunAs.UID, GID: plan.RunAs.GID}
		}
		for _, slot := range plan.ExternalStorage {
			rootID := strings.TrimSpace(r.FormValue("storage_" + slot.ID))
			if rootID == "" {
				_ = a.db.QueryRowContext(r.Context(), `SELECT root_id FROM installation_storage_selections WHERE installation_id=? AND component_id='' AND slot_id=?`, inst, slot.ID).Scan(&rootID)
			}
			q.ExternalStorage = append(q.ExternalStorage, protocol.ExternalStorageBinding{SlotID: slot.ID, RootID: rootID})
		}
		for _, s := range plan.Services {
			q.Services = append(q.Services, protocol.Service{ID: s.ID, Protocol: s.Protocol, ContainerPort: s.ContainerPort})
		}
		q.Bindings, e = a.buildServiceBindings(r.Context(), inst, plan.Services, opType, r.URL.Query())
		if e != nil {
			bindingFailureSummary, _ = safeBindingConflictSummary(e)
		}
		for _, component := range plan.ResolvedComponents {
			pc := protocol.Component{ID: component.ID, Image: component.ImageDigest, Restart: component.Restart, DependsOn: append([]string(nil), component.DependsOn...)}
			if component.RunAs != nil {
				pc.RunAs = &protocol.RuntimeIdentity{UID: component.RunAs.UID, GID: component.RunAs.GID}
			}
			for _, item := range component.Hardware {
				pc.Hardware = append(pc.Hardware, protocol.HardwareRequirement{Class: item.Class, Optional: item.Optional, CPUFallback: item.CPUFallback})
			}
			for _, arg := range component.Command {
				pc.Command = append(pc.Command, arg)
			}
			for _, variable := range component.Environment {
				pc.Environment = append(pc.Environment, protocol.EnvVar{Name: variable.Name, Value: variable.Value, Secret: variable.Secret, Generate: variable.Generate})
			}
			for _, storage := range component.Storage {
				pc.Storage = append(pc.Storage, protocol.StorageMount{ID: storage.ID, ContainerPath: storage.ContainerPath, HostPath: "/srv/kitpro/apps/" + plan.ApplicationID + "/" + inst + "/" + component.ID + "/" + storage.ID, ReadOnly: storage.ReadOnly, OwnerUID: storage.OwnerUID, OwnerGID: storage.OwnerGID})
			}
			for _, slot := range component.ExternalStorage {
				rootID := strings.TrimSpace(r.FormValue("storage_" + component.ID + "_" + slot.ID))
				if rootID == "" {
					_ = a.db.QueryRowContext(r.Context(), `SELECT root_id FROM installation_storage_selections WHERE installation_id=? AND component_id=? AND slot_id=?`, inst, component.ID, slot.ID).Scan(&rootID)
				}
				pc.ExternalStorage = append(pc.ExternalStorage, protocol.ExternalStorageBinding{SlotID: slot.ID, RootID: rootID})
			}
			for _, service := range component.Services {
				pc.Services = append(pc.Services, protocol.Service{ID: service.ID, Protocol: service.Protocol, ContainerPort: service.ContainerPort})
			}
			q.Components = append(q.Components, pc)
		}
		if e == nil {
			helperRequest = q
			var resp protocol.Response
			resp, e = a.callHelperOperation(q)
			helperResponse = resp
			if e == nil && !resp.OK {
				helperRejected = true
				e = fmt.Errorf("helper: %s", resp.Error)
			} else if e == nil {
				helperPending = helperStatePending(resp.State)
			}
		}
	}
	status := "succeeded"
	summary := "helper accepted"
	failureCategory := ""
	if e != nil {
		status = "failed"
		failureCategory = operationErrorCategory(e)
		summary = safeOperationSummary(failureCategory)
		if bindingFailureSummary != "" {
			summary = bindingFailureSummary
		}
		if existing == "" && helperRejected {
			// The helper did not establish trusted runtime ownership. Preserve the
			// installation and its data, but keep the next supported recreation at
			// generation 1 rather than inventing a discarded runtime generation.
			_, _ = a.db.ExecContext(r.Context(), "UPDATE installations SET desired_state='runtime_removed',runtime_generation=0,updated_at=? WHERE installation_id=?", now, inst)
		}
	} else if helperPending {
		status = "accepted"
		summary = "privileged operation continues in helper"
	} else {
		for _, binding := range helperRequest.ExternalStorage {
			_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO installation_storage_selections(installation_id,component_id,slot_id,root_id) VALUES(?,'',?,?)`, inst, binding.SlotID, binding.RootID)
		}
		for _, component := range helperRequest.Components {
			for _, binding := range component.ExternalStorage {
				_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO installation_storage_selections(installation_id,component_id,slot_id,root_id) VALUES(?,?,?,?)`, inst, component.ID, binding.SlotID, binding.RootID)
			}
		}
	}
	if e == nil && !helperPending {
		committed := committedLifecycleResult{Generation: gen, ReleaseID: plan.ReleaseID, RuntimeState: "running", Bindings: exposureBindings(helperRequest.Bindings)}
		if helperResponse.State != "" {
			committed, e = decodeCommittedLifecycleResult(helperResponse.Result)
			if e != nil {
				status, summary = "failed", "helper result could not be projected"
			}
		}
		if e != nil {
			operations.Update(r.Context(), a.db, id, status, summary)
			http.Error(w, summary, http.StatusBadGateway)
			return
		}
		var projectionErr error
		if a.beforeControlProjection != nil {
			projectionErr = a.beforeControlProjection()
		}
		var projectionTx *sql.Tx
		if projectionErr == nil {
			projectionTx, projectionErr = a.db.BeginTx(r.Context(), nil)
		}
		if projectionErr != nil {
			status, summary = "helper_succeeded_projection_pending", "privileged operation completed; control projection repair pending"
		} else {
			defer projectionTx.Rollback()
			updateSQL := "UPDATE installations SET desired_state=?,runtime_state=?,runtime_generation=?,updated_at=?,projection_source_operation_id=?"
			args := []any{committed.RuntimeState, committed.RuntimeState, committed.Generation, now, id}
			if opType == "UpdateApplication" || len(helperRequest.Components) == 0 {
				updateSQL += ",release_id=?"
				args = append(args, committed.ReleaseID)
			}
			updateSQL += " WHERE installation_id=?"
			args = append(args, inst)
			if _, updateErr := projectionTx.ExecContext(r.Context(), updateSQL, args...); updateErr != nil {
				e = updateErr
			}
			if e == nil {
				for _, binding := range committed.Bindings {
					if updateErr := upsertExposureProjection(r.Context(), projectionTx, inst, binding, env("KITPRO_LAN_BIND_ADDRESS", ""), now); updateErr != nil {
						e = updateErr
						break
					}
				}
			}
			if e == nil {
				e = projectionTx.Commit()
			} else {
				_ = projectionTx.Rollback()
			}
			if e != nil {
				status, summary = "helper_succeeded_projection_pending", "privileged operation completed; control projection repair pending"
				e = nil
			}
		}
	}
	if opType == "ConfigureServiceExposure" {
		event := "exposure_applied"
		if status == "failed" {
			event = "exposure_failed"
			if failureCategory == "port_collision" {
				event = "exposure_collision"
			}
		} else if status == "helper_succeeded_projection_pending" {
			event = "exposure_projection_pending"
		} else if r.URL.Query().Get("exposure_mode") == string(exposure.Internal) {
			event = "exposure_disabled"
		}
		slog.Default().Info("service exposure operation", "event", event, "operation_id", id, "installation_id", inst, "service_id", r.URL.Query().Get("service_id"), "mode", r.URL.Query().Get("exposure_mode"), "host_address", r.URL.Query().Get("host_address"), "host_port", r.URL.Query().Get("host_port"), "result", status)
	}
	if opType == "UpdateApplication" {
		event := "app_update_succeeded"
		if status == "failed" {
			event = "app_update_failed"
		}
		slog.Default().Info("application update completed", "event", event, "operation_id", id, "installation_id", inst, "target_release", plan.ReleaseID, "result", status)
	}
	slog.Default().Info("operation", "event", "operation_completed", "operation_id", id, "instance_id", inst, "result", status)
	operations.Update(r.Context(), a.db, id, status, summary)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}

func upsertExposureProjection(ctx context.Context, tx *sql.Tx, installationID string, binding exposure.ServiceBinding, configuredLAN, now string) error {
	if err := exposure.ValidateBinding(binding, configuredLAN, binding.HostPort); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO installation_service_exposure(installation_id,service_id,transport,mode,host_address,host_port,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(installation_id,service_id) DO UPDATE SET transport=excluded.transport,mode=excluded.mode,host_address=excluded.host_address,host_port=excluded.host_port,updated_at=excluded.updated_at`, installationID, binding.ServiceID, binding.Transport, binding.Mode, binding.HostAddress, binding.HostPort, now, now)
	return err
}

func exposureBindings(in []protocol.ServiceBinding) []exposure.ServiceBinding {
	out := make([]exposure.ServiceBinding, 0, len(in))
	for _, b := range in {
		out = append(out, exposure.ServiceBinding{ServiceID: b.ServiceID, Transport: exposure.Transport(b.Transport), ContainerPort: b.ContainerPort, Mode: exposure.Mode(b.Mode), HostAddress: b.HostAddress, HostPort: b.HostPort})
	}
	return exposure.Normalize(out)
}

func (a *app) buildServiceBindings(ctx context.Context, installation string, services []manifest.Service, operation string, query url.Values) ([]protocol.ServiceBinding, error) {
	target := query.Get("service_id")
	configuredLAN := env("KITPRO_LAN_BIND_ADDRESS", "")
	bindings := make([]exposure.ServiceBinding, 0, len(services))
	for _, service := range services {
		transport, err := exposure.TransportFor(service.Protocol)
		if err != nil {
			return nil, err
		}
		binding := exposure.ServiceBinding{ServiceID: service.ID, Transport: transport, ContainerPort: service.ContainerPort, Mode: exposure.Internal, HostPort: service.FixedHostPort}
		stored := false
		if record, getErr := exposure.Get(ctx, a.db, installation, service.ID); getErr == nil {
			if record.Transport != transport {
				return nil, errors.New("stored service transport does not match trusted manifest")
			}
			binding.Mode, binding.HostAddress, binding.HostPort = record.Mode, record.HostAddress, record.HostPort
			stored = true
		}
		if !stored && (operation == "InstallApplication" || operation == "RecreateApplication") && service.DefaultExposure != "" {
			binding.Mode = exposure.Mode(service.DefaultExposure)
			switch binding.Mode {
			case exposure.Loopback:
				binding.HostAddress = "127.0.0.1"
			case exposure.LAN:
				binding.HostAddress = configuredLAN
				if configuredLAN == "" {
					return nil, errors.New("LAN bind address not configured")
				}
				if err = exposure.VerifyLocalAddress(configuredLAN); err != nil {
					return nil, errors.New("LAN bind address is not assigned")
				}
			}
			if binding.Mode != exposure.Internal && service.FixedHostPort == 0 {
				binding.HostPort, err = a.allocateDefaultServicePort(ctx, installation, binding, bindings)
				if err != nil {
					return nil, err
				}
			}
		}
		if operation == "ConfigureServiceExposure" && target == service.ID {
			binding.Mode = exposure.Mode(query.Get("exposure_mode"))
			binding.HostAddress = query.Get("host_address")
			binding.HostPort, _ = strconv.Atoi(query.Get("host_port"))
			if service.FixedHostPort != 0 {
				binding.HostPort = service.FixedHostPort
			}
			if binding.Mode == exposure.Internal {
				binding.HostAddress = ""
			}
		}
		if err = exposure.ValidateBinding(binding, configuredLAN, service.FixedHostPort); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if operation == "ConfigureServiceExposure" && target == "" {
		return nil, errors.New("service binding target is required")
	}
	if err := a.preflightBindings(ctx, installation, bindings); err != nil {
		return nil, err
	}
	bindings = exposure.Normalize(bindings)
	out := make([]protocol.ServiceBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, protocol.ServiceBinding{ServiceID: b.ServiceID, Transport: string(b.Transport), ContainerPort: b.ContainerPort, Mode: string(b.Mode), HostAddress: b.HostAddress, HostPort: b.HostPort})
	}
	return out, nil
}

func (a *app) allocateDefaultServicePort(ctx context.Context, installation string, binding exposure.ServiceBinding, pending []exposure.ServiceBinding) (int, error) {
	used := map[int]bool{}
	rows, err := a.db.QueryContext(ctx, `SELECT installation_id,transport,mode,host_address,host_port FROM installation_service_exposure WHERE host_port>0`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var owner string
		var transport exposure.Transport
		var mode exposure.Mode
		var address string
		var port int
		if err = rows.Scan(&owner, &transport, &mode, &address, &port); err != nil {
			return 0, err
		}
		candidate := binding
		candidate.HostPort = port
		if owner != installation && exposure.BindingsConflict(candidate, exposure.ServiceBinding{Transport: transport, Mode: mode, HostAddress: address, HostPort: port}) {
			used[port] = true
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, current := range pending {
		if current.Transport == binding.Transport && current.HostAddress == binding.HostAddress && current.HostPort > 0 {
			used[current.HostPort] = true
		}
	}
	if a.allocatePort != nil && binding.Transport == exposure.TCP {
		return a.allocatePort(used, binding.HostAddress)
	}
	return exposure.AllocateAvailableTransport(used, binding.HostAddress, binding.Transport)
}

func (a *app) preflightBindings(ctx context.Context, installation string, requested []exposure.ServiceBinding) error {
	if err := exposure.ValidateConflicts(requested); err != nil {
		return err
	}
	hasExposed := false
	for _, binding := range requested {
		if binding.Mode != exposure.Internal {
			hasExposed = true
			break
		}
	}
	if !hasExposed {
		return nil
	}
	rows, err := a.db.QueryContext(ctx, `SELECT installation_id,service_id,transport,mode,host_address,host_port FROM installation_service_exposure WHERE mode<>'internal' ORDER BY installation_id,service_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var reserved, own []exposure.ServiceBinding
	for rows.Next() {
		var owner string
		var b exposure.ServiceBinding
		if err = rows.Scan(&owner, &b.ServiceID, &b.Transport, &b.Mode, &b.HostAddress, &b.HostPort); err != nil {
			return err
		}
		if owner == installation {
			own = append(own, b)
		} else {
			reserved = append(reserved, b)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, want := range requested {
		for _, used := range reserved {
			if exposure.BindingsConflict(want, used) {
				return fmt.Errorf("binding is reserved by another KITPro installation: %s:%d/%s", want.HostAddress, want.HostPort, want.Transport)
			}
		}
	}
	listenerSource := a.hostListeners
	if listenerSource == nil {
		listenerSource = exposure.HostListeners
	}
	listeners, err := listenerSource()
	if err != nil {
		return fmt.Errorf("host listener preflight unavailable: %w", err)
	}
	for _, want := range requested {
		for _, listener := range listeners {
			if !exposure.BindingsConflict(want, listener) {
				continue
			}
			owned := false
			for _, current := range own {
				if current.Transport == want.Transport && current.HostAddress == want.HostAddress && current.HostPort == want.HostPort {
					owned = true
					break
				}
			}
			if !owned {
				return fmt.Errorf("host listener %s:%d/%s conflicts with requested %s:%d/%s", listener.HostAddress, listener.HostPort, listener.Transport, want.HostAddress, want.HostPort, want.Transport)
			}
		}
	}
	return nil
}

func (a *app) repairCommittedProjection(ctx context.Context, installationID, sourceOperation string, value any, updateDesired bool) error {
	committed, err := decodeCommittedLifecycleResult(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT runtime_generation FROM installations WHERE installation_id=?`, installationID).Scan(&current); err != nil {
		return err
	}
	if current > committed.Generation {
		return errors.New("control projection is newer than helper generation")
	}
	query := `UPDATE installations SET runtime_generation=?,release_id=?,runtime_state=?,projection_repaired_at=?,projection_source_operation_id=?,updated_at=?`
	args := []any{committed.Generation, committed.ReleaseID, committed.RuntimeState, now, sourceOperation, now}
	if updateDesired {
		query += `,desired_state=?`
		args = append(args, committed.RuntimeState)
	}
	query += ` WHERE installation_id=? AND runtime_generation<=?`
	args = append(args, installationID, committed.Generation)
	updated, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if changed, _ := updated.RowsAffected(); changed != 1 {
		return errors.New("control projection repair lost")
	}
	for _, binding := range committed.Bindings {
		if err = upsertExposureProjection(ctx, tx, installationID, binding, env("KITPRO_LAN_BIND_ADDRESS", ""), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *app) projectReconciliation(ctx context.Context, sourceOperation string, value any) (reconciliationResult, error) {
	result, err := decodeReconciliationResult(value)
	if err != nil {
		return result, err
	}
	codes, err := json.Marshal(result.MismatchCodes)
	if err != nil {
		return result, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE installations SET runtime_state=?,reconciliation_state=?,reconciliation_codes_json=?,reconciled_at=?,updated_at=? WHERE installation_id=?`, result.RuntimeState, result.State, string(codes), result.ObservedAt, time.Now().UTC().Format(time.RFC3339Nano), result.InstallationID)
	if err != nil {
		return result, err
	}
	if changed, _ := updated.RowsAffected(); changed != 1 {
		return result, errors.New("reconciliation projection installation mismatch")
	}
	if result.Projection != nil {
		var current int
		if err = tx.QueryRowContext(ctx, `SELECT runtime_generation FROM installations WHERE installation_id=?`, result.InstallationID).Scan(&current); err != nil {
			return result, err
		}
		if current > result.Projection.Generation {
			return result, errors.New("control projection is newer than helper generation")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE installations SET runtime_generation=?,release_id=?,projection_repaired_at=?,projection_source_operation_id=? WHERE installation_id=?`, result.Projection.Generation, result.Projection.ReleaseID, result.ObservedAt, sourceOperation, result.InstallationID); err != nil {
			return result, err
		}
		for _, binding := range result.Projection.Bindings {
			if err = upsertExposureProjection(ctx, tx, result.InstallationID, binding, env("KITPRO_LAN_BIND_ADDRESS", ""), result.ObservedAt); err != nil {
				return result, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func operationErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "storage unavailable") || strings.Contains(message, "storage identity changed") {
		return "storage_unavailable"
	}
	if strings.Contains(message, "address already in use") || strings.Contains(message, "port is already allocated") || strings.Contains(message, "port collision") || strings.HasPrefix(message, "binding conflict:") || strings.HasPrefix(message, "binding is reserved by another kitpro installation:") || strings.HasPrefix(message, "host listener ") {
		return "port_collision"
	}
	if strings.Contains(message, "validation") || strings.Contains(message, "mismatch") || strings.Contains(message, "rejected") {
		return "policy_rejected"
	}
	return "helper_failed"
}

func safeBindingConflictSummary(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	message := err.Error()
	lower := strings.ToLower(message)
	for _, prefix := range []string{"binding conflict:", "binding is reserved by another kitpro installation:", "host listener "} {
		if strings.HasPrefix(lower, prefix) {
			return message, true
		}
	}
	return "", false
}

func safeOperationSummary(category string) string {
	switch category {
	case "storage_unavailable":
		return "storage unavailable"
	case "port_collision":
		return "host port unavailable"
	case "policy_rejected":
		return "operation rejected by helper policy"
	default:
		return "privileged operation failed"
	}
}

func (a *app) apps(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/apps")
	if path == "" || path == "/" {
		type item struct {
			ID               string   `json:"id"`
			Name             string   `json:"name"`
			Description      string   `json:"description,omitempty"`
			Category         string   `json:"category,omitempty"`
			Kind             string   `json:"kind,omitempty"`
			CatalogStatus    string   `json:"catalog_status,omitempty"`
			WebsiteURL       string   `json:"website_url,omitempty"`
			SourceURL        string   `json:"source_url,omitempty"`
			DocumentationURL string   `json:"documentation_url,omitempty"`
			Logo             string   `json:"logo,omitempty"`
			Limitations      []string `json:"limitations,omitempty"`
			Releases         []string `json:"releases"`
		}
		out := []item{}
		for _, id := range catalog.IDs(a.catalog) {
			m := a.catalog[id].Manifest
			versions := make([]string, 0, len(m.Releases))
			for _, rel := range m.Releases {
				versions = append(versions, rel.Version)
			}
			out = append(out, item{ID: m.ID, Name: m.Name, Description: m.Description, Category: m.Category, Kind: m.Kind, CatalogStatus: m.CatalogStatus, WebsiteURL: m.WebsiteURL, SourceURL: m.SourceURL, DocumentationURL: m.DocumentationURL, Logo: m.Logo, Limitations: append([]string(nil), m.Limitations...), Releases: versions})
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	entry, ok := a.catalog[parts[0]]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "install" {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		} // the same constrained path as the legacy test operation
		r.URL.Path = "/api/v1/operations"
		q := r.URL.Query()
		q.Set("app_id", parts[0])
		r.URL.RawQuery = q.Encode()
		r = r.WithContext(context.WithValue(r.Context(), internalDispatchKey{}, true))
		a.ops(w, r)
		return
	}
	m := entry.Manifest
	type detail struct {
		ID               string                    `json:"id"`
		Name             string                    `json:"name"`
		Description      string                    `json:"description,omitempty"`
		Category         string                    `json:"category,omitempty"`
		Kind             string                    `json:"kind,omitempty"`
		CatalogStatus    string                    `json:"catalog_status,omitempty"`
		WebsiteURL       string                    `json:"website_url,omitempty"`
		SourceURL        string                    `json:"source_url,omitempty"`
		DocumentationURL string                    `json:"documentation_url,omitempty"`
		Logo             string                    `json:"logo,omitempty"`
		Limitations      []string                  `json:"limitations,omitempty"`
		LifecycleNotice  *manifest.LifecycleNotice `json:"lifecycle_notice,omitempty"`
		SchemaVersion    int                       `json:"schema_version"`
		Releases         []manifest.Release        `json:"releases"`
		Storage          []manifest.Storage        `json:"storage,omitempty"`
	}
	_ = json.NewEncoder(w).Encode(detail{ID: m.ID, Name: m.Name, Description: m.Description, Category: m.Category, Kind: m.Kind, CatalogStatus: m.CatalogStatus, WebsiteURL: m.WebsiteURL, SourceURL: m.SourceURL, DocumentationURL: m.DocumentationURL, Logo: m.Logo, Limitations: append([]string(nil), m.Limitations...), LifecycleNotice: m.LifecycleNotice, SchemaVersion: m.SchemaVersion, Releases: m.Releases, Storage: m.Storage})
}

func (a *app) runInstallationLifecycle(w http.ResponseWriter, r *http.Request, installationID, action string) {
	var generation int
	var appID string
	if err := a.db.QueryRowContext(r.Context(), "SELECT runtime_generation,application_id FROM installations WHERE installation_id=?", installationID).Scan(&generation, &appID); err != nil {
		http.NotFound(w, r)
		return
	}
	acknowledged, err := lifecycleAcknowledgement(r)
	if err != nil {
		http.Error(w, "invalid acknowledgement", http.StatusBadRequest)
		return
	}
	if a.requiresLifecycleAcknowledgement(appID) && action != "start" && !acknowledged {
		http.Error(w, "acknowledgement is required for this network service operation", http.StatusPreconditionRequired)
		return
	}
	operationType := map[string]string{"start": "StartApplication", "stop": "StopApplication", "restart": "RestartApplication", "remove": "RemoveApplication"}[action]
	desired := map[string]string{"start": "running", "stop": "stopped", "restart": "running", "remove": "runtime_removed"}[action]
	operationID := operations.NewID()
	if err := operations.Insert(r.Context(), a.db, operationID, operationType, installationID); err != nil {
		http.Error(w, "operation state unavailable", http.StatusInternalServerError)
		return
	}
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: operationType, OperationRevision: 1, InstanceID: installationID, RuntimeGeneration: generation})
	if err == nil && !response.OK {
		err = fmt.Errorf("helper rejected operation")
	}
	status, summary := "succeeded", "helper accepted"
	if err != nil {
		status, summary = "failed", safeOperationSummary(operationErrorCategory(err))
	} else if helperStatePending(response.State) {
		status, summary = "accepted", "privileged operation continues in helper"
	}
	if err != nil {
		_ = operations.Update(r.Context(), a.db, operationID, status, summary)
		http.Error(w, "KITPro could not complete the lifecycle operation", http.StatusServiceUnavailable)
		return
	}
	if status == "succeeded" {
		if response.State != "" {
			if projectionErr := a.repairCommittedProjection(r.Context(), installationID, operationID, response.Result, true); projectionErr != nil {
				status, summary = "helper_succeeded_projection_pending", "privileged operation completed; control projection repair pending"
			}
		} else {
			_, _ = a.db.ExecContext(r.Context(), "UPDATE installations SET desired_state=?,runtime_state=?,updated_at=? WHERE installation_id=?", desired, desired, time.Now().UTC().Format(time.RFC3339Nano), installationID)
		}
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": operationID, "status": status})
}

func (a *app) requiresLifecycleAcknowledgement(appID string) bool {
	entry, ok := a.catalog[appID]
	return ok && entry.Manifest.Kind == "network-service" && entry.Manifest.LifecycleNotice != nil && entry.Manifest.LifecycleNotice.RequireAcknowledgement
}

func lifecycleAcknowledgement(r *http.Request) (bool, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var input struct {
			Acknowledged bool `json:"acknowledged"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil && err != io.EOF {
			return false, err
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return false, errors.New("trailing request data")
		}
		return input.Acknowledged, nil
	}
	if err := r.ParseForm(); err != nil {
		return false, err
	}
	return r.FormValue("acknowledgement") == "accepted", nil
}

func (a *app) installations(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/installations/"), "/"), "/")
	if len(parts) == 4 && parts[1] == "credentials" && parts[3] == "reveal" {
		a.revealApplicationCredential(w, r, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "reconciliation" {
		a.installationReconciliation(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "repair" {
		a.installationRepair(w, r, parts[0])
		return
	}
	if len(parts) == 2 && (parts[1] == "backup" || parts[1] == "restore") {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		a.applicationBackupOperation(w, r, parts[0], parts[1])
		return
	}
	if len(parts) == 2 && (parts[1] == "start" || parts[1] == "stop" || parts[1] == "restart" || parts[1] == "remove") {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		a.runInstallationLifecycle(w, r, parts[0], parts[1])
		return
	}
	if len(parts) == 2 && parts[1] == "update" {
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		var in struct {
			Release      string `json:"release"`
			Acknowledged bool   `json:"acknowledged"`
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil {
				http.Error(w, "release is required", 400)
				return
			}
			var trailing any
			if dec.Decode(&trailing) != io.EOF {
				http.Error(w, "invalid request", 400)
				return
			}
		} else {
			in.Release = r.FormValue("release")
			in.Acknowledged = r.FormValue("acknowledgement") == "accepted"
		}
		if in.Release == "" {
			http.Error(w, "release is required", 400)
			return
		}
		var appID, current string
		if err := a.db.QueryRowContext(r.Context(), "SELECT application_id,release_id FROM installations WHERE installation_id=?", parts[0]).Scan(&appID, &current); err != nil {
			http.NotFound(w, r)
			return
		}
		entry, ok := a.catalog[appID]
		if !ok {
			http.Error(w, "catalog unavailable", 500)
			return
		}
		if a.requiresLifecycleAcknowledgement(appID) && !in.Acknowledged {
			http.Error(w, "acknowledgement is required for this network service operation", http.StatusPreconditionRequired)
			return
		}
		found := false
		for _, rel := range entry.Manifest.Releases {
			if rel.Version == in.Release {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "release is not trusted", 400)
			return
		}
		if in.Release == current {
			http.Error(w, "installation already uses release", 409)
			return
		}
		backupPath, backupErr := backup.Vacuum(r.Context(), a.db, env("KITPRO_CONTROL_BACKUP_DIR", "/var/lib/kitpro-api/backups"), "pre-app-update-"+operations.NewID()+".db")
		if backupErr != nil {
			slog.Default().Warn("application update backup failed", "event", "app_update_failed", "installation_id", parts[0], "result", "failed", "error_category", "backup")
			http.Error(w, "pre-update backup failed", 503)
			return
		}
		slog.Default().Info("application update started", "event", "app_update_started", "installation_id", parts[0], "from_release", current, "to_release", in.Release, "backup", backupPath)
		q := r.URL.Query()
		q.Set("installation_id", parts[0])
		q.Set("app_id", appID)
		q.Set("target_release", in.Release)
		q.Set("operation_type", "UpdateApplication")
		r.URL.Path = "/api/v1/operations"
		r.URL.RawQuery = q.Encode()
		r = r.WithContext(context.WithValue(r.Context(), internalDispatchKey{}, true))
		a.ops(w, r)
		return
	}
	if len(parts) >= 2 && parts[1] == "services" {
		inst := parts[0]
		service := ""
		if len(parts) >= 4 && parts[3] == "exposure" {
			service = parts[2]
		}
		if r.Method == "GET" {
			if service != "" {
				http.Error(w, "method", 405)
				return
			}
			out, err := a.serviceStatuses(r.Context(), inst)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if service == "" {
			http.Error(w, "service required", 400)
			return
		}
		var in struct {
			Mode         string `json:"mode"`
			Acknowledged bool   `json:"acknowledged"`
		}
		if r.Method == http.MethodDelete {
			in.Mode = string(exposure.Internal)
			in.Acknowledged = r.URL.Query().Get("acknowledgement") == "accepted"
		} else if r.Method == http.MethodPost {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&in) != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				var trailing any
				if decoder.Decode(&trailing) != io.EOF {
					http.Error(w, "invalid request", 400)
					return
				}
			} else {
				in.Mode = r.FormValue("mode")
				in.Acknowledged = r.FormValue("acknowledgement") == "accepted"
			}
		} else {
			http.Error(w, "method", 405)
			return
		}
		if in.Mode != string(exposure.Internal) && in.Mode != string(exposure.Loopback) && in.Mode != string(exposure.LAN) {
			http.Error(w, "invalid exposure mode", 400)
			return
		}
		var appID string
		var gen int
		if e := a.db.QueryRow("SELECT application_id,runtime_generation FROM installations WHERE installation_id=?", inst).Scan(&appID, &gen); e != nil {
			http.NotFound(w, r)
			return
		}
		entry, ok := a.catalog[appID]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if a.requiresLifecycleAcknowledgement(appID) && !in.Acknowledged {
			http.Error(w, "acknowledgement is required for this network service operation", http.StatusPreconditionRequired)
			return
		}
		var svc manifest.Service
		found := false
		for _, s := range entry.Manifest.Services {
			if s.ID == service {
				svc = s
				found = true
			}
		}
		if !found {
			http.Error(w, "service not declared", 400)
			return
		}
		addr := ""
		port := 0
		transport, transportErr := exposure.TransportFor(svc.Protocol)
		if transportErr != nil {
			http.Error(w, "service transport is unsupported", 500)
			return
		}
		old, oldErr := exposure.Get(r.Context(), a.db, inst, service)
		if oldErr == nil {
			port = old.HostPort
		}
		slog.Default().Info("service exposure requested", "event", "exposure_requested", "installation_id", inst, "service_id", service, "mode", in.Mode)
		if in.Mode == string(exposure.Loopback) {
			addr = "127.0.0.1"
		}
		if in.Mode == string(exposure.LAN) {
			addr = env("KITPRO_LAN_BIND_ADDRESS", "")
			if addr == "" {
				http.Error(w, "LAN bind address not configured", 400)
				return
			}
			if e := exposure.VerifyLocalAddress(addr); e != nil {
				http.Error(w, "LAN bind address is not assigned", 400)
				return
			}
		}
		if in.Mode != string(exposure.Internal) {
			if svc.FixedHostPort != 0 {
				port = svc.FixedHostPort
			} else if port > 0 {
				slog.Default().Info("service exposure port reused", "event", "exposure_reused", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "host_port", port)
			} else {
				used := map[int]bool{}
				rows, queryErr := a.db.Query("SELECT transport,mode,host_address,host_port FROM installation_service_exposure WHERE host_port>0")
				if queryErr != nil {
					http.Error(w, "exposure state unavailable", 500)
					return
				}
				for rows.Next() {
					var storedTransport exposure.Transport
					var storedMode exposure.Mode
					var storedAddress string
					var p int
					if scanErr := rows.Scan(&storedTransport, &storedMode, &storedAddress, &p); scanErr != nil {
						_ = rows.Close()
						http.Error(w, "exposure state unavailable", 500)
						return
					}
					if storedTransport == transport && (storedMode == exposure.Internal || exposure.BindingsConflict(exposure.ServiceBinding{Transport: transport, Mode: exposure.Mode(in.Mode), HostAddress: addr, HostPort: p}, exposure.ServiceBinding{Transport: storedTransport, Mode: storedMode, HostAddress: storedAddress, HostPort: p})) {
						used[p] = true
					}
				}
				if rowsErr := rows.Err(); rowsErr != nil {
					_ = rows.Close()
					http.Error(w, "exposure state unavailable", 500)
					return
				}
				_ = rows.Close()
				var ae error
				allocator := a.allocatePort
				if allocator != nil && transport == exposure.TCP {
					port, ae = allocator(used, addr)
				} else {
					port, ae = exposure.AllocateAvailableTransport(used, addr, transport)
				}
				if ae != nil {
					slog.Default().Warn("service exposure collision", "event", "exposure_collision", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "result", "failed", "error_category", "port_unavailable")
					http.Error(w, ae.Error(), 409)
					return
				}
				slog.Default().Info("service exposure port allocated", "event", "exposure_allocated", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "host_port", port, "result", "allocated")
			}
		}
		q := r.URL.Query()
		q.Set("installation_id", inst)
		q.Set("service_id", service)
		q.Set("exposure_mode", in.Mode)
		q.Set("host_address", addr)
		q.Set("host_port", strconv.Itoa(port))
		q.Set("container_port", strconv.Itoa(svc.ContainerPort))
		q.Set("service_protocol", svc.Protocol)
		q.Set("operation_type", "ConfigureServiceExposure")
		r.URL.Path = "/api/v1/operations"
		r.URL.RawQuery = q.Encode()
		r.Method = http.MethodPost
		r = r.WithContext(context.WithValue(r.Context(), internalDispatchKey{}, true))
		a.ops(w, r)
		return
	}
	if len(parts) != 2 || parts[1] != "recreate" || r.Method != "POST" {
		http.Error(w, "use POST /api/v1/installations/{id}/recreate", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	installationID := parts[0]
	q.Set("installation_id", installationID)
	var appID string
	if err := a.db.QueryRowContext(r.Context(), "SELECT application_id FROM installations WHERE installation_id=?", installationID).Scan(&appID); err != nil {
		http.NotFound(w, r)
		return
	}
	acknowledged, err := lifecycleAcknowledgement(r)
	if err != nil {
		http.Error(w, "invalid acknowledgement", http.StatusBadRequest)
		return
	}
	if a.requiresLifecycleAcknowledgement(appID) && !acknowledged {
		http.Error(w, "acknowledgement is required for this network service operation", http.StatusPreconditionRequired)
		return
	}
	q.Set("operation_type", "RecreateApplication")
	r.URL.Path = "/api/v1/operations"
	r.URL.RawQuery = q.Encode()
	r = r.WithContext(context.WithValue(r.Context(), internalDispatchKey{}, true))
	a.ops(w, r)
}

func (a *app) revealApplicationCredential(w http.ResponseWriter, r *http.Request, installationID, credentialID string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var applicationID string
	if err := a.db.QueryRowContext(r.Context(), `SELECT application_id FROM installations WHERE installation_id=?`, installationID).Scan(&applicationID); err != nil {
		http.Error(w, "application credential unavailable", http.StatusNotFound)
		return
	}
	entry, ok := a.catalog[applicationID]
	if !ok {
		http.Error(w, "application credential unavailable", http.StatusNotFound)
		return
	}
	credential, ok := manifest.FindPresentedCredential(entry.Manifest, credentialID)
	if !ok {
		http.Error(w, "application credential unavailable", http.StatusNotFound)
		return
	}
	response, err := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), Operation: "RevealApplicationCredential", InstanceID: installationID, ApplicationID: applicationID, CredentialID: credentialID})
	if err != nil || !response.OK {
		http.Error(w, "application credential unavailable", http.StatusServiceUnavailable)
		return
	}
	disclosure, err := decodeCredentialDisclosure(response.Result)
	if err != nil || disclosure.ID != credential.ID || disclosure.Label != credential.Label || disclosure.Username != credential.Username || len(disclosure.Value) != 64 {
		http.Error(w, "application credential unavailable", http.StatusBadGateway)
		return
	}
	if _, err = hex.DecodeString(disclosure.Value); err != nil {
		http.Error(w, "application credential unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(disclosure)
}

func decodeCredentialDisclosure(value any) (protocol.CredentialDisclosure, error) {
	if disclosure, ok := value.(protocol.CredentialDisclosure); ok {
		return disclosure, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return protocol.CredentialDisclosure{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var disclosure protocol.CredentialDisclosure
	if err = decoder.Decode(&disclosure); err != nil {
		return protocol.CredentialDisclosure{}, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return protocol.CredentialDisclosure{}, errors.New("invalid credential disclosure")
	}
	return disclosure, nil
}

func (a *app) installationReconciliation(w http.ResponseWriter, r *http.Request, installationID string) {
	if r.Method == http.MethodGet {
		response, err := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), Operation: "GetReconciliation", InstanceID: installationID})
		if err != nil || !response.OK {
			http.Error(w, "reconciliation state unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response.Result)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var exists int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM installations WHERE installation_id=?`, installationID).Scan(&exists); err != nil || exists != 1 {
		http.NotFound(w, r)
		return
	}
	operationID := operations.NewID()
	if err := operations.Insert(r.Context(), a.db, operationID, "ReconcileInstallation", installationID); err != nil {
		http.Error(w, "operation state unavailable", http.StatusInternalServerError)
		return
	}
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: "ReconcileInstallation", OperationRevision: 1, InstanceID: installationID})
	if err == nil && !response.OK {
		err = errors.New("helper rejected reconciliation")
	}
	status, summary := "succeeded", "installation reconciled"
	if err == nil && helperStatePending(response.State) {
		status, summary = "accepted", "reconciliation continues in helper"
	} else if err == nil {
		_, err = a.projectReconciliation(r.Context(), operationID, response.Result)
	}
	if err != nil {
		status, summary = "failed", "installation reconciliation failed"
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	if err != nil {
		http.Error(w, summary, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": operationID, "status": status, "result": response.Result})
}

func (a *app) installationRepair(w http.ResponseWriter, r *http.Request, installationID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Action == "" {
		http.Error(w, "repair action is required", http.StatusBadRequest)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if input.Action == "recreate_generation" {
		observed, err := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), Operation: "GetReconciliation", InstanceID: installationID})
		if err != nil || !observed.OK {
			http.Error(w, "reconciliation state unavailable", http.StatusConflict)
			return
		}
		result, err := decodeReconciliationResult(observed.Result)
		if err != nil || result.RecommendedAction != "recreate_generation" {
			http.Error(w, "installation is not eligible for controlled recreation", http.StatusConflict)
			return
		}
		query := r.URL.Query()
		query.Set("repair_action", input.Action)
		r.URL.Path = "/api/v1/installations/" + installationID + "/recreate"
		r.URL.RawQuery = query.Encode()
		a.installations(w, r)
		return
	}
	if input.Action != "start_active" && input.Action != "cleanup_resources" && input.Action != "acknowledge_retained_missing" {
		http.Error(w, "unsupported repair action", http.StatusBadRequest)
		return
	}
	operationID := operations.NewID()
	if err := operations.Insert(r.Context(), a.db, operationID, "RepairInstallation", installationID); err != nil {
		http.Error(w, "operation state unavailable", http.StatusInternalServerError)
		return
	}
	response, err := a.callHelperOperation(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: "RepairInstallation", OperationRevision: 1, InstanceID: installationID, RepairAction: input.Action})
	if err == nil && !response.OK {
		err = errors.New("helper rejected repair")
	}
	status, summary := "succeeded", "installation repair completed"
	if err == nil && helperStatePending(response.State) {
		status, summary = "accepted", "installation repair continues in helper"
	} else if err == nil {
		_, err = a.projectReconciliation(r.Context(), operationID, response.Result)
	}
	if err != nil {
		status, summary = "failed", "installation repair failed"
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	if err != nil {
		http.Error(w, summary, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": operationID, "status": status, "result": response.Result})
}

func (a *app) applicationBackupOperation(w http.ResponseWriter, r *http.Request, installationID, action string) {
	var applicationID, releaseID string
	var generation int
	if err := a.db.QueryRowContext(r.Context(), `SELECT application_id,release_id,runtime_generation FROM installations WHERE installation_id=?`, installationID).Scan(&applicationID, &releaseID, &generation); err != nil {
		http.NotFound(w, r)
		return
	}
	entry, ok := a.catalog[applicationID]
	if !ok || entry.Manifest.Backup == nil {
		http.Error(w, "application backup policy unavailable", http.StatusConflict)
		return
	}
	operationType := "CreateApplicationBackup"
	operationID := operations.NewID()
	request := protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: operationID, Operation: operationType, OperationRevision: 1, InstanceID: installationID, ApplicationID: applicationID, ReleaseID: releaseID, RuntimeGeneration: generation}
	if action == "restore" {
		operationType = "RestoreApplicationBackup"
		request.Operation = operationType
		var input struct {
			BackupID string `json:"backup_id"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.BackupID == "" {
			http.Error(w, "backup_id is required", http.StatusBadRequest)
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.BackupID = input.BackupID
	}
	if err := operations.Insert(r.Context(), a.db, operationID, operationType, installationID); err != nil {
		http.Error(w, "operation state unavailable", http.StatusInternalServerError)
		return
	}
	response, err := a.callHelperOperation(request)
	if err == nil && !response.OK {
		err = fmt.Errorf("helper rejected application backup operation")
	}
	status, summary := "succeeded", "application backup operation completed"
	if err != nil {
		status, summary = "failed", "application backup operation failed"
	} else if helperStatePending(response.State) {
		status, summary = "accepted", "application backup operation continues in helper"
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	if err != nil {
		http.Error(w, "KITPro could not complete the application backup operation", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response.Result)
}

func (a *app) serviceStatuses(ctx context.Context, installationID string) ([]serviceStatus, error) {
	var appID string
	if err := a.db.QueryRowContext(ctx, "SELECT application_id FROM installations WHERE installation_id=?", installationID).Scan(&appID); err != nil {
		return nil, err
	}
	entry, ok := a.catalog[appID]
	if !ok {
		return nil, fmt.Errorf("catalog application unavailable")
	}
	out := make([]serviceStatus, 0, len(entry.Manifest.Services))
	for _, declared := range entry.Manifest.Services {
		transport, _ := exposure.ContainerProtocol(declared.Protocol)
		status := serviceStatus{ID: declared.ID, Name: declared.Name, Protocol: declared.Protocol, Transport: transport, ContainerPort: declared.ContainerPort, Mode: string(exposure.Internal), ModeLabel: "Private"}
		if record, err := exposure.Get(ctx, a.db, installationID, declared.ID); err == nil {
			status.Mode, status.HostAddress, status.HostPort = string(record.Mode), record.HostAddress, record.HostPort
			if record.Mode != exposure.Internal {
				if declared.Protocol == "http" || declared.Protocol == "https" {
					status.Endpoint = declared.Protocol + "://" + net.JoinHostPort(record.HostAddress, strconv.Itoa(record.HostPort))
					status.OpenEndpoint = status.Endpoint
				} else {
					status.Endpoint = net.JoinHostPort(record.HostAddress, strconv.Itoa(record.HostPort)) + "/" + transport
				}
			}
			status.ModeLabel = exposureLabel(status.Mode)
		}
		out = append(out, status)
	}
	return out, nil
}
func (a *app) host(w http.ResponseWriter, r *http.Request) {
	v, e := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectRuntime"})
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	json.NewEncoder(w).Encode(v)
}

func (a *app) callHelper(request protocol.Request) (protocol.Response, error) {
	if a.helperCall != nil {
		return a.helperCall(request)
	}
	connectTimeout := helperDuration("KITPRO_HELPER_CONNECT_TIMEOUT", 2*time.Second)
	writeTimeout := helperDuration("KITPRO_HELPER_WRITE_TIMEOUT", 10*time.Second)
	// Pulls and application backups can legitimately run for a long time. This
	// bounded, configurable wait limits the API transport only; expiry never
	// cancels helper execution.
	responseTimeout := helperDuration("KITPRO_HELPER_RESPONSE_TIMEOUT", 30*time.Minute)
	dialer := net.Dialer{Timeout: connectTimeout}
	connection, err := dialer.Dial("unix", a.helper)
	if err != nil {
		return protocol.Response{}, helperTransportError{cause: err, acceptancePossible: false}
	}
	defer connection.Close()
	if request.Deadline == "" {
		request.Deadline = time.Now().Add(responseTimeout).UTC().Format(time.RFC3339Nano)
	}
	if err = connection.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return protocol.Response{}, helperTransportError{cause: err, acceptancePossible: false}
	}
	if err = protocol.Write(connection, request); err != nil {
		return protocol.Response{}, helperTransportError{cause: err, acceptancePossible: true}
	}
	if err = connection.SetReadDeadline(time.Now().Add(responseTimeout)); err != nil {
		return protocol.Response{}, helperTransportError{cause: err, acceptancePossible: true}
	}
	response, err := protocol.ReadResponse(connection)
	if err != nil {
		return protocol.Response{}, helperTransportError{cause: err, acceptancePossible: true}
	}
	return response, nil
}

type helperTransportError struct {
	cause              error
	acceptancePossible bool
}

func (e helperTransportError) Error() string { return e.cause.Error() }
func (e helperTransportError) Unwrap() error { return e.cause }

// callHelperOperation resolves a lost mutation response by semantic operation
// ID. If both transports are unavailable, it preserves an accepted projection:
// absence of a response is not evidence that privileged execution failed.
func (a *app) callHelperOperation(request protocol.Request) (protocol.Response, error) {
	response, err := a.callHelper(request)
	if err == nil {
		return response, nil
	}
	lookup, lookupErr := a.callHelper(protocol.Request{Version: 2, ID: operations.NewRequestID(), OperationID: request.OperationID, Operation: "GetOperation"})
	if lookupErr == nil && lookup.ErrorCode != "OperationNotFound" {
		return lookup, nil
	}
	var transport helperTransportError
	if errors.As(err, &transport) && !transport.acceptancePossible {
		return protocol.Response{}, err
	}
	return protocol.Response{OK: true, RequestID: request.ID, OperationID: request.OperationID, State: "accepted", Phase: "transport_outcome_unknown"}, nil
}

func helperStatePending(state string) bool {
	return state == "accepted" || state == "executing" || state == "reconciling"
}

func isCommittedLifecycleOperation(operation string) bool {
	switch operation {
	case "InstallApplication", "UpdateApplication", "ConfigureServiceExposure", "RecreateApplication", "StartApplication", "StopApplication", "RestartApplication", "RemoveApplication":
		return true
	default:
		return false
	}
}

func helperDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var _ embed.FS
