# KITPro Server roadmap

This roadmap separates released alpha.12 behavior from future work. It does
not set delivery dates or turn direction into a commitment.

## Current state

The current release is `v0.1.0-alpha.12`. KITPro Server coordinates a trusted
application lifecycle on Debian 13 and Arch Linux. The API records durable
operations, and a narrowly privileged helper validates and applies host and
container changes.

KITPro records installation identity, desired state, runtime state, managed
storage identity, and reconciliation evidence separately. A running runtime is
not proof that an application is ready. Read the [current-state reference](product/kitpro-server-current-state.md)
for the full shipped boundary.

## Shipped foundations

Alpha.12 includes these foundations:

- local administrator setup and a local browser interface;
- a trusted, digest-pinned application catalog;
- single-component and multi-component application installations;
- durable operation identity, request hashing, leases, and fencing;
- runtime generations, staged replacement, and bounded reconciliation;
- explicit repair actions chosen from fresh helper evidence;
- managed persistent storage that survives runtime replacement;
- bounded backup and same-installation restore for managed application storage;
- generated-secret preservation within supported backup and restore;
- exact loopback or LAN service exposure without wildcard publication;
- guarded alpha.11 to alpha.12 upgrade wrappers;
- release checksums, an SBOM, build metadata, and frozen source identity; and
- native packages for the supported Debian and Arch baselines.

## Current limitations

Alpha.12 does not provide these capabilities:

- application-aware readiness checks;
- imported-storage backup;
- host-to-host restore or bare-host recovery;
- automatic rollback of irreversible upstream schema changes;
- automatic resolution of ambiguous ownership or mixed restore state;
- a complete browser workflow for every repair, backup, or recovery action;
- destructive application-data deletion;
- clustering, high availability, or automatic failover;
- public TLS or domain automation; or
- generic Docker or Compose administration.

Rocky Linux and Podman remain Experimental. Ubuntu has development and
validation evidence only. Neither is part of the supported alpha.12 baseline.
The [known limitations](release/known-limitations.md) document has the complete
public list.

## Near-term work

Near-term work should make the existing lifecycle easier to operate and
recover without weakening its safety boundaries:

- add application-aware readiness contracts where an application can support
  a reliable check;
- expose more reconciliation, repair, backup, restore, and action-required
  workflows in the browser;
- complete the explicit data-deletion lifecycle with previews and proof of the
  selected installation identity;
- improve recovery guidance and diagnostics for interrupted operations;
- expand upgrade and restore failure testing on supported hosts; and
- improve public documentation without promoting experimental platforms.

## Longer-term direction

Longer-term work may extend backup scope, recovery portability, platform
coverage, and network access. Any such work must preserve installation
identity, user-owned data, inspectable Linux and container foundations, and
explicit handling of uncertainty.

These items are direction, not shipped capability:

- imported-storage backup policies;
- reviewed host-to-host recovery;
- broader hardware and application coverage;
- public TLS and domain integration;
- additional supported operating systems and container runtimes; and
- optional remote operations that do not make local use depend on a KITPro
  cloud service.

The [historical Phase 1 roadmap](history/phase-1-roadmap.md) remains available
as a record of the plan that led to the current implementation.
