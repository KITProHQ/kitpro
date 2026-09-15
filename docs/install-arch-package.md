# Install KITPro Server on Arch Linux

KITPro supports fully updated Arch Linux amd64 hosts that use official
repositories and the `linux-lts` kernel. Partial system upgrades and AUR
replacements for the kernel, AppArmor, Docker, containerd, or systemd are not
supported.

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

Builds use `makepkg` as a non-root account. Install the resulting package with:

```sh
sudo pacman -U kitpro-server-0.1.0_alpha2-1-x86_64.pkg.tar.zst
```

The transaction fails closed unless Docker is active and AppArmor is enabled.
The API listens on `127.0.0.1:8080` by default. Use an SSH tunnel and open
`http://127.0.0.1:8080/setup` to create the first local administrator.

## Configuration

Arch package configuration is `/etc/conf.d/kitpro-server`. Set
`KITPRO_LAN_BIND_ADDRESS` to one address actually assigned to the host before
enabling LAN exposure for an application. KITPro never publishes the control
plane or application services to wildcard addresses.

## Updates and removal

Always perform full Arch updates with `pacman -Syu`; partial upgrades are not
supported. A KITPro package upgrade backs up and migrates control/helper state
before services restart. Downgrades are unsupported after schema migration.

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
