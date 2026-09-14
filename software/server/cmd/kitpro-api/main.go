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
	"strconv"
	"strings"
	"time"
)

//go:embed web.html
var page string

type app struct {
	db            *sql.DB
	helper        string
	tmpl          *template.Template
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
	allowed := map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true}
	for _, h := range strings.Split(env("KITPRO_ALLOWED_HOSTS", ""), ",") {
		if h = strings.TrimSpace(h); h != "" {
			allowed[h] = true
		}
	}
	a := &app{db: db, helper: env("KITPRO_HELPER_SOCKET", "/run/kitpro/helper.sock"), tmpl: template.Must(template.New("page").Parse(page)), authCfg: auth.DefaultConfig, secureCookies: env("KITPRO_SECURE_COOKIES", "") == "1", allowedHosts: allowed}
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
	http.HandleFunc("/api/v1/reconcile/", a.guard(a.reconcile))
	http.HandleFunc("/api/v1/backup", a.guard(a.backup))
	http.HandleFunc("/api/v1/version", a.guard(a.version))
	http.ListenAndServe(env("KITPRO_API_ADDR", "127.0.0.1:8080"), nil)
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		csrf := ""
		if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
			csrf = template.HTMLEscapeString(cookie.Value)
		}
		_, _ = fmt.Fprintf(w, "<h1>Change password</h1><form method=post><input name=current_password type=password autocomplete=current-password required><input name=new_password type=password autocomplete=new-password minlength=12 maxlength=72 required><input name=csrf_token type=hidden value=\"%s\"><button>Change password</button></form>", csrf)
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
		http.Error(w, "password change failed", 400)
		return
	}
	a.setCookies(w, tok, csrf)
	http.Redirect(w, r, "/", 303)
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
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
			http.Error(w, "setup failed", 400)
			return
		}
		slog.Default().Info("setup completed", "event", "setup_completed", "username", r.FormValue("username"))
		http.Redirect(w, r, "/login", 303)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Set up KITPro</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:30rem;margin:10vh auto;padding:1.25rem;color:#172033;background:#f6f7fb}main{background:#fff;border:1px solid #dfe3ec;border-radius:12px;padding:1.5rem}label{display:block;font-weight:650;margin-top:1rem}input{display:block;width:100%;box-sizing:border-box;padding:.65rem;margin-top:.35rem;border:1px solid #9aa5b5;border-radius:8px;font:inherit}button{margin-top:1.25rem;padding:.65rem .9rem;border:0;border-radius:8px;background:#175cd3;color:#fff;font:inherit;font-weight:700}button:focus-visible,input:focus-visible{outline:3px solid #8ab4ff;outline-offset:2px}.muted{color:#596579}</style></head><body><main><h1>Set up KITPro Server</h1><p class="muted">Create the local administrator for this server. Your application data stays on your server.</p><form method="post"><label for="username">Username</label><input id="username" name="username" autocomplete="username" required><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="new-password" minlength="12" required><p class="muted">Use at least 12 characters.</p><button type="submit">Create administrator</button></form></main></body></html>`))
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
			http.Error(w, "invalid username or password", 401)
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
	w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign in · KITPro</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:30rem;margin:10vh auto;padding:1.25rem;color:#172033;background:#f6f7fb}main{background:#fff;border:1px solid #dfe3ec;border-radius:12px;padding:1.5rem}label{display:block;font-weight:650;margin-top:1rem}input{display:block;width:100%;box-sizing:border-box;padding:.65rem;margin-top:.35rem;border:1px solid #9aa5b5;border-radius:8px;font:inherit}button{margin-top:1.25rem;padding:.65rem .9rem;border:0;border-radius:8px;background:#175cd3;color:#fff;font:inherit;font-weight:700}button:focus-visible,input:focus-visible{outline:3px solid #8ab4ff;outline-offset:2px}</style></head><body><main><h1>Sign in to KITPro</h1><p>Manage your local applications and server settings.</p><form method="post"><label for="username">Username</label><input id="username" name="username" autocomplete="username" required><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required><button type="submit">Sign in</button></form></main></body></html>`))
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
		"architecture":           "linux/amd64",
		"package_family":         "debian-or-arch",
		"schema_version":         schema,
		"catalog_schema_version": manifest.MultiContainerSchemaVersion,
	})
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
	rows, _ := operations.List(r.Context(), a.db)
	type appView struct {
		ID          string
		Name        string
		Description string
		Release     string
		Releases    []string
	}
	apps := []appView{}
	for _, id := range catalog.IDs(a.catalog) {
		m := a.catalog[id].Manifest
		rel := ""
		if len(m.Releases) > 0 {
			rel = m.Releases[0].Version
		}
		versions := make([]string, 0, len(m.Releases))
		for _, release := range m.Releases {
			versions = append(versions, release.Version)
		}
		apps = append(apps, appView{m.ID, m.Name, m.Description, rel, versions})
	}
	type installationView struct {
		ID, Name, State, AppID, Release string
		Generation                      int
		Services                        []serviceStatus
	}
	installations := []installationView{}
	installedRows, _ := a.db.QueryContext(r.Context(), "SELECT installation_id,application_id,desired_state,runtime_generation FROM installations ORDER BY created_at")
	if installedRows != nil {
		defer installedRows.Close()
		for installedRows.Next() {
			var id, appID, desired string
			var generation int
			if installedRows.Scan(&id, &appID, &desired, &generation) != nil {
				continue
			}
			name := appID
			if entry, ok := a.catalog[appID]; ok {
				name = entry.Manifest.Name
			}
			services, _ := a.serviceStatuses(r.Context(), id)
			release := ""
			_ = a.db.QueryRowContext(r.Context(), "SELECT release_id FROM installations WHERE installation_id=?", id).Scan(&release)
			installations = append(installations, installationView{id, name, desired, appID, release, generation, services})
		}
	}
	csrf := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil {
		csrf = cookie.Value
	}
	a.tmpl.Execute(w, map[string]any{"Rows": rows, "Apps": apps, "Installations": installations, "CSRF": csrf, "Version": buildinfo.Version})
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
		release = entry.Manifest.Releases[0].Version
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
	{
		helperOperation := "InstallApplication"
		if opType == "ConfigureServiceExposure" {
			helperOperation = opType
		} else if opType == "UpdateApplication" {
			helperOperation = "InstallApplication"
		}
		q := protocol.Request{Version: 1, ID: id, Operation: helperOperation, InstanceID: inst, RuntimeGeneration: gen, Image: plan.ImageDigest, ApplicationID: plan.ApplicationID, ReleaseID: plan.ReleaseID, NetworkName: plan.NetworkName, DataPath: plan.DataPath, RestartPolicy: plan.Restart}
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
			q.Environment = append(q.Environment, protocol.EnvVar{Name: x.Name, Value: x.Value, Secret: x.Secret})
		}
		for _, x := range plan.Storage {
			q.Storage = append(q.Storage, protocol.StorageMount{ID: x.ID, ContainerPath: x.ContainerPath, HostPath: "/srv/kitpro/apps/" + plan.ApplicationID + "/" + inst + "/" + x.ID, ReadOnly: x.ReadOnly})
		}
		for _, s := range plan.Services {
			q.Services = append(q.Services, protocol.Service{ID: s.ID, Protocol: s.Protocol, ContainerPort: s.ContainerPort})
		}
		for _, component := range plan.ResolvedComponents {
			pc := protocol.Component{ID: component.ID, Image: component.ImageDigest, Restart: component.Restart, DependsOn: append([]string(nil), component.DependsOn...)}
			for _, arg := range component.Command {
				pc.Command = append(pc.Command, arg)
			}
			for _, variable := range component.Environment {
				pc.Environment = append(pc.Environment, protocol.EnvVar{Name: variable.Name, Value: variable.Value, Secret: variable.Secret})
			}
			for _, storage := range component.Storage {
				pc.Storage = append(pc.Storage, protocol.StorageMount{ID: storage.ID, ContainerPath: storage.ContainerPath, HostPath: "/srv/kitpro/apps/" + plan.ApplicationID + "/" + inst + "/" + component.ID + "/" + storage.ID, ReadOnly: storage.ReadOnly})
			}
			for _, service := range component.Services {
				pc.Services = append(pc.Services, protocol.Service{ID: service.ID, Protocol: service.Protocol, ContainerPort: service.ContainerPort})
			}
			q.Components = append(q.Components, pc)
		}
		{
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
	} else if existing != "" {
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

func (a *app) installations(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/installations/"), "/"), "/")
	if len(parts) == 2 && parts[1] == "update" {
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		var in struct {
			Release string `json:"release"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil || in.Release == "" {
			http.Error(w, "release is required", 400)
			return
		}
		var trailing any
		if dec.Decode(&trailing) != io.EOF {
			http.Error(w, "invalid request", 400)
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
		status := serviceStatus{ID: declared.ID, Name: declared.Name, Protocol: declared.Protocol, ContainerPort: declared.ContainerPort, Mode: string(exposure.Internal)}
		if record, err := exposure.Get(ctx, a.db, installationID, declared.ID); err == nil {
			status.Mode, status.HostAddress, status.HostPort = string(record.Mode), record.HostAddress, record.HostPort
			if record.Mode != exposure.Internal {
				status.Endpoint = declared.Protocol + "://" + net.JoinHostPort(record.HostAddress, strconv.Itoa(record.HostPort))
			}
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
