# Install KITPro Server on Arch Linux

KITPro supports fully updated Arch Linux amd64 hosts that use official
repositories and the `linux-lts` kernel. Partial system upgrades and AUR
replacements for the kernel, AppArmor, Docker, containerd, or systemd are not
supported.

> KITPro Server is active alpha software. Breaking changes and incomplete
> workflows may occur. Review the [alpha.12 current state](product/kitpro-server-current-state.md)
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

## Install

These commands are for a fresh alpha.12 installation. An existing alpha.11
host must use the Arch transition wrapper supplied with the alpha.12 release.
A raw `pacman -U` transition from alpha.11 is unsupported because it bypasses
KITPro's application-level safety checks.

Builds use `makepkg` as a non-root account. Install the resulting package with:

```sh
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
sudo pacman -U ./kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst
```

The transaction fails closed unless Docker is active and AppArmor is enabled.
The API listens on `127.0.0.1:8080` by default. Use an SSH tunnel and open
`http://127.0.0.1:8080/setup` to create the first local administrator.

## Upgrade from alpha.11 to alpha.12

The alpha.11 package does not contain the pacman pre-transaction hook needed to
stop an unsafe upgrade before package files are changed. For this one bootstrap
transition, verify both the alpha.12 package and its upgrade wrapper, then run:

```sh
sha256sum -c SHA256SUMS --ignore-missing
sudo ./kitpro-server-0.1.0_alpha12-1-upgrade.sh \
  ./kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst
```

Do not use raw `pacman -U` for the alpha.11-to-alpha.12 upgrade. The wrapper
extracts the incoming maintenance binaries, installs a temporary pacman
pre-transaction hook, verifies both existing databases, and creates verified
WAL-safe backups before pacman may modify package files. If validation fails,
the transaction is aborted and the existing package and databases remain
unchanged. KITPro does not attempt to repair corrupt databases during upgrade.

Alpha.12 installs the same fail-closed gate permanently for future package
upgrades. Fresh installs may continue to use `pacman -U` directly.

If preflight reports a corrupt or unreadable database, do not delete, replace,
or attempt to repair it during the package transaction. Preserve the reported
database path, confirm that the installed package version has not changed with
`pacman -Q kitpro-server`, and report the failure with the package version and
redacted pacman output. Database contents can include sensitive application
metadata and should not be attached to a public issue.

## Configuration

Arch package configuration is `/etc/conf.d/kitpro-server`. Set
`KITPRO_LAN_BIND_ADDRESS` to one address actually assigned to the host before
enabling LAN exposure for an application. KITPro never publishes the control
plane or application services to wildcard addresses.

## Upgrade from alpha.11 to alpha.12

A package manager cannot determine whether KITPro's control and helper state
form a recoverable ownership record. The supported wrapper validates both
state databases, creates and verifies the required backups, binds approval to
the expected package, and only then permits the package transaction.

Raw `pacman -U` transitions from alpha.11 are unsupported. They bypass
KITPro's application-level safety checks. This restriction does not apply to a
fresh alpha.12 installation.

```sh
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-server-0.1.0_alpha12-1-upgrade.sh
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-alpha12-SHA256SUMS
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
chmod +x kitpro-server-0.1.0_alpha12-1-upgrade.sh
sudo ./kitpro-server-0.1.0_alpha12-1-upgrade.sh \
  ./kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst
```

## Later updates and removal

Always perform full Arch updates with `pacman -Syu`; partial upgrades are not
supported. After the alpha.12 bootstrap transition, the installed pacman hook
backs up and validates control/helper state before package files change, and
the package migrates that state before services restart. Downgrades are
unsupported after schema migration.

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
