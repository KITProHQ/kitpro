# Trusted external storage architecture

## Boundary

KITPro applications never submit Docker bind strings or host paths. A local
administrator registers an existing directory as a trusted root. Application
manifests declare logical storage slots, and an installation binds a slot to a
trusted-root ID. The privileged helper resolves the ID to the recorded path and
constructs the exact Docker mount.

Managed application data remains under
`/srv/kitpro/apps/<application>/<installation>/`. Imported data remains owned
by the administrator. Runtime recreation, removal, package upgrade, and KITPro
uninstall do not delete imported data.

## Root registration and identity

Registration requires an absolute, existing directory and an explicit
`read-only` or `read-write` ceiling. The helper resolves symbolic links and
rejects any path whose canonical form differs from the submitted path. It
rejects traversal, container-runtime sockets, KITPro state, and protected host
trees including `/`, `/boot`, `/dev`, `/etc`, `/home`, `/proc`, `/root`,
`/run`, `/sys`, and `/usr`. Registration is positively limited to dedicated
children of `/mnt`, `/media`, `/data`, or `/srv`; the top-level parent itself
cannot be registered.

The helper records the canonical path, root inode, device major/minor,
filesystem type, mount source, mount point, and whether the filesystem is
network-backed. Every create, recreate, start, inventory, and reconciliation
path rechecks that identity. A changed or missing identity is unavailable; it
is never replaced by an automatically created directory.

## Access and mount rules

- A read-only slot always becomes an exact `:ro` Docker bind.
- A read-write slot is accepted only when both the manifest and trusted root
  permit writes.
- Container targets come from trusted manifests and cannot be supplied by the
  browser or API request.
- Mount propagation, arbitrary flags, relative paths, and raw bind syntax are
  not representable.
- Only the component declaring a slot receives the mount.

The helper's AppArmor profile can inspect directory metadata under common data
trees. It cannot read imported files. Docker performs the bind after helper
validation.

## Reconciliation and restart

Helper ownership records bind installation, component, slot, root, mode,
container target, and runtime generation. Reconciliation compares this state
with current root identity and Docker `HostConfig.Binds`. Missing, extra,
retargeted, or writable-instead-of-read-only mounts are security drift.

At helper startup, installations with unavailable imported storage are stopped.
An explicit start also fails before Docker starts the container. Recreation
resolves storage before replacing the old generation, so a missing NAS cannot
silently become an empty local directory.

KITPro does not mount NFS or SMB shares. Administrators mount them with normal
operating-system tools and then register the mounted directory. Restore carries
root definitions and bindings but never assumes that a different host has the
same storage identity.

## Threat review

| Threat | Mitigation |
| --- | --- |
| Malicious manifest requests `/etc` | Schema has no host-path field; helper resolves only registered IDs. |
| Compromised API sends a bind string | Protocol has only slot and root IDs; helper reloads the trusted manifest. |
| Symlink or traversal escape | Canonical equality and segment validation fail closed. |
| Missing NAS exposes local directory | Filesystem and mount identity mismatch blocks start/recreate. |
| Read-only becomes writable | Reconciliation compares the exact `:ro` bind. |
| Extra bind is injected | Exact bind-set comparison reports security drift. |
| Root is deleted while used | Foreign-key ownership and the removal operation reject it. |
| Restore moves to another host | Identity revalidation marks the root unavailable until reassociated. |
