# Install KITPro Server on Debian 13

KITPro Server's current public `.deb` baseline supports Debian 13 on amd64.
Ubuntu Server 26.04 LTS has development validation evidence and uses the same
`.deb` format, but it is not currently part of the public support baseline.
Rocky Linux 10 remains Experimental.
Use the filename and checksum from the [current public release](https://github.com/KITProHQ/kitpro/releases).

> KITPro Server is active alpha software. Breaking changes and incomplete
> workflows may occur. Review the [alpha.12 current state](product/kitpro-server-current-state.md)
> and [known limitations](release/known-limitations.md) before using it with
> important data.

## Prerequisites

- Debian 13 on amd64, with systemd and AppArmor enabled
- a compatible rootful Docker Engine that is running and exposes its local Unix
  socket
- an explicit, operator-selected Docker default address pool that passes the
  [KITPro address-pool prerequisite](docker-address-pool-prerequisite.md)
- root or sudo access for package installation

The package deliberately does not depend on a distribution-specific Docker
package. Debian's `docker.io` and Docker Inc.'s `docker-ce` can both provide a
compatible engine. KITPro verifies that Docker is active and that its network
allocator has sufficient non-overlapping capacity before package activation.
It never installs Docker, rewrites `/etc/docker/daemon.json`, or adds
`kitpro-api` to the `docker` group.

## Install

These commands are for a fresh alpha.12 installation. If this host runs
alpha.11, use the [guarded alpha.11 to alpha.12 transition](upgrade-uninstall-debian-package.md#upgrade-from-alpha11-to-alpha12).
Raw `apt install` and `dpkg -i` transitions from alpha.11 are unsupported.

Verify the adjacent checksum, then install the local artifact:

```sh
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha12_amd64.deb
```

Installation creates the `kitpro-api` system account, loads the helper's
AppArmor profile, creates state directories through tmpfiles, initializes both
SQLite databases through the production migration code, and enables the helper
socket and API service. The root helper resolves the packaged API account to a
numeric UID at startup and verifies it with `SO_PEERCRED`; the account is not a
member of the Docker group.

The API listens only on `127.0.0.1:8080` by default. From an administrator
workstation, create an SSH tunnel:

```sh
ssh -L 8080:127.0.0.1:8080 server.example
```

Then open `http://127.0.0.1:8080/setup` and create the first administrator.
The package does not enable public or LAN access to the KITPro control plane.

Configuration is in `/etc/default/kitpro-server`. Set
`KITPRO_LAN_BIND_ADDRESS` only to a non-wildcard LAN address assigned to the
server when catalog application LAN exposure is required.

## Filesystem layout

| Path | Owner | Purpose | Package removal |
| --- | --- | --- | --- |
| `/usr/bin/kitpro-api` | root | Unprivileged control-plane binary | Removed |
| `/usr/libexec/kitpro-helper` | root | Root helper binary | Removed |
| `/etc/default/kitpro-server` | root | Administrator configuration | Kept on remove; removed on purge |
| `/etc/apparmor.d/usr.libexec.kitpro-helper` | root | Helper policy | Removed and unloaded |
| `/usr/share/kitpro-server/apparmor/` | root | Immutable reinstall copy of helper policy | Removed |
| `/usr/lib/systemd/system/kitpro-*` | root | Services and socket | Removed |
| `/usr/lib/tmpfiles.d/kitpro.conf` | root | Runtime/state directory policy | Removed |
| `/var/lib/kitpro-api` | kitpro-api | Control DB and backups | Always preserved |
| `/var/lib/kitpro-helper` | root | Trusted helper DB and backups | Always preserved |
| `/run/kitpro` | root:kitpro-api | Runtime socket directory | Recreated at boot/install |
| `/srv/kitpro/apps` | root | Persistent application data | Always preserved |

## Troubleshooting

If installation reports that Docker is unavailable, verify both the service
and Unix socket before retrying:

```sh
systemctl status docker.service
test -S /run/docker.sock
sudo /usr/libexec/kitpro-helper --verify-host-prerequisites
```

An address-pool failure is actionable host configuration, not permission to
remove KITPro generation networks or merge application networks. Select a pool
for this environment, preserve unrelated Docker settings, validate the JSON,
and restart Docker. The new pool applies only to networks created afterward.

If package configuration fails, inspect `journalctl -u kitpro-api -u
kitpro-helper` and AppArmor denials. Do not place the API user in the Docker
group and do not switch the helper profile to complain mode. Fix the narrow
package or policy rule instead.

Check embedded build metadata with:

```sh
/usr/bin/kitpro-api --version
/usr/libexec/kitpro-helper --version
```

## Ubuntu development-evidence note

Ubuntu 26.04 validation used the same package, Docker Engine 29.8.0, and an
inactive UFW policy. Exact-address loopback and LAN publications produced
exact-address Docker nftables DNAT rules and no wildcard binding. Docker warns
that published container traffic can bypass UFW's normal INPUT/OUTPUT chains,
so administrators must not treat UFW alone as the policy boundary for a
KITPro-published application. KITPro's Phase 1 boundary is the exact bind
address and assigned port; hosts with custom UFW or nftables policy require a
separate compatibility check before enabling LAN exposure. This evidence does
not make Ubuntu a supported public platform.
