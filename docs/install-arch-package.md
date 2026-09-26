# Install KITPro Server on Arch Linux

KITPro supports fully updated Arch Linux amd64 hosts that use official
repositories and the `linux-lts` kernel. Partial system upgrades and AUR
replacements for the kernel, AppArmor, Docker, containerd, or systemd are not
supported.

> KITPro Server is active alpha software. Breaking changes and incomplete
> workflows may occur. Review the [current state](product/kitpro-server-current-state.md)
> and [known limitations](release/known-limitations.md) before using it with
> important data.

## Prerequisites

1. Read current [Arch Linux news](https://archlinux.org/news/) for manual
   intervention notices that apply to the host.
2. Fully update the host with `sudo pacman -Syu` and reboot when the kernel
   changes.
3. Install and enable AppArmor. The kernel command line must include
   `lsm=landlock,lockdown,yama,integrity,apparmor,bpf`, and
   `apparmor.service` must be enabled. Reboot, then require both `aa-enabled`
   and `sudo aa-status` to report enforcement.
4. Install Docker with `sudo ./tools/install-docker.sh --yes`. The installer
   uses only Arch official repository packages and does not grant the invoking
   user Docker group membership.
5. Configure an explicit, operator-selected Docker default address pool that
   passes the [KITPro address-pool prerequisite](docker-address-pool-prerequisite.md).
   Preserve unrelated daemon settings, validate the complete JSON, and restart
   Docker before installing KITPro.

## Install

These commands are for a fresh alpha.13 installation.

Builds use `makepkg` as a non-root account. Install the resulting package with:

```sh
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo pacman -U ./kitpro-server-0.1.0_alpha13-1-x86_64.pkg.tar.zst
```

The transaction fails closed unless Docker is active, its address-pool
configuration has sufficient non-overlapping capacity, and AppArmor is
enabled. KITPro does not rewrite Docker daemon configuration. Verify the host
at any time with `sudo /usr/libexec/kitpro-helper
--verify-host-prerequisites`.
The API listens on `127.0.0.1:8080` by default. Use an SSH tunnel and open
`http://127.0.0.1:8080/setup` to create the first local administrator.

## Upgrade from alpha.12 to alpha.13

Verify the alpha.13 package and its package-bound upgrade wrapper, then run:

```sh
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo ./kitpro-server-0.1.0_alpha13-1-upgrade.sh \
  ./kitpro-server-0.1.0_alpha13-1-x86_64.pkg.tar.zst
```

Do not use raw `pacman -U` for the alpha.12-to-alpha.13 upgrade. Alpha.13 adds
a narrowly scoped AppArmor permission used to atomically publish verified
upgrade backups. The package-bound wrapper validates the package identity,
validates and loads its incoming AppArmor profile, and then lets alpha.12's
installed pre-transaction hook verify both databases and create WAL-safe
backups before pacman may modify package files. If the transaction fails, the
wrapper restores the installed profile. KITPro does not attempt to repair
corrupt databases during upgrade.

Fresh installs may continue to use `pacman -U` directly.

If preflight reports a corrupt or unreadable database, do not delete, replace,
or attempt to repair it during the package transaction. Preserve the reported
database path, confirm that the installed package version has not changed with
`pacman -Q kitpro-server`, and report the failure with the package version and
redacted pacman output. Database contents can include sensitive application
metadata and should not be attached to a public issue.

## Configuration

Arch package configuration is `/etc/conf.d/kitpro-server`. Set
`KITPRO_LAN_BIND_ADDRESS` to one address actually assigned to the host before
enabling LAN exposure for an application. After changing this value while no
application mutation is active, restart both services so the API and
privileged helper enforce the same address:

```sh
sudo systemctl restart kitpro-helper.service
sudo systemctl restart kitpro-api.service
```

KITPro never publishes the control plane or application services to wildcard
addresses.

## Later updates and removal

Always perform full Arch updates with `pacman -Syu`; partial upgrades are not
supported. Use a package-bound KITPro upgrade wrapper when one is supplied for
a release transition. The installed pacman hook backs up and validates
control/helper state before package files change, and the package migrates that
state before services restart. Downgrades are unsupported after schema
migration.

`pacman -R kitpro-server` removes package-owned binaries, units, and the
AppArmor profile. It deliberately retains `/etc/conf.d/kitpro-server`, the
`kitpro-api` service identity, control/helper databases, backups, logs, and
`/srv/kitpro`, including application data. Arch has no Debian-style purge
phase. Deleting installed application data remains a separate future
destructive operation.

## Platform note

Arch is a rolling release, so KITPro certification follows the current official
repositories rather than a numbered Arch release. `linux-lts` reduces kernel
churn but does not replace the requirement to read Arch news and apply complete
system upgrades.
