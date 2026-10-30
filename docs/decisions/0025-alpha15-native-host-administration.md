# ADR-0025: Native Host Administration for alpha.15

- Status: Accepted. Read-only foundation and narrowly controlled service operations are implemented; other privileged operations remain disabled.
- Date: 2026-10-28
- Owners: KITPro Server
- Related principles: existing security boundary, native Linux integration, Ubuntu support

## Options considered

An embedded Cockpit/Houston console would introduce another application and security model. An integrated KITPro module reuses the authenticated UI and typed API. The integrated module is selected for this read-only slice. Privileged interfaces remain unselected until security and recovery qualification.

## Context and ownership

KITPro Server owns Host Administration in its existing UI and API. It is not a second web application. The API's authenticated administrator session protects read endpoints. The API process may read bounded, non-secret kernel and OS facts; it may not gain root authority for this feature. A future helper extension would own any privileged operation and independently validate semantic requests, peer identity, resource policy, and audit receipts. The current helper protocol must never become a shell or arbitrary-path interface.

KITPro Linux Server remains a separate OS product with its own installer, immutable generation, signing, and release process. KITPro Server integrates through the published operating-system metadata contract and does not merge operating-system history. A future KITPro Linux release identity must be established by that separate release process.

## Modules and API

Controlled systemd mutations for explicitly approved noncritical services are implemented as described in [alpha15-systemd-service-management](../architecture/alpha15-systemd-service-management.md). They are disabled by default and do not authorize storage, network, unit-file, reboot, or arbitrary command operations.

A `hostadmin` backend provider reads host facts using bounded `/proc`, `/sys`, `os-release`, `statfs`, and standard network interfaces. The API exposes `GET /api/v1/host/overview`, protected by the existing session and host-origin guard. It returns an explicit collection state for each domain. A failed collector is `unavailable`, never zero or healthy. The page uses the existing navigation, styles, and client-side view pattern. No raw journal, arbitrary path, service action, network probe, or disk mutation is in this API.

The read-only `/api/v1/host/services` and `/api/v1/host/logs` families are implemented as described in [alpha15-services-logs](../architecture/alpha15-services-logs.md). The read-only `/api/v1/host/storage` inventory is implemented as described in [alpha15-storage-inventory](../architecture/alpha15-storage-inventory.md). The read-only `/api/v1/host/network` inventory and policy-restricted diagnostics are implemented as described in [alpha15-network-administration](../architecture/alpha15-network-administration.md). The bounded System Health evaluator and unified read-only Dashboard are implemented as described in [alpha15-system-health-dashboard](../architecture/alpha15-system-health-dashboard.md). The existing single administrator role may read the initial host inventory; introducing other roles requires separate `host.inspect`, `host.logs`, and narrowly scoped `host.mutate.*` permissions, with deny by default. Frontend visibility never grants authority.

## Capability matrix

| Platform | Status | Overview kernel facts | systemd/journal | Storage and network mutation |
| --- | --- | --- | --- | --- |
| Debian 13 | Supported | Read-only foundation | Planned, subject to D-Bus/journal access | Disabled |
| Ubuntu 26.04 LTS | Supported, mandatory | Read-only foundation | Planned, subject to D-Bus/journal access | Disabled |
| Arch Linux with linux-lts | Supported | Read-only foundation | Planned, subject to D-Bus/journal access | Disabled |
| KITPro Linux | Supported integration target | Capability-aware Host Administration | Policy controlled | Disabled by default |
| Rocky Linux 10 | Experimental | Read-only foundation where readable | Planned, SELinux policy required | Disabled |

Capability detection is per facility, not by distribution name alone. NetworkManager, systemd-networkd, Netplan, UDisks2, LVM, Btrfs, ZFS, AppArmor, and SELinux must be reported as available, unavailable, unsupported, or not configured according to observed interfaces and permissions. An installed binary alone does not establish an active backend. Native D-Bus/Netlink or supported library interfaces are preferred for later service, journal, storage, and network providers. No CLI output parser or general command runner is approved.

## Privilege, safety, and recovery

Read-only collectors have fixed paths, bounded input and output, request timeouts, and no credentials. Host strings and future journal text are untrusted and must be escaped in HTML. Raw journals and secrets must not enter support bundles or audit records. Reads of `/proc`, interfaces, and mounts can race; stale or partial data is shown as such. No writable host state is added to backup archives by this slice.

Implemented service actions accept an enumerated action and a validated unit identity, and the helper denies KITPro API/helper, ingress, SSH, networking, storage, container runtime, and essential systemd identities. An exact operator allowlist and fail-closed metadata resolution bound remaining indirect effects. Every accepted action carries administrator identity, operation ID, previous and resulting observed state, timestamp, outcome, and redacted failure reason. Service operation replay and uncertain outcomes use the existing durable operation model.

Persistent mounts require target identity, dependency checks, configuration backup, atomic staged change, post-check, rollback, and same-host recovery. Root, boot, EFI, swap, KITPro state, container data, and active app volumes are protected. Destructive disk operations are outside alpha.15. Network changes remain read-only until an independently qualified staged transaction snapshots configuration, confirms management connectivity, times out to automatic rollback, and proves rollback after process and host failure. No live network mutation is authorized here.

The threat model includes session theft, CSRF, helper protocol abuse, malicious unit names, path traversal/symlinks, dependency disruption, journal secret leakage, unbounded queries, race conditions, and loss of management connectivity. Existing session, origin, CSRF, peer-credential, MAC, and audit controls are reused; each new privileged path must be tested against these threats before activation.

## KITPro Linux capability contract v1 (proposed)

The proposed detailed contract is [alpha15-host-capability-contract-v1](../architecture/alpha15-host-capability-contract-v1.md).

The OS may advertise a versioned, non-secret host-capability document from its package-owned release metadata. KITPro Server must still probe actual runtime facilities and permissions. The contract records OS identity/generation, package schema, service units, helper socket ownership, available D-Bus/journal facilities, network backend, storage adapters, security enforcement, upgrade behavior, and recovery prerequisites. Unknown contract versions only permit generic read-only inspection. Package changes must follow existing KITPro Linux generation and Installer 1.1 RC1 composition paths; no new management service is implied. Same-host restore may reapply validated KITPro settings, while cross-host restore cannot replay storage/network identity without revalidation.

## Qualification and release gates

Phase A verifies repository lineages, API/helper boundaries, platform matrix, and this ADR. Phase B qualifies the read-only overview and authentication on Debian, Ubuntu, Arch, and current KITPro Linux development media; Rocky remains experimental. Phases C and D add bounded service/journal and storage/network visibility. Phase E requires separate security, dependency, rollback, audit, and failure-path evidence before any mutation. Phase F validates the package against the supported KITPro Linux package contract. Phase G reruns application-management, upgrade, backup/restore, and platform acceptance. Disposable VMs are required for disruptive failure tests; Athena and production hosts are excluded. No alpha.15 publication follows from local tests.

## Review conditions

Revisit the provider or API contract if platform qualification exposes missing data permissions, if the Linux package contract changes, or before any privileged service, storage, or network action is enabled.
