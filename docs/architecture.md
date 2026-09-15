# KITPro Server architecture

## Status

This document explains the implemented alpha architecture and its Phase 1
constraints. It links to the accepted decisions that govern the production
implementation.

KITPro Server is the name of the public-alpha product. Each decision is
recorded in [`docs/decisions/`](decisions/README.md). The production API,
helper, Docker, state, catalog, exposure, multi-container, generated-secret,
hardware, and trusted-storage boundaries are implemented on the certified
alpha platforms. TLS automation, general rollback, and off-host backup remain
open.

ADR-0017 selects Debian 13 as the primary/reference host. Ubuntu Server 26.04
LTS and fully updated Arch Linux amd64 hosts using official repositories and
`linux-lts` are also supported after native-package certification. Rocky Linux
10 remains a secondary experimental host. The core helper and Docker
integration use Linux, systemd, Unix sockets, and Docker Engine contracts.
Distribution packaging, firewall integration, AppArmor profiles, and SELinux
policy stay outside those core contracts. ADR-0019 requires enforcing helper
MAC on supported hosts; Rocky must run with SELinux Enforcing. KITPro does not
disable enforcement to support it. ADR-0001 accepts one Go toolchain for
separate API and helper binaries; ADR-0020 accepts separate SQLite state files;
ADR-0021 accepts strict framed JSON.

## System boundary

The alpha covers one supported Linux host and trusted single- or
multi-container applications. The product supports this complete local
workflow:

1. Install KITPro Server on the host.
2. Open the local dashboard.
3. Detect and display host information.
4. Register approved external storage when an app needs it.
5. Deploy a supported single- or multi-container application from an immutable catalog release.
6. Display runtime, storage, and optional accelerator state.
7. Start, stop, recreate, and update it through typed operations.
8. Choose internal, loopback, or exact-address LAN exposure for declared services.
9. Remove the runtime without automatically deleting managed or imported user data.

The alpha does not include fleet management, custom hardware, KITPro OS,
mandatory accounts, a hosted control plane, a proprietary runtime, arbitrary
Docker configuration, or complete disaster recovery.

The 20 GiB free-space figure in ADR-0017 is a KITPro and system-capacity floor. Application data, media, photos, databases, backups, and other workload content need separate capacity planning.

Trusted external data follows the [trusted storage architecture](architecture/trusted-storage.md): administrators register roots, manifests request logical slots, and the helper owns canonical resolution, filesystem identity, mount mode, and reconciliation. Imported data is never part of application lifecycle deletion.

## Fixed constraints

Any proposed architecture must preserve these constraints:

- Core operations work on the local network without KITPro cloud services.
- The workload and its persistent data remain usable without KITPro.
- Workloads use normal OCI containers, standard host services, or both.
- The product does not conceal security reductions behind convenience features.
- Users can inspect the configuration and host changes that KITPro owns.
- Lifecycle operations identify recovery behavior before they run.
- Uninstall and persistent-data deletion are separate operations.
- Optional future cloud features cannot become a runtime dependency for local applications.
- The implementation must preserve the [`docs/security/invariants.md`](security/invariants.md) security invariants.

## Provisional responsibility map

The architecture must assign the following responsibilities. The list does not prescribe processes, services, languages, or deployment units.

| Responsibility | Required behavior |
| --- | --- |
| Local user interface | Presents host state, application state, planned changes, controls, logs, and recovery information. |
| Application lifecycle coordination | Validates and records install, start, stop, update, rollback, and uninstall operations. Prevents conflicting operations. |
| Host inspection | Reads supported host facts and distinguishes unavailable data from unhealthy state. |
| Workload integration | The privileged helper translates constrained lifecycle operations to an allowlisted subset of the Docker Engine API. The browser-facing service has no Docker socket access. |
| Privileged host changes | A root-owned helper accepts versioned semantic operations over a protected Unix socket. It authenticates the API service with kernel peer credentials and enforces policy again. |
| Application specification | Describes a trusted application, its source, configuration, health checks, networking, storage, secrets, and lifecycle behavior through the bounded manifest schemas. |
| State and operation history | The control plane stores desired state and user-facing metadata. The helper independently stores trusted ownership, privileged receipts, leases, and audit events. Fresh host and Docker inspection supplies observed state. The storage technologies remain open. |
| Logs and observations | Collects relevant KITPro, host, and workload information without presenting raw volume as useful diagnosis. |
| Data protection integration | Separates managed and imported data, creates validated control-state backups before migrations and trusted updates, and documents that imported data and full application recovery remain external responsibilities. |

No responsibility in this table implies that KITPro owns user application data. KITPro may record where data lives and how an operation affects it.

The disposable fixture under [`prototypes/privilege-boundary/`](../prototypes/privilege-boundary/) validates the protocol parser, peer-credential check, constrained Docker request construction, ownership failure behavior, and safe relative directory handling. The fixture under [`prototypes/reconciliation/`](../prototypes/reconciliation/) validates durable receipt transitions, unknown external outcomes, ownership disagreement, restart recovery, replay handling, and per-instance exclusion against a fake Docker adapter. The scripts under [`tools/platform-validation/`](../tools/platform-validation/) prepare repeatable candidate-host runs without adding distribution behavior to the protocol. These artifacts are not production code and do not choose a language. Candidate-host evidence is tracked in [`docs/testing/platform-comparison.md`](testing/platform-comparison.md). Debian 13 and Rocky Linux 10 both completed real-host runs, and Rocky retained SELinux Enforcing throughout.

## Required lifecycle behavior

### Deploy

A deployment must validate host compatibility, application inputs, required resources, storage targets, network exposure, and secrets before changing the host. The operation must record its progress and distinguish a failed deployment from a running but unhealthy application.

### Start and stop

Start and stop operations must be idempotent or report why a repeated request cannot be handled safely. The displayed state must distinguish the requested state from the observed runtime state.

### Update

An update must identify the current version, target version, persistent data, expected configuration changes, compatibility requirements, and available recovery path before it runs. The project must define what "safe update" means before implementing updates.

The alpha implementation keeps native apt/dpkg or pacman authoritative for
KITPro software upgrades. It reports build metadata at `/api/v1/version`,
creates validated SQLite backups before package migration, and exposes a
strict application-release update operation. Application updates accept only
catalog releases, preserve installation identity and storage, and advance the
runtime generation.

### Uninstall

Uninstall must remove the managed application components without automatically deleting persistent user data. Data deletion, if later supported, must be a separate operation with explicit scope and confirmation.

### Failure and restart

The system must not rely on a browser session to finish a host operation. The privileged helper owns accepted privileged work and records phase intent before each external mutation. After a process or host restart, KITPro observes deterministic resources before it retries an uncertain mutation. [ADR-0016](decisions/0016-durable-state-and-reconciliation.md) defines this model without selecting the storage technology.

## Architectural decision register

Each row is open unless its linked record says otherwise. A proposed record is not an accepted implementation decision. The project should not treat examples in the questions as selected technologies.

| ID | Decision | Questions the record must answer |
| --- | --- | --- |
| [ADR-0001](decisions/0001-production-implementation-stack.md) | Backend language and runtime, accepted | Go for API and helper; separate binaries from one toolchain. |
| [ADR-0002](decisions/0002-frontend-architecture.md) | Frontend approach, accepted | Server-rendered HTML with small local progressive enhancement. |
| [ADR-0003](decisions/0003-privileged-helper-protocol.md) | Privileged helper protocol, proposed | A root-owned, systemd socket-activated helper accepts framed, typed JSON over a protected Unix socket. It verifies `SO_PEERCRED`, revalidates policy, records idempotent operations, and exposes no generic root or Docker mechanism. |
| [ADR-0004](decisions/0004-docker-integration.md) | Docker integration, proposed | The helper calls an allowlisted subset of the Docker Engine API over its local Unix socket. Trusted helper state plus matching fresh Docker identity and configuration establish ownership. Catalog workloads receive constrained containers and one bridge network per application instance. |
| ADR-0005 | Application manifest or specification | What information defines an application and its lifecycle? How will the format support validation, versions, migrations, health checks, storage, networks, secrets, provenance, and human inspection without becoming a proprietary workload format? |
| ADR-0006 | Storage and volume handling | Where may persistent data live? How will KITPro handle permissions, ownership, mounts, capacity checks, moves, imports, exports, orphaned data, and uninstall preservation? |
| ADR-0007 | Secrets handling | Where will secrets originate and live? Which components may read them? How will the system avoid leaking them through logs, process arguments, generated files, backups, exports, and the interface? |
| ADR-0008 | Authentication | Who may open the local dashboard and authorize consequential operations? What is the bootstrap flow? Are local-only, LAN, and remote access separate trust modes? How are sessions and recovery handled? |
| ADR-0009 | Local TLS | How will the dashboard protect traffic on the local network? How will KITPro issue, trust, renew, replace, and recover certificates without teaching users to ignore browser warnings? |
| ADR-0010 | Networking and reverse proxy | How will KITPro allocate ports, expose applications, resolve names, route traffic, manage certificates, detect conflicts, and coexist with existing host networking or proxy configuration? |
| ADR-0011 | Update model | Who defines available KITPro and application updates? How will the system verify source and integrity, stage changes, handle database migrations, schedule downtime, and report partial failure? |
| ADR-0012 | Rollback strategy | Which artifacts can KITPro roll back: application images, generated configuration, secrets, persistent files, and database state? What preconditions and backup guarantees apply to each? |
| ADR-0013 | Logs and observability | Which KITPro, host, and workload events will the product collect? How will it bound retention, protect sensitive data, preserve timestamps, correlate operations, and provide useful health without requiring cloud telemetry? |
| ADR-0014 | Backup integration | Which backup responsibilities belong to KITPro? How will the product define data sets, consistency, scheduling, destinations, encryption, retention, verification, and restore tests while allowing standard external tools? |
| [ADR-0015](decisions/0015-api-design.md) | API design, accepted | Versioned REST/JSON with operation resources. |
| [ADR-0016](decisions/0016-durable-state-and-reconciliation.md) | Durable state and reconciliation, accepted | The control plane owns desired state. The helper independently owns trusted resource ownership, privileged receipts, leases, and audit events. Fresh observation resolves external reality. Unknown outcomes reconcile before retry. The database engine remains open. |
| [ADR-0017](decisions/0017-phase-1-host-compatibility.md) | Host compatibility, accepted | Debian 13 is primary/reference; Ubuntu Server 26.04 LTS and fully updated Arch Linux with `linux-lts` are supported; Rocky Linux 10 is experimental. All use the platform-neutral helper and Docker contracts. |
| [ADR-0018](decisions/0018-security-boundaries.md) | Security boundaries, proposed | What assets and actors are in the threat model? Which inputs cross browser, network, manifest, runtime, host, update, backup, and optional cloud boundaries? What is the response to a compromised application, dashboard session, update source, or privileged component? |
| [ADR-0019](decisions/0019-helper-mandatory-access-control.md) | Helper mandatory-access-control confinement, accepted | Debian, Ubuntu, and Arch production helpers require enforcing AppArmor; Rocky experimental helpers require an enforcing dedicated SELinux domain. Policy remains outside the common protocol. |
| [ADR-0020](decisions/0020-production-state-database.md) | Phase 1 production state database, accepted | Two physically separate SQLite files; PostgreSQL remains the fallback if one-node requirements are disproven. |
| [ADR-0021](decisions/0021-helper-protocol-serialization.md) | Helper protocol serialization, accepted | Strict length-framed JSON with canonical hashing. |

## Cross-decision requirements

The records above affect one another. The project must review them together before implementation starts.

- The threat model and supported-host policy constrain privilege separation, authentication, local TLS, networking, secrets, and updates.
- The helper protocol and Docker integration constrain the public API, operation state, application specification, storage references, and audit events.
- The application specification constrains the runtime integration, storage, networking, health, updates, rollback, and backup model.
- The lifecycle state model constrains the API, both state stores, operation recovery, logs, and interface.
- State-store selection must satisfy the requirements in [`state-database-requirements.md`](architecture/state-database-requirements.md) without collapsing desired, observed, ownership, operation, and audit state.
- Packaging and installation constrain the backend runtime, frontend delivery, privileges, host compatibility, and KITPro update path.

## Readiness for implementation-stack selection

The pre-language architecture is mature enough to compare implementation stacks. Must-resolve constraints are the accepted privilege boundary, durable receipt/reconciliation semantics, enforcing helper MAC, systemd hardening contract, no-child/no-IP helper model, and the state-store requirements. Language selection may resolve runtime-specific syscall filters, exact MAC library rules, and packaging details. Application catalog, authentication implementation, TLS, reverse proxy, backup engine, and production database remain implementation ADRs rather than blockers to beginning stack evaluation.

## Evidence required before implementation

Architecture selection needs evidence from small, disposable investigations. These investigations must not become the application by accident.

- Compare viable backend and frontend options against measurable packaging, resource, maintenance, and security requirements.
- Test the ADR-0003 Unix-socket identity, framing, replay, restart, filesystem, and policy boundary on candidate supported hosts.
- Verify the ADR-0004 Docker Engine API subset for lifecycle, ownership, storage, network, image, health, and log behavior.
- Test ADR-0016 receipt durability, fencing, state restoration, unknown-outcome recovery, and every destructive crash point against the selected storage engines.
- Validate the ADR-0019 helper profiles and combined systemd, DAC, semantic-policy, and Docker boundaries on both reference hosts.
- Run the same helper protocol on an SELinux-enforcing RHEL-family candidate before that family becomes a reference or supported host.
- Walk one real application through install, failed install, restart, update, failed update, rollback, uninstall, and data recovery.
- Validate the threat model against a hostile workload and a compromised unprivileged service on the same host.
- Test installation and cleanup on clean, supported host images.

An architecture decision is ready only when its record states the context, constraints, options, decision, consequences, validation plan, and conditions that would require review.
