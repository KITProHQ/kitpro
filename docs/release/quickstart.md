# KITPro Server public alpha quickstart

Download the package and matching `SHA256SUMS` from the [current GitHub release](https://github.com/KITProHQ/kitpro/releases). Use the version and filename from that release; development release notes do not mean a package was published.

## Debian 13

Requirements: amd64, rootful Docker, systemd, and an enforcing AppArmor kernel/userspace setup.

```sh
sha256sum -c SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_VERSION_amd64.deb
sudo systemctl status kitpro-api kitpro-helper
```

The package is named `kitpro-server`. If Docker is absent, review and run `tools/install-docker.sh` or install Docker using the operating-system policy before installing KITPro.

## Arch Linux

Requirements: x86_64, a fully updated system using official repositories, `linux-lts`, rootful Docker, systemd, and enforcing AppArmor. Partial upgrades are unsupported.

```sh
sudo pacman -Syu
sha256sum -c SHA256SUMS --ignore-missing
sudo pacman -U ./kitpro-server-VERSION-1-x86_64.pkg.tar.zst
sudo systemctl status kitpro-api kitpro-helper
```

Reboot into `linux-lts` after installing or changing the kernel/AppArmor boundary before expecting the helper to pass readiness checks.

## First run

1. From the server itself, open `http://127.0.0.1:8080/`.
2. Create the first local administrator. KITPro has no default password.
3. Review Settings for platform, helper, hardware, and trusted-storage status.
4. Choose an application from Catalog. Apps start Private.
5. For a media/file app, register an administrator-approved storage root first and choose the compatible read-only or read-write slot.
6. Change a declared service to This server only or Local network only when needed. LAN exposure binds the configured address, never a wildcard.

Managed data lives under `/srv/kitpro/apps/<application>/<installation>/` and survives runtime recreation. Imported data stays at the administrator-approved root and is not deleted or backed up by KITPro. Package migrations and trusted app updates protect control state, not the complete external library.

Ubuntu 26.04 has development validation evidence but is not in the current public support baseline. Rocky Linux and Podman remain Experimental.

See the [Debian guide](../install-debian-package.md), [Arch guide](../install-arch-package.md), [support matrix](../support-matrix.md), and [known limitations](known-limitations.md).
