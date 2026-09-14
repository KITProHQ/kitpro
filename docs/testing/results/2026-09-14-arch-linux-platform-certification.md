# Arch Linux platform certification

- Date: 2026-09-14
- Result: `ARCH LINUX PLATFORM CERTIFICATION: PASS`
- Host: Proxmox VM 503 (`10.10.0.119`)
- Starting snapshot: `clean-os`
- Source checkpoint: `a1f410d246e27b38c7564a74023f31af389820b8`
- Package: `kitpro-server-0.1.0_alpha4-1-x86_64.pkg.tar.zst`
- Package SHA-256: `d01c9bcbf93921dd9b84f24e91d29ca5dc17611344fb9487a0369a6c03ed3ba2`
- Package size: 10,198,151 bytes

This record is immutable evidence for the first native Arch Linux package
certification. It does not replace the Debian or Ubuntu package records and
does not claim that the Debian package is usable on Arch.

## Decision

KITPro Server is supported on fully updated Arch Linux `x86_64` hosts that use
official repositories, the `linux-lts` kernel, systemd, cgroup v2, rootful
Docker, and enforcing AppArmor. Partial upgrades and AUR replacements for the
kernel, AppArmor, Docker, containerd, or systemd are outside the supported
boundary. Certification follows current Arch repositories rather than a fixed
release number.

Debian 13 remains the primary/reference platform. Ubuntu Server 26.04 LTS and
Arch Linux are additional supported platforms. Rocky Linux 10 remains
experimental.

## Baseline

| Property | Measured value | Result |
| --- | --- | --- |
| Distribution | Arch Linux, `BUILD_ID=rolling` | PASS |
| Architecture | `x86_64` | PASS |
| Running kernel | `6.18.51-1-lts` | PASS |
| Installed kernel | `linux-lts 6.18.51-1` | PASS |
| Bootloader | GRUB 2.14 with `linux-lts` kernel and initramfs entries | PASS |
| systemd | `261.3-1` | PASS |
| pacman | package `7.1.0.r9.g54d9411-2`; libalpm 16.0.1 | PASS |
| glibc | `2.44+r24+g16be1518495f-1` | PASS |
| Cgroups | unified cgroup v2 | PASS |
| Root filesystem | ext4, 31.1 GiB total, 25.4 GiB available after validation tooling | PASS |
| LAN | `ens18`, `10.10.0.119/24`, default route via `10.10.0.1` | PASS |
| Firewall baseline | no UFW; nftables had no policy rules before Docker | OBSERVATION |
| Repositories | official Arch repositories and configured HTTPS mirrors | PASS |

The first full `pacman -Syu --noconfirm` found the clean host synchronized.
The final supported update cycle also completed with no pending packages. The
current Arch News entries were reviewed; no published manual intervention
applied to this minimal package set.

## AppArmor

The stock `linux-lts` kernel reported `CONFIG_SECURITY_APPARMOR=y`, but the
clean host's initial active LSM list omitted AppArmor. The official `apparmor`
package was installed. GRUB's kernel command line was updated to:

```text
lsm=landlock,lockdown,yama,integrity,apparmor,bpf
```

After regenerating GRUB configuration and rebooting:

- `/sys/kernel/security/lsm` reported
  `capability,landlock,lockdown,yama,apparmor,bpf`;
- `aa-enabled` returned `Yes`;
- `apparmor.service` was enabled and active;
- `aa-status` reported 80 enforcing profiles, including
  `/usr/libexec/kitpro-helper` and Docker's `docker-default`; and
- `apparmor_parser -Q -T` accepted the packaged helper profile.

The package refuses installation when AppArmor is not kernel-enabled. The
helper never fell back to an unconfined mode. A direct profile test attempted
to execute `/usr/bin/true` as a child of the helper profile and received
`Permission denied` with exit status 126. The unprivileged API account also
could not open the Docker socket.

## Docker installation

`tools/install-docker.sh --yes` detected Arch and used only official repository
packages. It performed a full `pacman -Syu`, installed Docker and its official
plugins, enabled Docker/containerd, and did not add the invoking account to the
Docker group.

| Component | Measured version/result |
| --- | --- |
| Docker Engine | `29.8.0` |
| containerd | `2.3.5` |
| Buildx | `0.37.1` |
| Compose plugin | `5.5.1` |
| Storage driver | overlayfs |
| Cgroups | v2 |
| `josh` Docker-group membership | absent |

The installer detection suite passed 26 of 26 cases, including Arch rendering
and official-package selection.

## Native package

The package was built as the unprivileged `josh` account with `makepkg`. Its
build used Go `1.27.1`, `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, and
stripped static binaries. `SOURCE_DATE_EPOCH=1789370088` and source commit
`a1f410d246e27b38c7564a74023f31af389820b8` identify the build input.

Two clean output directories produced byte-identical packages:

```text
d01c9bcbf93921dd9b84f24e91d29ca5dc17611344fb9487a0369a6c03ed3ba2  build-one
d01c9bcbf93921dd9b84f24e91d29ca5dc17611344fb9487a0369a6c03ed3ba2  build-two
cmp exit status: 0
```

`pacman -Qip`, `pacman -Qlp`, and `makepkg --printsrcinfo` confirmed the
expected name, version, architecture, runtime dependencies, maintainer hook,
and file inventory. The package contains:

- `/usr/bin/kitpro-api`;
- `/usr/libexec/kitpro-helper`;
- API/helper systemd units and helper socket;
- sysusers and tmpfiles definitions;
- the helper AppArmor profile;
- the Arch configuration template;
- API/helper manual pages; and
- package documentation and license metadata.

`namcap` reported expected static-analysis exceptions: `/usr/libexec` is
required by KITPro's accepted cross-platform privilege boundary; static Go
binaries do not present ELF PIE/FULL-RELRO metadata; sysusers/tmpfiles are run
early because migrations require their identities and directories; and runtime
dependencies used by maintainer hooks are not visible to namcap's binary-only
scan. It no longer reported a missing license file.

## Install and service layout

Installation through `pacman -U` completed without manually unpacking files.
The package created and validated:

| Path or identity | Measured result |
| --- | --- |
| `kitpro-api` account | system UID/GID 967; not in `docker` |
| `/var/lib/kitpro-api` | `0750`, owned by `kitpro-api` |
| `/var/lib/kitpro-helper` | `0700`, owned by root |
| `/srv/kitpro` and `/srv/kitpro/apps` | `0750`, owned by root |
| `/run/kitpro` | `0750`, root:`kitpro-api`, recreated by tmpfiles |
| `/run/kitpro/helper.sock` | `0660`, root:`kitpro-api` |

The API and helper socket enabled and started. `systemd-analyze verify` returned
zero for all three packaged units. The API and helper security scores were 2.9
and 3.0 respectively. `MemoryDenyWriteExecute`, the system-call filter, empty
capability sets, socket activation, and the accepted omission of
`RestrictSUIDSGID` remained intact.

## Authentication and application acceptance

A temporary administrator was created through the production setup flow. The
setup route then closed, login succeeded, and the authenticated dashboard and
catalog loaded. Anonymous mutation returned 401, authenticated mutation without
CSRF returned 403, and a wrong Origin returned 403. A valid authenticated,
CSRF-protected request succeeded. Temporary plaintext credential material was
not recorded and was removed after validation.

FreshRSS 1.29.1 installed from the catalog by immutable digest:

```text
sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21
```

- installation ID: `inst-a184faff8a8bb83a`;
- operation ID: `op-0fe150274a044f24b74821c2ee137239`;
- initial runtime generation: 1;
- initial exposure: internal, with no host publication;
- storage: the catalog-declared `data` and `extensions` roots below the stable
  installation directory; and
- internal HTTP: 302 response from a disposable same-network diagnostic client.

Initial reconciliation returned `exact`.

## Exposure and persistence

A benign marker at
`/srv/kitpro/apps/freshrss/inst-a184faff8a8bb83a/data/KITPRO-ARCH-PERSISTENCE-001`
had SHA-256
`2860dea69ad0ba744ad16f7f5367c74f4cf95852135759edea456ecd00aa4dc1`.

| Transition | Runtime | Observed binding | HTTP | Marker |
| --- | --- | --- | --- | --- |
| Internal install | generation 1 | none | internal 302 | created |
| Loopback exposure | generation 2 | `127.0.0.1:20000->80/tcp` | loopback 302; LAN connection refused | unchanged |
| LAN exposure | generation 3 | `10.10.0.119:20000->80/tcp` | VM and authorized external peer 302 | unchanged |
| Remove/recreate | generation 4 | same LAN address and port | 302 | unchanged |
| Post-drift recovery | generation 5 | same LAN address and port | 302 | unchanged |
| Disable exposure | generation 6 | no host publication | internal 302 | unchanged |
| Re-enable LAN | generation 7 | same `10.10.0.119:20000` assignment | 302 | unchanged |
| Final safe state | generation 8 | internal, no host publication | internal 302 | unchanged |

The installation ID, storage roots, and allocated port remained stable across
runtime recreation and API, helper, and Docker restarts. Runtime removal
preserved the installation and both storage roots. No wildcard IPv4 or IPv6
binding appeared.

## Reconciliation

The real runtime returned `exact` in internal and exposed states. Representative
fail-closed integration cases also passed:

- a stopped foreign container attached to the application network produced
  `security_drift`;
- external deletion of the expected runtime produced `missing`;
- a foreign container occupying the deterministic managed name produced
  `ownership_conflict`; and
- removing each foreign object and using the normal recreate flow restored
  `exact` without adoption.

Existing exposure-comparison tests cover missing, extra, wildcard, wrong-IP,
wrong-port, undeclared-port, and protocol-mismatch bindings. On the real host,
replacing a container to inject a wrong binding first triggered the stronger
trusted-container identity conflict. The helper did not start or adopt the
replacement.

## Upgrade, removal, and reinstall

A controlled `pkgrel=2` package exercised the Arch upgrade hook. Both databases
were integrity-checked and backed up at schema version 3 before migration.
The API/helper services recovered, the AppArmor profile reloaded, the LAN bind
configuration was preserved, and FreshRSS retained its installation, storage,
port, and endpoint.

`pacman -R kitpro-server` removed package-owned binaries, units, and the
AppArmor profile while Docker and the running application remained intact.
It preserved `/etc/conf.d/kitpro-server`, both state databases, backups, the
service identity, and `/srv/kitpro/apps`. Reinstalling the final package restored
the package assets and confinement without a permission repair; the existing
session, installation, endpoint, and marker remained valid.

Arch has no Debian-style purge phase. The native removal policy deliberately
retains configuration, trusted state, and application data. Data deletion is a
separate future destructive operation.

## Restart, reboot, and rolling update

API, helper, and Docker restarts preserved the assignment, container identity,
marker, and endpoint without duplicates. After a host reboot:

- `linux-lts 6.18.51-1` remained active;
- AppArmor, Docker, `kitpro-api`, and `kitpro-helper.socket` were active and
  enabled;
- `/run/kitpro/helper.sock` was recreated with the expected owner and mode;
- FreshRSS generation 7 returned with the exact LAN binding and HTTP 302;
- the marker checksum was unchanged; and
- reconciliation returned `exact`.

The final full `pacman -Syu` reported no pending upgrades. The host was rebooted
and the same checks passed, satisfying the rolling-release update cycle without
a partial upgrade.

## Firewall observation

Docker used iptables-nft managed tables. The observed DNAT rule matched
destination `10.10.0.119`, TCP destination port 20000, and the per-installation
bridge; it was not a wildcard bind. No host listening socket appeared in
`ss -ltn`, consistent with Docker's kernel NAT path. UFW was not installed and
no global firewall policy was changed.

## Final security and cleanup

- The API account never received Docker-group membership.
- AppArmor remained enforcing; the helper profile was never disabled.
- No privileged, host-networked, host-namespace, device, capability, Docker
  socket, arbitrary bind-mount, or wildcard-exposure exception was introduced.
- No credential, session token, Docker authentication file, database, socket,
  marker, or VM-specific secret is part of the package or repository changes.
- Diagnostic containers/listeners and temporary plaintext authentication
  material were removed.
- FreshRSS was left internal-only with persistent data retained.

## Final gate

`ARCH LINUX PLATFORM CERTIFICATION: PASS`
