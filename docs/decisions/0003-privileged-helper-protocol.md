# ADR-0003: Privileged helper protocol

- Status: Accepted
- Date: 2026-09-12
- Owners: Unassigned
- Review: Production helper, package, and supported-platform validation passed
- Related principles: Secure defaults, Inspectability, Reversibility, Open foundations
- Related decisions: ADR-0004, ADR-0005, ADR-0006, ADR-0007, ADR-0013, ADR-0015, ADR-0016, ADR-0017, ADR-0018

## Context

KITPro Server needs root-level authority for a small set of host and Docker operations. Its browser-facing service processes network input, sessions, application definitions, logs, and runtime output. Running that whole process as root, or giving it the Docker socket, would turn a web-process compromise into host control.

The helper boundary must work on modern systemd-based Linux distributions. ADR-0017 selects Debian 13 as primary/reference and Rocky Linux 10 as secondary/experimental. The protocol cannot depend on APT, DNF, distribution packages, distribution service defaults, a particular firewall tool, an AppArmor profile, or an SELinux policy.

The helper must treat the API process as a hostile but identifiable client. Kernel-authenticated caller identity answers who connected. It does not answer whether a requested change is safe.

## Decision

KITPro will use a root-owned, systemd socket-activated helper daemon. The API and helper communicate over a pathname Unix domain stream socket at a location such as `/run/kitpro/privileged.sock`.

The helper exposes a versioned, typed, semantic protocol. It does not expose shell commands, arbitrary paths, Docker arguments, raw Docker API messages, Compose documents, or general file writes.

The first protocol uses length-prefixed UTF-8 JSON:

- a four-byte unsigned big-endian length precedes each message;
- the maximum encoded message size is 1 MiB;
- each connection may carry more than one request;
- the helper rejects malformed UTF-8, duplicate object keys, unknown operation names, unknown fields, invalid numeric ranges, and messages over the limit; and
- the helper validates the whole message before it accepts an operation.

JSON keeps the initial contract language-neutral and inspectable. A schema document will be authoritative when implementation begins. The implementation must generate or derive its wire types from that schema where the selected language permits it.

The selected transport and framing are part of the protocol. The implementation language, JSON library, state database, and public API remain open.

## Trust boundary

The exact call path is:

```text
Administrator's browser
        |
        | authenticated public API request
        v
KITPro API as kitpro-api
        |
        | typed request over a protected Unix socket
        v
KITPro privileged helper as root
        |
        | policy-derived Docker Engine API request
        +---------------------> Docker Unix socket
        |
        +---------------------> approved KITPro storage roots
        |
        +---------------------> named host adapters
```

The browser cannot connect to the helper. The API service identity cannot open the Docker socket. Only the helper can translate a KITPro operation into a Docker Engine request or a privileged filesystem change.

The helper does not trust the API's plan, validation result, Docker object identifier, path resolution, or statement that an administrator approved an action. It reconstructs and enforces every privileged constraint from trusted policy and helper-owned state.

## Caller authentication and authorization

Four checks remain separate:

| Check | Question | Owner |
| --- | --- | --- |
| Caller authentication | Which local process identity opened the socket? | Kernel credentials and helper |
| Request validation | Does the message match the supported schema and value rules? | Helper protocol parser |
| Authorization | May this authenticated caller request this operation on this KITPro target? | Helper authorization policy |
| Policy enforcement | Is the requested effective host and Docker change allowed now? | Operation handler and host or Docker adapter |

A root-owned package rule using a standard systemd runtime-directory or `tmpfiles.d` mechanism creates `/run/kitpro` as `root:kitpro-api` with mode `0750`. The socket unit fails closed if that directory is missing and creates the socket as `root:kitpro-api` with mode `0660`. The API service uses `kitpro-api` as its fixed primary service identity. No ordinary account joins that group, and the API receives no general-purpose privileged group membership.

On every accepted Unix stream connection, the helper reads `SO_PEERCRED` and requires the configured `kitpro-api` UID. A matching group is not enough. The helper records the peer PID and UID for diagnosis but does not treat the PID as a durable identity.

SELinux-enforcing hosts may also check the peer security context with `SO_PEERSEC` or an equivalent systemd and SELinux policy. The core authorization rule remains the fixed UID plus helper policy. Distribution policy can add a mandatory-access-control check without changing the messages.

Phase 1 does not add a static shared secret between the API and helper. A compromised API process could read that secret, so it would not reduce that threat. Socket permissions and kernel credentials authenticate the service identity.

A compromised process running as the fixed API identity can request every semantic helper operation authorized to that identity. The helper cannot infer that a human administrator approved a request. Its security value comes from its closed operation vocabulary, independent argument and policy checks, resource and path limits, Docker capability denials, ownership checks, destructive-operation rules, and authoritative audit events.

Administrator and session metadata may accompany a later request for audit correlation. The helper must treat that metadata as an untrusted claim unless an independent trusted component verifies it. Proof that a human administrator approved a high-impact operation remains open for ADR-0008 and ADR-0015.

## Request and response envelope

Every request contains only these envelope fields plus one typed operation body:

```text
protocol_major
protocol_minor
request_id
operation_id
operation_kind
operation_revision
deadline
target
expected_state
parameters
```

The fields have these roles:

- `request_id` correlates one transport attempt with logs and its response.
- `operation_id` is a caller-generated idempotency key for one intended state change.
- `operation_kind` selects a closed message variant.
- `operation_revision` identifies the exact operation-body schema.
- `deadline` limits queueing and initial acceptance. It does not promise that an accepted mutation can stop safely.
- `target` contains typed semantic identifiers, never an arbitrary Docker ID or host path.
- `expected_state` carries generation or observed-state preconditions where stale work could be destructive.
- `parameters` is the closed body for the selected operation.

Every response contains the protocol version, request ID, operation ID when present, a result state, and either a typed result or a typed error. Runtime error strings may appear only as bounded, sanitized diagnostic details. Callers do not branch on those strings.

## Version and compatibility rules

The helper and API negotiate no unbounded feature set.

- A major-version mismatch fails before mutation.
- Each operation has its own schema revision.
- Unknown operations, fields, enum values, and revisions fail closed.
- A newer API sends only operation revisions that the helper reports as supported.
- A newer helper retains the prior supported revision during the package's documented upgrade window.
- Additive protocol work still needs a new operation revision when an older helper would misread its security meaning.
- Package activation tests the API and helper compatibility before the new API begins privileged work.

The protocol does not silently ignore a field for forward compatibility. Ignoring a future security field could turn a constrained request into a broader one.

## Helper operation model

Each operation represents one policy-owned resource transition. This is the middle ground between one `DeployEverything` request and hundreds of root-level building blocks.

The proposed first set is:

| Operation | Purpose | Mutation |
| --- | --- | --- |
| `InspectHostCapabilities` | Return the bounded host facts required by an approved compatibility check | No |
| `InspectDockerEngine` | Return the supported Docker version, API range, architecture, and required capabilities | No |
| `EnsureApprovedImage` | Ensure that an approved registry and digest is present for an approved platform | Yes |
| `PrepareApplicationStorage` | Create or verify one declared storage slot below an approved root | Yes |
| `EnsureApplicationNetwork` | Create or verify the network for one application instance | Yes |
| `EnsureApplicationContainer` | Create or verify one declared container from constrained fields | Yes |
| `SetApplicationRunState` | Start or stop all owned containers for one instance in declared order | Yes |
| `RemoveApplicationRuntime` | Remove owned containers and disposable networks while retaining persistent data | Yes |
| `InspectApplicationRuntime` | Return bounded state for resources owned by one instance | No |
| `ReadApplicationLogs` | Return a bounded log window for one owned container role | No |
| `GetOperation` | Return the accepted operation's state and result | No |
| `CancelOperation` | Request cancellation at an operation-defined safe point | Yes |

`EnsureApplicationContainer` is not raw container creation. Its handler accepts only the fields in the application request model defined by ADR-0004. The helper constructs the Docker request.

The helper does not expose any operation equivalent to:

```text
run(command)
exec(shell)
write_file(path, contents)
docker(args)
compose(yaml)
request(method, docker_path, body)
delete_resource(docker_id)
```

New operations require an ADR update or a security review recorded with the protocol schema. A request must name the KITPro goal and target. It must not name a privileged mechanism.

## Idempotency, replay, and recovery

Every mutation requires an `operation_id`. The helper stores a root-owned operation receipt before the first external mutation. The receipt binds the operation ID to the canonical request digest, caller identity, state, affected resources, and audit correlation ID.

The exact storage technology remains open in ADR-0016. Regardless of that choice:

- the same operation ID and same request digest returns the existing status or result;
- the same operation ID and a different request digest returns `OperationConflict`;
- a completed operation is not run again;
- a partially completed operation reconciles observed state against its recorded steps before it continues or reports `RecoveryRequired`; and
- receipts survive API and helper restarts.

Once the helper records an operation as accepted, a client disconnect does not cancel it. The API reconnects and calls `GetOperation`. `CancelOperation` is best effort and works only at operation-specific safe points. A timeout or lost connection never means that a mutation rolled back.

Read operations use request IDs but do not need durable idempotency receipts.

## Concurrency and resource limits

The helper bounds connection count, in-flight requests per peer, request rate, message size, response size, and log volume. Limits protect the root process from a compromised API and a noisy runtime.

The helper serializes mutations by application instance. It also takes narrower locks for shared image work and a global lock for any approved operation that changes runtime-wide or host-wide state. Read operations may run concurrently within fixed limits.

Conflicting operations return `OperationConflict`; they do not wait without a bound. Lock ownership and accepted operation state survive or recover after a helper crash. The implementation must test a crash after each externally visible step.

## Error taxonomy

The protocol uses stable error codes with a `retryable` field and bounded diagnostic context:

| Family | Codes |
| --- | --- |
| Protocol | `MalformedMessage`, `MessageTooLarge`, `UnsupportedProtocol`, `UnsupportedOperation` |
| Identity and policy | `UnauthenticatedCaller`, `AuthorizationDenied`, `PolicyDenied` |
| Input | `InvalidArgument`, `InvalidIdentifier`, `ForbiddenAttribute`, `ForbiddenPath` |
| Ownership and state | `ResourceNotOwned`, `OwnershipConflict`, `PreconditionFailed`, `NotFound`, `OperationConflict` |
| Dependency | `DockerUnavailable`, `DockerIncompatible`, `RegistryUnavailable`, `MountUnavailable` |
| Execution | `DeadlineExceeded`, `CancellationPending`, `PartiallyApplied`, `RecoveryRequired`, `InternalFailure` |

An error response does not expose secrets, raw environment values, filesystem contents, Docker authorization headers, or unbounded runtime output.

## Filesystem safety

The API never supplies an absolute host path for a privileged write, ownership change, permission change, move, or removal. It supplies typed values such as:

```text
StorageRootId
ApplicationInstanceId
StorageSlotId
AccessIntent
ExpectedMountIdentity
```

The helper maps `StorageRootId` through root-owned policy. It derives the relative location from validated identifiers. The final data layout remains open in ADR-0006.

Privileged filesystem handlers must:

- open a trusted root directory first and resolve descendants relative to its file descriptor;
- reject `..`, empty path components, absolute paths, NUL bytes, and identifiers outside their grammar;
- use `openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS`, and operation-specific `RESOLVE_NO_SYMLINKS` or `RESOLVE_NO_XDEV` where the host supports them;
- use descriptor-relative operations, `O_NOFOLLOW`, and post-open `fstat` checks instead of check-then-open path logic;
- reject unexpected symbolic links and hard-linked protected files;
- compare the expected mount and filesystem identity at the point of mutation;
- fail if an approved external mount is missing or has been substituted;
- create temporary objects in the destination directory and use atomic replacement where the operation permits it;
- set ownership and mode from policy, not caller-supplied UID, GID, or mode values; and
- preserve application data unless a separate data-deletion operation is later approved.

If a required kernel path-resolution guarantee is unavailable, the helper fails that operation. It does not fall back to unsafe string-prefix checks.

On an SELinux-enforcing host, a distribution adapter applies the approved persistent file contexts and access rules. Catalog requests cannot supply raw labels, `chcon` commands, or Docker relabel flags. KITPro does not disable or set SELinux to permissive mode.

## Lifecycle

The chosen lifecycle combines a root-owned systemd socket unit with a persistent helper service:

- systemd owns the stable socket and its permissions;
- the first connection can activate the helper;
- systemd passes the listening file descriptor to the helper;
- the helper processes bounded concurrent work and remains alive while operations run;
- systemd restarts the helper after a crash under a bounded restart policy; and
- operation receipts let the restarted helper reconcile accepted work.

Socket activation avoids a connection gap during helper restart and lets systemd create the socket with fixed ownership. A persistent process supports long Docker pulls, lifecycle operations, bounded concurrency, and one audit stream.

The helper will not use a process-per-request socket unit. It will not use one-shot privileged commands or broad sudo rules. Those choices scatter policy across command entry points and make restart recovery harder.

## Hardening

The service definition should apply controls that do not block its named duties. Candidate controls include:

- a root-owned executable, configuration, unit files, policy, and operation-receipt store that the API identity cannot change;
- no TCP listener and an address-family allowlist limited to `AF_UNIX` unless an accepted operation proves that another family is needed;
- a clean environment, fixed `PATH`, fixed umask, no shell startup, and no inherited proxy variables;
- no home-directory access, no writable temporary directory shared with the API, and explicit writable paths;
- no access to devices, kernel modules, boot files, or package managers unless a later operation and ADR require it;
- process, file-descriptor, memory, task, and request limits;
- a syscall allowlist or denylist tested against each supported distribution and Docker operation;
- `NoNewPrivileges=yes` where it is compatible with the helper's root duties;
- read-only host filesystem views with named writable paths where mount namespacing does not interfere with Docker access or mount verification; and
- an SELinux domain and allow rules on enforcing hosts.

Root ownership, the fixed socket identity, closed operation schemas, independent policy, and safe target resolution reduce the authority reachable from the API. Generic systemd hardening remains defense in depth. Docker socket access lets the helper ask Docker to create host-powerful containers, so a compromised helper is still a host-root compromise.

`CapabilityBoundingSet` may remove capabilities that no approved handler uses, and the helper must receive no ambient capabilities. The final set must come from tests of the fixed operation set on each supported host. Capability filtering cannot contain a process that still controls rootful Docker, so it is defense in depth rather than the trust boundary.

The implementation must measure each hardening control on every supported distribution. It must not copy a unit profile that silently disables a required kernel check or makes the helper depend on a Debian-only path.

## Distribution portability

The core helper protocol depends on Linux, Unix domain sockets, kernel peer credentials, and systemd service and socket management. It does not invoke a package manager, firewall CLI, service CLI, or SELinux CLI through a generic operation.

Distribution-specific work stays behind named adapters and separate packaging:

- package installation and upgrades;
- service file placement and package ownership;
- firewall inspection and approved changes;
- SELinux policy packaging and file contexts;
- Docker package source and socket discovery; and
- host compatibility probes.

An adapter cannot add a generic command escape hatch. If Rocky Linux or a RHEL-family host needs a new privileged action, that action needs the same typed operation, policy, audit, and negative tests as the current reference host.

## Consequences

### Benefits

- The web process has no root identity and no Docker socket.
- Kernel credentials prevent an unrelated local account from posing as the API service.
- Closed operations keep Docker and filesystem authority behind one policy owner.
- Durable operation IDs make API disconnects and process restarts observable and recoverable.
- The wire contract does not bind the helper to a language, package manager, firewall tool, or disabled SELinux state.

### Costs

- The project must maintain a versioned schema, operation journal, policy tests, and compatibility tests.
- The API and helper each validate at their own trust boundary.
- Socket activation and crash recovery add service-lifecycle work before application workflows begin.
- Filesystem safety needs Linux-specific descriptor APIs and tests against mounts, links, and races.
- SELinux-enforcing hosts need packaged policy and file-context rules outside the protocol.

## Risks

| Risk | Effect | Required response |
| --- | --- | --- |
| Semantic operations become generic over time | The helper turns into a root RPC service | Require closed schemas, security review, negative tests, and no mechanism-shaped operations |
| A compromised API can request every operation authorized to its service identity | Socket authentication proves the process identity, not human intent | Keep operations narrow and independently enforced; treat administrator metadata as untrusted unless another trusted component verifies it |
| Operation receipts and Docker state diverge | Retries delete or recreate the wrong object | Require state, labels, expected generation, and observed-state reconciliation |
| A parser flaw compromises the root process | Malformed API input becomes root code execution | Keep one small parser, bound messages, fuzz it, and minimize dependencies |
| A path or mount changes during an operation | The helper writes outside the intended data root | Use descriptor-relative resolution and recheck mount identity at mutation time |
| systemd hardening is treated as containment | A compromised Docker-capable helper is called safe | Keep root-equivalent compromise explicit and limit reachable operations before Docker |
| SELinux policy becomes an afterthought | RHEL-family support requires disabling enforcement or a redesign | Test enforcing mode early and keep access intent semantic |

## Alternatives considered

### Localhost TCP

TCP is language-neutral and familiar. It creates a network listener, has no direct Unix UID proof, adds port and firewall behavior, and increases exposure to browser-origin mistakes and containers. Mutual TLS or another credential system would then be required. The local-only helper does not need those costs.

### Unix socket plus a shared secret

A shared secret could authenticate a process that lacks peer credentials. The fixed API identity can read any secret made available to that process. The secret therefore adds rotation and storage work without containing a compromised API. Kernel credentials and policy provide the Phase 1 boundary.

### D-Bus or another system message bus

D-Bus has typed interfaces, peer identity, and policy. It also adds bus policy, activation behavior, binding choices, and a larger interface than this local two-service protocol needs. The proposed framing keeps the contract small and directly testable.

### Protocol Buffers or CBOR

Both formats can produce smaller messages and strict generated types. They add schema tooling or reduce direct inspectability before the implementation language is known. The 1 MiB local limit removes a performance reason to choose them now. The project may revisit serialization before implementation if fuzzing or cross-language tooling shows a concrete benefit.

### HTTP over a Unix socket

HTTP libraries handle framing and status. They also bring methods, headers, routing, content negotiation, and parser behavior that this fixed operation set does not need. A small framed protocol makes unsupported input easier to reject.

### Persistent helper without socket activation

This is workable, but it creates socket ownership and stale-socket cleanup inside the root process. It also leaves a connection gap during restart. systemd already owns those jobs on the supported host class.

### One-shot privileged commands or sudo rules

One-shot commands reduce daemon lifetime. They spread parsing, authorization, audit, locking, and recovery across executables. Any command that accepts general arguments risks becoming a root command runner. Long-running pulls and lifecycle work also outlive a single request process.

## Validation plan

Before implementation begins, a disposable reference VM must prove these properties:

1. Only the fixed API service UID can connect to the helper socket.
2. The helper reads the expected UID through `SO_PEERCRED` and rejects every other UID, even if filesystem permissions are misconfigured to allow a connection.
3. SELinux enforcing mode can add a peer-domain check without changing the wire protocol.
4. Unknown operations, fields, revisions, duplicate keys, malformed UTF-8, invalid IDs, and messages over 1 MiB fail before mutation.
5. Replaying the same operation ID and request returns one result. Reusing the ID with different content fails.
6. API restart, helper restart, Docker restart, client disconnect, timeout, and cancellation produce a known operation state.
7. Concurrent work on one application serializes. Independent bounded reads do not block each other without cause.
8. Every accepted mutation emits start and result audit events without secret values.
9. Traversal, symbolic-link, hard-link, mount-substitution, and race tests cannot escape approved roots.
10. The API identity cannot open the Docker socket, execute a root command, write a root-owned file, or invoke any helper operation outside the schema.
11. A systemd security analysis and functional test records which hardening controls work on each supported host.
12. The same protocol tests run on a current Rocky Linux or RHEL-family SELinux-enforcing VM before that family receives official support.

The detailed environment and cases are in [`docs/testing/debian-reference-host.md`](../testing/debian-reference-host.md). The disposable implementation, exact VM procedure, and result records are in [`prototypes/privilege-boundary/`](../../prototypes/privilege-boundary/) and [`docs/testing/results/`](../testing/results/).

The repository-only fixture passed its parser, peer-credential, concurrency, restart, receipt, ownership-policy, fixed Docker-request, and safe-path tests on 2026-09-12. Later Debian 13 and Rocky Linux 10.2 runs also proved root-owned systemd socket activation, exact `SO_PEERCRED`, direct-Docker denial for the API identity, bounded Docker outage, receipt replay across helper and host restart, and real ext4/XFS mount-boundary rejection.

The Rocky run found two unresolved hardening issues. `RestrictSUIDSGID=yes` under systemd 257 made the required `openat2` call return `ENOSYS`, so the disposable unit removed that defense-in-depth control. The Rocky helper also ran as `unconfined_service_t`. The production unit and mandatory-access-control design must resolve both without changing the protocol or weakening path enforcement; see [`docs/testing/platform-comparison.md`](../testing/platform-comparison.md).

## Revisit conditions

Revisit this decision if:

- a supported host cannot provide reliable Unix peer credentials or systemd socket activation;
- the selected language cannot implement safe framing, peer checks, or descriptor-relative filesystem work without an unsafe binding;
- schema evolution tests show that strict JSON creates more compatibility risk than another format;
- high-impact operations need proof of administrator intent that cannot fit the envelope safely;
- a supported operation needs helper network access;
- rootless runtime support changes the helper's required authority;
- SELinux enforcing tests require protocol-level information that `AccessIntent` does not represent; or
- the operation set starts to resemble a shell, file server, Docker proxy, or general root RPC interface.

## References

- [Linux `unix(7)`](https://man7.org/linux/man-pages/man7/unix.7.html) documents pathname socket permissions, `SO_PEERCRED`, and SELinux peer credentials.
- [systemd socket units](https://www.man7.org/linux/man-pages/man5/systemd.socket.5.html) document socket activation, ownership, group, and mode controls.
- [Linux `openat2(2)`](https://man7.org/linux/man-pages/man2/openat2.2.html) documents resolution constraints for links, mount crossings, and escapes below a directory file descriptor.
- [systemd service execution settings](https://github.com/systemd/systemd/blob/main/man/systemd.exec.xml) document `NoNewPrivileges` and other service controls.
- [Docker Engine security](https://docs.docker.com/engine/security/) explains why Docker daemon control and unsafe container parameters can become host control.
- [The upstream Udica project](https://github.com/containers/udica) documents container-aware SELinux policy generation from runtime inspection.
