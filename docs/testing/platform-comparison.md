# Debian 13 and Rocky Linux 10 platform comparison

## Decision status

Both candidates completed meaningful real-host validation on the same Docker release, image digest, helper protocol, and corrected ownership adapter. Josh approved **Debian 13 as the Phase 1 primary/reference platform** and **Rocky Linux 10 as a secondary experimental platform**. ADR-0017 records that decision.

Immutable records:

- [Debian capacity-blocked run](results/2026-09-12-debian13-validation.md)
- [Debian storage remediation](results/2026-09-12-debian13-storage-remediation.md)
- [Debian completed validation](results/2026-09-12-debian13-completed-validation.md)
- [Rocky initial validation](results/2026-09-12-rocky10-validation.md)
- [Rocky SELinux follow-up](results/2026-09-12-rocky10-selinux-followup.md)

## Evidence summary

| Area | Debian 13.7 amd64 | Rocky Linux 10.2 x86_64 |
| --- | --- | --- |
| Host capacity | PASS after layout correction: 40 GiB ext4 root, 36.6 GiB free | PASS: approximately 34.1 GiB XFS root, 31.9 GiB free |
| Docker installer | PASS through Docker's documented Debian repository | Initial package/repository workflow PASS through Docker's RHEL repository with derivative caveat; manual follow-up required explicit SELinux daemon configuration, and the corrected installer has not had a clean-snapshot rerun |
| Docker versions | Engine/CLI 29.8.0, API 1.56, containerd 2.3.5, Buildx 0.37.1, Compose 5.5.1 | Same |
| systemd/socket activation | PASS | PASS |
| `SO_PEERCRED` | PASS, API UID 988 | PASS, API UID 993 |
| API identity Docker denial | PASS | PASS |
| Helper Docker API access | PASS | PASS |
| Ownership matrix | PASS, including corrected stopped-member query | PASS; corrected stopped-member query rerun end to end |
| Filesystem protection | PASS for traversal, links, descriptor-relative containment, and nested mount; specialized race NOT RUN | Same substantive result on XFS; specialized race NOT RUN |
| Network default | PASS: internal per-instance bridge and no ports | PASS: internal per-instance bridge and no ports |
| Restart/recovery | PASS for helper, Docker, client, socket, and VM reboot; arbitrary interrupted mutation NOT RUN | Same; guest-agent control failed but in-guest reboot passed |
| Container MAC | `docker-default (enforce)` under AppArmor without extra daemon configuration | `container_t`/`container_file_t` after explicit Docker SELinux enablement; initial state was `spc_t` |
| Helper MAC | PASS: test-only AppArmor profile enforced; production package/upgrade NOT RUN | PASS: test-only dedicated SELinux domain under Enforcing; production packaging/upgrades NOT RUN |
| Cleanup | PASS; no containers/volumes and only built-in networks | PASS; no containers/volumes and only built-in networks |

## Host and installation differences

| Concern | Debian 13 | Rocky Linux 10 | Consequence |
| --- | --- | --- | --- |
| Vendor path | Docker documents Debian 13 directly | Docker documents RHEL 10, not independent Rocky certification | Rocky compatibility and every upgrade remain KITPro support obligations. |
| Repository | Distribution-specific deb822 source and scoped keyring | RHEL-compatible RPM repository | Both installed cleanly; packaging adapters remain separate from the core protocol. |
| Filesystem | ext4 reference | XFS reference candidate | `openat2` containment and Docker overlayfs worked on both. |
| Initial storage layout | Installer created an 8.6 GiB root and 29.3 GiB `/home`; required correction | Approximately 34.1 GiB root met the capacity gate | Nominal disk size is insufficient; KITPro must inspect the filesystem containing `/var` and state. |
| Mandatory access control | AppArmor active; Docker automatically loaded an enforcing default profile | SELinux Enforcing; policy package present, but Docker labeling required explicit daemon enablement | Both need a future helper-specific MAC design. Rocky adds mandatory daemon and policy lifecycle work. |
| Firewall | No firewall manager was configured; Docker installed nftables-backed iptables chains | firewalld active; Docker created a `docker` zone, forwarding policy, and iptables-nft chains | KITPro must document Docker-managed forwarding on both and must not promise that host firewall defaults protect published ports. |
| CPU baseline | General amd64 target | Rocky 10 AMD/Intel requires x86-64-v3 | Rocky excludes some older repurposed servers and mini PCs. |
| Lifecycle | Shorter major-version horizon | Longer published major security horizon; latest minor must be followed | Rocky's lifecycle advantage brings more platform-specific maintenance rather than eliminating it. |

## Privilege-boundary behavior

The core architecture behaved consistently on both hosts:

- the browser-facing/API identity had no direct Docker socket authority;
- the helper authenticated a fixed local peer with socket permissions and `SO_PEERCRED`;
- root or an unrelated UID was not treated as the authorized API identity;
- the protocol could not carry a Docker ID, raw Engine request, Compose document, shell command, arbitrary path, mount, device, capability, namespace, security option, or sysctl;
- the helper required state, exact Docker identity, exact test labels, fixed image digest, expected name, resource kind, run identity, and network membership before mutation; and
- a normal request did not prove human authorization.

`Requires=docker.service` was removed from the shared unit after real testing showed that it activated Docker during an intended outage. `After=docker.service` retains ordering without changing runtime state. Both corrected full runs returned bounded `DockerUnavailable` and recovered after an explicit Docker start.

## Ownership and Docker API behavior

Docker 29's network-inspect member map omitted created/stopped endpoints on both platforms. Querying all containers by the exact network name returned both the managed and stopped foreign endpoints. The corrected helper rejected the unexpected member with `PolicyDenied` end to end on Debian and Rocky.

This finding is platform-independent and security-relevant. Network inspection alone cannot authorize destructive network removal. The adapter's bounded all-container query is required unless a future Engine API contract provides an equally complete source.

Every other ownership disagreement failed closed on Debian, matching the earlier Rocky matrix: missing labels, missing records, record-only and label-only resources, wrong instance/kind, changed image, unexpected network, running foreign member, and unknown object ID. No unknown resource was adopted or deleted.

## Filesystem and systemd behavior

Descriptor-relative `openat2` enforcement worked on Debian ext4 and Rocky XFS. Traversal, absolute paths, deterministic symlink escape/replacement, and crossing a nested disposable filesystem were rejected. Hard-link file replacement was not applicable because the fixture has no privileged file-write operation. A high-frequency race harness remains future work.

`RestrictSUIDSGID=yes` caused the required `openat2` call to return `ENOSYS` under Rocky systemd 257. Removing that defense-in-depth directive restored the primary containment mechanism, while the rest of the sandbox remained in place. Debian used the same corrected unit and passed. This does not justify permanently omitting the control; the production unit review must reproduce the seccomp interaction and select a narrower compatible replacement.

Rapid manual ownership fault injection hit systemd's service start-rate limit on Debian. Resetting the disposable unit and returning to socket-only activation resolved it. Directly starting a socket-activated helper is invalid because the helper correctly requires one inherited socket. The runbook should encode that operational detail.

## Networking and security controls

Both platforms created one internal, non-attachable, non-ingress bridge per test instance. Containers had no host/default-bridge attachment, no host networking, and no published port. Docker installed `DOCKER-INTERNAL` rules that dropped traffic crossing outside the instance subnet. Unrelated networks remained untouched.

Loopback IPv4/IPv6 publication remains `NOT RUN` because the constrained fixture exposes no publication operation. The successful no-port default is not evidence about future published-port or reverse-proxy behavior.

Debian's container ran under `docker-default (enforce)` without an installer-specific MAC setting. Rocky initially ran containers as `spc_t` even though SELinux was Enforcing and `container-selinux` was installed. Enabling Docker's documented `selinux-enabled` daemon option produced `container_t` process labels and `container_file_t` mount labels with MCS categories. No AVC or policy workaround was required.

The test-only helpers now have platform-specific confinement: Debian AppArmor profile and Rocky dedicated SELinux domain. Production packaging, upgrade, rollback, and policy lifecycle remain open on both; Rocky additionally needs SELinux-aware daemon and firewalld regression coverage. The Docker socket remains powerful host authority under either MAC system.

## Operational burden and user experience

Debian's advantages for Phase 1 are:

- a Docker-documented distribution path rather than derivative compatibility;
- broader compatibility with repurposed amd64 hardware;
- automatic Docker AppArmor confinement in the measured installation;
- fewer distribution-specific daemon and policy requirements; and
- successful parity across the helper, ownership, filesystem, network, and recovery tests.

Its material caveat is installation layout: the default partitioning used on this VM failed the capacity contract despite a 40 GiB disk. KITPro documentation and host checks must make root/`/var` capacity explicit. The corrected test VM also has no swap, which is not an approved production default.

Rocky's advantages are its longer lifecycle, SELinux policy ecosystem, firewalld integration, and enterprise familiarity. Its costs are Docker's derivative-support caveat, x86-64-v3, explicit Docker SELinux configuration, future helper-policy packaging, and a larger platform-specific support surface. These are manageable for experimental support but unnecessary risk for the first vertical slice.

## Conclusion

- **Primary/reference platform: Debian 13.** It has complete real-host evidence and the lower Phase 1 support burden.
- **Secondary/experimental platform: Rocky Linux 10.** The architecture is technically viable with SELinux Enforcing, but official support waits for helper-domain policy, daemon-config lifecycle, upgrades, and broader installer coverage.
- **ADR status: accepted.** ADR-0017 records Josh's evidence-based decision.

Sources retained for vendor-path and lifecycle context: [Docker Engine on Debian](https://docs.docker.com/engine/install/debian/), [Docker Engine on RHEL](https://docs.docker.com/engine/install/rhel/), [Docker Engine security](https://docs.docker.com/engine/security/), [Docker daemon configuration](https://docs.docker.com/reference/cli/dockerd/), [Debian 13 release information](https://www.debian.org/releases/stable/), and [Rocky Linux release policy](https://docs.rockylinux.org/releases/).

Open platform work is tracked in the [Debian security-validation backlog](../follow-ups/debian-security-validation.md) and [Rocky experimental-support backlog](../follow-ups/rocky-experimental-support.md). If no more privileged validation is planned now, Josh may remove `/etc/sudoers.d/kitpro-validation` from both disposable VMs. This documentation change does not remove it automatically.
