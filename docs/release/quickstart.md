# KITPro Server public alpha quickstart

KITPro Server `v0.1.0-alpha.12` is active alpha software. Breaking changes and
incomplete workflows may occur. Review the [current state](../product/kitpro-server-current-state.md)
and [known limitations](known-limitations.md) before using it with important
data.

Download the package and matching checksum file from the
[`v0.1.0-alpha.12` release](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.12).
Verify the package before installation.

If this host runs alpha.11, do not use the fresh-install commands below. Use
the guarded alpha.11 to alpha.12 transition for
[Debian](../upgrade-uninstall-debian-package.md#upgrade-from-alpha11-to-alpha12)
or [Arch Linux](../install-arch-package.md#upgrade-from-alpha11-to-alpha12).
Raw `apt install`, `dpkg -i`, and `pacman -U` transitions from alpha.11 are
unsupported because they bypass KITPro's application-level safety checks.

## Debian 13

Requirements: amd64, rootful Docker, systemd, and an enforcing AppArmor kernel/userspace setup.

```sh
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha12_amd64.deb
sudo systemctl status kitpro-api kitpro-helper
```

The package is named `kitpro-server`. If Docker is absent, review and run `tools/install-docker.sh` or install Docker using the operating-system policy before installing KITPro.

## Arch Linux

Requirements: x86_64, a fully updated system using official repositories, `linux-lts`, rootful Docker, systemd, and enforcing AppArmor. Partial upgrades are unsupported.

```sh
sudo pacman -Syu
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
sudo pacman -U ./kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst
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

Ubuntu 26.04 has development validation evidence but is not in the current public support baseline. Rocky Linux and Podman remain Experimental.

See the [Debian guide](../install-debian-package.md), [Arch guide](../install-arch-package.md),
[backup and restore guide](../operations/application-backup-restore.md),
[lifecycle recovery guide](../operations/lifecycle-recovery.md),
[support matrix](../support-matrix.md), and [known limitations](known-limitations.md).
