package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/maintenance"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/multicontainer"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"golang.org/x/sys/unix"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

func main() {
	if handleMaintenance(os.Args[1:]) {
		return
	}
	sock := env("KITPRO_HELPER_SOCKET", "/run/kitpro/helper.sock")
	u, e := expectedAPIUID()
	if e != nil {
		fatal(e.Error())
	}
	db, e := state.Open(env("KITPRO_HELPER_DB", "/var/lib/kitpro-helper/helper.db"))
	if e != nil {
		fatal(e.Error())
	}
	defer db.Close()
	if e = state.Migrate(context.Background(), db, true); e != nil {
		fatal(e.Error())
	}
	var l net.Listener
	if os.Getenv("LISTEN_FDS") == "1" {
		l, e = net.FileListener(os.NewFile(3, "kitpro-helper.socket"))
	} else {
		os.Remove(sock)
		l, e = net.Listen("unix", sock)
	}
	if e != nil {
		fatal(e.Error())
	}
	defer func() {
		if os.Getenv("LISTEN_FDS") != "1" {
			os.Remove(sock)
		}
	}()
	for {
		c, e := l.Accept()
		if e == nil {
			go serve(c, u, db)
		}
	}
}

func expectedAPIUID() (uint32, error) {
	if value := os.Getenv("KITPRO_API_UID"); value != "" {
		u, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid KITPRO_API_UID")
		}
		return uint32(u), nil
	}
	account := env("KITPRO_API_USER", "kitpro-api")
	u, err := user.Lookup(account)
	if err != nil {
		return 0, fmt.Errorf("resolve API service account %q: %w", account, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid UID for API service account %q", account)
	}
	return uint32(uid), nil
}

func handleMaintenance(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version":
		fmt.Println(buildinfo.String("kitpro-helper"))
		return true
	case "--migrate-only":
		if len(args) != 1 {
			fatal("--migrate-only accepts no arguments")
		}
		db, err := state.Open(env("KITPRO_HELPER_DB", "/var/lib/kitpro-helper/helper.db"))
		if err == nil {
			defer db.Close()
			err = state.Migrate(context.Background(), db, true)
		}
		if err != nil {
			fatal(err.Error())
		}
		slog.Info("database migration completed", "component", "helper", "event", "migration_completed", "result", "success")
		return true
	case "--prepare-upgrade":
		if len(args) != 2 {
			fatal("--prepare-upgrade requires target version")
		}
		db, err := state.Open(env("KITPRO_HELPER_DB", "/var/lib/kitpro-helper/helper.db"))
		if err == nil {
			defer db.Close()
			var result maintenance.Result
			result, err = maintenance.PrepareUpgrade(context.Background(), db, env("KITPRO_HELPER_BACKUP_DIR", "/var/lib/kitpro-helper/backups"), "helper", args[1], time.Now())
			if err == nil {
				slog.Info("upgrade backup completed", "component", "helper", "event", "upgrade_backup_completed", "result", "success", "schema_version", result.SchemaVersion, "path", result.BackupPath)
			}
		}
		if err != nil {
			fatal(err.Error())
		}
		return true
	case "--verify-database":
		if len(args) != 1 {
			fatal("--verify-database accepts no arguments")
		}
		if err := backup.Verify(context.Background(), env("KITPRO_HELPER_DB", "/var/lib/kitpro-helper/helper.db")); err != nil {
			fatal(err.Error())
		}
		slog.Info("database verification completed", "component", "helper", "event", "database_verification_completed", "result", "success")
		return true
	default:
		fatal("unknown argument")
	}
	return true
}
func serve(c net.Conn, api uint32, db *sql.DB) {
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	f, e := uc.File()
	if e != nil {
		return
	}
	defer f.Close()
	cred, e := unix.GetsockoptUcred(int(f.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if e != nil || cred.Uid != api {
		return
	}
	for {
		r, e := protocol.Read(c)
		if e != nil {
			return
		}
		h, _ := protocol.Hash(r)
		_, _ = db.Exec("INSERT OR REPLACE INTO receipts(operation_id,request_hash,operation_type,instance_id,phase,outcome,container_id,created_at) VALUES(?,?,?,?,?,?,?,?)", r.ID, h, r.Operation, r.InstanceID, "received", "accepted", "", time.Now().UTC().Format(time.RFC3339Nano))
		slog.Default().Info("helper request", "event", "helper_request_accepted", "operation_id", r.ID, "instance_id", r.InstanceID, "operation", r.Operation)
		if r.Version != 1 {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "unsupported protocol"})
			continue
		}
		switch r.Operation {
		case "Ping":
			protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"status": "ok"}})
		case "InspectDocker":
			v, e := docker.New().Version()
			if e != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: v})
			}
		case "InstallApplication", "ConfigureServiceExposure", "UpdateApplication":
			createApplication(c, db, r)
		case "StopApplication", "RemoveApplication":
			remove(c, db, r)
		case "StartApplication":
			start(c, db, r)
		case "ReconcileTestWorkload":
			reconcile(c, db, r)
		case "BackupHelperState":
			path, be := backup.Vacuum(context.Background(), db, "/var/lib/kitpro-helper/backups", r.ID+".db")
			if be != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: be.Error()})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"path": path}})
			}
		default:
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "operation not permitted"})
		}
	}
}
func validateApplicationPlan(r protocol.Request) error {
	digest := r.Image[strings.LastIndex(r.Image, "@sha256:")+1:]
	if r.ApplicationID == "" || r.ReleaseID == "" || r.InstanceID == "" || (!strings.HasPrefix(r.Image, "docker.io/") && !strings.HasPrefix(r.Image, "ghcr.io/")) || !strings.Contains(r.Image, "@sha256:") || len(digest) != 71 {
		return fmt.Errorf("invalid application plan")
	}
	for _, c := range digest[7:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return fmt.Errorf("invalid image digest")
		}
	}
	if r.RuntimeGeneration < 1 || r.NetworkName != "kitpro-net-"+r.InstanceID+"-g"+strconv.Itoa(r.RuntimeGeneration) || r.DataPath != "/srv/kitpro/apps/"+r.ApplicationID+"/"+r.InstanceID+"/data" {
		return fmt.Errorf("invalid application paths")
	}
	entries, err := catalog.Load()
	if err != nil {
		return fmt.Errorf("trusted catalog unavailable")
	}
	entry, ok := entries[r.ApplicationID]
	if !ok {
		return fmt.Errorf("application not in trusted catalog")
	}
	if entry.Manifest.SchemaVersion >= manifest.MultiContainerSchemaVersion {
		return validateMultiComponentPlan(r, entry.Manifest)
	}
	var releaseImage string
	for _, release := range entry.Manifest.Releases {
		if release.Version == r.ReleaseID {
			releaseImage = release.Registry + "/" + release.Repository + "@" + release.Digest
		}
	}
	if releaseImage == "" || releaseImage != r.Image || entry.Manifest.Restart != r.RestartPolicy {
		return fmt.Errorf("trusted release mismatch")
	}
	if len(r.Command) != len(entry.Manifest.Command) || len(r.Environment) != len(entry.Manifest.Environment) || len(r.Storage) != len(entry.Manifest.Storage) || len(r.Services) != len(entry.Manifest.Services) {
		return fmt.Errorf("trusted application plan shape mismatch")
	}
	for i, command := range entry.Manifest.Command {
		if r.Command[i] != command {
			return fmt.Errorf("trusted command mismatch")
		}
	}
	for i, variable := range entry.Manifest.Environment {
		if r.Environment[i].Name != variable.Name || r.Environment[i].Value != variable.Value || r.Environment[i].Secret != variable.Secret {
			return fmt.Errorf("trusted environment mismatch")
		}
	}
	for i, storage := range entry.Manifest.Storage {
		expectedHost := "/srv/kitpro/apps/" + r.ApplicationID + "/" + r.InstanceID + "/" + storage.ID
		if r.Storage[i].ID != storage.ID || r.Storage[i].ContainerPath != storage.ContainerPath || r.Storage[i].HostPath != expectedHost || r.Storage[i].ReadOnly != storage.ReadOnly {
			return fmt.Errorf("trusted storage mismatch")
		}
	}
	for i, service := range entry.Manifest.Services {
		if r.Services[i].ID != service.ID || r.Services[i].Protocol != service.Protocol || r.Services[i].ContainerPort != service.ContainerPort {
			return fmt.Errorf("trusted service plan mismatch")
		}
	}
	if r.ExposureMode != "internal" && r.ExposureMode != "loopback" && r.ExposureMode != "lan" {
		return fmt.Errorf("invalid exposure mode")
	}
	if r.ExposureMode == "internal" && (r.HostAddress != "" || (r.HostPort != 0 && (r.HostPort < exposure.FirstPort || r.HostPort > exposure.LastPort))) {
		return fmt.Errorf("internal exposure cannot bind")
	}
	if r.ExposureMode == "loopback" && r.HostAddress != "127.0.0.1" {
		return fmt.Errorf("invalid loopback address")
	}
	if r.ExposureMode == "lan" && (net.ParseIP(r.HostAddress) == nil || net.ParseIP(r.HostAddress).IsUnspecified() || net.ParseIP(r.HostAddress).IsMulticast() || net.ParseIP(r.HostAddress).IsLinkLocalUnicast()) {
		return fmt.Errorf("invalid LAN address")
	}
	if r.ExposureMode == "lan" && (env("KITPRO_LAN_BIND_ADDRESS", "") == "" || r.HostAddress != env("KITPRO_LAN_BIND_ADDRESS", "")) {
		return fmt.Errorf("LAN address is not configured host address")
	}
	if r.ExposureMode == "loopback" || r.ExposureMode == "lan" {
		if r.ServiceID == "" || r.ContainerPort == 0 || r.ServiceProtocol == "" {
			return fmt.Errorf("service declaration required")
		}
		found := false
		for _, s := range r.Services {
			if s.ID == r.ServiceID {
				if s.ContainerPort != r.ContainerPort || s.Protocol != r.ServiceProtocol {
					return fmt.Errorf("service declaration mismatch")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("service not declared")
		}
		if r.HostPort < 20000 || r.HostPort > 29999 || r.ContainerPort < 1 || r.ContainerPort > 65535 {
			return fmt.Errorf("invalid exposure binding")
		}
	}
	for _, c := range r.Command {
		if c == "" || strings.ContainsAny(c, "\r\n") || c == "/bin/sh" || c == "/bin/bash" || c == "-c" {
			return fmt.Errorf("invalid command")
		}
	}
	for _, e := range r.Environment {
		if e.Name == "" || e.Secret {
			return fmt.Errorf("invalid environment")
		}
	}
	for _, s := range r.Storage {
		if s.ID == "" || s.ContainerPath == "" || !strings.HasPrefix(s.HostPath, "/srv/kitpro/apps/") || strings.Contains(s.HostPath, "..") || strings.Contains(s.ContainerPath, "..") || strings.Contains(s.HostPath, "docker.sock") || s.ContainerPath == "/" {
			return fmt.Errorf("invalid storage mapping")
		}
	}
	return nil
}

func validateMultiComponentPlan(r protocol.Request, m manifest.Manifest) error {
	if r.ApplicationID != m.ID || len(m.Releases) == 0 || r.ReleaseID != m.Releases[0].Version || r.Image != m.Releases[0].Registry+"/"+m.Releases[0].Repository+"@"+m.Releases[0].Digest {
		return fmt.Errorf("top-level release identity mismatch")
	}
	if len(r.Components) != len(m.Components) || len(r.Components) < 2 {
		return fmt.Errorf("component plan shape mismatch")
	}
	if r.NetworkName != "kitpro-net-"+r.InstanceID+"-g"+strconv.Itoa(r.RuntimeGeneration) {
		return fmt.Errorf("invalid component network")
	}
	if len(r.Services) != len(m.Services) {
		return fmt.Errorf("service plan shape mismatch")
	}
	for i, service := range m.Services {
		if r.Services[i].ID != service.ID || r.Services[i].Protocol != service.Protocol || r.Services[i].ContainerPort != service.ContainerPort {
			return fmt.Errorf("service plan mismatch")
		}
	}
	seen := map[string]bool{}
	components := make([]manifest.Component, 0, len(r.Components))
	for i, got := range r.Components {
		if seen[got.ID] {
			return fmt.Errorf("duplicate component")
		}
		seen[got.ID] = true
		want := m.Components[i]
		var rel manifest.Release
		for _, candidate := range m.Releases {
			if candidate.Version == want.Release {
				rel = candidate
				break
			}
		}
		image := rel.Registry + "/" + rel.Repository + "@" + rel.Digest
		if got.ID != want.ID || got.Image != image || got.Restart != want.Restart || len(got.DependsOn) != len(want.DependsOn) {
			return fmt.Errorf("component %s identity mismatch", got.ID)
		}
		for j, dep := range want.DependsOn {
			if got.DependsOn[j] != dep {
				return fmt.Errorf("component %s dependency mismatch", got.ID)
			}
		}
		if len(got.Storage) != len(want.Storage) || len(got.Services) != len(want.Services) {
			return fmt.Errorf("component %s plan shape mismatch", got.ID)
		}
		if len(got.Command) != len(want.Command) || len(got.Environment) != len(want.Environment) {
			return fmt.Errorf("component %s plan shape mismatch", got.ID)
		}
		for j, arg := range want.Command {
			if got.Command[j] != arg || arg == "/bin/sh" || arg == "/bin/bash" || arg == "-c" || arg == "" {
				return fmt.Errorf("component %s command mismatch", got.ID)
			}
		}
		for j, variable := range want.Environment {
			if got.Environment[j].Name != variable.Name || got.Environment[j].Value != variable.Value || got.Environment[j].Secret != variable.Secret || got.Environment[j].Secret {
				return fmt.Errorf("component %s environment mismatch", got.ID)
			}
		}
		for j, s := range want.Storage {
			expected := "/srv/kitpro/apps/" + r.ApplicationID + "/" + r.InstanceID + "/" + got.ID + "/" + s.ID
			if got.Storage[j].ID != s.ID || got.Storage[j].ContainerPath != s.ContainerPath || got.Storage[j].HostPath != expected || got.Storage[j].ReadOnly != s.ReadOnly {
				return fmt.Errorf("component %s storage mismatch", got.ID)
			}
		}
		for j, s := range want.Services {
			if got.Services[j].ID != s.ID || got.Services[j].Protocol != s.Protocol || got.Services[j].ContainerPort != s.ContainerPort {
				return fmt.Errorf("component %s service mismatch", got.ID)
			}
		}
		for _, e := range got.Environment {
			if e.Secret || e.Name == "" {
				return fmt.Errorf("component %s invalid environment", got.ID)
			}
		}
		components = append(components, manifest.Component{ID: got.ID, DependsOn: append([]string(nil), got.DependsOn...)})
	}
	if _, err := multicontainer.StartOrder(components); err != nil {
		return err
	}
	if r.ExposureMode != "internal" && r.ExposureMode != "loopback" && r.ExposureMode != "lan" {
		return fmt.Errorf("invalid exposure mode")
	}
	if r.ExposureMode != "internal" {
		if r.ServiceID == "" || r.ContainerPort < 1 || r.HostPort < exposure.FirstPort || r.HostPort > exposure.LastPort {
			return fmt.Errorf("invalid component exposure")
		}
		declared := false
		for _, c := range m.Components {
			for _, s := range c.Services {
				if s.ID == r.ServiceID && s.ContainerPort == r.ContainerPort && s.Protocol == r.ServiceProtocol {
					declared = true
				}
			}
		}
		if !declared {
			return fmt.Errorf("component service not declared")
		}
	}
	return nil
}

func validateTrustedRecreation(db *sql.DB, r protocol.Request) error {
	var generation, trustedPort int
	var applicationID, releaseID, image, dataPath string
	err := db.QueryRow(`SELECT runtime_generation,application_id,release_id,image_digest,data_path,host_port FROM ownership WHERE instance_id=?`, r.InstanceID).Scan(&generation, &applicationID, &releaseID, &image, &dataPath, &trustedPort)
	if err == sql.ErrNoRows {
		if r.RuntimeGeneration != 1 {
			return fmt.Errorf("new installation must begin at runtime generation 1")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("trusted ownership unavailable")
	}
	if r.RuntimeGeneration != generation+1 || (applicationID != "" && applicationID != r.ApplicationID) || dataPath != r.DataPath {
		return fmt.Errorf("trusted runtime identity mismatch")
	}
	if r.Operation != "UpdateApplication" && ((releaseID != "" && releaseID != r.ReleaseID) || image != r.Image) {
		return fmt.Errorf("trusted runtime identity mismatch")
	}
	if trustedPort != 0 && r.HostPort != trustedPort {
		return fmt.Errorf("trusted exposure assignment mismatch")
	}
	return nil
}
func createApplication(c net.Conn, db *sql.DB, r protocol.Request) {
	if e := validateApplicationPlan(r); e != nil {
		slog.Default().Warn("application plan rejected", "event", "plan_rejected", "operation_id", r.ID, "error_category", "validation")
		_, _ = db.Exec("UPDATE receipts SET phase='rejected',outcome='failed' WHERE operation_id=?", r.ID)
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	var trustedErr error
	if len(r.Components) > 0 {
		trustedErr = validateTrustedMultiRecreation(db, r)
	} else {
		trustedErr = validateTrustedRecreation(db, r)
	}
	if e := trustedErr; e != nil {
		slog.Default().Warn("application plan rejected", "event", "plan_rejected", "operation_id", r.ID, "error_category", "trusted_state")
		_, _ = db.Exec("UPDATE receipts SET phase='rejected',outcome='failed' WHERE operation_id=?", r.ID)
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	if len(r.Components) > 0 {
		createMultiApplication(c, db, r)
		return
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: r.InstanceID, ownership.LabelResource: "application", "com.kitpro.application": r.ApplicationID, "com.kitpro.release": r.ReleaseID, "com.kitpro.runtime-generation": strconv.Itoa(maxGeneration(r.RuntimeGeneration))}
	gen := maxGeneration(r.RuntimeGeneration)
	name := "kitpro-" + r.ApplicationID + "-" + r.InstanceID + "-g" + strconv.Itoa(gen)
	d := docker.New()
	// Exposure changes and explicit recreation replace only the previously
	// trusted runtime; persistent storage remains untouched.
	var oldID, oldNetwork string
	_ = db.QueryRow("SELECT container_id,network_name FROM ownership WHERE instance_id=?", r.InstanceID).Scan(&oldID, &oldNetwork)
	if oldID != "" && oldID != name {
		_ = d.Stop(oldID)
		_ = d.Remove(oldID)
		if oldNetwork != "" {
			_ = d.RemoveNetwork(oldNetwork)
		}
	}
	if e := d.Pull(r.Image); e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	if e := os.MkdirAll(r.DataPath, 0750); e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	for _, s := range r.Storage {
		if e := os.MkdirAll(s.HostPath, 0750); e != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
			return
		}
	}
	networkCreated := false
	if _, e := d.CreateNetwork(r.NetworkName, labels); e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	networkCreated = true
	mounts := make([]docker.StorageMount, 0, len(r.Storage))
	for _, s := range r.Storage {
		mounts = append(mounts, docker.StorageMount{ContainerPath: s.ContainerPath, HostPath: s.HostPath, ReadOnly: s.ReadOnly})
	}
	env := make([]string, 0, len(r.Environment))
	for _, v := range r.Environment {
		env = append(env, v.Name+"="+v.Value)
	}
	bindings := map[string][]docker.PortBinding{}
	if r.ExposureMode == "loopback" || r.ExposureMode == "lan" {
		proto, _ := exposure.DockerProtocol(r.ServiceProtocol)
		bindings[fmt.Sprintf("%d/%s", r.ContainerPort, proto)] = []docker.PortBinding{{HostIP: r.HostAddress, HostPort: strconv.Itoa(r.HostPort)}}
	}
	id, e := d.CreateContainerPlan(docker.ContainerPlan{Image: r.Image, Name: name, Network: r.NetworkName, Labels: labels, Command: r.Command, Environment: env, DataPath: r.DataPath, Storage: mounts, PortBindings: bindings, RestartPolicy: r.RestartPolicy})
	if e == nil {
		e = d.Start(id)
	}
	if e == nil {
		var observed map[string]any
		observed, e = d.Inspect(id)
		if e == nil {
			host, _ := observed["HostConfig"].(map[string]any)
			portBindings, _ := host["PortBindings"].(map[string]any)
			if !exposure.ObservedBindingsExact(exposure.Assignment{Mode: exposure.Mode(r.ExposureMode), Address: r.HostAddress, Port: r.HostPort}, r.ContainerPort, r.ServiceProtocol, portBindings) {
				e = fmt.Errorf("observed Docker exposure does not match trusted assignment")
			}
		}
	}
	if e == nil {
		_, e = db.Exec(`INSERT OR REPLACE INTO ownership(instance_id,container_id,container_name,network_name,image_digest,data_path,created_at,runtime_generation,application_id,release_id,exposure_mode,service_id,host_address,host_port,container_port,service_protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.InstanceID, id, name, r.NetworkName, r.Image, r.DataPath, time.Now().UTC().Format(time.RFC3339Nano), gen, r.ApplicationID, r.ReleaseID, r.ExposureMode, r.ServiceID, r.HostAddress, r.HostPort, r.ContainerPort, r.ServiceProtocol)
	}
	if e != nil {
		if id != "" {
			_ = d.Stop(id)
			_ = d.Remove(id)
		}
		if networkCreated {
			_ = d.RemoveNetwork(r.NetworkName)
		}
		event := "exposure_failed"
		if strings.Contains(e.Error(), "address already in use") || strings.Contains(e.Error(), "port is already allocated") {
			event = "exposure_collision"
		}
		slog.Default().Warn("runtime creation failed", "event", event, "operation_id", r.ID, "installation_id", r.InstanceID, "service_id", r.ServiceID, "mode", r.ExposureMode, "host_address", r.HostAddress, "host_port", r.HostPort, "result", "failed", "error_category", "docker")
		_, _ = db.Exec("UPDATE receipts SET phase='completed',outcome='failed' WHERE operation_id=?", r.ID)
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	_, _ = db.Exec("UPDATE receipts SET phase='completed',outcome='succeeded',container_id=? WHERE operation_id=?", id, r.ID)
	slog.Default().Info("runtime created", "event", "exposure_applied", "operation_id", r.ID, "installation_id", r.InstanceID, "service_id", r.ServiceID, "mode", r.ExposureMode, "host_address", r.HostAddress, "host_port", r.HostPort, "result", "succeeded")
	protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"container": name, "network": r.NetworkName}})
}

func validateTrustedMultiRecreation(db *sql.DB, r protocol.Request) error {
	var generation int
	err := db.QueryRow("SELECT runtime_generation FROM component_ownership WHERE installation_id=? LIMIT 1", r.InstanceID).Scan(&generation)
	if err == sql.ErrNoRows {
		if r.RuntimeGeneration != 1 {
			return fmt.Errorf("new installation must begin at runtime generation 1")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("trusted component ownership unavailable")
	}
	if r.RuntimeGeneration != generation+1 {
		return fmt.Errorf("trusted runtime generation mismatch")
	}
	return nil
}

// createMultiApplication creates a bounded, dependency-ordered runtime set.
// It is deliberately separate from the single-container path: no raw Docker
// objects cross the helper boundary, and every component is revalidated above.
func createMultiApplication(c net.Conn, db *sql.DB, r protocol.Request) {
	components := make([]manifest.Component, 0, len(r.Components))
	for _, component := range r.Components {
		components = append(components, manifest.Component{ID: component.ID, DependsOn: component.DependsOn})
	}
	order, err := multicontainer.StartOrder(components)
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
		return
	}
	d := docker.New()
	// Replace the prior trusted component set for this installation. The
	// generation and network names differ, so persistent storage is untouched.
	if oldRows, qe := db.Query("SELECT container_id,network_name FROM component_ownership WHERE installation_id=?", r.InstanceID); qe == nil {
		oldIDs := []string{}
		oldNetwork := ""
		for oldRows.Next() {
			var id, network string
			if oldRows.Scan(&id, &network) == nil {
				oldIDs = append(oldIDs, id)
				oldNetwork = network
			}
		}
		_ = oldRows.Close()
		for _, id := range oldIDs {
			if id != "" {
				_ = d.Stop(id)
				_ = d.Remove(id)
			}
		}
		if oldNetwork != "" && oldNetwork != r.NetworkName {
			_ = d.RemoveNetwork(oldNetwork)
		}
		_, _ = db.Exec("DELETE FROM component_ownership WHERE installation_id=?", r.InstanceID)
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: r.InstanceID, ownership.LabelResource: "application", "com.kitpro.application": r.ApplicationID, "com.kitpro.release": r.ReleaseID, "com.kitpro.runtime-generation": strconv.Itoa(r.RuntimeGeneration)}
	if _, err = d.CreateNetwork(r.NetworkName, labels); err != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
		return
	}
	created := map[string]string{}
	cleanup := func() {
		for _, id := range created {
			_ = d.Stop(id)
			_ = d.Remove(id)
		}
		_ = d.RemoveNetwork(r.NetworkName)
	}
	byID := map[string]protocol.Component{}
	for _, component := range r.Components {
		byID[component.ID] = component
	}
	for _, componentID := range order {
		component := byID[componentID]
		if err = d.Pull(component.Image); err != nil {
			cleanup()
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
			return
		}
		for _, storage := range component.Storage {
			if err = os.MkdirAll(storage.HostPath, 0750); err != nil {
				cleanup()
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
				return
			}
		}
		envs := make([]string, 0, len(component.Environment))
		for _, v := range component.Environment {
			envs = append(envs, v.Name+"="+v.Value)
		}
		mounts := make([]docker.StorageMount, 0, len(component.Storage))
		for _, s := range component.Storage {
			mounts = append(mounts, docker.StorageMount{ContainerPath: s.ContainerPath, HostPath: s.HostPath, ReadOnly: s.ReadOnly})
		}
		bindings := map[string][]docker.PortBinding{}
		for _, service := range component.Services {
			if service.ID == r.ServiceID {
				if service.ContainerPort != r.ContainerPort || service.Protocol != r.ServiceProtocol {
					cleanup()
					protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "service binding mismatch"})
					return
				}
				proto, _ := exposure.DockerProtocol(service.Protocol)
				if r.ExposureMode != "internal" {
					bindings[fmt.Sprintf("%d/%s", service.ContainerPort, proto)] = []docker.PortBinding{{HostIP: r.HostAddress, HostPort: strconv.Itoa(r.HostPort)}}
				}
			}
		}
		name := "kitpro-" + r.ApplicationID + "-" + r.InstanceID + "-" + component.ID + "-g" + strconv.Itoa(r.RuntimeGeneration)
		id, ce := d.CreateContainerPlan(docker.ContainerPlan{Image: component.Image, Name: name, Network: r.NetworkName, Labels: labels, Command: component.Command, Environment: envs, Storage: mounts, PortBindings: bindings, RestartPolicy: component.Restart, NetworkAliases: []string{component.ID}})
		if ce == nil {
			ce = d.Start(id)
		}
		if ce != nil {
			cleanup()
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: ce.Error()})
			return
		}
		created[componentID] = id
		if _, ce = db.Exec(`INSERT OR REPLACE INTO component_ownership(installation_id,component_id,container_id,container_name,network_name,image_digest,runtime_generation,created_at) VALUES(?,?,?,?,?,?,?,?)`, r.InstanceID, component.ID, id, name, r.NetworkName, component.Image, r.RuntimeGeneration, time.Now().UTC().Format(time.RFC3339Nano)); ce != nil {
			cleanup()
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: ce.Error()})
			return
		}
	}
	_, _ = db.Exec("UPDATE receipts SET phase='completed',outcome='succeeded',container_id=? WHERE operation_id=?", created[order[0]], r.ID)
	protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]any{"components": created, "network": r.NetworkName}})
}
func maxGeneration(g int) int {
	if g < 1 {
		return 1
	}
	return g
}
func reconcile(c net.Conn, db *sql.DB, r protocol.Request) {
	var componentCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM component_ownership WHERE installation_id=?", r.InstanceID).Scan(&componentCount)
	if componentCount > 0 {
		rows, err := db.Query("SELECT component_id,container_id,network_name,image_digest FROM component_ownership WHERE installation_id=? ORDER BY component_id", r.InstanceID)
		if err != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
			return
		}
		classification, summary := "exact", "all managed components match trusted state"
		d := docker.New()
		owned := map[string]bool{}
		network := ""
		bindingMatches := 0
		for rows.Next() {
			var cid, n, image string
			if err = rows.Scan(new(string), &cid, &n, &image); err != nil {
				classification = "missing"
				summary = "component ownership unreadable"
				break
			}
			owned[cid] = true
			network = n
			observed, ie := d.Inspect(cid)
			if ie != nil {
				classification = "missing"
				summary = "managed component missing"
				break
			}
			cfg, _ := observed["Config"].(map[string]any)
			labels, _ := cfg["Labels"].(map[string]any)
			if labels[ownership.LabelManaged] != "true" || labels[ownership.LabelInstance] != r.InstanceID || cfg["Image"] != image {
				classification = "security_drift"
				summary = "component identity drift"
				break
			}
			host, _ := observed["HostConfig"].(map[string]any)
			bindings, _ := host["PortBindings"].(map[string]any)
			if r.ExposureMode == "internal" {
				if len(bindings) != 0 {
					classification, summary = "security_drift", "unexpected host exposure"
					break
				}
			} else if exposure.ObservedBindingsExact(exposure.Assignment{Mode: exposure.Mode(r.ExposureMode), Address: r.HostAddress, Port: r.HostPort}, r.ContainerPort, r.ServiceProtocol, bindings) {
				bindingMatches++
			} else if len(bindings) != 0 {
				classification, summary = "security_drift", "unexpected host exposure"
				break
			}
		}
		_ = rows.Close()
		if classification == "exact" && r.ExposureMode != "internal" && bindingMatches != 1 {
			classification, summary = "security_drift", "expected host exposure missing"
		}
		if classification == "exact" && network != "" {
			if foreign, ne := d.HasForeignNetworkMembers(network, owned); ne == nil && foreign {
				classification, summary = "security_drift", "unexpected network member"
			}
		}
		protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"classification": classification, "summary": summary}})
		return
	}
	var id, name, network, image, mode, hostAddress, serviceID, serviceProtocol string
	var hostPort, containerPort int
	e := db.QueryRow("SELECT container_id,container_name,network_name,image_digest,exposure_mode,host_address,host_port,service_id,container_port,service_protocol FROM ownership WHERE instance_id=?", r.InstanceID).Scan(&id, &name, &network, &image, &mode, &hostAddress, &hostPort, &serviceID, &containerPort, &serviceProtocol)
	classification, summary := "missing", "trusted ownership record absent"
	if e == nil {
		got, ie := docker.New().Inspect(id)
		if ie != nil {
			items, le := docker.New().ListContainers()
			if le == nil {
				for _, item := range items {
					for _, n := range item.Names {
						if n == "/"+name {
							classification, summary = "ownership_conflict", "conflicting object occupies expected name"
						}
					}
				}
			}
			if classification != "ownership_conflict" {
				summary = "managed container missing"
			}
		} else {
			cfg, _ := got["Config"].(map[string]any)
			labels, _ := cfg["Labels"].(map[string]any)
			if labels[ownership.LabelManaged] == "true" && labels[ownership.LabelInstance] == r.InstanceID && cfg["Image"] == image {
				host, _ := got["HostConfig"].(map[string]any)
				pb, _ := host["PortBindings"].(map[string]any)
				if !exposure.ObservedBindingsExact(exposure.Assignment{Mode: exposure.Mode(mode), Address: hostAddress, Port: hostPort}, containerPort, serviceProtocol, pb) {
					classification, summary = "security_drift", "unexpected or missing host exposure"
					slog.Default().Warn("exposure drift", "event", "exposure_drift_detected", "installation_id", r.InstanceID, "service_id", serviceID, "mode", mode, "host_address", hostAddress, "host_port", hostPort, "result", "security_drift")
				}
				if foreign, ne := docker.New().HasForeignNetworkMember(network, id); ne == nil && foreign {
					classification, summary = "security_drift", "unexpected network member"
				}
				if classification == "security_drift" { /* preserve drift classification */
				} else {
					classification, summary = "exact", "managed resource matches trusted state"
				}
			} else {
				classification, summary = "ownership-conflict", "observed labels or image differ"
			}
		}
	}
	_, _ = db.Exec("INSERT OR REPLACE INTO reconciliation VALUES(?,?,?,?)", r.InstanceID, classification, time.Now().UTC().Format(time.RFC3339Nano), summary)
	protocol.Write(c, protocol.Response{OK: classification == "exact", RequestID: r.ID, Result: map[string]string{"classification": classification, "summary": summary}})
}
func remove(c net.Conn, db *sql.DB, r protocol.Request) {
	var componentCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM component_ownership WHERE installation_id=?", r.InstanceID).Scan(&componentCount)
	if componentCount > 0 {
		rows, err := db.Query("SELECT container_id,network_name FROM component_ownership WHERE installation_id=?", r.InstanceID)
		if err != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
			return
		}
		var ids []string
		network := ""
		for rows.Next() {
			var id, n string
			if err = rows.Scan(&id, &n); err != nil {
				_ = rows.Close()
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
				return
			}
			ids = append(ids, id)
			network = n
		}
		_ = rows.Close()
		d := docker.New()
		for _, id := range ids {
			if id != "" {
				err = d.Stop(id)
				if err == nil && r.Operation == "RemoveApplication" {
					err = d.Remove(id)
				}
			}
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
				return
			}
		}
		if r.Operation == "RemoveApplication" {
			if network != "" {
				err = d.RemoveNetwork(network)
			}
			if err == nil {
				_, err = db.Exec("DELETE FROM component_ownership WHERE installation_id=?", r.InstanceID)
			}
		}
		if err != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
			return
		}
		protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"network": network}})
		return
	}
	var id, name, network string
	e := db.QueryRow("SELECT container_id,container_name,network_name FROM ownership WHERE instance_id=?", r.InstanceID).Scan(&id, &name, &network)
	if e == nil {
		var got map[string]any
		got, e = docker.New().Inspect(id)
		if e == nil {
			cfg, _ := got["Config"].(map[string]any)
			labels, _ := cfg["Labels"].(map[string]any)
			if labels[ownership.LabelManaged] != "true" || labels[ownership.LabelInstance] != r.InstanceID {
				e = fmt.Errorf("ownership labels mismatch")
			}
		}
	}
	if e == nil {
		if foreign, checkErr := docker.New().HasForeignNetworkMember(network, id); checkErr != nil {
			e = checkErr
		} else if foreign {
			e = fmt.Errorf("network security drift")
		}
	}
	if e == nil {
		e = docker.New().Stop(id)
	}
	if e == nil && r.Operation == "RemoveApplication" {
		e = docker.New().Remove(id)
		if e == nil {
			e = docker.New().RemoveNetwork(network)
		}
		if e == nil {
			e = markRuntimeRemoved(db, r.InstanceID)
		}
	}
	if e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
	} else {
		protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"container": name, "network": network}})
	}
}

// markRuntimeRemoved retains the helper-owned installation identity and its
// storage/exposure trust anchors while clearing the disposable Docker runtime.
// A later recreate can therefore prove continuity without adopting state from
// the control plane or from Docker labels alone.
func markRuntimeRemoved(db *sql.DB, instanceID string) error {
	_, err := db.Exec(`UPDATE ownership
		SET container_id='', container_name='', network_name=''
		WHERE instance_id=?`, instanceID)
	return err
}
func start(c net.Conn, db *sql.DB, r protocol.Request) {
	var componentCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM component_ownership WHERE installation_id=?", r.InstanceID).Scan(&componentCount)
	if componentCount > 0 {
		rows, err := db.Query("SELECT container_id,container_name FROM component_ownership WHERE installation_id=?", r.InstanceID)
		if err != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
			return
		}
		defer rows.Close()
		d := docker.New()
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
				return
			}
			if err = d.Start(id); err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: err.Error()})
				return
			}
		}
		protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"status": "started"}})
		return
	}
	var id, name, network, mode, hostAddress, serviceProtocol string
	var hostPort, containerPort int
	e := db.QueryRow("SELECT container_id,container_name,network_name,exposure_mode,host_address,host_port,container_port,service_protocol FROM ownership WHERE instance_id=?", r.InstanceID).Scan(&id, &name, &network, &mode, &hostAddress, &hostPort, &containerPort, &serviceProtocol)
	if e == nil {
		var got map[string]any
		got, e = docker.New().Inspect(id)
		if e == nil {
			cfg, _ := got["Config"].(map[string]any)
			labels, _ := cfg["Labels"].(map[string]any)
			if labels[ownership.LabelManaged] != "true" || labels[ownership.LabelInstance] != r.InstanceID {
				e = fmt.Errorf("ownership labels mismatch")
			} else if !trustedExposureMatches(got, mode, hostAddress, hostPort, containerPort, serviceProtocol) {
				slog.Default().Warn("start blocked by exposure drift", "event", "exposure_drift_detected", "operation_id", r.ID, "installation_id", r.InstanceID, "mode", mode, "host_address", hostAddress, "host_port", hostPort, "result", "security_drift")
				e = fmt.Errorf("exposure security drift")
			}
		}
	}
	if e == nil {
		if foreign, checkErr := docker.New().HasForeignNetworkMember(network, id); checkErr != nil {
			e = checkErr
		} else if foreign {
			slog.Default().Warn("start blocked by network drift", "event", "network_drift_detected", "operation_id", r.ID, "installation_id", r.InstanceID, "result", "security_drift")
			e = fmt.Errorf("network security drift")
		}
	}
	if e == nil {
		e = docker.New().Start(id)
	}
	if e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"container": name}})
}

func trustedExposureMatches(observed map[string]any, mode, hostAddress string, hostPort, containerPort int, serviceProtocol string) bool {
	host, _ := observed["HostConfig"].(map[string]any)
	bindings, _ := host["PortBindings"].(map[string]any)
	return exposure.ObservedBindingsExact(exposure.Assignment{Mode: exposure.Mode(mode), Address: hostAddress, Port: hostPort}, containerPort, serviceProtocol, bindings)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func fatal(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(1) }
