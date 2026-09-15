package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/maintenance"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/operations"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
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
}

type internalDispatchKey struct{}

type serviceStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	ContainerPort int    `json:"container_port"`
	Mode          string `json:"mode"`
	HostAddress   string `json:"host_address,omitempty"`
	HostPort      int    `json:"host_port,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
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
	http.HandleFunc("/api/v1/reconcile/", a.guard(a.reconcile))
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
	c, e := net.Dial("unix", a.helper)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	defer c.Close()
	protocol.Write(c, protocol.Request{Version: 1, ID: id, Operation: "BackupHelperState"})
	resp, e := protocol.ReadResponse(c)
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
		"catalog_schema_version": manifest.ExternalStorageSchemaVersion,
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
	case http.MethodDelete:
		request.Operation = "RemoveStorageRoot"
		request.RootID = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/storage-roots/"), "/")
	default:
		http.Error(w, "method denied", http.StatusMethodNotAllowed)
		return
	}
	response, err := a.callHelper(request)
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
func (a *app) reconcile(w http.ResponseWriter, r *http.Request) {
	inst := strings.TrimPrefix(r.URL.Path, "/api/v1/reconcile/")
	if inst == "" {
		http.Error(w, "instance required", 400)
		return
	}
	var desired string
	if e := a.db.QueryRowContext(r.Context(), "SELECT desired_state FROM installations WHERE installation_id=?", inst).Scan(&desired); e == nil && desired == "runtime_removed" {
		json.NewEncoder(w).Encode(protocol.Response{OK: true, RequestID: operations.NewID(), Result: map[string]string{"classification": "runtime_removed", "summary": "runtime intentionally removed; installation and data retained"}})
		return
	}
	c, e := net.Dial("unix", a.helper)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	defer c.Close()
	id := operations.NewID()
	q := protocol.Request{Version: 1, ID: id, Operation: "ReconcileTestWorkload", InstanceID: inst}
	var appID string
	if err := a.db.QueryRowContext(r.Context(), "SELECT application_id FROM installations WHERE installation_id=?", inst).Scan(&appID); err == nil {
		if entry, ok := a.catalog[appID]; ok {
			for _, service := range entry.Manifest.Services {
				if record, err := exposure.Get(r.Context(), a.db, inst, service.ID); err == nil {
					q.ServiceID = service.ID
					q.ExposureMode = string(record.Mode)
					q.HostAddress = record.HostAddress
					q.HostPort = record.HostPort
					q.ContainerPort = service.ContainerPort
					q.ServiceProtocol = service.Protocol
					break
				}
			}
		}
	}
	protocol.Write(c, q)
	resp, e := protocol.ReadResponse(c)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	json.NewEncoder(w).Encode(resp)
}
func (a *app) home(w http.ResponseWriter, r *http.Request) {
	type appView struct {
		ID, Name, Description, Release, Category, Initials, Hardware string
		InstalledCount                                               int
		HardwareUnavailable                                          bool
		StorageSlots                                                 []storageSlotView
	}
	type installationView struct {
		ID, Name, Description, Category, Initials, State, StateLabel, StateClass, AppID, Release string
		AccessLabel, OpenEndpoint, AvailableRelease, HardwareLabel, HardwareClass, HardwareID    string
		Generation, InstalledCount                                                               int
		UpdateAvailable                                                                          bool
		Services                                                                                 []serviceStatus
		Components                                                                               []string
		Storage                                                                                  []installedStorageView
	}
	type operationView struct {
		Title, Initial, StatusLabel, StatusClass, Summary, RequestedAt, TimeLabel string
	}
	type healthView struct {
		Label, Headline, Description, Docker, Helper, Class string
	}

	storageRoots := a.trustedStorageRoots()
	installations := []installationView{}
	installedCountByApp := map[string]int{}
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
			view := installationView{ID: id, Name: appID, AppID: appID, State: desired, Generation: generation, AccessLabel: "Private"}
			services, _ := a.serviceStatuses(r.Context(), id)
			view.Services = services
			_ = a.db.QueryRowContext(r.Context(), "SELECT release_id FROM installations WHERE installation_id=?", id).Scan(&view.Release)
			if entry, ok := a.catalog[appID]; ok {
				m := entry.Manifest
				view.Name, view.Description, view.Category, view.Initials = m.Name, m.Description, catalogCategory(m.ID), appInitials(m.Name)
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
				if services[i].Endpoint != "" && view.OpenEndpoint == "" {
					view.OpenEndpoint = services[i].Endpoint
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
		app := appView{ID: m.ID, Name: m.Name, Description: m.Description, Release: release, Category: catalogCategory(m.ID), Initials: appInitials(m.Name), Hardware: hardwareLabel, HardwareUnavailable: hardwareUnavailable, InstalledCount: installedCountByApp[m.ID]}
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
	health := healthView{Label: "Healthy", Headline: "Your server is ready.", Description: "KITPro is connected to its protected helper and ready to manage trusted applications.", Docker: "Connected", Helper: "Protected", Class: "is-healthy"}
	if response, err := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectDocker"}); err != nil || !response.OK {
		health = healthView{Label: "Attention needed", Headline: "Container services need attention.", Description: "KITPro cannot reach Docker through its protected helper. Existing data remains in place.", Docker: "Unavailable", Helper: "Check service", Class: "needs-attention"}
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
							hardwareSummary = model + " — GPU detected, but Docker GPU runtime is unavailable"
						}
					}
				}
			}
		}
	}
	if err := a.tmpl.Execute(w, map[string]any{"Rows": rows, "Apps": apps, "Installations": installations, "StorageRoots": storageRoots, "CSRF": csrf, "Version": buildinfo.Version, "SourceCommit": buildinfo.SourceCommit, "Platform": platformName(), "Architecture": runtime.GOARCH, "PackageFamily": packageFamily(), "SchemaVersion": schema, "CatalogSchemaVersion": manifest.ExternalStorageSchemaVersion, "Health": health, "InstalledCount": len(installations), "RunningCount": runningCount, "PrivateCount": privateCount, "AttentionCount": attentionCount, "UpdateCount": updateCount, "HardwareSummary": hardwareSummary}); err != nil {
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
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
		}
	}
	return "Linux"
}

func packageFamily() string {
	data, _ := os.ReadFile("/etc/os-release")
	if strings.Contains(string(data), "ID=arch") {
		return "pacman"
	}
	return "apt/dpkg"
}

func catalogVisible(id string) bool { return id != "busybox" }

func catalogCategory(id string) string {
	categories := map[string]string{
		"open-webui": "AI", "it-tools": "Developer Tools",
		"ollama":        "AI",
		"actual-budget": "Finance", "freshrss": "Reading", "home-assistant": "Home automation",
		"mealie": "Food and recipes", "memos": "Notes", "paperless-ngx": "Documents",
		"uptime-kuma": "Monitoring", "vaultwarden": "Security",
		"jellyfin": "Media", "navidrome": "Music", "audiobookshelf": "Media", "sftpgo": "Files",
	}
	if category := categories[id]; category != "" {
		return category
	}
	return "Application"
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
		return "Healthy", "badge-success"
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
		"StartApplication": "Application started", "StopApplication": "Application stopped",
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
	if strings.HasSuffix(r.URL.Path, "/stop") || strings.HasSuffix(r.URL.Path, "/start") || strings.HasSuffix(r.URL.Path, "/remove") {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 {
			http.Error(w, "invalid operation path", 400)
			return
		}
		prior, e := operations.Get(r.Context(), a.db, parts[3])
		if e != nil {
			http.NotFound(w, r)
			return
		}
		id := operations.NewID()
		typ := "StopApplication"
		if strings.HasSuffix(r.URL.Path, "/start") {
			typ = "StartApplication"
		}
		if strings.HasSuffix(r.URL.Path, "/remove") {
			typ = "RemoveApplication"
		}
		if e = operations.Insert(r.Context(), a.db, id, typ, prior.InstanceID); e == nil {
			c, de := net.Dial("unix", a.helper)
			if de == nil {
				defer c.Close()
				protocol.Write(c, protocol.Request{Version: 1, ID: id, Operation: typ, InstanceID: prior.InstanceID})
				var resp protocol.Response
				resp, de = protocol.ReadResponse(c)
				if de == nil && !resp.OK {
					de = fmt.Errorf("helper: %s", resp.Error)
				}
			}
			e = de
		}
		status, summary := "succeeded", "helper accepted"
		if e != nil {
			status, summary = "failed", e.Error()
		}
		operations.Update(r.Context(), a.db, id, status, summary)
		if e == nil {
			desired := "stopped"
			if typ == "StartApplication" {
				desired = "running"
			}
			if typ == "RemoveApplication" {
				desired = "runtime_removed"
			}
			_, _ = a.db.ExecContext(r.Context(), "UPDATE installations SET desired_state=?,updated_at=? WHERE installation_id=?", desired, time.Now().UTC().Format(time.RFC3339Nano), prior.InstanceID)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
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
		release = entry.Manifest.Releases[len(entry.Manifest.Releases)-1].Version
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
	if opType != "InstallApplication" && opType != "ConfigureServiceExposure" && opType != "UpdateApplication" {
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
		_, e := a.db.ExecContext(r.Context(), "INSERT INTO installations(installation_id,application_id,release_id,desired_state,runtime_generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", inst, plan.ApplicationID, plan.ReleaseID, "running", gen, now, now)
		if e != nil {
			operations.Update(r.Context(), a.db, id, "failed", e.Error())
			http.Error(w, "installation state failed", 500)
			return
		}
		for _, service := range plan.Services {
			if _, e = exposure.Upsert(r.Context(), a.db, inst, service.ID, exposure.Assignment{Mode: exposure.Internal}, env("KITPRO_LAN_BIND_ADDRESS", "")); e != nil {
				operations.Update(r.Context(), a.db, id, "failed", e.Error())
				http.Error(w, "service state failed", 500)
				return
			}
		}
	}
	var e error
	helperRejected := false
	var helperRequest protocol.Request
	{
		helperOperation := "InstallApplication"
		if opType == "ConfigureServiceExposure" {
			helperOperation = opType
		} else if opType == "UpdateApplication" {
			helperOperation = "UpdateApplication"
		}
		q := protocol.Request{Version: 1, ID: id, Operation: helperOperation, InstanceID: inst, RuntimeGeneration: gen, Image: plan.ImageDigest, ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID, NetworkName: plan.NetworkName, DataPath: plan.DataPath, RestartPolicy: plan.Restart}
		helperRequest = q
		for _, item := range plan.Hardware {
			q.Hardware = append(q.Hardware, protocol.HardwareRequirement{Class: item.Class, Optional: item.Optional, CPUFallback: item.CPUFallback})
		}
		q.ExposureMode, q.HostAddress, q.HostPort, q.ServiceID, q.ContainerPort, q.ServiceProtocol = r.URL.Query().Get("exposure_mode"), r.URL.Query().Get("host_address"), 0, r.URL.Query().Get("service_id"), 0, r.URL.Query().Get("service_protocol")
		if q.ExposureMode == "" {
			q.ExposureMode = string(exposure.Internal)
		}
		if p, _ := strconv.Atoi(r.URL.Query().Get("host_port")); p > 0 {
			q.HostPort = p
		}
		if p, _ := strconv.Atoi(r.URL.Query().Get("container_port")); p > 0 {
			q.ContainerPort = p
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
		{
			helperRequest = q
			var resp protocol.Response
			resp, e = a.callHelper(q)
			if e == nil && !resp.OK {
				helperRejected = true
				e = fmt.Errorf("helper: %s", resp.Error)
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
		if existing == "" && helperRejected {
			// The helper did not establish trusted runtime ownership. Preserve the
			// installation and its data, but keep the next supported recreation at
			// generation 1 rather than inventing a discarded runtime generation.
			_, _ = a.db.ExecContext(r.Context(), "UPDATE installations SET desired_state='runtime_removed',runtime_generation=0,updated_at=? WHERE installation_id=?", now, inst)
		}
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
	if e == nil && existing != "" {
		updateSQL := "UPDATE installations SET desired_state='running',runtime_generation=?,updated_at=?"
		args := []any{gen, now}
		if opType == "UpdateApplication" {
			updateSQL += ",release_id=?"
			args = append(args, plan.ReleaseID)
		}
		updateSQL += " WHERE installation_id=?"
		args = append(args, inst)
		if _, updateErr := a.db.ExecContext(r.Context(), updateSQL, args...); updateErr != nil {
			status = "failed"
			summary = updateErr.Error()
			e = updateErr
		}
	}
	if opType == "ConfigureServiceExposure" {
		event := "exposure_applied"
		if status == "failed" {
			event = "exposure_failed"
			if failureCategory == "port_collision" {
				event = "exposure_collision"
			}
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

func operationErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "storage unavailable") || strings.Contains(message, "storage identity changed") {
		return "storage_unavailable"
	}
	if strings.Contains(message, "address already in use") || strings.Contains(message, "port is already allocated") || strings.Contains(message, "port collision") {
		return "port_collision"
	}
	if strings.Contains(message, "validation") || strings.Contains(message, "mismatch") || strings.Contains(message, "rejected") {
		return "policy_rejected"
	}
	return "helper_failed"
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
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description,omitempty"`
			Releases    []string `json:"releases"`
		}
		out := []item{}
		for _, id := range catalog.IDs(a.catalog) {
			m := a.catalog[id].Manifest
			versions := make([]string, 0, len(m.Releases))
			for _, rel := range m.Releases {
				versions = append(versions, rel.Version)
			}
			out = append(out, item{m.ID, m.Name, m.Description, versions})
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
		ID            string             `json:"id"`
		Name          string             `json:"name"`
		Description   string             `json:"description,omitempty"`
		SchemaVersion int                `json:"schema_version"`
		Releases      []manifest.Release `json:"releases"`
		Storage       []manifest.Storage `json:"storage,omitempty"`
	}
	_ = json.NewEncoder(w).Encode(detail{m.ID, m.Name, m.Description, m.SchemaVersion, m.Releases, m.Storage})
}

func (a *app) runInstallationLifecycle(w http.ResponseWriter, r *http.Request, installationID, action string) {
	var generation int
	if err := a.db.QueryRowContext(r.Context(), "SELECT runtime_generation FROM installations WHERE installation_id=?", installationID).Scan(&generation); err != nil {
		http.NotFound(w, r)
		return
	}
	operationType := map[string]string{"start": "StartApplication", "stop": "StopApplication", "remove": "RemoveApplication"}[action]
	desired := map[string]string{"start": "running", "stop": "stopped", "remove": "runtime_removed"}[action]
	operationID := operations.NewID()
	if err := operations.Insert(r.Context(), a.db, operationID, operationType, installationID); err != nil {
		http.Error(w, "operation state unavailable", http.StatusInternalServerError)
		return
	}
	response, err := a.callHelper(protocol.Request{Version: 1, ID: operationID, Operation: operationType, InstanceID: installationID, RuntimeGeneration: generation})
	if err == nil && !response.OK {
		err = fmt.Errorf("helper rejected operation")
	}
	status, summary := "succeeded", "helper accepted"
	if err != nil {
		status, summary = "failed", safeOperationSummary(operationErrorCategory(err))
	}
	_ = operations.Update(r.Context(), a.db, operationID, status, summary)
	if err != nil {
		http.Error(w, "KITPro could not complete the lifecycle operation", http.StatusServiceUnavailable)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), "UPDATE installations SET desired_state=?,updated_at=? WHERE installation_id=?", desired, time.Now().UTC().Format(time.RFC3339Nano), installationID)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": operationID, "status": status})
}

func (a *app) installations(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/installations/"), "/"), "/")
	if len(parts) == 2 && (parts[1] == "start" || parts[1] == "stop" || parts[1] == "remove") {
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
			Release string `json:"release"`
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
		for _, service := range entry.Manifest.Services {
			if record, err := exposure.Get(r.Context(), a.db, parts[0], service.ID); err == nil {
				q.Set("service_id", service.ID)
				q.Set("exposure_mode", string(record.Mode))
				q.Set("host_address", record.HostAddress)
				q.Set("host_port", strconv.Itoa(record.HostPort))
				q.Set("container_port", strconv.Itoa(service.ContainerPort))
				q.Set("service_protocol", service.Protocol)
				break
			}
		}
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
			Mode string `json:"mode"`
		}
		if r.Method == http.MethodDelete {
			in.Mode = string(exposure.Internal)
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
			if port > 0 {
				slog.Default().Info("service exposure port reused", "event", "exposure_reused", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "host_port", port)
			} else {
				used := map[int]bool{}
				rows, queryErr := a.db.Query("SELECT host_port FROM installation_service_exposure WHERE host_port IS NOT NULL")
				if queryErr != nil {
					http.Error(w, "exposure state unavailable", 500)
					return
				}
				for rows.Next() {
					var p int
					if scanErr := rows.Scan(&p); scanErr != nil {
						_ = rows.Close()
						http.Error(w, "exposure state unavailable", 500)
						return
					}
					used[p] = true
				}
				if rowsErr := rows.Err(); rowsErr != nil {
					_ = rows.Close()
					http.Error(w, "exposure state unavailable", 500)
					return
				}
				_ = rows.Close()
				var ae error
				allocator := a.allocatePort
				if allocator == nil {
					allocator = exposure.AllocateAvailable
				}
				port, ae = allocator(used, addr)
				if ae != nil {
					slog.Default().Warn("service exposure collision", "event", "exposure_collision", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "result", "failed", "error_category", "port_unavailable")
					http.Error(w, ae.Error(), 409)
					return
				}
				slog.Default().Info("service exposure port allocated", "event", "exposure_allocated", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "host_port", port, "result", "allocated")
			}
		}
		if _, err := exposure.Upsert(r.Context(), a.db, inst, service, exposure.Assignment{Mode: exposure.Mode(in.Mode), Address: addr, Port: port}, env("KITPRO_LAN_BIND_ADDRESS", "")); err != nil {
			slog.Default().Warn("service exposure persistence failed", "event", "exposure_failed", "installation_id", inst, "service_id", service, "mode", in.Mode, "host_address", addr, "host_port", port, "result", "failed", "error_category", "state")
			http.Error(w, "exposure state conflict", 409)
			return
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
	if entry, ok := a.catalog[appID]; ok {
		for _, service := range entry.Manifest.Services {
			record, err := exposure.Get(r.Context(), a.db, installationID, service.ID)
			if err != nil {
				continue
			}
			q.Set("service_id", service.ID)
			q.Set("exposure_mode", string(record.Mode))
			q.Set("host_address", record.HostAddress)
			q.Set("host_port", strconv.Itoa(record.HostPort))
			q.Set("container_port", strconv.Itoa(service.ContainerPort))
			q.Set("service_protocol", service.Protocol)
			break
		}
	}
	r.URL.Path = "/api/v1/operations"
	r.URL.RawQuery = q.Encode()
	r = r.WithContext(context.WithValue(r.Context(), internalDispatchKey{}, true))
	a.ops(w, r)
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
		status := serviceStatus{ID: declared.ID, Name: declared.Name, Protocol: declared.Protocol, ContainerPort: declared.ContainerPort, Mode: string(exposure.Internal), ModeLabel: "Private"}
		if record, err := exposure.Get(ctx, a.db, installationID, declared.ID); err == nil {
			status.Mode, status.HostAddress, status.HostPort = string(record.Mode), record.HostAddress, record.HostPort
			if record.Mode != exposure.Internal {
				status.Endpoint = declared.Protocol + "://" + net.JoinHostPort(record.HostAddress, strconv.Itoa(record.HostPort))
			}
			status.ModeLabel = exposureLabel(status.Mode)
		}
		out = append(out, status)
	}
	return out, nil
}
func (a *app) host(w http.ResponseWriter, r *http.Request) {
	v, e := a.callHelper(protocol.Request{Version: 1, ID: operations.NewID(), Operation: "InspectDocker"})
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
	connection, err := net.Dial("unix", a.helper)
	if err != nil {
		return protocol.Response{}, err
	}
	defer connection.Close()
	if err = protocol.Write(connection, request); err != nil {
		return protocol.Response{}, err
	}
	return protocol.ReadResponse(connection)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var _ = strings.TrimSpace
var _ embed.FS
