# Alpha.15 read-only storage inventory

Status: Implemented for supported Debian and Arch-family systems. Platform support remains capability-aware and Rocky Linux remains Experimental.

## Scope and provider architecture

Storage Inventory is part of the existing `hostadmin` subsystem and uses the existing authenticated KITPro API and interface. It does not add a daemon, helper method, storage mutation, or privileged process. `GET /api/v1/host/storage` returns one bounded snapshot containing a summary, capability states, block devices, mounted filesystems, and optional LVM and ZFS inventory.

The required provider combines three read-only sources:

- a fixed `/usr/bin/lsblk --json --bytes --paths` invocation from `util-linux` for the block-device graph and udev metadata;
- bounded `/proc/1/mountinfo` and `/proc/swaps` reads for the host mount namespace and swap protection; and
- `statfs` for capacity and utilization of each mounted filesystem.

The API service has a private systemd mount namespace because of its sandbox. Reading its own mount table would incorrectly report `ProtectSystem` remounts as host storage, so the provider reads PID 1's fixed mountinfo path. If that host table is not readable, the required collector reports unavailable rather than substituting sandbox state. The adapter does not invoke a shell. Its executable and argument list are fixed in the binary, its environment is reduced to locale and a fixed path, command output is limited to 4 MiB, the API request deadline is four seconds, and device and mount inventories are limited to 4,096 records each. No request value becomes a command argument or host path. `util-linux` is an explicit Debian, Arch, and RPM runtime dependency.

LVM and ZFS are optional providers. LVM uses fixed `lvs` JSON output. ZFS uses fixed tab-separated `zpool list` and `zfs list` fields. The provider checks only the standard `/usr/bin` and `/usr/sbin` locations, keeps the same output and deadline limits, and reports `unsupported`, `permission_denied`, `unavailable`, or `failed` instead of broadening the API service's privileges. Optional-provider failure does not hide the required generic inventory.

## API response

The endpoint accepts no query parameters. The existing administrator session and host-origin guard are required, and responses use `Cache-Control: no-store`.

`summary` aggregates unique mounted local persistent filesystems. It excludes bind duplicates, overlay and pseudo filesystems, memory-backed filesystems, and network filesystems. This scope prevents device-mapper layers and repeated mounts from inflating capacity. `used_bytes` is `total - free`, while `available_bytes` uses the filesystem's unprivileged available-block count; these can differ because of reserved blocks.

`devices` reports a bounded parent-child graph with category, device type, path, model/vendor, capacity, transport, rotational state, read-only/removable state, partition count, filesystem metadata, mounts, and protection markers. Categories distinguish physical disks, virtual disks, partitions, logical devices, loops, and other kernel block devices. VirtIO, QEMU, Xen-style, and zram disks are classified as virtual. Device IDs prefer a filesystem UUID and otherwise use the current kernel major/minor identity or path. IDs without a UUID can change after reboot or device replacement and must be revalidated before any future operation.

`filesystems` reports every valid mountinfo entry, including bind, overlay, tmpfs, network, and device-less filesystems. Capacity comes from the mounted filesystem, not partition size. Each entry identifies its class, mount root and point, source, filesystem type, UUID/label where correlated, read-only state, protection markers, capacity, usage, warnings, and collection status. A mount that disappears during collection remains visible with an `unavailable` capacity state.

`capabilities` includes required block-device/filesystem collectors, optional LVM and ZFS providers, and device-health status. Device presence is never interpreted as health. SMART and similar health data remain unsupported until a separately authorized provider can preserve the API's unprivileged boundary.

## Filesystem capability matrix

| Technology | Alpha.15 behavior | Limitation |
| --- | --- | --- |
| ext4 | Inventory, mounts, capacity, utilization | No filesystem operations |
| XFS | Inventory, mounts, capacity, utilization | No filesystem operations |
| Btrfs | Generic inventory, subvolume mount visibility, capacity | No subvolume or snapshot enumeration |
| ZFS | Optional pool and dataset inventory when the fixed tools are installed and readable | No pool, dataset, snapshot, or key operations |
| LVM | Optional logical-volume inventory when the fixed tool is installed and readable | No volume-group detail or mutations |
| NFS/CIFS/SSHFS/9p/Ceph/Gluster | Mount and capacity visibility, classified as network storage | Excluded from local capacity totals |
| tmpfs/ramfs/devtmpfs | Mount and capacity visibility, classified as memory-backed | Excluded from local capacity totals |
| Overlay and kernel pseudo filesystems | Mount visibility, classified as virtual | Excluded from local capacity totals |
| Other kernel filesystems | Generic mount and capacity inventory when readable | Technology-specific features are not claimed |

## Warnings and protected resources

Filesystem usage from 85 percent through 94.99 percent produces `capacity_warning`; 95 percent or higher produces `capacity_critical`. A failed capacity read produces `capacity_unavailable` for persistent, virtual, and network mounts. Memory-backed mounts retain an explicit unavailable status without inflating the persistent-storage warning total. A read-only persistent or network mount produces `mounted_read_only`; expected read-only kernel pseudo and memory-backed mounts retain their read-only field without inflating the warning count. These indicators describe observed conditions and do not assert device health.

Protection markers identify root, boot/EFI, swap, KITPro state, and Docker/containerd storage. KITPro state includes the fixed API/helper state paths and `/srv/kitpro`, which contains active application data. The markers are advisory inputs for future design; this milestone exposes no mount, format, partition, resize, LVM, ZFS, RAID, wipe, SMART-test, ownership, or permission action.

## Interface and safety

The Storage page shows scoped capacity totals, warnings, capability state, searchable device and filesystem cards, expandable device details, and optional LVM/ZFS rows. All host-provided strings are inserted through `textContent`. There are no mutation controls.

The installed API retains `NoNewPrivileges`, an empty capability set, `PrivateDevices`, and its existing systemd sandbox. Generic inventory is designed to work inside that identity. An optional provider blocked by the sandbox remains unavailable or permission denied. Future storage operations require a separate versioned helper protocol, exact stable resource resolution, protected-resource denial, dependency checks, configuration backup, staged validation, rollback, audit receipts, and disposable-host failure qualification.

## Known limitations

- Device IDs based on kernel major/minor numbers or paths are session identities, not permanent hardware identities.
- Bind-mount detection comes from mount roots. Btrfs and ZFS non-root mount roots are treated as subvolume/dataset mounts rather than bind mounts.
- Aggregate totals describe mounted local persistent filesystems, not raw disk capacity or thin-provisioned physical allocation.
- SMART, RAID health, encryption state, Btrfs subvolumes, ZFS snapshots, and complete LVM topology are deferred.
- Mounts and devices can change during collection. The response is a bounded snapshot with per-entry unavailable states where possible.
