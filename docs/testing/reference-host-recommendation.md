# Phase 1 reference-host recommendation

## Approved decision

**Debian 13 is the Phase 1 primary/reference platform. Rocky Linux 10 is a secondary experimental platform.**

Josh approved this decision from the measured evidence below. [ADR-0017](../decisions/0017-phase-1-host-compatibility.md) now records the support boundary.

## Why Debian is primary

Debian 13.7 completed the full real-host workflow after its installer-created disk layout was corrected:

- Docker's repository directly documents Debian 13.
- The installer, Engine API, systemd socket activation, `SO_PEERCRED`, Docker authority split, and restart/reboot behavior passed.
- The complete ownership matrix failed closed, including stopped foreign network endpoints that Docker network inspection omitted.
- Descriptor-relative path checks worked on ext4 and rejected traversal, links, and a nested mount boundary.
- Docker's default AppArmor profile was loaded in enforce mode and applied to the test container without additional daemon configuration.
- No API account received Docker-group access, and no host security control was weakened.
- Debian supports a broader amd64 hardware population than Rocky Linux 10's x86-64-v3 baseline.

The main Debian defect was the initial storage layout, not the platform mechanics. A 40 GiB virtual disk yielded only 7.0 GiB free on the root filesystem because most capacity went to `/home`. KITPro installation must check the actual filesystem that contains `/var`, Docker metadata, packages, logs, and KITPro state. The corrected validation root had 36.6 GiB free.

Debian's reference image and installation guidance still need an explicit swap decision. The validation VM has no swap after the partition correction; that is not an approved production default.

## Why Rocky is experimental

Rocky Linux 10.2 is technically viable. Its corrected run proved:

- systemd, Unix sockets, peer credentials, Docker Engine API access, XFS path enforcement, ownership safeguards, firewalld coexistence, and restart/reboot behavior;
- Docker container SELinux confinement while the host remained Enforcing;
- normal workload labels in `container_t` with `container_file_t` mounts and MCS categories; and
- no need to disable SELinux, create a permissive domain, weaken firewalld, or grant Docker-group access.

Rocky nevertheless adds Phase 1 obligations that Debian does not:

- Docker documents RHEL, not independent Rocky certification, so repository and upgrade compatibility remain KITPro's responsibility.
- `container-selinux` was installed but did not enable Docker labeling. The installer must explicitly manage or verify `selinux-enabled` without overwriting administrator daemon configuration.
- The helper still ran as `unconfined_service_t`; a dedicated executable domain and state/runtime/socket types remain untested.
- Rocky 10 requires x86-64-v3 on AMD/Intel, excluding some repurposed machines.
- firewalld and Docker's forwarding policy require additional user-facing diagnostics and support knowledge.
- The reference VM lacked a working QEMU guest agent and persistent journal configuration during the initial run.

Those are manageable for a secondary validation track. They are unnecessary risk for the first application-management vertical slice.

## Comparative evidence

| Criterion | Debian 13 | Rocky Linux 10 |
| --- | --- | --- |
| Corrected host capacity | PASS | PASS |
| Docker vendor path | Direct Debian support | RHEL path used as derivative compatibility |
| Installer and verification | PASS | Initial package/repository workflow PASS; corrected SELinux-aware installer clean-snapshot rerun NOT RUN |
| Docker authority boundary | PASS | PASS |
| Helper protocol and peer credentials | PASS | PASS |
| Full ownership safeguards | PASS | PASS |
| ext4/XFS path safeguards | PASS | PASS |
| Internal/no-port network default | PASS | PASS |
| Docker outage and recovery | PASS | PASS |
| Full VM reboot | PASS | PASS in guest; hypervisor guest-agent control failed on Rocky |
| Container mandatory-access control | AppArmor `docker-default` enforced | SELinux `container_t` enforced after remediation |
| Dedicated helper MAC policy | PASS test-only AppArmor; production packaging/upgrades NOT RUN | PASS test-only dedicated domain under Enforcing; production packaging/upgrades NOT RUN |
| Older amd64 suitability | Better | Limited by x86-64-v3 |
| Security compromise required | None | None |

## Next pre-production work

Production implementation has not started. Complete the next work in this order:

1. Complete production packaging/lifecycle validation for the Debian AppArmor profile and Rocky SELinux policy (Rocky remains experimental).
2. Review the production systemd unit, including the `RestrictSUIDSGID` and `openat2` interaction.
3. Select the production implementation language against explicit security, packaging, maintenance, and runtime-integration requirements.
4. Begin the first vertical slice only after the prior decisions are accepted.

## Decision threshold

A supported host must not require Docker socket access for the unprivileged API, a remote helper, a generic root RPC, mutable-tag-only deployment, adoption or deletion of unknown resources, SELinux disablement, or global firewall weakening. Neither completed run crossed those boundaries.

See the [platform comparison](platform-comparison.md), [Debian completed result](results/2026-09-12-debian13-completed-validation.md), [Rocky initial result](results/2026-09-12-rocky10-validation.md), [Rocky SELinux follow-up](results/2026-09-12-rocky10-selinux-followup.md), and [Rocky SELinux analysis](selinux-rocky.md).

The remaining work is in the [Debian security-validation backlog](../follow-ups/debian-security-validation.md) and [Rocky experimental-support backlog](../follow-ups/rocky-experimental-support.md).
