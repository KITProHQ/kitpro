package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/containers"
	"github.com/kitpro/kitpro/software/server/internal/exposure"
	"github.com/kitpro/kitpro/software/server/internal/hardware"
	"github.com/kitpro/kitpro/software/server/internal/lifecycle"
	"github.com/kitpro/kitpro/software/server/internal/ownership"
)

// prepareMigratedSingleConfiguration records an exact observation hash only
// after the legacy runtime matches configuration reconstructed from
// helper-owned state and the embedded trusted catalog. A mismatch deliberately
// leaves the hash empty; reconciliation then classifies the generation as
// action-required and lifecycle mutation remains blocked.
func prepareMigratedSingleConfiguration(ctx context.Context, db *sql.DB, runtime containers.LifecycleRuntime, installation string) error {
	generation, err := (lifecycle.Store{DB: db}).Current(ctx, installation)
	if errors.Is(err, sql.ErrNoRows) || generation.Status != "verification_required" || generation.Component.ConfigurationHash != "" {
		return nil
	}
	if err != nil {
		return err
	}
	var topologyHash string
	if err = db.QueryRowContext(ctx, `SELECT topology_hash FROM runtime_generations WHERE installation_id=? AND runtime_generation=?`, installation, generation.Generation).Scan(&topologyHash); err != nil {
		return err
	}
	if topologyHash != "" {
		return nil
	}
	observed, err := runtime.ObserveContainer(ctx, generation.Component.ContainerID)
	if err != nil {
		return err
	}
	if !observed.Exists {
		return errors.New("migrated runtime is missing")
	}
	expected, err := migratedSinglePlan(ctx, db, generation)
	if err != nil {
		return err
	}
	if err = lifecycle.VerifyMigratedConfiguration(expected, observed); err != nil {
		return err
	}
	return (lifecycle.Store{DB: db}).RecordMigratedConfiguration(ctx, installation, generation.Generation, lifecycle.ConfigurationHash(observed))
}

func prepareAllMigratedSingleConfigurations(ctx context.Context, db *sql.DB, runtime containers.LifecycleRuntime) error {
	rows, err := db.QueryContext(ctx, `SELECT installation_id FROM runtime_generations WHERE status='verification_required' AND topology_hash='' ORDER BY installation_id`)
	if err != nil {
		return err
	}
	var installations []string
	for rows.Next() {
		var installation string
		if err = rows.Scan(&installation); err != nil {
			rows.Close()
			return err
		}
		installations = append(installations, installation)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, installation := range installations {
		// Verification failure is represented by the deliberately empty hash.
		// Reconciliation then distinguishes missing/runtime-unreachable evidence
		// from configuration drift without preventing the helper from starting.
		_ = prepareMigratedSingleConfiguration(ctx, db, runtime, installation)
	}
	return nil
}

func migratedSinglePlan(ctx context.Context, db *sql.DB, generation lifecycle.Generation) (containers.ContainerPlan, error) {
	entries, err := catalog.Load()
	if err != nil {
		return containers.ContainerPlan{}, errors.New("trusted catalog unavailable")
	}
	entry, ok := entries[generation.ApplicationID]
	if !ok || len(entry.Manifest.Components) != 0 {
		return containers.ContainerPlan{}, errors.New("migrated application is not a trusted single-component application")
	}
	trustedImage := ""
	for _, release := range entry.Manifest.Releases {
		if release.Version == generation.ReleaseID {
			trustedImage = release.Registry + "/" + release.Repository + "@" + release.Digest
			break
		}
	}
	if trustedImage == "" || trustedImage != generation.Component.Image {
		return containers.ContainerPlan{}, errors.New("migrated release does not match the trusted catalog")
	}
	labels := map[string]string{
		ownership.LabelManaged:          "true",
		ownership.LabelInstance:         generation.InstallationID,
		ownership.LabelResource:         "application",
		"com.kitpro.application":        generation.ApplicationID,
		"com.kitpro.release":            generation.ReleaseID,
		"com.kitpro.runtime-generation": strconv.Itoa(generation.Generation),
	}
	plan := containers.ContainerPlan{Image: trustedImage, Name: generation.Component.ContainerName, Network: generation.NetworkName, Labels: labels, Command: append([]string(nil), entry.Manifest.Command...), RestartPolicy: entry.Manifest.Restart, PortBindings: map[string][]containers.PortBinding{}}
	if entry.Manifest.RunAs != nil {
		plan.User = strconv.Itoa(entry.Manifest.RunAs.UID) + ":" + strconv.Itoa(entry.Manifest.RunAs.GID)
	}
	for _, variable := range entry.Manifest.Environment {
		value := variable.Value
		if variable.Secret {
			if err = db.QueryRowContext(ctx, `SELECT value FROM installation_secrets WHERE installation_id=? AND component_id='' AND name=?`, generation.InstallationID, variable.Name).Scan(&value); err != nil {
				return containers.ContainerPlan{}, errors.New("migrated application secret is unavailable")
			}
		}
		plan.Environment = append(plan.Environment, variable.Name+"="+value)
	}
	for _, storage := range entry.Manifest.Storage {
		plan.Storage = append(plan.Storage, containers.StorageMount{HostPath: "/srv/kitpro/apps/" + generation.ApplicationID + "/" + generation.InstallationID + "/" + storage.ID, ContainerPath: storage.ContainerPath, ReadOnly: storage.ReadOnly})
	}
	externalRows, err := db.QueryContext(ctx, `SELECT r.canonical_path,b.container_path,b.access_mode FROM external_storage_bindings b JOIN trusted_storage_roots r ON r.root_id=b.root_id WHERE b.installation_id=? AND b.component_id='' ORDER BY b.slot_id`, generation.InstallationID)
	if err != nil {
		return containers.ContainerPlan{}, err
	}
	for externalRows.Next() {
		var source, target, mode string
		if err = externalRows.Scan(&source, &target, &mode); err != nil {
			externalRows.Close()
			return containers.ContainerPlan{}, err
		}
		plan.Storage = append(plan.Storage, containers.StorageMount{HostPath: source, ContainerPath: target, ReadOnly: mode == "read-only"})
	}
	if err = externalRows.Close(); err != nil {
		return containers.ContainerPlan{}, err
	}
	if generation.ExposureMode == "loopback" || generation.ExposureMode == "lan" {
		protocolName, protocolErr := exposure.ContainerProtocol(generation.ServiceProtocol)
		if protocolErr != nil {
			return containers.ContainerPlan{}, protocolErr
		}
		plan.PortBindings[fmt.Sprintf("%d/%s", generation.ContainerPort, protocolName)] = []containers.PortBinding{{HostIP: generation.HostAddress, HostPort: strconv.Itoa(generation.HostPort)}}
	}
	hardwareRows, err := db.QueryContext(ctx, `SELECT device_class,mode,resolved_devices FROM hardware_assignments WHERE installation_id=? AND component_id='' ORDER BY device_class`, generation.InstallationID)
	if err != nil {
		return containers.ContainerPlan{}, err
	}
	for hardwareRows.Next() {
		var class, mode, encoded string
		if err = hardwareRows.Scan(&class, &mode, &encoded); err != nil {
			hardwareRows.Close()
			return containers.ContainerPlan{}, err
		}
		if mode == "cpu" {
			continue
		}
		var paths []string
		if err = json.Unmarshal([]byte(encoded), &paths); err != nil {
			hardwareRows.Close()
			return containers.ContainerPlan{}, errors.New("trusted hardware assignment is unreadable")
		}
		for _, path := range paths {
			plan.Devices = append(plan.Devices, containers.DeviceMapping{PathOnHost: path, PathInContainer: path, CgroupPermissions: "rwm"})
		}
		if class == hardware.NVIDIAClass {
			plan.DeviceRequests = append(plan.DeviceRequests, containers.DeviceRequest{Driver: "nvidia", Count: -1, Capabilities: [][]string{{"gpu"}}})
		}
	}
	if err = hardwareRows.Close(); err != nil {
		return containers.ContainerPlan{}, err
	}
	return plan, nil
}
