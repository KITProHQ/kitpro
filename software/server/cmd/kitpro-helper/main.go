package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/backup"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/hardware"
	"github.com/kitpro/kitpro/software/server/internal/helperops"
	"github.com/kitpro/kitpro/software/server/internal/lifecycle"
	"github.com/kitpro/kitpro/software/server/internal/maintenance"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/multicontainer"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
	"github.com/kitpro/kitpro/software/server/internal/platform"
	"github.com/kitpro/kitpro/software/server/internal/podman"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"github.com/kitpro/kitpro/software/server/internal/state"
	"golang.org/x/sys/unix"
	"io"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

var detectPlatform = platform.Detect

var newContainerRuntime = func() containers.Runtime {
	name, _ := configuredRuntimeName()
	if name == "podman" {
		return podman.New()
	}
	return docker.New()
}

func main() {
	if handleMaintenance(os.Args[1:]) {
		return
	}
	runtimeName, err := configuredRuntimeName()
	if err != nil {
		fatal(err.Error())
	}
	slog.Info("container runtime selected", "component", "helper", "runtime", runtimeName)
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
	coordinator := helperops.Coordinator{DB: db}
	if recovered, recoverErr := coordinator.RecoverInterrupted(context.Background()); recoverErr != nil {
		fatal(recoverErr.Error())
	} else if recovered > 0 {
		slog.Warn("interrupted helper operations classified", "component", "helper", "count", recovered)
	}
	if recovered, recoverErr := recoverInterruptedRestores(context.Background(), db, coordinator); recoverErr != nil {
		fatal(recoverErr.Error())
	} else if recovered > 0 {
		slog.Warn("interrupted application restores recovered", "component", "helper", "count", recovered)
	}
	cleanupCommittedRestores(context.Background(), db)
	if lifecycleRuntime, ok := newContainerRuntime().(containers.LifecycleRuntime); ok {
		if _, backfillErr := backfillLegacyMultiAtStartup(context.Background(), db); backfillErr != nil {
			fatal(backfillErr.Error())
		}
		if verificationErr := prepareAllMigratedSingleConfigurations(context.Background(), db, lifecycleRuntime); verificationErr != nil {
			fatal(verificationErr.Error())
		}
		if recovered, recoverErr := lifecycle.RecoverInterrupted(context.Background(), lifecycle.Store{DB: db}, lifecycleRuntime); recoverErr != nil {
			fatal(recoverErr.Error())
		} else if recovered > 0 {
			slog.Warn("interrupted lifecycle generations reconciled", "component", "helper", "count", recovered)
		}
		startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		reconciled, reconcileErr := lifecycle.ReconcileAll(startupCtx, lifecycle.Store{DB: db}, lifecycleRuntime)
		cancel()
		if reconcileErr != nil {
			fatal(reconcileErr.Error())
		} else if reconciled > 0 {
			slog.Info("managed installations observed", "component", "helper", "count", reconciled)
		}
	}
	stopInstallationsWithUnavailableStorage(db)
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
			go serve(c, u, db, coordinator)
		}
	}
}

func configuredRuntimeName() (string, error) {
	p, err := detectPlatform()
	if err != nil {
		return "", err
	}
	return selectRuntime(os.Getenv("KITPRO_CONTAINER_RUNTIME"), p)
}

func selectRuntime(explicit string, p platform.Platform) (string, error) {
	if explicit != "" {
		if explicit != "docker" && explicit != "podman" {
			return "", fmt.Errorf("unsupported KITPRO_CONTAINER_RUNTIME %q", explicit)
		}
		return explicit, nil
	}
	if !p.Installable || p.ContainerRuntime == "" {
		return "", fmt.Errorf("unsupported host platform %s %s", p.ID, p.Version)
	}
	return p.ContainerRuntime, nil
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
	case "--resolve-operation":
		if len(args) != 3 || args[2] != "--release-as-failed" {
			fatal("--resolve-operation requires an operation ID and --release-as-failed")
		}
		db, err := state.Open(env("KITPRO_HELPER_DB", "/var/lib/kitpro-helper/helper.db"))
		if err == nil {
			defer db.Close()
			err = state.Migrate(context.Background(), db, true)
		}
		if err == nil {
			err = (helperops.Coordinator{DB: db}).ResolveActionRequired(context.Background(), args[1])
		}
		if err != nil {
			fatal(err.Error())
		}
		slog.Info("operation recovery lock released as failed", "component", "helper", "operation_id", args[1])
		return true
	default:
		fatal("unknown argument")
	}
	return true
}
func serve(c net.Conn, api uint32, db *sql.DB, coordinator helperops.Coordinator) {
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
		slog.Default().Info("helper request", "event", "helper_request_received", "request_id", r.ID, "operation_id", r.OperationID, "instance_id", r.InstanceID, "operation", r.Operation)
		if r.Version != 1 && r.Version != 2 {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "unsupported protocol"})
			continue
		}
		if r.Operation == "GetOperation" {
			response, err := coordinator.Response(context.Background(), r.OperationID, r.ID)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "OperationNotFound", Error: "operation not found"})
			} else {
				protocol.Write(c, response)
			}
			continue
		}
		if r.Operation == "GetReconciliation" {
			result, err := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), r.InstanceID)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, ErrorCode: "ReconciliationNotFound", Error: "reconciliation state not found"})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: result})
			}
			continue
		}
		if isDurableMutation(r.Operation) {
			if r.Version != 2 {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "ProtocolUpgradeRequired", Error: "lifecycle mutations require protocol version 2"})
				continue
			}
			if (r.Operation == "ReconcileInstallation" || r.Operation == "RepairInstallation") && r.InstanceID == "" {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "InvalidRequest", Error: "installation ID is required"})
				continue
			}
			if r.Operation == "RepairInstallation" && r.RepairAction != lifecycle.RepairStartActive && r.RepairAction != lifecycle.RepairCleanupResources && r.RepairAction != lifecycle.RepairAcknowledgeRetainedLoss {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "InvalidRequest", Error: "unsupported repair action"})
				continue
			}
			if r.Operation == "InstallApplication" && r.RepairAction != "" {
				reconciliation, reconcileErr := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), r.InstanceID)
				if r.RepairAction != lifecycle.RepairRecreateGeneration || reconcileErr != nil || reconciliation.CheckedGeneration != r.RuntimeGeneration-1 || reconciliation.RecommendedAction != lifecycle.RepairRecreateGeneration {
					protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "InvalidRepair", Error: "controlled recreation requires current helper reconciliation evidence"})
					continue
				}
			}
			if r.InstanceID != "" && r.Operation != "ReconcileInstallation" {
				blocked, blockErr := restoreBlocksInstallation(context.Background(), db, r.InstanceID, r.OperationID)
				if blockErr != nil {
					protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "OperationStateUnavailable", Error: "restore recovery state unavailable"})
					continue
				}
				if blocked {
					protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "RestoreRecoveryRequired", Error: "installation has an unresolved restore; lifecycle mutation is blocked"})
					continue
				}
			}
			decision, beginErr := coordinator.Begin(context.Background(), r, api, operationScope(r))
			if beginErr != nil {
				if decision.Response.Error != "" {
					protocol.Write(c, decision.Response)
				} else {
					protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "OperationStateUnavailable", Error: beginErr.Error()})
				}
				continue
			}
			if !decision.Execute {
				protocol.Write(c, decision.Response)
				continue
			}
			if err := coordinator.AuthorizeMutation(context.Background(), r.OperationID, decision.FencingToken); err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "OperationFenceRejected", Error: err.Error()})
				continue
			}
			var resultFrame bytes.Buffer
			executeMutation(&resultFrame, db, r, coordinator, decision.FencingToken)
			response, err := protocol.ReadResponse(&resultFrame)
			if err != nil {
				response = protocol.Response{RequestID: r.ID, OperationID: r.OperationID, ErrorCode: "HandlerProtocolFailure", Error: "mutation handler did not return a valid result"}
			}
			response.RequestID = r.ID
			response.OperationID = r.OperationID
			response, err = coordinator.Complete(context.Background(), r.OperationID, decision.FencingToken, response)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, OperationID: r.OperationID, State: helperops.StateActionRequired, ErrorCode: "RecoveryRequired", Error: err.Error()})
			} else {
				protocol.Write(c, response)
			}
			continue
		}
		switch r.Operation {
		case "Ping":
			protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"status": "ok"}})
		case "InspectRuntime", "InspectDocker":
			runtime := newContainerRuntime()
			v, e := runtime.Version()
			if e != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
			} else {
				v["KitproRuntime"] = runtime.Name()
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: v})
			}
		case "GetHardwareInventory":
			d := newContainerRuntime()
			info, err := d.Info()
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "container runtime inventory unavailable"})
				break
			}
			inv, err := hardware.Discover(hardware.ParseDockerRuntimes(info["Runtimes"]))
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "hardware inventory unavailable"})
				break
			}
			protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: hardware.SafeView(inv)})
		case "GetHardwareAssignment":
			result, err := hardwareAssignmentView(db, r.InstanceID)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "hardware assignment unavailable"})
				break
			}
			protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		case "ListStorageRoots":
			result, err := listStorageRoots(db)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "storage inventory unavailable"})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]any{"roots": result}})
			}
		case "GetStorageAssignments":
			result, err := storageAssignments(db, r.InstanceID)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "storage assignments unavailable"})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]any{"assignments": result}})
			}
		case "RevealApplicationCredential":
			result, err := revealApplicationCredential(db, r)
			if err != nil {
				protocol.Write(c, protocol.Response{RequestID: r.ID, ErrorCode: "CredentialUnavailable", Error: "application credential unavailable"})
			} else {
				protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: result})
			}
		default:
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "operation not permitted"})
		}
	}
}

func revealApplicationCredential(db *sql.DB, request protocol.Request) (protocol.CredentialDisclosure, error) {
	if request.Version != 2 || !strings.HasPrefix(request.InstanceID, "inst-") || len(request.InstanceID) < 13 || len(request.InstanceID) > 80 || request.ApplicationID == "" || request.CredentialID == "" {
		return protocol.CredentialDisclosure{}, errors.New("invalid credential request")
	}
	entries, err := catalog.Load()
	if err != nil {
		return protocol.CredentialDisclosure{}, errors.New("trusted catalog unavailable")
	}
	entry, ok := entries[request.ApplicationID]
	if !ok {
		return protocol.CredentialDisclosure{}, errors.New("application credential unavailable")
	}
	credential, ok := manifest.FindPresentedCredential(entry.Manifest, request.CredentialID)
	if !ok {
		return protocol.CredentialDisclosure{}, errors.New("application credential unavailable")
	}
	var applicationID string
	if err = db.QueryRow(`SELECT application_id FROM runtime_generations WHERE installation_id=? AND application_id=? ORDER BY runtime_generation DESC LIMIT 1`, request.InstanceID, request.ApplicationID).Scan(&applicationID); err != nil || applicationID != request.ApplicationID {
		return protocol.CredentialDisclosure{}, errors.New("installation credential unavailable")
	}
	var value string
	if err = db.QueryRow(`SELECT value FROM installation_secrets WHERE installation_id=? AND component_id=? AND name=?`, request.InstanceID, credential.ComponentID, credential.EnvironmentName).Scan(&value); err != nil || len(value) != 64 {
		return protocol.CredentialDisclosure{}, errors.New("application credential unavailable")
	}
	if _, err = hex.DecodeString(value); err != nil {
		return protocol.CredentialDisclosure{}, errors.New("application credential unavailable")
	}
	return protocol.CredentialDisclosure{ID: credential.ID, Label: credential.Label, Username: credential.Username, Value: value}, nil
}

func isDurableMutation(operation string) bool {
	switch operation {
	case "InstallApplication", "ConfigureServiceExposure", "UpdateApplication", "StopApplication", "RemoveApplication", "StartApplication", "RestartApplication", "ReconcileInstallation", "RepairInstallation", "RegisterStorageRoot", "RemoveStorageRoot", "BackupHelperState", "CreateApplicationBackup", "RestoreApplicationBackup":
		return true
	default:
		return false
	}
}

func operationScope(r protocol.Request) string {
	if r.InstanceID != "" {
		return r.InstanceID
	}
	if r.Operation == "RegisterStorageRoot" || r.Operation == "RemoveStorageRoot" {
		return "host-storage"
	}
	return "helper-state"
}

func executeMutation(w io.Writer, db *sql.DB, r protocol.Request, coordinator helperops.Coordinator, fencingToken int64) {
	switch r.Operation {
	case "InstallApplication", "ConfigureServiceExposure", "UpdateApplication":
		createApplication(w, db, r, coordinator, fencingToken)
	case "StopApplication", "RemoveApplication":
		changeApplicationState(w, db, r, coordinator, fencingToken, map[string]string{"StopApplication": "stop", "RemoveApplication": "remove"}[r.Operation])
	case "StartApplication":
		changeApplicationState(w, db, r, coordinator, fencingToken, "start")
	case "RestartApplication":
		changeApplicationState(w, db, r, coordinator, fencingToken, "restart")
	case "ReconcileInstallation":
		runtime, ok := newContainerRuntime().(containers.LifecycleRuntime)
		if !ok {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: "reconciliation requires the supported Docker lifecycle runtime"})
			return
		}
		_ = prepareMigratedSingleConfiguration(context.Background(), db, runtime, r.InstanceID)
		result, err := (lifecycle.Reconciler{Runtime: runtime, Store: lifecycle.Store{DB: db}}).Reconcile(context.Background(), r.InstanceID)
		if err != nil {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		}
	case "RepairInstallation":
		runtime, ok := newContainerRuntime().(containers.LifecycleRuntime)
		if !ok {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: "repair requires the supported Docker lifecycle runtime"})
			return
		}
		_ = prepareMigratedSingleConfiguration(context.Background(), db, runtime, r.InstanceID)
		result, err := (lifecycle.Repairer{Runtime: runtime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Repair(context.Background(), r.InstanceID, r.OperationID, fencingToken, r.RepairAction)
		if err != nil {
			var unknown lifecycle.UnknownOutcomeError
			if errors.As(err, &unknown) {
				protocol.Write(w, protocol.Response{RequestID: r.ID, ErrorCode: "RecoveryRequired", Error: err.Error()})
			} else {
				protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
			}
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		}
	case "RegisterStorageRoot":
		result, err := registerStorageRoot(db, r)
		if err != nil {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		}
	case "RemoveStorageRoot":
		err := removeStorageRoot(db, r.RootID)
		if err != nil {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"status": "removed"}})
		}
	case "BackupHelperState":
		path, err := backup.Vacuum(context.Background(), db, "/var/lib/kitpro-helper/backups", r.SemanticOperationID()+".db")
		if err != nil {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: map[string]string{"path": path}})
		}
	case "CreateApplicationBackup":
		result, err := createApplicationBackup(context.Background(), db, r)
		if err != nil {
			protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		}
	case "RestoreApplicationBackup":
		result, err := restoreApplicationBackupWithFence(context.Background(), db, r, fencingToken)
		if err != nil {
			needsRecovery, stateErr := restoreOperationNeedsRecovery(context.Background(), db, r.OperationID)
			if stateErr != nil || needsRecovery {
				protocol.Write(w, protocol.Response{RequestID: r.ID, ErrorCode: "RecoveryRequired", Error: err.Error()})
			} else {
				protocol.Write(w, protocol.Response{RequestID: r.ID, Error: err.Error()})
			}
		} else {
			protocol.Write(w, protocol.Response{OK: true, RequestID: r.ID, Result: result})
		}
	}
}

func hardwareAssignmentView(db *sql.DB, installation string) (map[string]any, error) {
	if !strings.HasPrefix(installation, "inst-") || len(installation) < 13 || len(installation) > 80 {
		return nil, fmt.Errorf("invalid installation identity")
	}
	rows, err := db.Query(`SELECT component_id,device_class,mode,vendor,stable_id,runtime_generation FROM hardware_assignments WHERE installation_id=? ORDER BY component_id,device_class`, installation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	info, _ := newContainerRuntime().Info()
	inv, _ := hardware.Discover(hardware.ParseDockerRuntimes(info["Runtimes"]))
	assignments := []map[string]any{}
	for rows.Next() {
		var component, class, mode, vendor, stableID string
		var generation int
		if err := rows.Scan(&component, &class, &mode, &vendor, &stableID, &generation); err != nil {
			return nil, err
		}
		item := map[string]any{"component": component, "class": class, "mode": mode, "vendor": vendor, "stable_id": stableID, "runtime_generation": generation, "available": false}
		if model := inv.ModelForStableID(stableID); model != "" {
			item["model"] = model
			item["available"] = true
		}
		assignments = append(assignments, item)
	}
	return map[string]any{"assignments": assignments}, rows.Err()
}
func validateApplicationPlan(r protocol.Request) error {
	digest := r.Image[strings.LastIndex(r.Image, "@sha256:")+1:]
	if r.ApplicationID == "" || r.ReleaseID == "" || r.InstanceID == "" || (!strings.HasPrefix(r.Image, "docker.io/") && !strings.HasPrefix(r.Image, "ghcr.io/") && !strings.HasPrefix(r.Image, "codeberg.org/")) || !strings.Contains(r.Image, "@sha256:") || len(digest) != 71 {
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
	if len(entry.Manifest.Components) > 0 {
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
	if !externalBindingsEqual(r.ExternalStorage, entry.Manifest.ExternalStorage) {
		return fmt.Errorf("trusted external storage requirement mismatch")
	}
	if !hardwareRequirementsEqual(r.Hardware, entry.Manifest.Hardware) {
		return fmt.Errorf("trusted hardware requirement mismatch")
	}
	if !runtimeIdentityEqual(r.RunAs, entry.Manifest.RunAs) {
		return fmt.Errorf("trusted runtime identity mismatch")
	}
	for i, command := range entry.Manifest.Command {
		if r.Command[i] != command {
			return fmt.Errorf("trusted command mismatch")
		}
	}
	for i, variable := range entry.Manifest.Environment {
		if r.Environment[i].Name != variable.Name || r.Environment[i].Value != variable.Value || r.Environment[i].Secret != variable.Secret || r.Environment[i].Generate != variable.Generate {
			return fmt.Errorf("trusted environment mismatch")
		}
	}
	for i, storage := range entry.Manifest.Storage {
		expectedHost := "/srv/kitpro/apps/" + r.ApplicationID + "/" + r.InstanceID + "/" + storage.ID
		if r.Storage[i].ID != storage.ID || r.Storage[i].ContainerPath != storage.ContainerPath || r.Storage[i].HostPath != expectedHost || r.Storage[i].ReadOnly != storage.ReadOnly || r.Storage[i].OwnerUID != storage.OwnerUID || r.Storage[i].OwnerGID != storage.OwnerGID {
			return fmt.Errorf("trusted storage mismatch")
		}
	}
	for i, service := range entry.Manifest.Services {
		if r.Services[i].ID != service.ID || r.Services[i].Protocol != service.Protocol || r.Services[i].ContainerPort != service.ContainerPort {
			return fmt.Errorf("trusted service plan mismatch")
		}
	}
	if err := validateTrustedBindings(r, entry.Manifest); err != nil {
		return err
	}
	for _, c := range r.Command {
		if c == "" || strings.ContainsAny(c, "\r\n") || c == "/bin/sh" || c == "/bin/bash" || c == "-c" {
			return fmt.Errorf("invalid command")
		}
	}
	for _, e := range r.Environment {
		if e.Name == "" || (e.Secret && (e.Value != "" || e.Generate != "random-hex-32")) || (!e.Secret && e.Generate != "") {
			return fmt.Errorf("invalid environment")
		}
	}
	for _, s := range r.Storage {
		if s.ID == "" || s.ContainerPath == "" || !strings.HasPrefix(s.HostPath, "/srv/kitpro/apps/") || strings.Contains(s.HostPath, "..") || strings.Contains(s.ContainerPath, "..") || containsRuntimeSocket(s.HostPath) || containsRuntimeSocket(s.ContainerPath) || s.ContainerPath == "/" {
			return fmt.Errorf("invalid storage mapping")
		}
	}
	return nil
}

func hardwareRequirementsEqual(got []protocol.HardwareRequirement, want []manifest.Accelerator) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].Class != want[i].Class || got[i].Optional != want[i].Optional || got[i].CPUFallback != want[i].CPUFallback {
			return false
		}
	}
	return true
}

func runtimeIdentityEqual(got *protocol.RuntimeIdentity, want *manifest.RuntimeIdentity) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return got.UID == want.UID && got.GID == want.GID
}

func containsRuntimeSocket(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if component == "docker.sock" || component == "podman.sock" || component == "containerd.sock" {
			return true
		}
	}
	return false
}

func externalBindingsEqual(got []protocol.ExternalStorageBinding, want []manifest.ExternalStorage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].SlotID != want[i].ID || (want[i].Required && got[i].RootID == "") {
			return false
		}
	}
	return true
}

type runtimeHardware struct {
	Devices     []containers.DeviceMapping
	Requests    []containers.DeviceRequest
	Assignments []hardware.Assignment
	CPU         []string
}

func resolveHardware(d containers.Runtime, requirements []protocol.HardwareRequirement) (runtimeHardware, error) {
	if len(requirements) == 0 {
		return runtimeHardware{}, nil
	}
	info, err := d.Info()
	if err != nil {
		return runtimeHardware{}, fmt.Errorf("container runtime inventory unavailable: %w", err)
	}
	inv, err := hardware.Discover(hardware.ParseDockerRuntimes(info["Runtimes"]))
	if err != nil {
		return runtimeHardware{}, fmt.Errorf("hardware inventory unavailable: %w", err)
	}
	var out runtimeHardware
	for _, requirement := range requirements {
		assignment, resolveErr := inv.Resolve(requirement.Class)
		if resolveErr != nil {
			if requirement.Optional && requirement.CPUFallback {
				out.CPU = append(out.CPU, requirement.Class)
				continue
			}
			return runtimeHardware{}, fmt.Errorf("required hardware unavailable: %s", requirement.Class)
		}
		out.Assignments = append(out.Assignments, assignment)
		if assignment.NVIDIARuntime {
			out.Requests = append(out.Requests, containers.DeviceRequest{Driver: "nvidia", Count: -1, Capabilities: [][]string{{"gpu"}}})
		}
		for _, node := range assignment.Devices {
			out.Devices = append(out.Devices, containers.DeviceMapping{PathOnHost: node.Path, PathInContainer: node.Path, CgroupPermissions: "rwm"})
		}
	}
	return out, nil
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func persistHardware(db sqlExecer, installation, component string, generation int, resolved runtimeHardware) error {
	if _, err := db.Exec("DELETE FROM hardware_assignments WHERE installation_id=? AND component_id=?", installation, component); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, class := range resolved.CPU {
		if _, err := db.Exec(`INSERT INTO hardware_assignments(installation_id,component_id,device_class,mode,runtime_generation,created_at) VALUES(?,?,?,'cpu',?,?)`, installation, component, class, generation, now); err != nil {
			return err
		}
	}
	for _, assignment := range resolved.Assignments {
		paths := make([]string, 0, len(assignment.Devices))
		for _, node := range assignment.Devices {
			paths = append(paths, node.Path)
		}
		encoded, _ := json.Marshal(paths)
		if _, err := db.Exec(`INSERT INTO hardware_assignments(installation_id,component_id,device_class,mode,vendor,stable_id,resolved_devices,runtime_generation,created_at) VALUES(?,?,?,'device',?,?,?,?,?)`, installation, component, assignment.Class, assignment.Vendor, assignment.StableID, string(encoded), generation, now); err != nil {
			return err
		}
	}
	return nil
}

func validateHardwareObservation(db *sql.DB, installation, component string, host map[string]any) error {
	rows, err := db.Query(`SELECT device_class,mode,vendor,stable_id,resolved_devices FROM hardware_assignments WHERE installation_id=? AND component_id=? ORDER BY device_class`, installation, component)
	if err != nil {
		return err
	}
	defer rows.Close()
	expectedPaths := map[string]bool{}
	expectNVIDIA := false
	var inv *hardware.Inventory
	for rows.Next() {
		var class, mode, vendor, stable, encoded string
		if err = rows.Scan(&class, &mode, &vendor, &stable, &encoded); err != nil {
			return err
		}
		if mode == "cpu" {
			continue
		}
		if inv == nil {
			info, infoErr := newContainerRuntime().Info()
			if infoErr != nil {
				return fmt.Errorf("accelerator inventory unavailable")
			}
			discovered, discoverErr := hardware.Discover(hardware.ParseDockerRuntimes(info["Runtimes"]))
			if discoverErr != nil {
				return fmt.Errorf("accelerator inventory unavailable")
			}
			inv = &discovered
		}
		current, resolveErr := inv.Resolve(class)
		if resolveErr != nil {
			return fmt.Errorf("accelerator unavailable")
		}
		if current.Vendor != vendor || current.StableID != stable {
			return fmt.Errorf("accelerator identity drift")
		}
		if class == hardware.NVIDIAClass {
			expectNVIDIA = true
		}
		var paths []string
		if json.Unmarshal([]byte(encoded), &paths) != nil {
			return fmt.Errorf("trusted hardware assignment unreadable")
		}
		for _, path := range paths {
			expectedPaths[path] = true
		}
	}
	observedPaths := map[string]bool{}
	if devices, ok := host["Devices"].([]any); ok {
		for _, raw := range devices {
			if item, ok := raw.(map[string]any); ok {
				if path, ok := item["PathOnHost"].(string); ok {
					observedPaths[path] = true
				}
			}
		}
	}
	if len(observedPaths) != len(expectedPaths) {
		return fmt.Errorf("device mapping count drift")
	}
	for path := range expectedPaths {
		if !observedPaths[path] {
			return fmt.Errorf("device mapping drift")
		}
	}
	requests, _ := host["DeviceRequests"].([]any)
	if expectNVIDIA != (len(requests) == 1) {
		return fmt.Errorf("GPU runtime request drift")
	}
	if !expectNVIDIA && len(requests) != 0 {
		return fmt.Errorf("unexpected GPU runtime request")
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
		if !hardwareRequirementsEqual(got.Hardware, want.Hardware) {
			return fmt.Errorf("component %s hardware mismatch", got.ID)
		}
		if !externalBindingsEqual(got.ExternalStorage, want.ExternalStorage) {
			return fmt.Errorf("component %s external storage mismatch", got.ID)
		}
		if !runtimeIdentityEqual(got.RunAs, want.RunAs) {
			return fmt.Errorf("component %s runtime identity mismatch", got.ID)
		}
		for j, arg := range want.Command {
			if got.Command[j] != arg || arg == "/bin/sh" || arg == "/bin/bash" || arg == "-c" || arg == "" {
				return fmt.Errorf("component %s command mismatch", got.ID)
			}
		}
		for j, variable := range want.Environment {
			if got.Environment[j].Name != variable.Name || got.Environment[j].Value != variable.Value || got.Environment[j].Secret != variable.Secret || got.Environment[j].Generate != variable.Generate {
				return fmt.Errorf("component %s environment mismatch", got.ID)
			}
		}
		for j, s := range want.Storage {
			expected := "/srv/kitpro/apps/" + r.ApplicationID + "/" + r.InstanceID + "/" + got.ID + "/" + s.ID
			if got.Storage[j].ID != s.ID || got.Storage[j].ContainerPath != s.ContainerPath || got.Storage[j].HostPath != expected || got.Storage[j].ReadOnly != s.ReadOnly || got.Storage[j].OwnerUID != s.OwnerUID || got.Storage[j].OwnerGID != s.OwnerGID {
				return fmt.Errorf("component %s storage mismatch", got.ID)
			}
		}
		for j, s := range want.Services {
			if got.Services[j].ID != s.ID || got.Services[j].Protocol != s.Protocol || got.Services[j].ContainerPort != s.ContainerPort {
				return fmt.Errorf("component %s service mismatch", got.ID)
			}
		}
		for _, e := range got.Environment {
			if e.Name == "" || (e.Secret && (e.Value != "" || e.Generate != "random-hex-32")) || (!e.Secret && e.Generate != "") {
				return fmt.Errorf("component %s invalid environment", got.ID)
			}
		}
		components = append(components, manifest.Component{ID: got.ID, DependsOn: append([]string(nil), got.DependsOn...)})
	}
	if _, err := multicontainer.StartOrder(components); err != nil {
		return err
	}
	return validateTrustedBindings(r, m)
}

func validateTrustedBindings(r protocol.Request, m manifest.Manifest) error {
	legacyScalar := r.ExposureMode != "" || r.ServiceID != "" || r.HostAddress != "" || r.HostPort != 0 || r.ContainerPort != 0 || r.ServiceProtocol != ""
	if legacyScalar && (r.Version >= 2 || len(r.Bindings) != 0) {
		return fmt.Errorf("scalar exposure fields are not accepted")
	}
	bindingsInput := r.Bindings
	if r.Version < 2 && len(bindingsInput) == 0 {
		bindingsInput = legacyBindings(r, m)
	}
	if len(bindingsInput) != len(m.Services) || len(bindingsInput) > exposure.MaxBindings {
		return fmt.Errorf("trusted binding set shape mismatch")
	}
	declared := map[string]manifest.Service{}
	for _, service := range m.Services {
		declared[service.ID] = service
	}
	seen := map[string]bool{}
	bindings := make([]exposure.ServiceBinding, 0, len(bindingsInput))
	for _, got := range bindingsInput {
		service, ok := declared[got.ServiceID]
		if !ok || seen[got.ServiceID] {
			return fmt.Errorf("binding service is not uniquely declared")
		}
		seen[got.ServiceID] = true
		transport, err := exposure.TransportFor(service.Protocol)
		if err != nil || string(transport) != got.Transport || service.ContainerPort != got.ContainerPort {
			return fmt.Errorf("trusted service binding mismatch")
		}
		binding := exposure.ServiceBinding{ServiceID: got.ServiceID, Transport: transport, ContainerPort: got.ContainerPort, Mode: exposure.Mode(got.Mode), HostAddress: got.HostAddress, HostPort: got.HostPort}
		if err = exposure.ValidateBinding(binding, env("KITPRO_LAN_BIND_ADDRESS", ""), service.FixedHostPort); err != nil {
			return err
		}
		bindings = append(bindings, binding)
	}
	return exposure.ValidateConflicts(bindings)
}

func legacyBindings(r protocol.Request, m manifest.Manifest) []protocol.ServiceBinding {
	mode := r.ExposureMode
	if mode == "" {
		mode = string(exposure.Internal)
	}
	out := make([]protocol.ServiceBinding, 0, len(m.Services))
	for _, service := range m.Services {
		transport, _ := exposure.TransportFor(service.Protocol)
		binding := protocol.ServiceBinding{ServiceID: service.ID, Transport: string(transport), ContainerPort: service.ContainerPort, Mode: string(exposure.Internal), HostPort: service.FixedHostPort}
		if service.ID == r.ServiceID && mode != string(exposure.Internal) {
			binding.Mode, binding.HostAddress, binding.HostPort = mode, r.HostAddress, r.HostPort
			if r.ContainerPort != 0 {
				binding.ContainerPort = r.ContainerPort
			}
			if r.ServiceProtocol != "" {
				binding.Transport, _ = exposure.ContainerProtocol(r.ServiceProtocol)
			}
		}
		out = append(out, binding)
	}
	return out
}

func requestBindings(r protocol.Request) []exposure.ServiceBinding {
	out := make([]exposure.ServiceBinding, 0, len(r.Bindings))
	for _, b := range r.Bindings {
		out = append(out, exposure.ServiceBinding{ServiceID: b.ServiceID, Transport: exposure.Transport(b.Transport), ContainerPort: b.ContainerPort, Mode: exposure.Mode(b.Mode), HostAddress: b.HostAddress, HostPort: b.HostPort})
	}
	return exposure.Normalize(out)
}

func trustedRequestBindings(r protocol.Request, m manifest.Manifest) []exposure.ServiceBinding {
	if len(r.Bindings) == 0 {
		r.Bindings = legacyBindings(r, m)
	}
	return requestBindings(r)
}

func runtimePortBindings(bindings []exposure.ServiceBinding) map[string][]containers.PortBinding {
	out := map[string][]containers.PortBinding{}
	for _, binding := range bindings {
		if binding.Mode == exposure.Internal {
			continue
		}
		key := fmt.Sprintf("%d/%s", binding.ContainerPort, binding.Transport)
		out[key] = append(out[key], containers.PortBinding{HostIP: binding.HostAddress, HostPort: strconv.Itoa(binding.HostPort)})
	}
	return out
}

func validateTrustedRecreation(db *sql.DB, r protocol.Request) error {
	var generation int
	var legacyPort int
	legacyOwnership := false
	var applicationID, releaseID, image, dataPath string
	err := db.QueryRow(`SELECT g.runtime_generation,g.application_id,g.release_id,c.image_digest,g.data_path FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id=? AND g.status IN ('active','verification_required') AND c.component_id='app' ORDER BY CASE g.status WHEN 'active' THEN 0 ELSE 1 END LIMIT 1`, r.InstanceID).Scan(&generation, &applicationID, &releaseID, &image, &dataPath)
	if err == sql.ErrNoRows {
		err = db.QueryRow(`SELECT runtime_generation,application_id,release_id,image_digest,data_path,host_port FROM ownership WHERE instance_id=?`, r.InstanceID).Scan(&generation, &applicationID, &releaseID, &image, &dataPath, &legacyPort)
		legacyOwnership = err == nil
	}
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
	if r.Operation != "ConfigureServiceExposure" {
		if legacyOwnership && r.Version < 2 {
			if legacyPort != 0 && r.HostPort != legacyPort {
				return fmt.Errorf("trusted exposure assignment mismatch")
			}
			return nil
		}
		rows, queryErr := db.Query(`SELECT service_id,transport,container_port,mode,host_address,host_port FROM runtime_generation_bindings WHERE installation_id=? AND runtime_generation=? ORDER BY service_id`, r.InstanceID, generation)
		if queryErr != nil {
			return fmt.Errorf("trusted binding state unavailable")
		}
		var stored []exposure.ServiceBinding
		for rows.Next() {
			var b exposure.ServiceBinding
			if queryErr = rows.Scan(&b.ServiceID, &b.Transport, &b.ContainerPort, &b.Mode, &b.HostAddress, &b.HostPort); queryErr != nil {
				break
			}
			stored = append(stored, b)
		}
		_ = rows.Close()
		requested := requestBindings(r)
		if queryErr != nil || len(stored) != len(requested) {
			return fmt.Errorf("trusted exposure assignment mismatch")
		}
		for i := range stored {
			if stored[i] != requested[i] {
				return fmt.Errorf("trusted exposure assignment mismatch")
			}
		}
	}
	return nil
}
func createApplication(c io.Writer, db *sql.DB, r protocol.Request, coordinator helperops.Coordinator, fencingToken int64) {
	if e := validateApplicationPlan(r); e != nil {
		slog.Default().Warn("application plan rejected", "event", "plan_rejected", "operation_id", r.SemanticOperationID(), "error_category", "validation")
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	if r.RepairAction != "" {
		if r.RepairAction != lifecycle.RepairRecreateGeneration || r.Operation != "InstallApplication" {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "invalid lifecycle repair request"})
			return
		}
		reconciliation, err := (lifecycle.Store{DB: db}).LoadReconciliation(context.Background(), r.InstanceID)
		if err != nil || reconciliation.CheckedGeneration != r.RuntimeGeneration-1 || reconciliation.RecommendedAction != lifecycle.RepairRecreateGeneration {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "missing runtime recreation requires current helper reconciliation evidence"})
			return
		}
	}
	var trustedErr error
	if len(r.Components) > 0 {
		trustedErr = validateTrustedMultiRecreation(db, r)
	} else {
		trustedErr = validateTrustedRecreation(db, r)
	}
	if e := trustedErr; e != nil {
		slog.Default().Warn("application plan rejected", "event", "plan_rejected", "operation_id", r.SemanticOperationID(), "error_category", "trusted_state")
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	if len(r.Components) > 0 {
		createMultiApplication(c, db, r, coordinator, fencingToken)
		return
	}
	gen := r.RuntimeGeneration
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: r.InstanceID, ownership.LabelResource: "application", "com.kitpro.application": r.ApplicationID, "com.kitpro.release": r.ReleaseID, "com.kitpro.runtime-generation": strconv.Itoa(gen), "com.kitpro.operation": r.OperationID}
	name := "kitpro-" + r.ApplicationID + "-" + r.InstanceID + "-g" + strconv.Itoa(gen)
	d := newContainerRuntime()
	lifecycleRuntime, supported := d.(containers.LifecycleRuntime)
	if !supported {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "staged replacement requires the supported Docker lifecycle runtime"})
		return
	}
	_ = prepareMigratedSingleConfiguration(context.Background(), db, lifecycleRuntime, r.InstanceID)
	hw, e := resolveHardware(d, r.Hardware)
	if e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	entries, _ := catalog.Load()
	externalMounts, e := resolveExternalMounts(db, r.InstanceID, "", gen, entries[r.ApplicationID].Manifest.ExternalStorage, r.ExternalStorage)
	if e != nil {
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
		if s.OwnerUID != 0 && os.Chown(s.HostPath, s.OwnerUID, s.OwnerGID) != nil {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "managed storage ownership failed"})
			return
		}
	}
	mounts := make([]containers.StorageMount, 0, len(r.Storage))
	for _, s := range r.Storage {
		mounts = append(mounts, containers.StorageMount{ContainerPath: s.ContainerPath, HostPath: s.HostPath, ReadOnly: s.ReadOnly})
	}
	mounts = append(mounts, externalMounts...)
	env, e := resolvedEnvironment(db, r.InstanceID, "", r.Environment)
	if e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "application secret preparation failed"})
		return
	}
	trustedBindings := trustedRequestBindings(r, entries[r.ApplicationID].Manifest)
	bindings := runtimePortBindings(trustedBindings)
	user := ""
	if r.RunAs != nil {
		user = strconv.Itoa(r.RunAs.UID) + ":" + strconv.Itoa(r.RunAs.GID)
	}
	containerPlan := containers.ContainerPlan{Image: r.Image, Name: name, Network: r.NetworkName, User: user, Labels: labels, Command: r.Command, Environment: env, DataPath: r.DataPath, Storage: mounts, PortBindings: bindings, RestartPolicy: r.RestartPolicy, Devices: hw.Devices, DeviceRequests: hw.Requests}
	planHash, e := protocol.Hash(r)
	if e != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		return
	}
	rollbackSafe := r.Operation != "UpdateApplication" && r.RepairAction == ""
	plan := lifecycle.Plan{OperationID: r.OperationID, InstallationID: r.InstanceID, ApplicationID: r.ApplicationID, ReleaseID: r.ReleaseID, Generation: gen, ExpectedGeneration: gen - 1, FencingToken: fencingToken, Image: r.Image, NetworkName: r.NetworkName, ContainerName: name, PlanHash: planHash, DataPath: r.DataPath, Bindings: trustedBindings, Container: containerPlan, RollbackSafe: rollbackSafe, AllowMissingActive: r.RepairAction == lifecycle.RepairRecreateGeneration}
	// Generation-scoped helper metadata is prepared before runtime cutover. It
	// is evidence for recovery, not the authoritative active-generation switch.
	tx, persistErr := db.Begin()
	if persistErr == nil {
		persistErr = persistHardware(tx, r.InstanceID, "", gen, hw)
	}
	if persistErr == nil {
		persistErr = persistExternalBindings(tx, r.InstanceID, "", gen, entries[r.ApplicationID].Manifest.ExternalStorage, r.ExternalStorage)
	}
	if persistErr == nil {
		persistErr = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if persistErr != nil {
		protocol.Write(c, protocol.Response{RequestID: r.ID, Error: "candidate metadata preparation failed"})
		return
	}
	result, e := (lifecycle.Runner{Runtime: lifecycleRuntime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Replace(context.Background(), plan)
	if e != nil {
		event := "exposure_failed"
		if strings.Contains(e.Error(), "address already in use") || strings.Contains(e.Error(), "port is already allocated") {
			event = "exposure_collision"
		}
		slog.Default().Warn("runtime creation failed", "event", event, "operation_id", r.SemanticOperationID(), "installation_id", r.InstanceID, "binding_count", len(r.Bindings), "result", "failed", "error_category", "runtime")
		var unknown lifecycle.UnknownOutcomeError
		if errors.As(e, &unknown) {
			protocol.Write(c, protocol.Response{RequestID: r.ID, ErrorCode: "RecoveryRequired", Error: e.Error()})
		} else {
			protocol.Write(c, protocol.Response{RequestID: r.ID, Error: e.Error()})
		}
		return
	}
	slog.Default().Info("runtime created", "event", "exposure_applied", "operation_id", r.SemanticOperationID(), "installation_id", r.InstanceID, "binding_count", len(r.Bindings), "result", "succeeded")
	protocol.Write(c, protocol.Response{OK: true, RequestID: r.ID, Result: result})
}

func validateTrustedMultiRecreation(db *sql.DB, r protocol.Request) error {
	var generation int
	err := db.QueryRow(`SELECT runtime_generation FROM runtime_generations WHERE installation_id=? AND status IN ('active','verification_required','removed') ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'verification_required' THEN 1 ELSE 2 END,runtime_generation DESC LIMIT 1`, r.InstanceID).Scan(&generation)
	if err == sql.ErrNoRows {
		err = db.QueryRow("SELECT MIN(runtime_generation) FROM component_ownership WHERE installation_id=? HAVING MIN(runtime_generation)=MAX(runtime_generation)", r.InstanceID).Scan(&generation)
	}
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

// createMultiApplication translates an already catalog-validated request into
// the same generation model used by staged single-container replacement. The
// older direct Docker loop remains below only as unreachable migration history.
func createMultiApplication(c io.Writer, db *sql.DB, request protocol.Request, coordinator helperops.Coordinator, fencingToken int64) {
	runtime := newContainerRuntime()
	lifecycleRuntime, supported := runtime.(containers.LifecycleRuntime)
	if !supported {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "staged replacement requires the supported Docker lifecycle runtime"})
		return
	}
	manifestNodes := make([]manifest.Component, 0, len(request.Components))
	byID := make(map[string]protocol.Component, len(request.Components))
	for _, component := range request.Components {
		manifestNodes = append(manifestNodes, manifest.Component{ID: component.ID, DependsOn: append([]string(nil), component.DependsOn...)})
		byID[component.ID] = component
	}
	topology, err := multicontainer.BuildTopology(manifestNodes)
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: err.Error()})
		return
	}
	entries, err := catalog.Load()
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "trusted catalog unavailable"})
		return
	}
	trustedComponents := map[string]manifest.Component{}
	for _, component := range entries[request.ApplicationID].Manifest.Components {
		trustedComponents[component.ID] = component
	}
	if err = backfillLegacyMulti(db, request, topology, entries[request.ApplicationID].Manifest); err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: err.Error()})
		return
	}
	labels := map[string]string{ownership.LabelManaged: "true", ownership.LabelInstance: request.InstanceID, ownership.LabelResource: "application", "com.kitpro.application": request.ApplicationID, "com.kitpro.release": request.ReleaseID, "com.kitpro.runtime-generation": strconv.Itoa(request.RuntimeGeneration), "com.kitpro.operation": request.OperationID}
	planHash, err := protocol.Hash(request)
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: err.Error()})
		return
	}
	trustedBindings := trustedRequestBindings(request, entries[request.ApplicationID].Manifest)
	plan := lifecycle.MultiPlan{OperationID: request.OperationID, InstallationID: request.InstanceID, ApplicationID: request.ApplicationID, ReleaseID: request.ReleaseID, Generation: request.RuntimeGeneration, ExpectedGeneration: request.RuntimeGeneration - 1, FencingToken: fencingToken, NetworkName: request.NetworkName, PlanHash: planHash, TopologyHash: topology.Hash, DataPath: request.DataPath, Bindings: trustedBindings, RollbackSafe: request.Operation != "UpdateApplication" && request.RepairAction == "", AllowMissingActive: request.RepairAction == lifecycle.RepairRecreateGeneration}
	tx, err := db.Begin()
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "candidate metadata preparation failed"})
		return
	}
	defer tx.Rollback()
	for _, node := range topology.Components {
		component := byID[node.ID]
		externalMounts, prepareErr := resolveExternalMounts(db, request.InstanceID, component.ID, request.RuntimeGeneration, trustedComponents[component.ID].ExternalStorage, component.ExternalStorage)
		if prepareErr != nil {
			err = prepareErr
			break
		}
		hardwarePlan, prepareErr := resolveHardware(runtime, component.Hardware)
		if prepareErr != nil {
			err = prepareErr
			break
		}
		for _, storage := range component.Storage {
			if prepareErr = os.MkdirAll(storage.HostPath, 0750); prepareErr != nil {
				err = prepareErr
				break
			}
			if storage.OwnerUID != 0 && os.Chown(storage.HostPath, storage.OwnerUID, storage.OwnerGID) != nil {
				err = errors.New("managed storage ownership failed")
				break
			}
		}
		if err != nil {
			break
		}
		environment, prepareErr := resolvedEnvironment(db, request.InstanceID, component.ID, component.Environment)
		if prepareErr != nil {
			err = errors.New("application secret preparation failed")
			break
		}
		mounts := make([]containers.StorageMount, 0, len(component.Storage)+len(externalMounts))
		for _, storage := range component.Storage {
			mounts = append(mounts, containers.StorageMount{ContainerPath: storage.ContainerPath, HostPath: storage.HostPath, ReadOnly: storage.ReadOnly})
		}
		mounts = append(mounts, externalMounts...)
		bindings := map[string][]containers.PortBinding{}
		for _, service := range component.Services {
			for _, binding := range trustedBindings {
				if binding.ServiceID == service.ID && binding.ContainerPort == service.ContainerPort && binding.Mode != exposure.Internal {
					key := fmt.Sprintf("%d/%s", binding.ContainerPort, binding.Transport)
					bindings[key] = append(bindings[key], containers.PortBinding{HostIP: binding.HostAddress, HostPort: strconv.Itoa(binding.HostPort)})
				}
			}
		}
		name := "kitpro-" + request.ApplicationID + "-" + request.InstanceID + "-" + component.ID + "-g" + strconv.Itoa(request.RuntimeGeneration)
		userName := ""
		if component.RunAs != nil {
			userName = strconv.Itoa(component.RunAs.UID) + ":" + strconv.Itoa(component.RunAs.GID)
		}
		dependencies := make([]string, 0, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			dependencies = append(dependencies, "kitpro-"+request.ApplicationID+"-"+request.InstanceID+"-"+dependency+"-g"+strconv.Itoa(request.RuntimeGeneration))
		}
		componentLabels := map[string]string{}
		for key, value := range labels {
			componentLabels[key] = value
		}
		componentLabels["com.kitpro.component"] = component.ID
		containerPlan := containers.ContainerPlan{Image: component.Image, Name: name, Network: request.NetworkName, User: userName, Labels: componentLabels, Command: component.Command, Environment: environment, Storage: mounts, PortBindings: bindings, RestartPolicy: component.Restart, NetworkAliases: []string{component.ID}, Dependencies: dependencies, Devices: hardwarePlan.Devices, DeviceRequests: hardwarePlan.Requests}
		plan.Components = append(plan.Components, lifecycle.MultiComponentPlan{ID: component.ID, Image: component.Image, ContainerName: name, DependsOn: append([]string(nil), node.DependsOn...), StartOrdinal: node.Ordinal, Container: containerPlan})
		if err = persistHardware(tx, request.InstanceID, component.ID, request.RuntimeGeneration, hardwarePlan); err != nil {
			break
		}
		if err = persistExternalBindings(tx, request.InstanceID, component.ID, request.RuntimeGeneration, trustedComponents[component.ID].ExternalStorage, component.ExternalStorage); err != nil {
			break
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "candidate metadata preparation failed: " + err.Error()})
		return
	}
	result, err := (lifecycle.MultiRunner{Runtime: lifecycleRuntime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Replace(context.Background(), plan)
	if err != nil {
		var unknown lifecycle.UnknownOutcomeError
		if errors.As(err, &unknown) {
			protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: err.Error()})
		} else {
			protocol.Write(c, protocol.Response{RequestID: request.ID, Error: err.Error()})
		}
		return
	}
	protocol.Write(c, protocol.Response{OK: true, RequestID: request.ID, Result: result})
}

func backfillLegacyMulti(db *sql.DB, request protocol.Request, topology multicontainer.Topology, trusted manifest.Manifest) error {
	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id=?`, request.InstanceID).Scan(&existing); err != nil || existing > 0 {
		return err
	}
	rows, err := db.Query(`SELECT component_id,container_id,container_name,network_name,image_digest,runtime_generation,created_at FROM component_ownership WHERE installation_id=? ORDER BY component_id`, request.InstanceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type legacyRow struct {
		componentID, containerID, containerName, networkName, image, createdAt string
		generation                                                             int
	}
	var legacy []legacyRow
	for rows.Next() {
		var row legacyRow
		if err = rows.Scan(&row.componentID, &row.containerID, &row.containerName, &row.networkName, &row.image, &row.generation, &row.createdAt); err != nil {
			return err
		}
		legacy = append(legacy, row)
	}
	if err = rows.Err(); err != nil || len(legacy) == 0 {
		return err
	}
	if len(legacy) != len(topology.Components) {
		return errors.New("legacy component ownership does not match one complete trusted topology")
	}
	trustedImages := map[string]string{}
	for _, component := range trusted.Components {
		for _, release := range trusted.Releases {
			if release.Version == component.Release {
				trustedImages[component.ID] = release.Registry + "/" + release.Repository + "@" + release.Digest
				break
			}
		}
	}
	byID := map[string]legacyRow{}
	generation, network := legacy[0].generation, legacy[0].networkName
	for _, row := range legacy {
		if row.generation != generation || row.networkName != network || row.containerID == "" || trustedImages[row.componentID] != row.image {
			return errors.New("legacy component ownership is mixed or does not match the trusted catalog")
		}
		if _, duplicate := byID[row.componentID]; duplicate {
			return errors.New("legacy component ownership contains a duplicate")
		}
		byID[row.componentID] = row
	}
	migrated := lifecycle.MultiGeneration{InstallationID: request.InstanceID, CreatingOperationID: "legacy-multi-migration", ApplicationID: request.ApplicationID, ReleaseID: trusted.Releases[0].Version, Generation: generation, Status: "verification_required", NetworkName: network, PlanHash: "legacy-component-ownership", TopologyHash: topology.Hash, DataPath: request.DataPath, Bindings: trustedRequestBindings(request, trusted), CleanupState: "not_required"}
	for _, node := range topology.Components {
		row, ok := byID[node.ID]
		if !ok {
			return errors.New("legacy component ownership is missing a trusted component")
		}
		migrated.Components = append(migrated.Components, lifecycle.Component{ID: node.ID, ContainerName: row.containerName, ContainerID: row.containerID, Image: row.image, State: "unknown", DependsOn: append([]string(nil), node.DependsOn...), StartOrdinal: node.Ordinal})
	}
	return (lifecycle.Store{DB: db}).BackfillLegacyMulti(context.Background(), migrated)
}

// createMultiApplication creates a bounded, dependency-ordered runtime set.
// It is deliberately separate from the single-container path: no raw runtime
// objects cross the helper boundary, and every component is revalidated above.
func resolvedEnvironment(db *sql.DB, installationID, componentID string, variables []protocol.EnvVar) ([]string, error) {
	result := make([]string, 0, len(variables))
	for _, variable := range variables {
		value := variable.Value
		if variable.Secret {
			if variable.Generate != "random-hex-32" || value != "" {
				return nil, fmt.Errorf("invalid generated secret")
			}
			var secret string
			err := db.QueryRow(`SELECT value FROM installation_secrets WHERE installation_id=? AND component_id=? AND name=?`, installationID, componentID, variable.Name).Scan(&secret)
			if err == sql.ErrNoRows {
				raw := make([]byte, 32)
				if _, err = rand.Read(raw); err != nil {
					return nil, err
				}
				secret = hex.EncodeToString(raw)
				_, err = db.Exec(`INSERT OR IGNORE INTO installation_secrets(installation_id,component_id,name,value,created_at) VALUES(?,?,?,?,?)`, installationID, componentID, variable.Name, secret, time.Now().UTC().Format(time.RFC3339Nano))
				if err == nil {
					err = db.QueryRow(`SELECT value FROM installation_secrets WHERE installation_id=? AND component_id=? AND name=?`, installationID, componentID, variable.Name).Scan(&secret)
				}
			}
			if err != nil || len(secret) != 64 {
				return nil, fmt.Errorf("generated secret unavailable")
			}
			value = secret
		}
		result = append(result, variable.Name+"="+value)
	}
	return result, nil
}
func maxGeneration(g int) int {
	if g < 1 {
		return 1
	}
	return g
}
func trustedRuntimeIdentityMatches(applicationID, componentID string, config map[string]any) bool {
	entries, err := catalog.Load()
	if err != nil {
		return false
	}
	entry, ok := entries[applicationID]
	if !ok {
		return false
	}
	want := entry.Manifest.RunAs
	if componentID != "" {
		want = nil
		for _, component := range entry.Manifest.Components {
			if component.ID == componentID {
				want = component.RunAs
				break
			}
		}
	}
	if want == nil {
		return true
	}
	got, _ := config["User"].(string)
	return got == strconv.Itoa(want.UID)+":"+strconv.Itoa(want.GID)
}
func changeApplicationState(c io.Writer, db *sql.DB, request protocol.Request, coordinator helperops.Coordinator, fencingToken int64, action string) {
	var componentCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM runtime_components c JOIN runtime_generations g USING(installation_id,runtime_generation) WHERE c.installation_id=? AND g.status IN ('active','verification_required')`, request.InstanceID).Scan(&componentCount)
	if err != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: err.Error()})
		return
	}
	var multiGeneration int
	_ = db.QueryRow(`SELECT COUNT(*) FROM runtime_generations WHERE installation_id=? AND status IN ('active','verification_required') AND topology_hash<>''`, request.InstanceID).Scan(&multiGeneration)
	if multiGeneration > 0 && componentCount < 2 {
		protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: "multi-component generation evidence is incomplete"})
		return
	}
	if componentCount > 1 {
		runtime, ok := newContainerRuntime().(containers.LifecycleRuntime)
		if !ok {
			protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "multi-component lifecycle requires the supported Docker lifecycle runtime"})
			return
		}
		result, operationErr := (lifecycle.MultiRunner{Runtime: runtime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Operate(context.Background(), request.InstanceID, request.OperationID, fencingToken, action)
		if operationErr != nil {
			var unknown lifecycle.UnknownOutcomeError
			if errors.As(operationErr, &unknown) {
				protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: operationErr.Error()})
			} else {
				protocol.Write(c, protocol.Response{RequestID: request.ID, Error: operationErr.Error()})
			}
			return
		}
		protocol.Write(c, protocol.Response{OK: true, RequestID: request.ID, Result: result})
		return
	}
	var legacyCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM component_ownership WHERE installation_id=?", request.InstanceID).Scan(&legacyCount)
	if legacyCount > 0 {
		if migrationErr := backfillLegacyMultiForLifecycle(db, request.InstanceID); migrationErr != nil {
			protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: migrationErr.Error()})
			return
		}
		if countErr := db.QueryRow(`SELECT COUNT(*) FROM runtime_components c JOIN runtime_generations g USING(installation_id,runtime_generation) WHERE c.installation_id=? AND g.status='verification_required'`, request.InstanceID).Scan(&componentCount); countErr == nil && componentCount > 1 {
			runtime, ok := newContainerRuntime().(containers.LifecycleRuntime)
			if !ok {
				protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "multi-component lifecycle requires the supported Docker lifecycle runtime"})
				return
			}
			result, operationErr := (lifecycle.MultiRunner{Runtime: runtime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Operate(context.Background(), request.InstanceID, request.OperationID, fencingToken, action)
			if operationErr != nil {
				protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: operationErr.Error()})
				return
			}
			protocol.Write(c, protocol.Response{OK: true, RequestID: request.ID, Result: result})
			return
		}
	}
	runtime, ok := newContainerRuntime().(containers.LifecycleRuntime)
	if !ok {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "single-component lifecycle requires the supported Docker lifecycle runtime"})
		return
	}
	_ = prepareMigratedSingleConfiguration(context.Background(), db, runtime, request.InstanceID)
	if (action == "start" || action == "restart") && validateInstallationStorageAvailable(db, request.InstanceID) != nil {
		protocol.Write(c, protocol.Response{RequestID: request.ID, Error: "storage unavailable"})
		return
	}
	result, operationErr := (lifecycle.SingleRunner{Runtime: runtime, Store: lifecycle.Store{DB: db}, Evidence: coordinator}).Operate(context.Background(), request.InstanceID, request.OperationID, fencingToken, action)
	if operationErr != nil {
		var unknown lifecycle.UnknownOutcomeError
		if errors.As(operationErr, &unknown) {
			protocol.Write(c, protocol.Response{RequestID: request.ID, ErrorCode: "RecoveryRequired", Error: operationErr.Error()})
		} else {
			protocol.Write(c, protocol.Response{RequestID: request.ID, Error: operationErr.Error()})
		}
		return
	}
	protocol.Write(c, protocol.Response{OK: true, RequestID: request.ID, Result: result})
}

func backfillLegacyMultiForLifecycle(db *sql.DB, installation string) error {
	rows, err := db.Query(`SELECT component_id,image_digest,runtime_generation FROM component_ownership WHERE installation_id=? ORDER BY component_id`, installation)
	if err != nil {
		return err
	}
	defer rows.Close()
	images := map[string]string{}
	generation := 0
	for rows.Next() {
		var componentID, image string
		var rowGeneration int
		if err = rows.Scan(&componentID, &image, &rowGeneration); err != nil {
			return err
		}
		if generation == 0 {
			generation = rowGeneration
		}
		if rowGeneration != generation || images[componentID] != "" {
			return errors.New("legacy component ownership has mixed generations or duplicates")
		}
		images[componentID] = image
	}
	if err = rows.Err(); err != nil || len(images) < 2 {
		return errors.New("legacy component ownership is incomplete")
	}
	entries, err := catalog.Load()
	if err != nil {
		return err
	}
	type match struct {
		manifest manifest.Manifest
		topology multicontainer.Topology
		request  protocol.Request
	}
	var matches []match
	for _, entry := range entries {
		if len(entry.Manifest.Components) != len(images) || len(entry.Manifest.Components) < 2 {
			continue
		}
		request := protocol.Request{InstanceID: installation, ApplicationID: entry.Manifest.ID, ReleaseID: entry.Manifest.Releases[0].Version, RuntimeGeneration: generation + 1, DataPath: "/srv/kitpro/apps/" + entry.Manifest.ID + "/" + installation + "/data", ExposureMode: "internal"}
		nodes := make([]manifest.Component, 0, len(entry.Manifest.Components))
		exact := true
		for _, component := range entry.Manifest.Components {
			image := ""
			for _, release := range entry.Manifest.Releases {
				if release.Version == component.Release {
					image = release.Registry + "/" + release.Repository + "@" + release.Digest
					break
				}
			}
			if images[component.ID] != image {
				exact = false
				break
			}
			request.Components = append(request.Components, protocol.Component{ID: component.ID, Image: image, DependsOn: append([]string(nil), component.DependsOn...)})
			nodes = append(nodes, manifest.Component{ID: component.ID, DependsOn: append([]string(nil), component.DependsOn...)})
		}
		if !exact {
			continue
		}
		topology, topologyErr := multicontainer.BuildTopology(nodes)
		if topologyErr != nil {
			continue
		}
		matches = append(matches, match{manifest: entry.Manifest, topology: topology, request: request})
	}
	if len(matches) != 1 {
		return errors.New("legacy component ownership does not match exactly one trusted catalog topology")
	}
	return backfillLegacyMulti(db, matches[0].request, matches[0].topology, matches[0].manifest)
}

func backfillLegacyMultiAtStartup(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT installation_id FROM component_ownership GROUP BY installation_id HAVING COUNT(*)>1 AND NOT EXISTS (SELECT 1 FROM runtime_generations g WHERE g.installation_id=component_ownership.installation_id) ORDER BY installation_id`)
	if err != nil {
		return 0, err
	}
	var installations []string
	for rows.Next() {
		var installation string
		if err = rows.Scan(&installation); err != nil {
			rows.Close()
			return 0, err
		}
		installations = append(installations, installation)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	store := lifecycle.Store{DB: db}
	for _, installation := range installations {
		if err = backfillLegacyMultiForLifecycle(db, installation); err != nil {
			result := lifecycle.ReconciliationResult{InstallationID: installation, State: lifecycle.ReconciliationActionRequired, RuntimeState: "unknown", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), MismatchCodes: []lifecycle.MismatchCode{lifecycle.MismatchOwnershipAmbiguous}, RecommendedAction: lifecycle.RepairNone, Summary: "legacy component ownership does not exactly match one trusted catalog topology"}
			if saveErr := store.SaveReconciliation(ctx, result); saveErr != nil {
				return 0, saveErr
			}
		}
	}
	return len(installations), nil
}

type singleRuntimeOwnership struct {
	Generation                                            int
	ContainerID, ContainerName, NetworkName, Image        string
	ApplicationID, ReleaseID, DataPath                    string
	ExposureMode, ServiceID, HostAddress, ServiceProtocol string
	HostPort, ContainerPort                               int
}

func loadSingleRuntime(db *sql.DB, installation string) (singleRuntimeOwnership, error) {
	var owned singleRuntimeOwnership
	err := db.QueryRow(`SELECT g.runtime_generation,c.observed_container_id,c.container_name,g.network_name,c.image_digest,g.application_id,g.release_id,g.data_path,g.exposure_mode,g.service_id,g.host_address,g.host_port,g.container_port,g.service_protocol FROM runtime_generations g JOIN runtime_components c USING(installation_id,runtime_generation) WHERE g.installation_id=? AND g.status IN ('active','verification_required') AND c.component_id='app' ORDER BY CASE g.status WHEN 'active' THEN 0 ELSE 1 END LIMIT 1`, installation).Scan(&owned.Generation, &owned.ContainerID, &owned.ContainerName, &owned.NetworkName, &owned.Image, &owned.ApplicationID, &owned.ReleaseID, &owned.DataPath, &owned.ExposureMode, &owned.ServiceID, &owned.HostAddress, &owned.HostPort, &owned.ContainerPort, &owned.ServiceProtocol)
	if err == sql.ErrNoRows {
		err = db.QueryRow(`SELECT runtime_generation,container_id,container_name,network_name,image_digest,application_id,release_id,data_path,exposure_mode,service_id,host_address,host_port,container_port,service_protocol FROM ownership WHERE instance_id=?`, installation).Scan(&owned.Generation, &owned.ContainerID, &owned.ContainerName, &owned.NetworkName, &owned.Image, &owned.ApplicationID, &owned.ReleaseID, &owned.DataPath, &owned.ExposureMode, &owned.ServiceID, &owned.HostAddress, &owned.HostPort, &owned.ContainerPort, &owned.ServiceProtocol)
	}
	return owned, err
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
