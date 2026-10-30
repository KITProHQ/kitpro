# ADR-0016: Durable state and reconciliation

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Approval: Architectural direction approved by Josh after final consistency review
- Related principles: Local-first, No lock-in, Secure defaults, Inspectability, Reversibility
- Related decisions: ADR-0003, ADR-0004, ADR-0005, ADR-0006, ADR-0011, ADR-0012, ADR-0013, ADR-0015, ADR-0017, ADR-0018

## Context

KITPro operations cross process, filesystem, Docker, and host-restart boundaries. A request can reach Docker and lose its connection before either side records the result. Docker objects can also change while KITPro is stopped or between an observation and a mutation.

No single record proves the whole system state:

- an API request proves intent, not execution;
- a helper receipt proves recorded progress, not current Docker reality;
- a Docker object proves existence, not KITPro ownership;
- labels are attacker-editable runtime metadata, not authority; and
- a database record can be stale, corrupt, restored from an old backup, or written by a compromised process.

KITPro needs a state model that makes uncertainty explicit. It must recover without duplicating resources, adopting foreign objects, deleting persistent data, or reporting success from an unverified request.

## Decision

KITPro will use a two-owner durable state model:

1. The unprivileged control plane owns administrator intent, desired application state, catalog references, and user-facing operation metadata.
2. The privileged helper owns trusted Docker and filesystem ownership records, privileged operation receipts, and helper-produced audit events.

The helper uses a small durable store that is independent of the control-plane database. The implementation selects separate SQLite databases for the control plane and helper. A control-plane row cannot grant Docker ownership or destructive authority.

The helper's receipt is authoritative for whether the helper accepted a privileged operation and which privileged phases it recorded. Current host and Docker state still comes from a fresh observation. The user-facing operation view joins the control-plane request, helper receipt, current observation, and audit events without collapsing them into one status field.

KITPro does not claim exactly-once external mutation. It records intent before dispatch, inspects after dispatch, and reconciles any uncertain result before retrying. Destructive operations fail closed when ownership or external outcome is ambiguous.

### Implemented release-candidate boundary

The schema-13 implementation realizes this decision with `helper_operations`,
`helper_operation_events`, `installation_leases`, `runtime_generations`,
`runtime_components`, `lifecycle_component_steps`, `reconciliation`,
`restore_operations`, and `restore_storage_steps`. The older `receipts`,
`ownership`, and `component_ownership` tables remain migration and forensic
evidence for public alpha.11 upgrades.

The API uses one `op-` identifier for semantic operation identity and a new
`req-` identifier for each helper exchange. The helper accepts only the closed
protocol-v2 mutation contract, hashes the canonical secret-free request,
serializes each installation with a durable lease, and checks the fencing token
at state transitions. Different installations are not globally serialized.

## Installation and runtime identity

The catalog application ID is stable product identity. An installation ID is generated once per user-installed application and owns persistent storage, desired state, and configuration. Each container/network incarnation has a monotonically increasing runtime generation. Removing runtime preserves the installation and data; explicit recreation advances only the runtime generation. Data deletion is a separate future destructive action.

## State categories

Each category has a separate owner and purpose.

| Category | Meaning | Durable owner | May authorize privileged mutation? |
| --- | --- | --- | --- |
| Desired state | What an approved application instance should become | Unprivileged control plane | No; it is an input to helper policy |
| Observed state | A timestamped account of current Docker, filesystem, mount, service, and host state | Derived by the helper or a constrained observer | No; it can satisfy a current precondition |
| Ownership state | What the helper independently recorded as KITPro-managed, including stable identity and observed external IDs | Privileged helper | Yes, only when fresh observation also agrees |
| Operation state | Which privileged action the helper accepted, its phase, outcome confidence, and recovery disposition | Privileged helper | Yes, within the accepted operation and policy |
| Audit state | What was requested, accepted, changed, denied, recovered, or overridden | Each trust boundary emits its own events | No |

### Desired state

Desired state records stable KITPro identifiers and declarative intent. It can state that an instance exists, uses an approved release and image digest, has a private network, preserves named storage, and should be running or stopped.

Desired state does not contain Docker object IDs. A compromised API can ask for an allowed semantic change, but it cannot make a foreign Docker object owned by inserting an ID into its database.

### Observed state

An observation contains its source, collection time, Docker daemon identity, and enough normalized configuration to evaluate policy. It can report missing objects, runtime state, image identity, labels, network membership, mounts, security settings, health, and filesystem or mount identity.

Observations expire. A mutation rechecks every security-sensitive precondition immediately before dispatch. A cached dashboard observation never authorizes a later mutation.

### Ownership state

The helper creates an ownership intent before it asks Docker or the filesystem to create a resource. The record contains:

- the KITPro installation ID;
- the application instance ID;
- the resource kind and role;
- the deterministic expected name or storage reference;
- the expected configuration hash and immutable image digest when applicable;
- the operation that created or prepared the resource;
- the observed Docker object ID, mount ID, or filesystem identity after verification;
- the expected managed-label values; and
- the ownership lifecycle, such as prepared, confirmed, retained, orphaned, or tombstoned.

Prepared ownership intent plus an exact fresh observation can resolve a crash after creation. Labels without that intent cannot. Confirmed state without matching Docker identity, labels, name, configuration, and network membership cannot.

### Operation state

Operation state records accepted privileged work. It is separate from application desired state and current health. A failed update does not make the application's desired state or observed runtime state unknowable.

### Audit state

Audit events form a logically append-only history. A correction or override adds an event. It does not edit the earlier event. Physical append-only enforcement, export, retention, and tamper evidence remain part of the production storage and audit decisions.

## Operation receipt model

Every privileged mutation has an API-generated semantic `operation_id` in the form `op-` plus 32 lowercase hexadecimal characters. The helper binds that ID to the canonical request hash on first acceptance. A separate `req-` transport ID is excluded from the hash.

A receipt contains at least:

| Field | Purpose |
| --- | --- |
| Protocol and operation revision | Selects the exact semantic contract used for recovery |
| Operation ID | Stable idempotency and audit identity |
| Operation type | Names the allowlisted semantic operation |
| Installation and instance IDs | Scope the operation without accepting Docker IDs |
| Caller service identity | Records the kernel-authenticated API identity |
| Administrator correlation | Carries untrusted audit context; it does not grant helper authority |
| Canonical request hash | Rejects conflicting reuse of the operation ID |
| Accepted and updated timestamps | Orders evidence and detects stale work |
| Started and completed timestamps | Records execution lifetime when known |
| Lifecycle state | Records accepted, executing, reconciling, terminal, or action-required state |
| Current phase and phase attempt | Locates the last durable transition |
| External-outcome confidence | Distinguishes not dispatched, confirmed applied, confirmed absent, and unknown |
| Recovery disposition | States whether retry, reconciliation, or administrator action is allowed |
| Expected resource identities | Supports exact post-crash observation |
| Resources created, observed, retained, or removed | Records IDs only after helper observation |
| Failure class and bounded detail | Separates policy, conflict, dependency, corruption, and unknown-outcome failures |
| Rollback reference and limits | Identifies the retained runtime or image and states what rollback cannot restore |
| Audit event references | Connects the receipt to helper-produced evidence |

Receipts do not store secret values, registry credentials, environment values, authorization headers, or unbounded Docker responses. A request hash must be computed from a canonical secret-free semantic request. Secret inputs use stable opaque references and separate protected handling.

### Lifecycle states

The state machine uses these concepts:

| State | Meaning |
| --- | --- |
| `accepted` | The helper durably bound the operation ID and request but has not dispatched an external mutation |
| `executing` | A phase intent is durable and execution is active |
| `reconciling` | An external outcome or prior observation is uncertain; no blind mutation is allowed |
| `succeeded` | Every required effect and postcondition was freshly verified |
| `failed` | The helper proved the operation did not reach its requested result and recorded the safe next action |
| `action_required` | Ownership, security, state corruption, or external outcome cannot be resolved automatically |
| `cancelled` | Cancellation occurred before mutation or at a declared safe point |
| `superseded` | An administrator explicitly closed or replaced non-running work; the original evidence remains |

Lifecycle state alone does not describe retry safety. Each receipt also stores one recovery disposition:

- `completed`: no retry is applicable;
- `retry_after_validation`: a fresh observation proved that repeating the named phase cannot duplicate or destroy a resource;
- `reconcile_first`: inspect deterministic identities before any dispatch;
- `administrator_action`: an authorized person must choose a documented recovery action; or
- `retry_forbidden`: policy or ownership evidence prohibits retry.

An exact replay returns the existing receipt. Reusing the same operation ID with a different request hash returns `OperationConflict`. A superseded operation never resumes.

## Durable phase protocol

Each external mutation follows this order:

1. Acquire the required durable lease and a new fencing token.
2. Observe current external state and validate ownership and policy.
3. Write and commit the phase intent, expected identity, precondition evidence, and `outcome=not_dispatched`.
4. Mark the phase `dispatching` in durable state before calling the external system.
5. Send the fixed semantic mutation.
6. Observe the external system through a separate read.
7. Compare the observation with the expected full configuration.
8. Commit the phase result, ownership changes, and audit event in one local transaction where the store permits it.

A crash after step 4 creates an unknown external outcome. Recovery starts at step 6, not step 5. A successful Docker response is evidence, but the helper still verifies the resulting object before recording success.

## Multi-step operations

### Install application

The install operation uses these phases:

1. validate the accepted intent and supported host;
2. resolve the approved catalog release and immutable image digest;
3. verify or pull the approved image;
4. prepare and record persistent storage without deleting existing data;
5. prepare ownership intent and create the deterministic application network;
6. prepare ownership intent and create the deterministic container generation;
7. start the container;
8. verify configuration, network membership, and requested runtime state; and
9. commit confirmed ownership and the final desired-state generation.

Image pull and inspection do not grant ownership of a shared image. Storage preparation records the stable storage identity before container creation.

### Update application

The update operation uses a new deterministic runtime generation rather than mutating an unknown container in place:

1. validate current desired state, ownership, and observation;
2. resolve and pull the approved target digest;
3. record the current confirmed generation and rollback limits;
4. prepare the new generation and its ownership intent;
5. stop or isolate the old runtime only at the declared cutover phase;
6. start and verify the new generation;
7. commit the new desired generation only after exact runtime verification; and
8. retain the old runtime or image reference for the defined rollback window.

Runtime-running verification is not application readiness. Readiness can only
be claimed when a trusted manifest supplies a supported check; the current
catalog does not yet define one. An image rollback does not imply a
persistent-data rollback. If an application migration changes data
incompatibly, the operation requires the backup and rollback guarantees defined
by later ADRs.

### Uninstall application

The uninstall operation uses these phases:

1. validate desired state, ownership, the complete target set, and current observation;
2. write tombstone intent for the exact disposable resources;
3. stop the owned runtime;
4. remove the exact owned container;
5. verify that no foreign member exists, then remove the exact KITPro-owned ephemeral network;
6. retain persistent storage and its ownership history; and
7. commit the desired state as absent while recording retained data.

Persistent-data deletion is a different operation with a different authorization and recovery design. Normal uninstall cannot enter that operation implicitly.

Before the first destructive call, the helper verifies every target. If any target has ambiguous ownership, the whole destructive set stops before deleting the first resource.

## Crash and recovery matrices

### Install

| Interruption point | Evidence after restart | Classification | Safe next action |
| --- | --- | --- | --- |
| Before any mutation | Accepted receipt; no phase dispatched | Safe to retry | Revalidate host and desired state, then execute |
| After storage creation | Prepared storage record; path and mount may exist | Requires reconciliation | Verify descriptor-relative path, mount identity, owner, mode, and storage record; continue only on exact match |
| After network creation | Prepared network intent; Docker outcome may be unknown | Requires reconciliation | Inspect the deterministic name, labels, full network configuration, foreign members, and daemon identity |
| After container creation | Prepared container intent; object ID may be missing | Requires reconciliation | Inspect by deterministic name and exact expected configuration; record Docker ID only after full match |
| After container start | Container may be running, stopped, or missing | Automatically recoverable only when ownership and configuration match | Inspect and resume exact runtime verification; retry start only if stopped and the start preconditions still hold. Do not infer application readiness. |
| Docker reported success before the helper recorded it | Phase remains dispatching | Requires reconciliation | Observe first; never issue create again from the stale receipt |
| After an intermediate receipt commit | Durable phase and external state may differ | Requires reconciliation | Treat the receipt as intent evidence and Docker as observation; compare both |

### Update

| Interruption point | Evidence after restart | Classification | Safe next action |
| --- | --- | --- | --- |
| Before runtime mutation | Target digest may be cached | Safe to retry | Revalidate release and current ownership |
| After new-generation creation | Old generation remains authoritative | Requires reconciliation | Verify the new deterministic generation; remove nothing on mismatch |
| After old runtime stops | Old and new generations may both be stopped | Recoverable | Verify both, then resume the recorded cutover or request administrator action if health or data preconditions changed |
| After new runtime starts | Desired generation may still point to old | Requires reconciliation | Verify exact new configuration and health before committing desired state |
| After desired generation commits | Cleanup or rollback retention may be incomplete | Automatically recoverable for non-destructive bookkeeping | Preserve both generations until ownership and rollback-window checks complete |
| During an irreversible data migration | Image state cannot prove data state | Administrator action required | Stop automatic progress and follow the application-specific backup or repair plan |
| Docker result is unknown | Dispatching phase lacks verified result | Requires reconciliation | Inspect both deterministic generations and their full configuration before any retry or rollback |

### Uninstall

| Interruption point | Evidence after restart | Classification | Safe next action |
| --- | --- | --- | --- |
| Before mutation | Tombstone intent may exist; resources remain | Safe to retry | Revalidate the complete destructive set |
| After container stop | Owned container remains | Recoverable | Verify ownership and continue removal if the approved uninstall is still current |
| After container removal | Docker may report missing while the receipt is dispatching | Requires reconciliation | Prove that the exact recorded object is absent; never select a replacement by name alone |
| After network removal | Container must already be absent and data retained | Requires reconciliation | Verify exact network absence and retained storage identity, then continue bookkeeping |
| Before desired state becomes absent | Runtime resources may already be gone | Automatically recoverable when absence is proven | Commit absent desired state and retained-data record |
| Any ownership or foreign-member conflict | Destructive target set is no longer proven | Administrator action required | Block all remaining deletion and show the conflicting evidence |

### Process and host interruptions

| Event | Required behavior |
| --- | --- |
| API restart | The API reads helper receipts by operation ID and rebuilds its user-facing view. Accepted helper work does not depend on the browser or API process. |
| Helper restart | Startup marks stale executing receipts for reconciliation, preserves leases as stale evidence, and performs no blind mutation. |
| Docker restart | In-flight calls become unknown outcomes. The helper waits for explicit Docker availability, records fresh daemon identity, and reconciles targets. |
| Host reboot | The same startup process runs after durable stores recover. Socket activation does not imply that operations resume before reconciliation. |
| Client disconnect | Accepted work continues or reaches a durable recovery state. A disconnect is never reported as cancellation or rollback. |

## External outcome uncertainty

When a Docker connection fails after dispatch, the helper records `outcome=unknown` and `recovery=reconcile_first`. It then:

1. obtains a fresh Docker daemon identity and API compatibility result;
2. queries by the deterministic resource name and the narrow managed-label filter;
3. compares the candidate with the prepared ownership intent;
4. verifies the object ID when one was previously observed;
5. verifies every security-sensitive field, including image digest, mounts, namespaces, capabilities, security options, devices, ports, and network membership; and
6. classifies the result as exactly applied, definitely absent, incomplete, conflicting, or unverifiable.

An exact match lets the helper record the previously unknown mutation as applied. Proven absence can make a create phase eligible for retry. An incomplete or conflicting object requires administrator action or a separately authorized cleanup operation. The helper never retries a create, remove, or replace call merely because the connection failed.

## Deterministic resource identity

KITPro stable identifiers remain authoritative within KITPro:

- `installation_id` distinguishes independent KITPro installations;
- `installation_id` is assigned before any resource exists and remains stable
  across runtime removal and recreation;
- `resource_role` is a closed value from the application plan;
- `runtime_generation` identifies one immutable runtime generation and may
  advance without changing the installation or its persistent storage;
- Docker container and network names are derived from the installation, instance, role, and generation identifiers with a bounded collision-resistant encoding;
- `storage_id` identifies persistent data independently of a container generation; and
- `operation_id` is an `op-` identifier unique to one semantic request.

Docker object IDs, mount IDs, filesystem device and inode observations, and daemon IDs are recorded as observed identifiers. They are never accepted from the browser-facing API as lifecycle targets. A changed Docker object ID triggers full reconciliation even if the deterministic name and labels match.

The exact production naming encoding and reverse-domain label namespace remain open. Production resource creation cannot use the prototype namespace.

## Reconciliation process

Phase 1 uses bounded reconciliation, not an autonomous repair loop.

Reconciliation runs:

- at helper startup for nonterminal receipts and held leases;
- at control-plane startup to rebuild user-facing operation views;
- before and after every privileged mutation for the target instance;
- after Docker becomes available following an outage;
- when an administrator requests an explicit inspection; and
- periodically as an observation-only scan with bounded frequency and scope, if later performance tests justify it.

Startup and periodic reconciliation do not perform destructive mutations. An operation may continue automatically only when its existing receipt authorizes the phase and current evidence proves the retry or completion rule. Other repair requires a new administrator-authorized operation.

Every observation has a timestamp, source, Docker daemon identity, and sequence or generation where available. A stale observation can inform the dashboard but cannot satisfy a mutation precondition.

## Drift classification

| Drift class | Example | Default response |
| --- | --- | --- |
| Consistent runtime | Docker restarted the exact owned container and all policy fields still match | Record the observation; no repair; do not infer application readiness |
| Recoverable | The exact owned container should run but is stopped | Report; an existing authorized recovery phase may restart it after fresh checks, otherwise require an explicit start |
| User modification | An administrator recreated or changed a managed container | Block automatic mutation and show the configuration difference |
| Ownership conflict | Labels claim KITPro ownership but helper state is missing or disagrees | Quarantine from mutation; never adopt or delete automatically |
| Security drift | An unexpected network, mount, capability, device, namespace, port, or security option appears | Block lifecycle and destructive operations; emit a high-priority audit event |
| Missing resource | A recorded container or network no longer exists | Report missing; recreate only through a new or safely recoverable operation after storage checks |
| Dependency unavailable | Docker, a mount, or an approved image source is unavailable | Preserve state and retry reads with bounds; do not infer mutation outcome |
| State corruption | A receipt, ownership record, or store integrity check fails | Stop privileged mutation for the affected scope and require recovery |

Automatic action is limited to recording observations, finalizing an exact proven result, releasing a proven stale lease, and continuing a previously authorized phase marked safe after fresh validation. The reconciler does not continuously enforce desired state, overwrite user changes, remove conflicts, or adopt labeled resources.

## Concurrency and locking

The helper enforces mutation serialization. API locks are advisory and improve user feedback, but they are not a security boundary.

- One durable lease permits one mutating operation per application instance.
- A global host-mutation lease protects shared host changes that cannot run safely in parallel.
- Read-only observations may run concurrently when they cannot race a mutation's authorization snapshot.
- Each lease records the operation ID, instance ID, acquisition time, heartbeat or update time, and monotonically increasing fencing token.
- Every committed phase checks the current fencing token. An executor with an old token cannot commit after a replacement executor takes ownership.
- A process death does not erase the lease. Startup marks it stale and reconciles the associated operation before release or transfer.
- Lease expiry alone never proves that an external mutation stopped. It permits investigation, not blind takeover.

Operations on different instances may run concurrently unless they touch a shared host resource, shared storage, the same port allocation, or a global runtime transition. Docker daemon restart and KITPro package update require the global host lease.

The components use the leases in this order:

1. The API records desired state and a user-facing request. Its in-memory lock is advisory.
2. An unprivileged operation coordinator submits the fixed semantic request and polls the helper receipt. It cannot commit a privileged phase.
3. The helper accepts the request, acquires the durable lease, assigns the fencing token, and executes each privileged phase.
4. The reconciler runs through the same helper-owned state machine and lease checks. It is not a second writer that bypasses operation policy.

An API or coordinator restart cannot release a helper lease. A helper restart cannot resume a stale executor until reconciliation determines whether the external mutation happened.

## State ownership boundary

### Unprivileged control plane

The control plane owns:

- desired application and runtime state;
- approved catalog release references;
- administrator-facing request and operation metadata;
- non-sensitive application configuration references;
- presentation preferences and cached observations; and
- links to helper operation and audit identities.

Compromise or loss of this state cannot create helper ownership. The helper treats every requested operation as untrusted even when the control-plane database says it was approved.

### Privileged helper

The helper owns:

- trusted resource ownership records;
- prepared ownership intent;
- privileged operation receipts and phases;
- observed Docker object and daemon identifiers;
- filesystem and mount identity used for privileged path decisions;
- durable leases and fencing tokens; and
- helper-produced audit events.

The helper store must be writable only by the helper identity and installation or recovery tooling. The API receives bounded projections, not direct file or database access.

## State loss and corruption

| Loss condition | Behavior |
| --- | --- |
| Control-plane state lost, helper state intact | Stop automatic desired-state changes. Expose a read-only helper inventory through a recovery flow. Restore the control-plane backup or create explicit new intent linked to verified helper records. |
| Helper state lost, control-plane state intact | Remove all destructive authority. Treat labels and API rows as discovery hints only. Restore a verified helper backup or use explicit per-resource recovery and adoption. |
| Both stores lost, Docker intact | Treat all Docker objects as unmanaged. Do not adopt, update, or delete them automatically. Standard Docker operation remains possible without KITPro. |
| Docker state lost, KITPro state intact | Mark runtime resources missing. Preserve storage records. Recreate only from approved desired state through a new operation after mount and ownership checks. |
| Docker daemon identity changes | Invalidate cached observations and reconcile every affected ownership record before mutation. |
| Store corruption detected | Stop mutations in the affected scope, preserve the corrupt artifact for diagnosis, and require restore or explicit recovery. |

Recovery never reconstructs destructive authority from labels alone. A future adoption flow must compare the complete effective configuration, image digest, network membership, storage identity, installation identity, and absence of security drift. It requires explicit administrator approval and a helper-produced audit event.

## Manual recovery operations

The implemented API exposes narrow workflows rather than a generic state editor:

- reconcile an installation and inspect component evidence;
- start an exact stopped active generation;
- recreate a missing runtime as a new generation from trusted desired state;
- clean exact non-active runtime resources while preserving storage; and
- acknowledge a missing retained generation without pretending it still exists.

Ambiguous ownership, mixed restore trees, configuration drift, and an
unreachable runtime remain action-required conditions. The root-local
`kitpro-helper --resolve-operation <operation-id> --release-as-failed` command
can close an action-required operation only as failed; it does not assert that
the requested runtime state was achieved.

Each workflow previews its scope, names irreversible effects, requires fresh authentication and authorization at the control plane, and produces control-plane and helper audit events. The helper still validates the operation independently. Administrator metadata does not become a second helper authentication factor.

## Audit and event model

The system records these event kinds:

- operation requested and rejected by the control plane;
- operation accepted or rejected by the helper;
- operation phase prepared, dispatched, verified, failed, or marked uncertain;
- resource ownership prepared, confirmed, retained, tombstoned, or removed;
- drift discovered and classified;
- ownership conflict or security drift detected;
- recovery started and completed;
- administrator override, adoption, supersession, or stale-record removal; and
- operation succeeded, failed, cancelled, or requires action.

Each event includes a stable event ID, timestamp, source component, operation ID, instance ID, authenticated service identity, optional untrusted administrator correlation, event kind, bounded non-secret facts, policy result, and previous-event reference where the store supports it.

Audit history is logically append-only. Events never contain secrets, raw environment values, authorization material, unrestricted manifests, unbounded logs, or full Docker error bodies.

## Security requirements

The state and reconciliation design adds these rules:

1. No cached observation authorizes a privileged mutation.
2. The helper commits phase intent before external mutation and reconciles an interrupted dispatch before retry.
3. Labels, deterministic names, control-plane rows, and helper records are each insufficient alone to prove ownership.
4. Losing helper ownership state removes destructive authority until verified recovery or explicit adoption completes.
5. Reconciliation does not automatically adopt, rewrite, or delete ambiguous resources.
6. A stale executor cannot commit after its fencing token loses ownership.
7. Destructive operations verify the complete target set before deleting the first object.
8. Supersession and administrator overrides add audit evidence rather than rewriting prior receipts.

## Database requirements

The implementation selects separate SQLite databases for the control-plane and helper stores. The production configuration must provide:

- atomic transactions for receipt, phase, ownership, lease, and event changes that must agree;
- crash recovery and documented durability guarantees after acknowledged commits;
- unique constraints for operation IDs, stable resource identities, deterministic names, and active per-instance leases;
- foreign-key or equivalent integrity between operations, resources, instances, generations, and events;
- compare-and-swap or serializable updates for fencing tokens and state transitions;
- bounded concurrent readers and writers;
- append-only event semantics or an enforceable equivalent;
- schema migration with rollback or forward-recovery rules;
- corruption detection and safe read-only recovery;
- local backup and restore with consistency verification;
- operation without a KITPro account or cloud service; and
- a narrow access boundary between the unprivileged and privileged stores.

The release gates test transaction behavior, backup restoration, schema-7 to
schema-13 migration, concurrent lease acquisition, stale fencing, helper
restart, runtime unavailability, and reboot recovery. SQLite transactions do
not make Docker or filesystem mutation atomic, so durable intent and fresh
observation remain required.

## Consequences

### Benefits

- The system represents uncertainty instead of converting it into false success or unsafe retry.
- The helper does not trust a compromised API database to establish ownership.
- Stable KITPro identities survive Docker object replacement while recorded Docker IDs expose drift.
- Bounded reconciliation supports recovery without a continuously mutating controller.
- The model preserves standard Docker workloads when either KITPro state store is unavailable.

### Costs

- Two state owners require explicit projections, backup rules, and recovery tooling.
- Every Docker mutation needs durable phase writes and postcondition inspection.
- Operations may stop for administrator action instead of guessing.
- Generation-based updates retain temporary resources and require cleanup policy.
- The production store must support stronger transaction and corruption behavior than the prototype file store.

## Risks

| Risk | Effect | Required response |
| --- | --- | --- |
| Helper state becomes a hidden proprietary control point | Users cannot understand or recover workloads without KITPro | Keep workloads standard, export inspectable state, and never make helper state the only location of user data |
| Reconciliation becomes an autonomous mutation loop | KITPro overwrites administrator changes or amplifies an attack | Keep startup and periodic scans observation-only unless an existing receipt proves a safe continuation |
| Prepared ownership intent is too broad | A foreign object can match after a crash | Include exact deterministic identity, installation, role, generation, configuration hash, digest, and policy fields |
| Leases are mistaken for proof that work stopped | Two executors mutate the same instance after a timeout | Use fencing tokens and reconcile stale leases before transfer |
| Audit and operation records diverge | Recovery cannot explain a privileged change | Commit related helper state atomically where possible and emit an explicit gap event after recovery |
| Backups restore mismatched control-plane and helper generations | Desired state and destructive authority refer to different points in time | Record backup generation IDs and require cross-store reconciliation after restore |

## Alternatives considered

### One control-plane database for all state

This design is operationally simpler. It also lets a compromised API write the records that authorize privileged deletion. The helper needs an independent ownership and receipt store.

### Docker as the only source of truth

Docker is authoritative for current runtime facts, but its labels and names do not prove KITPro intent or ownership. Docker also cannot record why an operation occurred, which request it belongs to, or whether persistent data must remain.

### Helper state as the only source of truth

The helper should not own catalog choices, user workflow, or all desired state. Giving it those concerns would expand the root process and make the browser-facing product depend on privileged storage.

### Blind idempotent retries

Some Docker calls appear idempotent when names are deterministic. A timeout can still leave a partial or conflicting object. Observation and full postcondition comparison must precede retry.

### Continuous desired-state enforcement

A controller that repairs every difference can keep applications running, but it can also overwrite intentional administrator changes or normalize an attacker's resource. Phase 1 uses startup, operation-triggered, and explicit reconciliation with observation-only periodic scans.

## Validation evidence

The production tests cover durable receipts, exact and conflicting replay,
crash points around external mutation, unknown runtime outcomes, ownership
disagreement, stale execution, supersession, and durable per-installation
exclusion. Debian and Arch VM acceptance additionally exercised schema
migration, reboot recovery, AppArmor/systemd confinement, live Docker same-port
cutover, backup/restore, and package upgrade behavior.

Before promotion beyond alpha, validation must continue to prove:

1. acknowledged helper commits survive process and host power interruption within the selected store's documented guarantees;
2. two helper processes cannot acquire the same instance lease;
3. a stale fencing token cannot commit;
4. a changed Docker daemon identity invalidates observations;
5. state restore cannot silently regain ownership of a newer or foreign object;
6. every destructive crash point preserves unknown resources and persistent data; and
7. Debian AppArmor and Rocky SELinux policy protect the helper store from the API identity and managed containers.

## Open questions

- Should the helper event store use hash chaining, external export, or another tamper-evidence mechanism?
- Which application updates can recover automatically after data migration begins?
- How long should completed receipts, tombstones, observations, and rollback generations remain?
- Which periodic observations are useful enough to justify their load?
- How will backup generation IDs coordinate independent control-plane and helper stores?
- Whether any future resource-adoption operation can be safe enough to add; none is currently implemented.

## Review conditions

Revisit this ADR if:

- the selected helper cannot maintain an independent durable store;
- Docker cannot provide enough inspection data to resolve unknown outcomes;
- the first application requires irreversible data migration that the phase model cannot represent;
- the chosen database cannot enforce leases, fencing, uniqueness, or crash durability;
- mandatory-access-control policy cannot isolate helper state from the API and containers; or
- operational testing shows that bounded reconciliation leaves common failures unrecoverable.
