# KITPro Server public alpha quickstart

KITPro Server `v0.1.0-alpha.13` is active alpha software. Breaking changes and
incomplete workflows may occur. Review the [current state](../product/kitpro-server-current-state.md)
and [known limitations](known-limitations.md) before using it with important
data.

After publication, download the package and matching checksum file from the
`v0.1.0-alpha.13` GitHub release. Until then, alpha.13 artifacts are release
candidates and must not be presented as published downloads.

If this host runs alpha.12, use the documented alpha.13 upgrade path for
[Debian or Ubuntu](../upgrade-uninstall-debian-package.md#upgrade-from-alpha12-to-alpha13)
or [Arch Linux](../install-arch-package.md#upgrade-from-alpha12-to-alpha13).

## Debian 13

Requirements: amd64, rootful Docker, systemd, enforcing AppArmor, and a Docker
address pool that passes the [Supported-host prerequisite](../docker-address-pool-prerequisite.md).

```sh
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha13_amd64.deb
sudo systemctl status kitpro-api kitpro-helper
```

The package is named `kitpro-server`. If Docker is absent, review and run
`tools/install-docker.sh` or install Docker through the operating-system
policy before installing KITPro.

## Ubuntu 26.04 LTS

Ubuntu 26.04 LTS is Supported with the same qualified `.deb` and prerequisites
as Debian. Custom UFW or nftables policies require a separate compatibility
review because Docker-published ports do not rely only on the normal UFW
INPUT and OUTPUT paths.

```sh
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha13_amd64.deb
sudo systemctl status kitpro-api kitpro-helper
```

## Arch Linux

Requirements: x86_64, a fully updated system using official repositories,
`linux-lts`, rootful Docker, systemd, enforcing AppArmor, and the documented
Docker address pool. Partial upgrades are unsupported.

```sh
sudo pacman -Syu
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo pacman -U ./kitpro-server-0.1.0_alpha13-1-x86_64.pkg.tar.zst
sudo systemctl status kitpro-api kitpro-helper
```

Reboot into `linux-lts` after installing or changing the kernel and AppArmor
boundary before expecting the helper's platform checks to pass.

## First run

1. From the server itself, open `http://127.0.0.1:8080/`.
2. Create the first local administrator. KITPro has no default password.
3. Review Settings for platform, helper, hardware, and trusted-storage status.
4. Choose an application from Catalog. Apps start Private.
5. For a media/file app, register an administrator-approved storage root first and choose the compatible read-only or read-write slot.
6. Change a declared service to This server only or Local network only when needed. LAN exposure binds the configured address, never a wildcard.

Managed data lives under `/srv/kitpro/apps/<application>/<installation>/` and survives runtime recreation. Imported data stays at the administrator-approved root and is not deleted or backed up by KITPro. Package migrations and trusted app updates protect control state, not the complete external library.

Rocky Linux and Podman remain Experimental. The alpha.13 native package and
host checks pass, but application installation is unavailable on the Podman
path. See the [Rocky notes](../install-rocky-linux.md).

See the [Debian guide](../install-debian-package.md), [Arch guide](../install-arch-package.md),
[backup and restore guide](../operations/application-backup-restore.md),
[lifecycle recovery guide](../operations/lifecycle-recovery.md),
[support matrix](../support-matrix.md), and [known limitations](known-limitations.md).
