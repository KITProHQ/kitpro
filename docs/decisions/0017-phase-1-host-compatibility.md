# ADR-0017: Phase 1 host compatibility

> Public-status update for alpha.13: Debian 13, Ubuntu 26.04 LTS, and Arch
> Linux with `linux-lts` are Supported. Rocky Linux 10 and Podman are
> Experimental. The decision text
> below preserves the earlier architecture decision and validation context.

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Approval: Debian 13 primary/reference and Rocky Linux 10 secondary/experimental approved by Josh after real-host validation; Ubuntu Server 26.04 LTS amd64 and Arch Linux amd64 with `linux-lts` added after native-package certification
- Related principles: Local-first, No lock-in, Open foundations, Secure defaults, Inspectability, Reversibility, Standard workloads
- Related decisions: ADR-0003, ADR-0004, ADR-0006, ADR-0010, ADR-0011, ADR-0012, ADR-0018

## Context

KITPro Server will manage host services, containers, storage, networks, updates, and application data. Small distribution differences become dangerous when software makes privileged changes. Phase 1 therefore needs a support boundary that the project can reproduce and test in full.

Debian 13 is the current Debian stable release. It uses systemd by default, ships a Linux 6.12 LTS kernel series, and supports both `amd64` and `arm64`. Docker publishes a Debian 13 installation path for Docker Engine.

The candidate hosts are not interchangeable. Debian and Rocky use different package systems, default filesystems, firewall integrations, and mandatory-access-control systems. Rocky also uses Docker's RHEL repository as a derivative and requires x86-64-v3 on AMD and Intel systems. Supporting both as official platforms would expand the first acceptance matrix before KITPro has proved one application lifecycle.

The project tested the same disposable helper and Docker workflow on clean Debian 13 and Rocky Linux 10 virtual machines. Both hosts used systemd, cgroup v2, rootful Docker Engine 29.8.0, and native test services. The results showed that the core protocol can remain distribution-neutral while packaging and host-security policy remain platform-specific.

## Decision

### Primary/reference platform

Phase 1 uses this reference host:

- Debian 13 stable, `amd64`.
- A minimal or server installation with systemd running as PID 1.
- The distribution-supported kernel from the Debian 13 security and point-release repositories. The initial test baseline is the Linux 6.12 series.
- The unified cgroup v2 hierarchy.
- A native host installation of KITPro Server, managed as systemd services.
- Rootful Docker Engine as the only supported Phase 1 container engine.
- Local `ext4` storage for the reference root filesystem, KITPro state, and container runtime state.
- Either bare metal or a full virtual machine that exposes the required kernel, cgroup, storage, and network capabilities.
- A clean or dedicated host with at least 20 GiB of free system capacity for KITPro, Docker, images, logs, and update staging.

Application data, media, photos, databases, and backups require separate capacity planning. The 20 GiB system floor does not include those workloads.

### Original additional platform decision

Ubuntu Server 26.04 LTS on `amd64` was originally accepted for the same native
`.deb` as Debian 13. For alpha.12, it has development and validation evidence
only and is not in the public support baseline. Certification covered Docker installation, package lifecycle,
AppArmor and systemd confinement, authentication, FreshRSS lifecycle and
exposure, persistent state, backup, and reboot recovery. Ubuntu's Docker/UFW
interaction remains an operator-visible caveat: exact-address publication is
the tested boundary, and custom firewall policy requires its own validation.

Fully updated Arch Linux on `x86_64` is supported when it uses official Arch
repositories, the `linux-lts` kernel, systemd, cgroup v2, rootful Docker, and
enforcing AppArmor. Arch uses its native `makepkg`/pacman package rather than
the Debian artifact. Support tracks the current rolling repositories, so
partial upgrades and AUR replacements for the kernel, AppArmor, Docker,
containerd, or systemd are unsupported. Certification covered a full
`pacman -Syu` cycle, native package install/upgrade/removal/reinstall, first-run
authentication, FreshRSS lifecycle and exposure, persistent state,
reconciliation, and reboot recovery.

### Secondary/experimental platform

Rocky Linux 10 on `x86_64` is an experimental platform. It uses the same native service, cgroup v2, rootful Docker Engine, helper protocol, Docker API, and clean-host boundaries as Debian. Its tested reference filesystem is XFS.

Rocky remains experimental because Docker documents the RHEL installation path rather than certifying Rocky independently. KITPro therefore owns derivative compatibility testing. Rocky 10 also requires x86-64-v3 hardware, Docker needs SELinux-aware daemon configuration, the helper's dedicated SELinux domain remains unvalidated, and firewalld and SELinux add platform support work.

Podman, rootless Docker, and simultaneous management of more than one container engine are deferred. ADR-0004 must still define the Docker integration contract, supported version policy, package source, and adapter boundary. The application specification must remain based on OCI concepts and KITPro policy rather than Docker-only manifest fields.

KITPro Server itself will not run as a container in Phase 1. A containerized control service would need broad host mounts, runtime control, or both. That arrangement would blur the privilege boundary and make service recovery depend on the runtime that KITPro manages.

The core helper protocol, ownership model, Docker Engine API abstraction, and
semantic operations must remain distribution-independent. Installers,
packages, firewall adapters, AppArmor profiles, SELinux policy, and
distribution upgrade logic own host-specific behavior.

## Validation evidence

Debian 13.7 passed the reference-host workflow after its root filesystem was corrected to provide 36.6 GiB of free system capacity. The measured run established these results:

- Docker Engine 29.8.0 installed through Docker's documented Debian repository.
- The unprivileged API identity could not access the Docker socket, while the privileged helper could use the approved Engine API subset.
- systemd socket activation and `SO_PEERCRED` authenticated the fixed service identity.
- Docker applied the AppArmor `docker-default (enforce)` profile to the test container.
- The ownership matrix, internal network, restart and reboot recovery, descriptor-relative filesystem checks, and cleanup passed.
- Debian's general `amd64` baseline supports more repurposed hardware than Rocky Linux 10's x86-64-v3 requirement.
- Debian required less platform-specific daemon and mandatory-access-control configuration.

Rocky Linux 10.2 also proved that the architecture is portable. Docker installation, the privilege boundary, ownership checks, filesystem containment, networking, Docker outage recovery, and reboot recovery passed while SELinux remained Enforcing. After Docker's `selinux-enabled` setting was applied, containers ran as `container_t` with `container_file_t` mounts. Rocky remains experimental because the helper ran as `unconfined_service_t` and the corrected installer has not completed a clean-snapshot rerun.

The immutable evidence is in the [Debian completed result](../testing/results/2026-09-12-debian13-completed-validation.md), [Ubuntu package certification](../testing/results/2026-09-13-ubuntu2604-package-certification.md), [Arch Linux certification](../testing/results/2026-09-14-arch-linux-platform-certification.md), [Rocky initial result](../testing/results/2026-09-12-rocky10-validation.md), [Rocky SELinux follow-up](../testing/results/2026-09-12-rocky10-selinux-followup.md), and [platform comparison](../testing/platform-comparison.md).

## Supported environments

### Official Phase 1 support

An officially supported host satisfies every row in this table.

| Area | Supported boundary |
| --- | --- |
| Distribution | Supported for alpha.13: Debian 13 stable, Ubuntu Server 26.04 LTS, or fully updated Arch Linux using current official repositories. |
| CPU architecture | `amd64`/`x86_64` |
| Init and service manager | systemd running as PID 1 |
| Control groups | cgroup v2 unified hierarchy with the CPU, memory, I/O, and process-count controllers available to the runtime |
| Kernel | The distribution-supported Debian 13 or Ubuntu 26.04 LTS kernel, or Arch `linux-lts`; no out-of-tree or vendor-modified kernel requirement |
| Container engine | Rootful Docker Engine; exact packages, version range, and integration method remain for ADR-0004 |
| Filesystem | Local `ext4` for the tested Debian and Arch layouts |
| Installation form | Native host services and files installed through a reviewable platform package path |
| Machine type | Bare metal or a full virtual machine |
| Network placement | A trusted private network; no direct public internet exposure of the KITPro dashboard |
| Existing workloads | A dedicated or clean host with no pre-existing containers, networks, or runtime configuration that KITPro could disrupt |
| Host administration | A person with root or sudo access for installation, repair, upgrade, and removal |
| System capacity | At least 20 GiB free for KITPro, Docker, images, logs, and update staging; application data needs separate capacity |

"Official" means the release acceptance suite passes on this exact class of host. It does not mean that nearby configurations cannot work.

### Experimental support

Experimental environments support compatibility work, but a failure does not block the Debian 13 Phase 1 release.

- Rocky Linux 10 on `x86_64` hardware that meets the x86-64-v3 requirement.
- systemd as PID 1, cgroup v2, rootful Docker Engine, native KITPro services, and a clean or dedicated host.
- SELinux Enforcing. KITPro must not disable enforcement or require a broad permissive domain.
- Local XFS with a Docker-supported storage configuration.
- At least 20 GiB of free system capacity, with application data planned separately.

Rocky becomes official only after the [Rocky experimental-support backlog](../follow-ups/rocky-experimental-support.md) passes. A Rocky failure does not permit Debian-specific behavior in the core helper or Docker contracts. The [Debian security-validation backlog](../follow-ups/debian-security-validation.md) tracks open primary-platform security and recovery tests.

### Deferred environments

The following environments are useful future targets but add a separate test or security model:

- Debian 13 and Ubuntu Server on `arm64`.
- Ubuntu Server 24.04 LTS and Ubuntu LTS releases other than 26.04.
- Debian releases other than Debian 13.
- Podman in either rootful or rootless mode.
- Rootless Docker.
- Btrfs, ZFS, and encrypted storage arrangements that require KITPro-specific handling.
- Removable disks, hot-plug storage, and application data on network filesystems such as NFS or CIFS.
- Existing hosts that already run containers, custom bridges, reverse proxies, or hand-written firewall policy.
- Public-cloud hosts and direct public exposure of KITPro management services.
- High-availability, clustered, and multi-host installations.
- Support for in-place distribution upgrades managed by KITPro.

Deferred means that KITPro does not claim compatibility and must not make untested changes on the host.

## Explicitly unsupported environments

Phase 1 will reject these environments before it changes the host:

- Linux distributions other than the supported and experimental versions above.
- Rolling-release distributions.
- Systems where systemd is not PID 1.
- cgroup v1 or hybrid cgroup hierarchies.
- 32-bit CPU architectures.
- Containers, chroots, WSL, shared-kernel virtual environments, and nested container hosts used as the KITPro host.
- Custom kernels or kernels outside the distribution's security support.
- Read-only root filesystems and ephemeral root filesystems.
- KITPro state or application data on an unmounted, missing, read-only, or unstable storage path.
- More than one active container engine or a Docker-compatible socket that is not the tested Docker Engine.
- Docker TCP API exposure, with or without TLS, as KITPro's local runtime integration.
- Hosts where required ports conflict or where KITPro cannot determine the effect of existing firewall and network policy.

An unsupported result must leave existing services, files, users, firewall rules, and runtime configuration unchanged.

## Required host capabilities

### Init, cgroups, and kernel

KITPro requires systemd as PID 1. Native system services, restart recovery, mount ordering, service credentials, logs, and the privileged-helper boundary will rely on systemd behavior. Supporting another init system would require a separate lifecycle and hardening design.

The host must expose one cgroup v2 hierarchy. KITPro will use runtime resource limits and observations without maintaining parallel cgroup v1 logic. Docker documents cgroup v2 as the default on Debian since version 11 and Ubuntu since version 21.10.

The installer must verify the running kernel and required namespace, cgroup, seccomp, OverlayFS, networking, and filesystem capabilities. A version string alone is not enough. The first baseline uses Debian's Linux 6.12 series, but later supported Debian kernel updates do not require a new ADR when the acceptance suite passes.

### OCI container support

The first slice uses rootful Docker Engine to run OCI images. Docker support is a practical compatibility choice for the first application, not permission to expose Docker Compose or the Docker API as KITPro's application model.

The host must expose the runtime only to the privileged KITPro boundary defined by ADR-0018. The web and API service must not join the `docker` group or open the Docker socket. Docker control can create a container with the host root mounted for writing, so runtime access is a form of host authority.

Podman remains a real alternative. Its daemonless and rootless modes could reduce some risks. Its API, systemd integration, networking, storage ownership, user namespaces, and rootless lifecycle create a different operational model. Phase 1 will not pretend that a Docker-compatible API makes the engines interchangeable.

### Filesystems and mounts

The reference host uses local `ext4`. The installer must identify the filesystem, mount source, mount options, free bytes, free inodes, ownership, and write access for each managed state or data path.

KITPro must not format disks, create partitions, unlock encrypted volumes, or edit mount definitions in Phase 1. If a user selects an existing data mount, KITPro must verify that the mount exists before every operation. A missing mount must fail closed rather than redirect writes into the underlying directory on the root filesystem.

Systemd service ordering must prevent a managed application from starting before its declared data mounts are ready. The exact application-data layout remains for ADR-0006.

The path roles follow the Filesystem Hierarchy Standard:

- `/etc/kitpro` is the candidate location for host-specific KITPro configuration. Secrets need stricter ownership and may require a separate location chosen by ADR-0007.
- `/var/lib/kitpro` is the candidate location for private mutable KITPro state.
- `/run/kitpro` is the candidate location for runtime-only sockets and process state.
- `/var/log/kitpro` is used only if ADR-0013 requires files beyond the system journal.
- `/srv/kitpro` is the candidate default for user-visible persistent application data. ADR-0006 must accept or replace this path.
- `/opt/kitpro` is not a mutable-data location. It remains available only if the packaging decision needs a self-contained software tree.

KITPro must never treat Docker's internal storage under `/var/lib/docker` as the user's only persistent application-data location. KITPro also must not edit files in `/etc` that it does not own without a named, reversible operation.

### Users, groups, and privileges

Installation requires root or sudo because it creates service identities, installs system services, and prepares protected directories. Routine use must not require an interactive sudo prompt.

The installed design must use a dedicated unprivileged identity for the web and API service. That identity must not belong to `docker`, `sudo`, or another group that grants broad host control. A separate privileged component may run with the minimum authority needed for its fixed operations.

The installer must report every user, group, directory, service, package source, and configuration file it will add or change. Removal must preserve application data and follow the reversible behavior defined by the product principles.

### Capacity

The minimum reference host has:

- two 64-bit CPU threads;
- 4 GiB of RAM available to the host;
- 20 GiB of free local storage for KITPro, the container engine, images, logs, and update staging; and
- additional storage required by the selected application manifest.

The platform tests validated this host floor, but the first supported application may raise it. KITPro must check available capacity before deployment and update. The minimum does not promise that every application will run.

### Virtualization and bare metal

Bare metal and full virtual machines are supported when they present the required capabilities. The acceptance matrix must include both.

Shared-kernel containers and nested container environments are unsupported because they change cgroup delegation, mounts, networking, device access, and privilege boundaries. KITPro must detect common virtualized environments and report the result for diagnosis.

### Package management and distribution upgrades

Package management is a platform-adapter concern. Debian uses `dpkg` and APT. Rocky uses RPM and DNF. Their repositories, package versions, service defaults, and upgrade policies differ. Installation must use explicit, signed package sources and noninteractive operations with predictable failure handling. The project must not use an unreviewed `curl | sh` path.

Phase 1 supports security updates and point releases within the accepted distribution major version after validation. It does not perform distribution upgrades. If the operator upgrades the distribution independently, KITPro must detect the new version and stop privileged mutations until that version passes compatibility checks. Managed applications should continue running through their standard runtime when possible.

### Host networking and firewall interaction

KITPro must inspect active listeners, interfaces, routes, network managers, and firewall tools before it changes network exposure. It must not rewrite Netplan, `/etc/network/interfaces`, systemd-networkd files, or an existing firewall policy during the first slice.

Docker documents that published container ports can bypass `ufw` or `firewalld` policy. Docker also supports `iptables-nft` and `iptables-legacy`, but not firewall rules written directly with `nft` in the documented installation model. Phase 1 therefore applies these boundaries:

- Managed application ports bind only to loopback unless ADR-0010 defines and validates a single ingress path.
- KITPro never assumes that an active `ufw` rule protects a Docker-published port.
- The installer rejects unknown or incompatible firewall arrangements instead of disabling them.
- KITPro does not expose the Docker API over TCP.
- A port conflict blocks deployment before any host change.

The exact reverse proxy, application naming, certificate, IPv6, and firewall-management model remains open in ADR-0010.

## Consequences

### Benefits

- One distribution, architecture, filesystem, service manager, and engine make the first acceptance matrix small enough to run after every lifecycle change.
- Debian stable provides a conservative base without tying KITPro to a custom operating system.
- Native services make the privilege boundary, startup order, logs, and recovery visible to host administrators.
- Deferring `arm64`, Podman, and existing-host adoption prevents compatibility claims that the project cannot yet prove.
- An engine-neutral application policy limits long-term Docker lock-in.

### Costs

- Many target users run Ubuntu or `arm64` mini PCs and will not receive first-release support.
- A clean, dedicated host requirement excludes experienced self-hosters with existing workloads.
- Rootful Docker creates a high-value control socket and a root-equivalent failure path.
- Native installation requires careful package, service, upgrade, and removal work.
- `ext4`-only official testing excludes valid ZFS, Btrfs, XFS, NAS, and encrypted-storage designs.

## Risks

| Risk | Effect | Required response |
| --- | --- | --- |
| Docker becomes an accidental product lock-in | The application model or state depends on Docker-only behavior | Keep manifests engine-neutral, isolate runtime integration, and schedule a Podman evaluation after the vertical slice |
| Docker firewall behavior exposes an application | A LAN or remote attacker reaches a port the operator believed was filtered | Bind workloads to loopback, test both IPv4 and IPv6, and block unsupported firewall arrangements |
| The capacity floor is too low for the first application | Small hosts pass installation but cannot run the workload | Measure the control service and first application, then raise the floor if required |
| Nominal disk size hides an undersized system filesystem | Docker and KITPro exhaust the filesystem that contains `/var` | Check free capacity on the actual system and Docker-data filesystems before installation |
| Debian-only release support misses other users | The initial release excludes Rocky, Ubuntu, and some enterprise environments | Maintain experimental Rocky evidence and require the full suite before promoting another distribution |
| Native installation leaves host changes behind | Removal is incomplete or confusing | Inventory every owned path, identity, service, and package change; test install and removal repeatedly |
| Dedicated-host support is too restrictive | Existing self-hosters cannot adopt KITPro | Add existing-host discovery only after collision and ownership rules exist |
| Distribution upgrades break privileged operations | KITPro mutates an untested host | Detect the OS version and disable mutations until compatibility is restored |

## Alternatives considered

### Support Debian and Ubuntu equally in the first release

This option reaches more users. It also doubles distribution, network, firewall, packaging, and upgrade paths before the product has one proven lifecycle. The project should earn the second distribution by passing the same acceptance suite, not by assuming APT makes the systems identical.

### Use Ubuntu Server as the reference platform

Ubuntu has broad adoption and extensive server documentation. Ubuntu 26.04 is newer than Debian 13 and adds Ubuntu-specific Netplan and firewall behavior to the first implementation. Debian 13 provides the smaller reference target. Ubuntu remains deferred until it passes the same suite.

### Use Rocky Linux as the reference platform

Rocky Linux 10 passed the core helper and Docker workflow with SELinux Enforcing. Its longer lifecycle and SELinux policy ecosystem are useful for later support. Selecting it as the first reference would make KITPro responsible for Docker's RHEL-to-Rocky compatibility, x86-64-v3 exclusions, SELinux-aware daemon configuration, helper policy packaging, and firewalld regression coverage before the first application slice. The measured Debian path has fewer initial support obligations.

### Support both Docker and Podman

Both engines run OCI workloads, but their daemon, API, privilege, user-namespace, storage, networking, and systemd models differ. A compatibility flag would hide real behavior differences. Supporting one engine first produces a testable lifecycle and leaves the application model portable.

### Use Podman only

Podman's rootless operation and systemd integration deserve later evaluation. Rootless operation also changes port, mount, user-ID, socket, and service-lifetime behavior. The first supported application ecosystem and common operator expectations favor Docker for the initial slice. This choice carries a root-equivalent socket risk that ADR-0018 must contain.

### Run KITPro Server as a container

Container packaging could make distribution differences smaller. The control service would still need host inspection, system service changes, persistent state, network control, and runtime access. Broad bind mounts or a runtime socket inside that container would create a privileged container whose apparent isolation is misleading.

### Support `amd64` and `arm64` together

Both Debian and Docker support these architectures. KITPro would still need physical or virtual test capacity, multi-architecture images, application-specific resource limits, and update coverage for both. `arm64` follows after the `amd64` lifecycle is complete.

## Remaining validation plan

The completed platform fixture validates the host choice and privilege boundary. Product acceptance still requires disposable Debian 13 `amd64` hosts that start from recorded clean checkpoints.

1. Test a minimal bare-metal installation and a clean full virtual machine.
2. Record `/etc/os-release`, kernel, systemd, cgroup hierarchy and controllers, CPU architecture, memory, filesystems, mounts, active listeners, network manager, firewall tools, and Docker information before installation.
3. Verify that an unsupported host exits before it changes packages, files, users, groups, services, firewall rules, runtime state, or workloads.
4. Install, reinstall, upgrade, repair, and remove KITPro Server. Compare the host inventory after each operation.
5. Deploy the supported application and exercise start, stop, health, logs, update, failed update, rollback, host restart, runtime restart, uninstall, and reinstall.
6. Prove that uninstall retains persistent application data and that standard Docker tools can still identify or recover the workload data.
7. Test missing mounts, read-only mounts, low space, low inodes, permission errors, and a mount that disappears before start.
8. Test port conflicts, IPv4 and IPv6 listeners, `iptables-nft`, active `ufw`, direct `nft` policy, and an unknown firewall state. Confirm that unsupported states fail closed.
9. Test a Debian point release and security-kernel update before adding it to the supported range.
10. Run the same suite on Rocky Linux 10 before promoting it from experimental support and on Ubuntu before assigning any Ubuntu support status.
11. Measure idle and active CPU, memory, disk, and log use. Raise the accepted capacity floor if the first application needs more resources.
12. Test every accepted Docker Engine update before expanding the supported version range.

The validation record must include exact images, package versions, kernel versions, commands, and results.

## Review conditions

Revisit this ADR when any of these conditions occurs:

- Debian 13 reaches the end of the project's supported maintenance window.
- Rocky Linux 10 or an Ubuntu LTS release passes the complete acceptance matrix and the project wants to promote it.
- User demand or supported hardware makes `arm64` a release requirement.
- ADR-0004 selects a runtime other than Docker Engine.
- ADR-0006 requires a filesystem or storage model outside this boundary.
- ADR-0010 cannot provide safe network exposure without changing the host policy.
- Docker changes its storage, cgroup, packaging, API, or firewall behavior in a way that invalidates tested assumptions.
- The first supported application cannot run within the accepted resource floor.
- Supporting existing workloads becomes a product requirement.
- A security review shows that native rootful Docker cannot meet ADR-0018.

## Evidence reviewed

- [Debian releases](https://www.debian.org/releases/) identifies Debian 13 as the current stable release and publishes its support dates.
- [Debian 13 release information](https://www.debian.org/releases/trixie/) lists supported architectures.
- [Debian 13 release announcement](https://www.debian.org/News/2025/20250809) records the Linux 6.12 LTS kernel series and systemd 257.
- [Debian Policy](https://www.debian.org/doc/debian-policy/ch-opersys.html) identifies systemd as Debian's default init and service manager.
- [Ubuntu release cycle](https://ubuntu.com/about/release-cycle) identifies Ubuntu 26.04 as the current LTS and describes its maintenance window.
- [Ubuntu 26.04 release notes](https://documentation.ubuntu.com/release-notes/26.04/) record server minimums, systemd changes, and cgroup v1 removal.
- [Docker Engine on Debian](https://docs.docker.com/engine/install/debian/) lists Debian 13 and documents package and firewall constraints.
- [Docker Engine on Ubuntu](https://docs.docker.com/engine/install/ubuntu/) lists Ubuntu 26.04 and documents package and firewall constraints.
- [Docker Engine on RHEL](https://docs.docker.com/engine/install/rhel/) documents the RHEL installation path used for KITPro's Rocky compatibility testing.
- [Docker runtime metrics](https://docs.docker.com/engine/containers/runmetrics/) documents cgroup v2 defaults and runtime requirements.
- [Docker storage drivers](https://docs.docker.com/engine/storage/drivers/select-storage-driver/) documents filesystem and storage-driver compatibility.
- [Podman](https://docs.podman.io/en/latest/markdown/podman.1.html) documents rootful and rootless storage, systemd cgroup management, and the daemonless model.
- [Filesystem Hierarchy Standard 3.0](https://refspecs.linuxfoundation.org/FHS_3.0/fhs-3.0.html) defines the roles of `/etc`, `/var`, `/opt`, and `/srv`.
- [Debian network setup](https://www.debian.org/doc/manuals/debian-reference/ch05) documents Debian's supported network-management arrangements.
- [Ubuntu Netplan update policy](https://documentation.ubuntu.com/project/SRU/reference/exception-Netplan-Updates/) documents Netplan's role in Ubuntu network configuration.
- [Debian completed validation](../testing/results/2026-09-12-debian13-completed-validation.md) records the accepted reference-host evidence.
- [Rocky initial validation](../testing/results/2026-09-12-rocky10-validation.md) preserves the initial SELinux confinement failure and the portable helper results.
- [Rocky SELinux follow-up](../testing/results/2026-09-12-rocky10-selinux-followup.md) records the corrected container confinement and remaining helper-policy work.
