# Debian 13 completed platform validation: 2026-09-12

## Run identity

- Overall status: `PASS` for the Phase 1 host and privilege-boundary prototype
- VM: Proxmox VM 500, `10.10.0.115`
- OS: Debian GNU/Linux 13.7 (trixie)
- Clean corrected snapshot: `storage-corrected-clean-os`, created 2026-09-12 21:58:47 PDT
- Post-install snapshot: `docker-installed`, created 2026-09-12 22:05:04 PDT
- Repository baseline: `8e8e05176b5f9c2669d702b679e034828bdc08f4`
- Corrected fixture run ID: `b4487485-473c-418d-85fb-4e8533897038`
- Previous immutable records: [capacity-blocked run](2026-09-12-debian13-validation.md) and [storage remediation](2026-09-12-debian13-storage-remediation.md)
- Procedure: [privilege-boundary runbook](../../../prototypes/privilege-boundary/RUNBOOK.md)

This record does not replace the earlier blocked run. It covers the corrected storage baseline, Docker installation, full helper run, ownership fault matrix, filesystem and network checks, reboot recovery, and cleanup.

## Corrected baseline

| Check | Status | Observation |
| --- | --- | --- |
| Distribution | PASS | Debian GNU/Linux 13.7. |
| Kernel and architecture | PASS | `6.12.107+deb13-amd64`, `x86_64`. |
| CPU and memory | PASS | 2 vCPU, approximately 3.8 GiB RAM. |
| PID 1 and systemd | PASS | systemd PID 1, version 257.13. |
| Cgroups | PASS | Unified cgroup v2. |
| Root filesystem | PASS | ext4 on `/dev/sda1`, approximately 40 GiB total and 36.6 GiB free before Docker. |
| Capacity rule | PASS | `/var`, Docker metadata, system packages, logs, and future KITPro state share the filesystem with more than 20 GiB free. Application data still needs additional planning. |
| Swap | OBSERVATION | No swap after storage remediation. This is acceptable for the disposable test but must not become an accidental production recommendation. |
| Network | PASS | `ens18` at `10.10.0.115/24`, private default route through `10.10.0.1`. |
| Time | PASS | Network time synchronized; America/Los_Angeles. |
| AppArmor baseline | OBSERVATION | AppArmor active and enabled. Docker later loaded `docker-default` in enforce mode. |
| Docker cleanliness | PASS | Docker absent before the installer run. |
| Snapshot | PASS | Josh confirmed `storage-corrected-clean-os` before installation. |

## Docker installation

| Check | Status | Observation |
| --- | --- | --- |
| KITPro installer | PASS | Detected Debian 13 and used `download.docker.com/linux/debian` with a deb822 source and scoped keyring. |
| Signing key | PASS | Primary fingerprint matched `9DC858229FC7DD38854AE2D88D81803C0EBFCD88`. |
| Packages | PASS | Installed Docker Engine/CLI, containerd, Buildx, and Compose plugin. No legacy `docker-compose` package. |
| Versions | PASS | Docker Engine/CLI 29.8.0, API 1.56, containerd 2.3.5, runc 1.5.1, Buildx 0.37.1, Compose 5.5.1. |
| Daemons | PASS | Docker and containerd active and enabled. |
| Runtime mode | PASS | cgroup v2, systemd cgroup driver, overlayfs storage driver. |
| User grant | PASS | Installer did not add `josh` or any other account to `docker`. |
| Snapshot | PASS | Josh confirmed `docker-installed` after installation and verification, before fixture deployment. |

## Authority and protocol boundary

| Check | Status | Observation |
| --- | --- | --- |
| Root Docker authority | PASS | Root/sudo communicated with the Engine. |
| `josh` Docker denial | PASS | `josh` was outside `docker`; direct Engine access returned permission denied. |
| API identity | PASS | Temporary UID/GID 988, non-root, no Docker or administrative group membership. |
| API Docker denial | PASS | Direct connection to `/var/run/docker.sock` returned permission denied. |
| Docker socket | PASS | `root:docker`, mode 0660. |
| Helper Docker access | PASS | Root helper reached Engine API 1.56 while the API identity remained denied. |
| Runtime directory | PASS | `root:kitpro-pb-api-test`, mode 0750. |
| Helper socket | PASS | `root:kitpro-pb-api-test`, mode 0660. |
| `SO_PEERCRED` | PASS | Helper authenticated UID 988 and explicitly reported that human authorization was not verified. |
| Wrong peer | PASS | Root could reach the socket DAC boundary but was rejected by the helper's fixed peer-UID policy. |
| Unrelated peer | PASS | `nobody` could access neither the helper socket nor Docker. |
| Protocol and filesystem suite | PASS | 35 of 35 tests passed on the Debian guest. |
| Dangerous input | PASS | Unknown, malformed, oversized, stale, raw-Docker, Compose, shell, path, mount, device, capability, namespace, security-option, and sysctl requests were rejected before Docker dispatch. |

## Docker lifecycle and ownership

| Check | Status | Observation |
| --- | --- | --- |
| Image identity | PASS | Discovery tag `docker.io/library/busybox:1.37.0`; deployed `docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0`, `linux/amd64`. |
| Safe container shape | PASS | UID/GID 65534, read-only root, all capabilities dropped, `no-new-privileges`, no devices, binds, host namespaces, or ports. |
| Lifecycle | PASS | Create, inspect, start, stop, restart, and remove worked through semantic helper operations. The corrected one-second BusyBox PID 1 loop exited normally with code 0. |
| Unrelated container | PASS | Helper rejected it and left it unchanged. |
| Missing expected labels | PASS | `OwnershipUnproven`; no mutation. |
| Missing helper record | PASS | `OwnershipUnproven`; no mutation. |
| Wrong instance ID | PASS | `OwnershipUnproven`; original object unchanged. |
| Wrong resource kind | PASS | `OwnershipUnproven`; no mutation. |
| Unknown Docker ID | PASS | `OwnershipUnproven`; no unrelated object touched. |
| Label-only resource | PASS | `OwnershipConflict`; no adoption or duplicate. |
| Helper-record-only resource | PASS | `OwnershipUnproven`; no other object touched. |
| Changed image identity | PASS | `OwnershipUnproven`; no mutation. |
| Unexpected network | PASS | `PolicyDenied`; additional attachment was removed explicitly. |
| Running foreign network member | PASS | `PolicyDenied`; foreign container remained running until exact test cleanup. |
| Stopped foreign network member | PASS | Docker network inspect reported zero members, the corrected all-container query reported two, and helper inspection returned `PolicyDenied`. |
| Unknown volume manipulation | PASS | Protocol contains no caller-selected volume operation; no Docker volume was created. |

Manual fault injection briefly hit systemd's service start-rate limit. Resetting the disposable unit and returning to socket-only activation allowed the matrix to continue. Starting the helper service directly is invalid because it requires an inherited systemd socket. This is a runbook/tooling observation, not a bypass or platform failure.

## Filesystem protection

| Check | Status | Observation |
| --- | --- | --- |
| Traversal and absolute path | PASS | Rejected on the Debian/Python/kernel path. |
| Symlink escape and replacement | PASS | Deterministic cases rejected. |
| Descriptor-relative containment | PASS | Helper created only the approved root-owned directory under its fixed storage root. |
| Mount boundary | PASS | A 1 MiB disposable tmpfs below the test root produced `ForbiddenPath` through `openat2` with `RESOLVE_NO_XDEV`, then was unmounted. |
| Hard-link-sensitive file mutation | NOT RUN | The prototype has no privileged file-replacement operation. |
| High-frequency race harness | NOT RUN | Deterministic replacement passed; no specialized rename/link/mount race harness exists. |

## Networking and host security

| Check | Status | Observation |
| --- | --- | --- |
| Per-instance network | PASS | One user-defined internal bridge, non-attachable, non-ingress, IPv6 disabled. |
| Network isolation | PASS | No host network or default-bridge attachment. |
| Port publication | PASS | No published ports and no Docker proxy/listener. |
| Unrelated networks | PASS | Built-in networks remained untouched. |
| Docker firewall behavior | OBSERVATION | Docker installed nftables-backed iptables chains. `DOCKER-INTERNAL` dropped traffic crossing outside the instance subnet. No KITPro rule changed host firewall policy. |
| IPv4/IPv6 loopback publication | NOT RUN | The constrained fixture intentionally exposes no port-publication operation. |
| AppArmor container profile | PASS | Running test container reported `docker-default (enforce)`. |
| Helper AppArmor profile | NOT RUN | The disposable helper had no dedicated AppArmor profile. This remains a future hardening question. |

## Restart, recovery, and cleanup

| Check | Status | Observation |
| --- | --- | --- |
| Helper restart and exact replay | PASS | Root-owned receipts survived; same UUID/body replayed, conflicting content returned `OperationConflict`. |
| Unknown and stale operation | PASS | Bounded `NotFound` and deadline errors. |
| Docker unavailable | PASS | Stopping both service and socket produced retryable `DockerUnavailable`; helper requests did not reactivate Docker. |
| Docker recovery | PASS | Explicit Docker start restored Engine inspection. |
| Persisted `running` receipt | PASS in guest suite | Restart converts it to `RecoveryRequired`; no automatic reconciliation is claimed. |
| Arbitrary real mutation interruption | NOT RUN | No deterministic interruption harness exists. |
| VM reboot | PASS | Boot ID changed; Docker, containerd, and helper socket returned. The helper remained demand-activated. |
| Receipt replay after reboot | PASS | Fixed operation ID replayed its completed response from durable fixture state. |
| Resource state after reboot | PASS | Managed container remained known and inspectable; it stayed stopped because the fixture intentionally configures no automatic container restart. |
| AppArmor after reboot | PASS | Restarted test container remained under `docker-default (enforce)`. |
| Cleanup | PASS | Managed container/network, exact controls, units, paths, staging, and temporary identity were removed. Docker ended with no containers or volumes and only built-in networks. |
| Temporary sudo rule | OBSERVATION | `/etc/sudoers.d/kitpro-validation` remains for milestone review and may now be removed by Josh. |

## Assessment

Debian 13 is technically viable as the Phase 1 reference host. Its direct Docker repository path, ext4 behavior, systemd socket semantics, Docker API behavior, ownership checks, AppArmor-backed default container profile, restart recovery, and cleanup all passed without granting the API identity Docker access or weakening a host security control.

The original installer-selected disk layout was unsuitable and required remediation. A production host check must inspect the filesystem that will hold `/var` and KITPro state rather than trusting nominal disk size. The lack of swap in this corrected disposable VM and the lack of a dedicated helper AppArmor profile are observations, not approved production defaults.
