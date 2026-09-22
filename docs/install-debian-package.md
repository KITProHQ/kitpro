# Install KITPro Server on Debian 13

KITPro Server supports Debian 13 on amd64. Rocky Linux 10 and Podman remain
Experimental. Download the package and checksum file from the
[v0.1.0-alpha.12 prerelease](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.12).

## Prerequisites

- Debian 13 or Ubuntu Server 26.04 LTS on amd64, with systemd and AppArmor enabled
- a compatible rootful Docker Engine that is running and exposes its local Unix
  socket
- root or sudo access for package installation

The package deliberately does not depend on a distribution-specific Docker
package. Debian's `docker.io` and Docker Inc.'s `docker-ce` can both provide a
compatible engine. KITPro verifies that Docker is active before installation;
it never installs Docker or adds `kitpro-api` to the `docker` group.

## Fresh alpha.12 install

These commands are for a new installation. Existing alpha.11 users must use
the guarded upgrade path in the next section.

```sh
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-server_0.1.0.alpha12_amd64.deb
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-alpha12-SHA256SUMS
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha12_amd64.deb
```

GitHub exposes `0.1.0.alpha12` in the filename. The installed Debian package
version is `0.1.0~alpha12`.

## Upgrade from alpha.11

Do not use raw `apt install` or `dpkg -i` for this transition. Download the
package-bound wrapper and the alpha.12 package, verify both, then run:

```sh
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-debian-upgrade
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-server_0.1.0.alpha12_amd64.deb
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-alpha12-SHA256SUMS
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
chmod +x kitpro-debian-upgrade
sudo ./kitpro-debian-upgrade ./kitpro-server_0.1.0.alpha12_amd64.deb
```

The wrapper validates the existing state, creates and verifies the complete
backup set, checks the exact package hash and metadata, and invokes APT only
after preflight succeeds. Alpha.11 schema 7 to alpha.12 schema 13 migration is
supported and validated. Downgrading after migration is unsupported.

## What installation changes

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
```

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

Ubuntu 26.04 certification used the same package, Docker Engine 29.8.0, and an
inactive UFW policy. Exact-address loopback and LAN publications produced
exact-address Docker nftables DNAT rules and no wildcard binding. Docker warns
that published container traffic can bypass UFW's normal INPUT/OUTPUT chains,
so administrators must not treat UFW alone as the policy boundary for a
KITPro-published application. KITPro's Phase 1 boundary is the exact bind
address and assigned port; hosts with custom UFW or nftables policy require a
separate compatibility check before enabling LAN exposure. This evidence does
not make Ubuntu part of the current public support baseline.
