# Alpha.15 controlled storage management

## Scope

Alpha.15 permits temporary mount, normal unmount, and transactional persistence operations for exact resources in a root-owned policy. It does not format, partition, resize, repair, force-unmount, lazy-unmount, edit `/etc/fstab`, or mutate LVM, ZFS, RAID, Docker storage, or application volumes.

The API and helper remain cgo-disabled. The helper does not run a shell or accept command arguments. Host-visible changes use typed transient systemd `.mount` units over the existing peer-authenticated system D-Bus connection. The helper retains its existing capability set.

## Policy

Storage management is disabled unless both services receive `KITPRO_HOST_STORAGE_MANAGEMENT_ENABLED=1` and `KITPRO_HOST_STORAGE_POLICY=/etc/kitpro/storage-management.json`.

The policy file must be a regular root-owned file with no group or world write bits. It is bounded to 64 exact resources and 64 KiB. Unknown fields, unknown actions, wildcard identifiers, relative targets, unstable block-device names, duplicate actions, and unreviewed mount options invalidate the complete policy.

Schema 1 example for the disposable qualification fixture:

```json
{
  "version": 1,
  "resources": [
    {
      "id": "qualification-tmpfs",
      "source": "tmpfs",
      "target": "/mnt/kitpro-qualification",
      "filesystem": "tmpfs",
      "options": ["nodev", "nosuid", "noexec"],
      "actions": ["mount", "unmount"]
    }
  ]
}
```

Block filesystems require an exact `/dev/disk/by-uuid/<uuid>` source and matching `uuid` field. Alpha.15 accepts only `tmpfs`, `ext4`, `xfs`, and `btrfs`, and only `ro`, `nodev`, `nosuid`, and `noexec` flags. KITPro adds the fixed `mode=0755` option to tmpfs mounts so the mounted root retains the trusted target ownership and write policy required for post-operation verification. The policy is operator-managed outside the HTTP API.

Targets must be root-owned, non-writable directories below `/mnt`, `/media`, `/data`, or `/srv`. Protected descendants such as `/srv/kitpro` remain denied. The helper also rejects targets that overlap any registered KITPro application storage root.

## Safety evaluation

The API provides a read-only preview and repeats the decision immediately before dispatch. The helper reloads the policy and independently repeats the same checks before calling systemd.

Checks include:

- exact resource identifier, source, UUID, target, filesystem, action, and flags;
- canonical target with no symlink resolution or path traversal;
- root, boot, EFI, KITPro state, Docker, containerd, runtime, kernel, and device path protection;
- empty target before mount;
- exact active mount identity before unmount;
- proof that an unmount target is a transient mount unit originally created by KITPro;
- denial for registered imported application storage and protected managed application storage;
- conflicting, descendant, and changed mounts;
- scoped `host.storage.manage` authorization and exact confirmation;
- per-resource serialization, bounded D-Bus execution, and resulting kernel mount-state verification.

Uncertain identity, policy, dependencies, audit persistence, or resulting state fails closed. Busy filesystems remain mounted. KITPro never kills processes, stops services, retries an uncertain operation, or applies an inverse operation automatically.

## API and audit

- `GET /api/v1/host/storage` includes bounded `management` previews when enabled.
- `GET /api/v1/host/storage/{id}/actions/{action}/preview` evaluates current eligibility.
- `POST /api/v1/host/storage/{id}/actions` requires `host.storage.manage`, CSRF, and exact resource confirmation.
- `GET /api/v1/host/storage/{id}/history` requires `host.audit.read` and supports bounded action, result, time, limit, and offset filters.

Audit records contain the operation ID, administrator, exact resource/source/target, authorization and policy outcomes, previous and resulting state, status, and bounded failure reason. They do not contain credentials, file contents, journal entries, or caller-supplied paths.

## Persistent mounts

`GET /api/v1/host/storage` retains its bounded read-only `/etc/fstab` view. KITPro never rewrites that file. Persistence uses one dedicated systemd `.mount` unit whose filename is derived from the exact approved target and one exact `multi-user.target.wants` link. Both carry content generated solely from validated policy fields. Existing administrator-managed units, aliases, unexpected links, and changed content are conflicts and are never replaced or deleted.

Persistent actions require a non-`tmpfs` resource with an exact `/dev/disk/by-uuid/<uuid>` identity. The UUID link must resolve to one block device at preview and dispatch. The filesystem type, target, options, source, and resource identifier remain the same exact allowlist fields used by transient management. Persistence cannot be used while identity is absent, ambiguous, protected, nonempty, or occupied by a different mount.

The helper publishes a root-owned recovery record before configuration. It writes the unit to a same-directory private temporary file, flushes it, atomically renames with no replacement, creates the activation link with no replacement, flushes both directories, reloads systemd, starts the unit, and verifies the final operation state. The unit is an optional `Wants` dependency with no default local-filesystem ordering, a 15-second job timeout, and no failure action. An absent optional device therefore cannot become an unbounded boot dependency.

Disable and recovery compare the current unit content with the recorded SHA-256 and revalidate the activation link before removal. A changed unit fails closed. Normal unmount is used; busy mounts remain mounted. Partial publication, reload failure, activation failure, audit failure, and state mismatch remain `needs_recovery` or `outcome_unknown`. KITPro never retries an uncertain mount operation or overwrites a concurrent administrator change.

Typed actions are `enable_persistence`, `disable_persistence`, and `recover_persistence`. They use the same authenticated preview, `host.storage.manage`, CSRF, exact-resource confirmation, helper peer verification, per-resource serialization, and bounded audit history as transient operations. The API can inspect only exact generated unit and link state; the private helper record is not exposed through HTTP.

The helper confinement permits writes only through its typed code path. The systemd sandbox exposes `/etc/systemd/system` read-write to the helper, while AppArmor restricts it to `.mount` units, the matching wants links, and private atomic temporary names. The helper remains unable to accept arbitrary unit text, paths, options, commands, or D-Bus methods.

The temporary transient path and persistent path are mutually exclusive for the same resource. An active or incomplete persistent configuration must be disabled or recovered through the persistence actions before transient mounting is considered.

System Health observes only configured or incomplete KITPro-managed persistent resources. A healthy mounted resource reports healthy. An enabled optional resource whose exact UUID source or mount is absent reports a storage warning and does not by itself make the platform critical. Incomplete publication reports degraded and directs the administrator to the explicit recovery action. Disabled resources do not create health noise.

## Qualification boundary

The approved tmpfs fixture proves the transient path. Persistent qualification uses a separate small ext4 loopback image with a known UUID and exact test-only target. That fixture can prove atomic publication, reboot persistence, missing-device boot behavior, rollback, authorization, audit, and cleanup. It does not establish that arbitrary physical filesystems, device-mapper topology, network filesystems, or workloads are safe to mutate.
