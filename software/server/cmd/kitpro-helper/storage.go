package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/catalog"
	"github.com/kitpro/kitpro/software/server/internal/docker"
	"github.com/kitpro/kitpro/software/server/internal/externalstorage"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"github.com/kitpro/kitpro/software/server/internal/protocol"
	"regexp"
	"sort"
	"strings"
	"time"
)

var rootNamePattern = regexp.MustCompile(`^[[:alnum:]][[:alnum:] ._-]{0,63}$`)
var inspectExternalStorage = externalstorage.Inspect
var validateExternalStorage = externalstorage.Validate

func registerStorageRoot(db *sql.DB, request protocol.Request) (map[string]any, error) {
	if !rootNamePattern.MatchString(request.RootName) || !externalstorage.ValidMode(request.RootMode) {
		return nil, fmt.Errorf("invalid storage root name or access mode")
	}
	identity, err := inspectExternalStorage(request.RootPath)
	if err != nil {
		return nil, err
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	rootID := "storage-" + hex.EncodeToString(random)
	_, err = db.Exec(`INSERT INTO trusted_storage_roots(root_id,display_name,canonical_path,allowed_mode,device_major,device_minor,inode,filesystem_type,mount_source,mount_point,network_backed,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, rootID, request.RootName, identity.CanonicalPath, request.RootMode, identity.DeviceMajor, identity.DeviceMinor, identity.Inode, identity.Filesystem, identity.MountSource, identity.MountPoint, identity.NetworkBacked, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("storage root already registered")
	}
	return storageRootView(rootID, request.RootName, request.RootMode, identity, true), nil
}

func validateStorageObservation(db *sql.DB, installation, component, application string, host map[string]any) error {
	entries, err := catalog.Load()
	if err != nil {
		return fmt.Errorf("trusted catalog unavailable")
	}
	m := entries[application].Manifest
	managed := m.Storage
	if component != "" {
		managed = nil
		for _, c := range m.Components {
			if c.ID == component {
				managed = c.Storage
			}
		}
	}
	expected := []string{}
	for _, item := range managed {
		source := "/srv/kitpro/apps/" + application + "/" + installation + "/"
		if component != "" {
			source += component + "/"
		}
		source += item.ID
		bind := source + ":" + item.ContainerPath
		if item.ReadOnly {
			bind += ":ro"
		}
		expected = append(expected, bind)
	}
	rows, err := db.Query(`SELECT b.access_mode,b.container_path,r.canonical_path,r.device_major,r.device_minor,r.inode,r.filesystem_type,r.mount_source,r.mount_point,r.network_backed FROM external_storage_bindings b JOIN trusted_storage_roots r ON r.root_id=b.root_id WHERE b.installation_id=? AND b.component_id=?`, installation, component)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mode, target, path, fs, source, point string
		var major, minor uint32
		var inode uint64
		var network bool
		if err = rows.Scan(&mode, &target, &path, &major, &minor, &inode, &fs, &source, &point, &network); err != nil {
			return err
		}
		identity := externalstorage.Identity{CanonicalPath: path, DeviceMajor: major, DeviceMinor: minor, Inode: inode, Filesystem: fs, MountSource: source, MountPoint: point, NetworkBacked: network}
		if _, err = validateExternalStorage(identity, path); err != nil {
			return fmt.Errorf("trusted storage unavailable or changed")
		}
		bind := path + ":" + target
		if mode == externalstorage.ReadOnly {
			bind += ":ro"
		}
		expected = append(expected, bind)
	}
	observed := []string{}
	if values, ok := host["Binds"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok {
				observed = append(observed, text)
			}
		}
	}
	sort.Strings(expected)
	sort.Strings(observed)
	if strings.Join(expected, "\x00") != strings.Join(observed, "\x00") {
		return fmt.Errorf("storage mount security drift")
	}
	return nil
}

func listStorageRoots(db *sql.DB) ([]map[string]any, error) {
	rows, err := db.Query(`SELECT root_id,display_name,canonical_path,allowed_mode,device_major,device_minor,inode,filesystem_type,mount_source,mount_point,network_backed FROM trusted_storage_roots ORDER BY display_name,root_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, name, path, mode, fs, source, point string
		var major, minor uint32
		var inode uint64
		var network bool
		if err = rows.Scan(&id, &name, &path, &mode, &major, &minor, &inode, &fs, &source, &point, &network); err != nil {
			return nil, err
		}
		identity := externalstorage.Identity{CanonicalPath: path, DeviceMajor: major, DeviceMinor: minor, Inode: inode, Filesystem: fs, MountSource: source, MountPoint: point, NetworkBacked: network}
		_, validationErr := validateExternalStorage(identity, path)
		result = append(result, storageRootView(id, name, mode, identity, validationErr == nil))
	}
	return result, rows.Err()
}

func storageRootView(id, name, mode string, identity externalstorage.Identity, available bool) map[string]any {
	return map[string]any{"id": id, "name": name, "path": identity.CanonicalPath, "mode": mode, "available": available, "filesystem": identity.Filesystem, "mount_source": identity.MountSource, "mount_point": identity.MountPoint, "network_backed": identity.NetworkBacked}
}

func removeStorageRoot(db *sql.DB, rootID string) error {
	if !regexp.MustCompile(`^storage-[a-f0-9]{16}$`).MatchString(rootID) {
		return fmt.Errorf("invalid storage root identity")
	}
	var uses int
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_storage_bindings WHERE root_id=?`, rootID).Scan(&uses); err != nil {
		return err
	}
	if uses != 0 {
		return fmt.Errorf("storage root is in use; detach it from applications first")
	}
	result, err := db.Exec(`DELETE FROM trusted_storage_roots WHERE root_id=?`, rootID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("storage root not found")
	}
	return nil
}

func resolveExternalMounts(db *sql.DB, installation, component string, generation int, declarations []manifest.ExternalStorage, requested []protocol.ExternalStorageBinding) ([]docker.StorageMount, error) {
	_ = generation
	if len(declarations) != len(requested) {
		return nil, fmt.Errorf("external storage selection required")
	}
	wanted := map[string]string{}
	seen := map[string]bool{}
	for _, binding := range requested {
		if binding.SlotID == "" || seen[binding.SlotID] {
			return nil, fmt.Errorf("invalid external storage selection")
		}
		seen[binding.SlotID] = true
		wanted[binding.SlotID] = binding.RootID
	}
	mounts := []docker.StorageMount{}
	for _, declaration := range declarations {
		rootID := wanted[declaration.ID]
		if rootID == "" && !declaration.Required {
			continue
		}
		if rootID == "" {
			return nil, fmt.Errorf("external storage slot %s is not selected", declaration.ID)
		}
		var name, path, allowed, fs, source, point string
		var major, minor uint32
		var inode uint64
		var network bool
		err := db.QueryRow(`SELECT display_name,canonical_path,allowed_mode,device_major,device_minor,inode,filesystem_type,mount_source,mount_point,network_backed FROM trusted_storage_roots WHERE root_id=?`, rootID).Scan(&name, &path, &allowed, &major, &minor, &inode, &fs, &source, &point, &network)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("trusted storage root not found")
		}
		if err != nil {
			return nil, err
		}
		if declaration.Mode == externalstorage.ReadWrite && allowed != externalstorage.ReadWrite {
			return nil, fmt.Errorf("trusted storage root does not allow read-write access")
		}
		var conflicts int
		if declaration.Mode == externalstorage.ReadWrite {
			err = db.QueryRow(`SELECT COUNT(*) FROM external_storage_bindings WHERE root_id=? AND installation_id<>?`, rootID, installation).Scan(&conflicts)
		} else {
			err = db.QueryRow(`SELECT COUNT(*) FROM external_storage_bindings WHERE root_id=? AND installation_id<>? AND access_mode=?`, rootID, installation, externalstorage.ReadWrite).Scan(&conflicts)
		}
		if err != nil {
			return nil, err
		}
		if conflicts != 0 {
			return nil, fmt.Errorf("storage already in use by an incompatible writer")
		}
		identity := externalstorage.Identity{CanonicalPath: path, DeviceMajor: major, DeviceMinor: minor, Inode: inode, Filesystem: fs, MountSource: source, MountPoint: point, NetworkBacked: network}
		if _, err = validateExternalStorage(identity, path); err != nil {
			return nil, fmt.Errorf("storage unavailable: %s", name)
		}
		mounts = append(mounts, docker.StorageMount{HostPath: path, ContainerPath: declaration.ContainerPath, ReadOnly: declaration.Mode == externalstorage.ReadOnly})
	}
	return mounts, nil
}

func persistExternalBindings(db sqlExecer, installation, component string, generation int, declarations []manifest.ExternalStorage, requested []protocol.ExternalStorageBinding) error {
	if _, err := db.Exec(`DELETE FROM external_storage_bindings WHERE installation_id=? AND component_id=?`, installation, component); err != nil {
		return err
	}
	bySlot := map[string]string{}
	for _, binding := range requested {
		bySlot[binding.SlotID] = binding.RootID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, declaration := range declarations {
		if bySlot[declaration.ID] == "" {
			continue
		}
		if _, err := db.Exec(`INSERT INTO external_storage_bindings(installation_id,component_id,slot_id,root_id,access_mode,container_path,runtime_generation,created_at) VALUES(?,?,?,?,?,?,?,?)`, installation, component, declaration.ID, bySlot[declaration.ID], declaration.Mode, declaration.ContainerPath, generation, now); err != nil {
			return err
		}
	}
	return nil
}

func storageAssignments(db *sql.DB, installation string) ([]map[string]any, error) {
	rows, err := db.Query(`SELECT b.component_id,b.slot_id,b.root_id,b.access_mode,b.container_path,r.display_name,r.canonical_path,r.device_major,r.device_minor,r.inode,r.filesystem_type,r.mount_source,r.mount_point,r.network_backed FROM external_storage_bindings b JOIN trusted_storage_roots r ON r.root_id=b.root_id WHERE b.installation_id=? ORDER BY b.component_id,b.slot_id`, installation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var component, slot, root, mode, target, name, path, fs, source, point string
		var major, minor uint32
		var inode uint64
		var network bool
		if err = rows.Scan(&component, &slot, &root, &mode, &target, &name, &path, &major, &minor, &inode, &fs, &source, &point, &network); err != nil {
			return nil, err
		}
		identity := externalstorage.Identity{CanonicalPath: path, DeviceMajor: major, DeviceMinor: minor, Inode: inode, Filesystem: fs, MountSource: source, MountPoint: point, NetworkBacked: network}
		_, identityErr := validateExternalStorage(identity, path)
		items = append(items, map[string]any{"component": component, "slot_id": slot, "root_id": root, "root_name": name, "mode": mode, "container_path": target, "path": path, "available": identityErr == nil})
	}
	return items, rows.Err()
}

func validateInstallationStorageAvailable(db *sql.DB, installation string) error {
	rows, err := db.Query(`SELECT DISTINCT r.canonical_path,r.device_major,r.device_minor,r.inode,r.filesystem_type,r.mount_source,r.mount_point,r.network_backed FROM external_storage_bindings b JOIN trusted_storage_roots r ON r.root_id=b.root_id WHERE b.installation_id=?`, installation)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, fs, source, point string
		var major, minor uint32
		var inode uint64
		var network bool
		if err = rows.Scan(&path, &major, &minor, &inode, &fs, &source, &point, &network); err != nil {
			return err
		}
		identity := externalstorage.Identity{CanonicalPath: path, DeviceMajor: major, DeviceMinor: minor, Inode: inode, Filesystem: fs, MountSource: source, MountPoint: point, NetworkBacked: network}
		if _, err = validateExternalStorage(identity, path); err != nil {
			return fmt.Errorf("storage unavailable")
		}
	}
	return rows.Err()
}

func stopInstallationsWithUnavailableStorage(db *sql.DB) {
	rows, err := db.Query(`SELECT DISTINCT installation_id FROM external_storage_bindings`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var installation string
		if rows.Scan(&installation) != nil || validateInstallationStorageAvailable(db, installation) == nil {
			continue
		}
		var id string
		if db.QueryRow(`SELECT container_id FROM ownership WHERE instance_id=?`, installation).Scan(&id) == nil && id != "" {
			_ = docker.New().Stop(id)
		}
		componentRows, e := db.Query(`SELECT container_id FROM component_ownership WHERE installation_id=?`, installation)
		if e == nil {
			for componentRows.Next() {
				if componentRows.Scan(&id) == nil && id != "" {
					_ = docker.New().Stop(id)
				}
			}
			_ = componentRows.Close()
		}
	}
}
