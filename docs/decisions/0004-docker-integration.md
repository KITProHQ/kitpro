# ADR-0004: Docker integration

- Status: Accepted
- Date: 2026-09-12
- Owners: Unassigned
- Review: Production Docker adapter and supported-platform validation passed
- Related principles: No lock-in, Open foundations, Secure defaults, Inspectability, Reversibility, Standard workloads
- Related decisions: ADR-0003, ADR-0005, ADR-0006, ADR-0007, ADR-0010, ADR-0011, ADR-0012, ADR-0013, ADR-0017, ADR-0018

## Context

ADR-0017 selects rootful Docker Engine for the first application slice, with Debian 13 as primary/reference and Rocky Linux 10 as secondary/experimental. The runtime interface must remain portable across modern systemd-based Linux hosts.

Docker control is host-powerful authority. A caller that can create an arbitrary container can mount the host root filesystem, add capabilities, join host namespaces, or attach devices. KITPro cannot expose Docker as a general remote-control interface and call the result privilege separation.

KITPro also shares the Docker daemon with objects it does not own. It must distinguish its resources from user-created containers, networks, volumes, and images. A malformed or compromised request must not turn a Docker object ID into proof of ownership.

## Decision

The privileged helper will call the versioned Docker Engine HTTP API directly through Docker's local Unix socket. The browser-facing API cannot open, receive, proxy, or mount that socket.

The helper constructs each Docker request from typed KITPro values after it checks policy, ownership, current state, and operation preconditions. It does not invoke the Docker CLI or Docker Compose. It does not forward a method, endpoint, query, headers, or body supplied by the API service.

This decision selects a protocol, not a language-specific Docker SDK. The chosen helper language may use an HTTP client, a maintained SDK, or a thin adapter later. Any library must expose and test the same approved Engine API subset.

## Docker Engine connection

The distribution package or host adapter locates the rootful Docker socket. The core adapter receives an already approved local socket endpoint. It does not assume that `/var/run/docker.sock`, a Debian package name, or an APT repository proves compatibility.

KITPro does not:

- enable Docker's TCP API;
- accept `DOCKER_HOST` or proxy environment variables from the API request;
- add the API service to the `docker` group;
- mount the Docker socket into an application container;
- proxy arbitrary Docker API traffic; or
- fall back to a Docker-compatible socket whose engine identity is unknown.

At startup and before mutation after a Docker restart, the helper calls `/version` and a constrained subset of `/info`. It verifies the engine identity, operating-system type, architecture, API minimum and maximum, and required runtime features.

The adapter selects the highest Engine API version within KITPro's tested range and the daemon's reported range. An unsupported range fails closed. The exact Docker Engine and API version floor remains a release-support decision backed by the disposable-host suite.

## First-slice Engine API subset

The adapter permits only the endpoints and fields needed for the first vertical slice:

| Capability | Engine API use |
| --- | --- |
| Engine inspection | Read `/version` and selected fields from `/info` |
| Image presence and identity | Inspect an exact, fully qualified image reference and its image ID and repository digests |
| Image pull | Pull an approved registry repository at an approved digest for an approved platform |
| Network lifecycle | List by labels, create, inspect, connect during container creation, disconnect when safe, and remove one owned network |
| Volume lifecycle | List by labels, create, and inspect an owned named volume; removal is outside normal uninstall and requires later data policy |
| Container lifecycle | List by labels, create, inspect, start, stop, and remove one owned container |
| Health | Read container state and the bounded health-check result from inspect data |
| Logs | Read a bounded log window for one owned container role |
| Reconciliation | Filter managed containers, networks, and volumes by mandatory KITPro labels; inspect each candidate; and enumerate all containers attached to an owned network before lifecycle or destructive operations |

The first slice does not use Docker build, exec, attach, archive copy, commit, rename, prune, plugins, Swarm, daemon configuration, events without bounds, or unrestricted stats. It does not use runtime-wide prune endpoints, even with label filters.

Adding an endpoint or a security-sensitive request field requires an adapter allowlist change, protocol tests, and a security review.

## Application request boundary

The data flow is:

```text
KITPro application specification
        |
        | parse and plan as unprivileged data
        v
Constrained application plan
        |
        | semantic operation with stable references
        v
Privileged helper
        |
        | authenticate caller, reparse, authorize, resolve trusted release,
        | verify ownership, validate effective Docker policy
        v
Docker Engine API adapter
```

The unprivileged planner may propose an operation. The helper derives the effective Docker request from a root-owned policy record and a trusted catalog-release identity. It does not accept the planner's serialized Docker configuration as evidence.

An `EnsureApplicationContainer` request may cross the boundary with these concepts:

```text
ApplicationInstanceId
ContainerRoleId
CatalogReleaseId
ApprovedImageId
ApplicationNetworkRef
StorageSlotRef list
ConfigurationValue list
SecretRef list
DeclaredPortRef list
HealthCheckRef
ExpectedGeneration
```

These are semantic identifiers and constrained values. The helper resolves their Docker names, object IDs, paths, image reference, environment keys, container targets, and health-check form from trusted policy.

Normal catalog workloads cannot request or override:

- `Privileged=true`;
- the host PID, IPC, user, UTS, or network namespace;
- added Linux capabilities;
- arbitrary devices or device cgroup rules;
- `/run/docker.sock`, `/var/run/docker.sock`, or another runtime socket;
- arbitrary host bind mounts;
- `/`, `/etc`, `/proc`, `/sys`, `/dev`, KITPro state, or another application's data;
- arbitrary commands, entry points, working directories, or user identities;
- raw environment variable names outside the release definition;
- raw security options, seccomp changes, AppArmor changes, or SELinux label changes;
- unrestricted sysctls, ulimits, DNS settings, extra hosts, or kernel tunables;
- a restart policy outside the small KITPro policy set;
- an unapproved network, link, alias, MAC address, or static IP; or
- an arbitrary host IP or host port.

The helper rejects forbidden fields even if Docker would accept them. A later manual-workload mode needs a separate trust model, interface, and ADR. It cannot weaken the catalog policy.

## Managed-resource identity

KITPro owns a Docker object only when all required proofs agree:

1. A root-owned helper record identifies the object by kind, Docker ID, Docker daemon identity, installation ID, application ID, instance ID, resource role, creation operation ID, and expected policy digest.
2. The Docker object has the required KITPro labels with the same values.
3. The object kind and inspected configuration match the requested semantic target and the operation's required preconditions.

Labels make objects inspectable and support discovery. Labels alone do not grant ownership because any Docker administrator can copy them.

The production label set will use a reverse-domain namespace controlled by the KITPro project or company:

```text
{kitpro-owned-reverse-domain}.managed=true
{kitpro-owned-reverse-domain}.schema=1
{kitpro-owned-reverse-domain}.installation=<installation-id>
{kitpro-owned-reverse-domain}.application=<application-id>
{kitpro-owned-reverse-domain}.instance=<instance-id>
{kitpro-owned-reverse-domain}.resource-role=<role>
{kitpro-owned-reverse-domain}.policy-digest=sha256:<digest>
```

`{kitpro-owned-reverse-domain}` is a placeholder, not a valid production label prefix. The project must choose and document the exact owned namespace before it creates persistent production Docker resources. The disposable fixture uses a separate unmistakable test namespace that must never appear on production resources. Changing a production prefix after resources exist would need a migration because Docker object labels are static for the object's lifetime.

Identifiers use strict grammars and are not Docker object IDs. The API may ask to stop `ApplicationInstanceId A`. It cannot ask to delete container ID `X`. The helper resolves the target from its own record and then checks Docker.

The helper record is an authority registry, not the product's final database choice. [ADR-0016](0016-durable-state-and-reconciliation.md) defines its durability, ownership, and recovery requirements while leaving the storage engine open. The API identity cannot edit the authority registry.

## State disagreement and drift

The helper uses this fail-safe classification:

| Helper record | Docker object | Classification | Allowed automatic action |
| --- | --- | --- | --- |
| Present | Present with exact labels and expected kind | Owned and matched | Apply the requested typed operation after full inspection |
| Present | Missing | Missing managed resource | Report and reconcile through an explicit repair path; do not substitute another object |
| Missing | Present with KITPro labels | Orphan candidate | Report; do not adopt, mutate, or delete automatically |
| Present | Present with missing or conflicting labels | Ownership conflict | Block mutation and report the mismatch |
| Present | More than one matching object | Ambiguous ownership | Block mutation and require repair |
| Missing | Present without exact KITPro labels | Foreign resource | Ignore except for name, port, or network collision reporting |

Manual changes to a matched object produce drift. Read-only inspect and bounded logs remain available. A stop request may proceed only when identity still matches and stopping reduces immediate risk. Start, update, replacement, and removal stop until an explicit reconciliation flow confirms the effective configuration. Automatic adoption and automatic deletion are forbidden.

Normal uninstall removes only matched KITPro containers and disposable instance networks. It retains persistent storage records, host directories, and named data volumes. A future data-deletion operation must be separate, explicit, narrow, and more strongly authorized.

## Network strategy

Phase 1 creates one user-defined bridge network per application instance. All containers that belong to a multi-container instance may share that network. Containers from unrelated application instances do not join it.

This topology avoids a shared flat application network and limits name discovery and direct container traffic between applications. It does not block traffic through the host, published ports, routed paths, or application egress. Those controls remain for ADR-0010.

KITPro does not attach catalog containers to Docker's default bridge, the host network, the helper, the API service, or an existing user network.

Container ports do not become host ports by default. For the first slice:

- a catalog release may declare a container port and protocol;
- local policy decides whether that declaration may be published;
- the API cannot provide a raw host binding;
- an approved binding is loopback-only and uses an unprivileged host port;
- IPv4 and IPv6 bindings are separate decisions and tests;
- a port conflict fails before container creation; and
- non-loopback, wildcard, routed, and public exposure remain blocked until ADR-0010 defines the ingress and firewall model.

The supported Docker version must pass a test that a loopback-published port is not reachable from another host on the test network. If it fails, KITPro publishes no port on that host.

The helper records the exact bind address, host port, container port, and protocol. It does not use Docker's publish-all behavior or silently allocate a public binding.

## Storage and SELinux

ADR-0006 still owns the application-data layout and the choice between named volumes and host directories. This ADR defines the Docker boundary for either choice.

The API passes `StorageSlotRef` and an access intent such as read-only or application-data read-write. It does not pass a host source path, mount string, UID, GID, mode, SELinux context, or relabel flag.

The helper resolves the host storage object, verifies its mount identity and ownership, and constructs the Docker mount. On an SELinux-enforcing host, a distribution adapter maps the semantic access intent to approved file contexts and runtime options. It never disables SELinux. A catalog cannot request `label=disable`, arbitrary `SecurityOpt`, or raw `:z` and `:Z` behavior.

Firewall handling, SELinux policy installation, package ownership, and host directory labels stay outside the core Docker adapter. Rocky Linux or RHEL-family support can add those host adapters without changing the helper protocol or application specification.

## Image identity and pull policy

Catalog releases will identify each image with:

- an explicit registry host;
- an explicit repository;
- a human-readable release tag for display and update discovery;
- an approved OCI image-index digest when the image is multi-platform;
- an approved platform, currently `linux/amd64`; and
- the expected platform-manifest digest when the catalog pipeline can provide it.

Deployment uses a fully qualified digest reference such as `registry.example/repository@sha256:...`. Tags never identify the deployed artifact. Unqualified names and implicit registry mirrors are rejected unless a later registry-policy ADR defines them.

The helper follows this policy:

1. Resolve the trusted `CatalogReleaseId` to an approved registry, repository, platform, and digest.
2. Inspect local content for that exact digest.
3. If it is absent, ask Docker to pull the exact digest for the approved platform.
4. Inspect the result and record the Docker image ID and repository digest. Retain the approved index and platform-manifest digests from catalog metadata when provided.
5. Create the container only from the verified local image identity.

The API never sends registry credentials inside the operation body. The helper receives only a `RegistryCredentialRef` where the approved registry needs authentication. ADR-0007 must define retrieval and lifetime. Credentials and Docker authorization headers never enter normal logs, audit payloads, labels, or container environments.

A digest prevents silent tag movement from changing an existing release. It does not prove that the publisher or image is trustworthy. Signing, provenance, revocation, and catalog release authorization remain in ADR-0011.

Update discovery may observe a moved tag, but KITPro updates only to a new approved catalog release with a new immutable digest. The prior image stays present through the documented rollback window. Image rollback does not imply application-data rollback, and the interface must state that boundary.

## Audit requirements

The helper emits an event before and after each Docker mutation. The event contains:

- operation and request IDs;
- caller UID and authorization context reference;
- installation, application, instance, and resource-role identifiers;
- Docker object kind and the resolved object ID after creation or lookup;
- old and new policy digest where relevant;
- image registry, repository, and digest without credentials;
- effective port and mount summaries without secret values;
- policy decision and stable reason code; and
- result, partial state, and recovery requirement.

Raw Docker errors are bounded and sanitized before storage. Container logs are operational data, not authoritative audit events.

## Consequences

### Benefits

- The integration avoids shell interpolation, CLI environment behavior, and human-readable output parsing.
- The helper exposes a smaller contract than Docker's API.
- Exact image digests make deployments repeatable and expose tag movement as an update event.
- State plus labels prevents a caller from turning an arbitrary Docker ID into a deletion target.
- Per-instance networks limit direct traffic between unrelated applications.
- Host packaging, firewall, and SELinux policy can vary without changing the protocol.

### Costs

- KITPro must maintain Engine API compatibility and test request fields across supported Docker versions.
- The helper needs a protected ownership registry and drift-reconciliation flow.
- Direct API progress streams, cancellation, and errors need explicit handling.
- Digest-pinned releases need a catalog process that records each supported platform artifact.
- Per-instance networks use more Docker objects than one shared network.

## Risks

| Risk | Effect | Required response |
| --- | --- | --- |
| The Docker adapter grows into an API proxy | A compromised API gains root-equivalent Docker control | Keep an endpoint and field allowlist; reject method, path, and raw-body inputs |
| Labels are mistaken for authorization | A user-labeled object is deleted | Require the protected helper record, exact labels, kind, and inspected state |
| The ownership registry is corrupt or lost | KITPro cannot prove ownership | Fail safe, preserve resources, and provide an explicit recovery and adoption process |
| A permitted field combination grants host authority | Individually safe-looking options create an escape path | Validate the effective Docker request and test forbidden combinations |
| A loopback port is reachable from the LAN | An application is exposed without intent | Test the supported Docker version and publish nothing when isolation is unproven |
| A digest-pinned image becomes vulnerable | Reproducibility preserves an old flaw | Publish a new reviewed catalog release and make stale-version status visible |
| An approved image is malicious | Digest verification faithfully installs harmful code | Add provenance, signing, review, and revocation in ADR-0011; retain runtime isolation |
| Docker or kernel compromise crosses the boundary | A hostile image reaches the host | Keep the residual risk explicit, minimize grants, and patch supported hosts promptly |
| SELinux policy is too broad | A container or helper accesses unrelated host data | Package narrow policy, test enforcing mode, and keep raw label changes out of manifests |

## Alternatives considered

### Docker CLI invocation

The CLI is familiar and follows installed Docker behavior. It also adds process execution, environment parsing, argument construction, exit-code mapping, stream parsing, CLI and daemon version skew, and human-readable output. Avoiding a shell would reduce injection risk but would not remove those other failure modes.

### Docker Compose CLI

Compose handles multi-container ordering and a common file format. Its model can express host mounts, privileged containers, devices, capabilities, namespaces, networks, secrets, and commands. Passing a catalog document to Compose would make Compose the privileged policy interpreter. That is too much authority for the normal catalog.

### Generated Compose followed by Compose execution

Generating Compose would avoid accepting arbitrary user YAML. KITPro would still need to prove that every generated field and Compose default matches its policy across Compose versions. The generated file would add another state and error layer without removing the need for direct inspection and ownership checks.

KITPro may later generate Compose as an inspectable export. Phase 1 will not execute it as the control path.

### A Docker API proxy in front of the socket

A verb-filtering proxy can block known endpoints. Docker's remaining endpoints and request fields still contain many paths to host authority. KITPro needs application policy, ownership, and state checks, not an HTTP verb filter.

### Direct containerd or OCI runtime integration

This would bypass Docker's higher-level image, network, volume, health, and lifecycle behavior. KITPro would need to build or select those layers. It would not help prove the first Docker-based application slice.

### Podman-compatible API

A compatible HTTP shape does not make Podman's daemon, rootless, storage, user-namespace, network, or systemd behavior identical. Podman support remains a separate runtime adapter and acceptance matrix. The KITPro application plan stays independent of raw Docker fields to keep that path open.

## Validation plan

The disposable-host suite must prove:

1. The API UID receives permission denied when it opens the Docker socket.
2. The helper recognizes only the tested rootful Docker Engine and compatible API range.
3. The adapter cannot call an endpoint or set a field outside its allowlist.
4. Engine inspection, exact-digest pull, create, inspect, health, bounded logs, start, stop, and removal work through the API without a Docker CLI dependency.
5. A tag move cannot change a deployment recorded against a digest.
6. The pulled index and platform manifest match the approved `linux/amd64` release.
7. Every created container, network, and volume has the mandatory labels and a matching helper record.
8. Labels without state, state without an object, conflicting labels, duplicate candidates, and manual drift all fail safely.
9. Requests cannot create privileged containers, host namespaces, capabilities, devices, Docker socket mounts, arbitrary bind mounts, security options, sysctls, commands, or environment keys.
10. A semantic delete request cannot remove a non-KITPro container or unknown volume, even when the attacker supplies its Docker ID in malformed input.
11. One application instance cannot resolve or directly connect to another instance through a KITPro network.
12. No port publishes by default. An approved loopback binding is unreachable from another test host over both IPv4 and IPv6.
13. Normal uninstall retains application storage and refuses volume or directory deletion.
14. Docker restart, helper restart, host restart, pull interruption, stop timeout, and partial removal reconcile to a recorded state.
15. The same request and ownership tests run on an SELinux-enforcing Rocky Linux or RHEL-family VM without disabling SELinux or changing the helper protocol before that family receives official support.

The detailed environment and cases are in [`docs/testing/debian-reference-host.md`](../testing/debian-reference-host.md). The disposable implementation and VM procedure are in [`prototypes/privilege-boundary/`](../../prototypes/privilege-boundary/), with results under [`docs/testing/results/`](../testing/results/).

The repository-only fixture passed tests of the fixed request builder and ownership policy against a fake Docker boundary on 2026-09-12. A later Rocky Linux 10.2 run exercised Docker Engine 29.8.0, an immutable BusyBox digest, real labels, an internal per-instance bridge, lifecycle operations, ownership disagreement, Docker outage/restart, and cleanup.

Docker 29 omitted created and stopped endpoints from the network inspect member map. Accepting an empty map could hide a stopped foreign endpoint, so the final prototype queries all containers by network before lifecycle or destructive operations. A targeted real Docker probe detected both stopped endpoints through that filter. The API still receives no Docker IDs or generic query control.

The first Rocky run found that Docker's tested default daemon did not enable SELinux labels and ran the container as `spc_t`. A separate follow-up enabled Docker's SELinux integration, preserved Enforcing mode, and produced `container_t` and `container_file_t` labels without an AVC or broad policy workaround. Debian then completed the same Docker and privilege-boundary workflow after its root filesystem was corrected to meet the capacity requirement. See [`docs/testing/platform-comparison.md`](../testing/platform-comparison.md). These results validate the Engine API design. They do not settle the production helper's mandatory-access-control policy.

## Revisit conditions

Revisit this decision if:

- ADR-0017 replaces Docker Engine as the first runtime;
- the supported Docker API cannot provide a required operation without a forbidden general interface;
- a selected language's SDK cannot limit fields and endpoints to this contract;
- Docker version behavior makes direct API compatibility too costly for the support matrix;
- a second runtime proves that the KITPro request model contains Docker-only concepts;
- the ownership registry cannot recover safely after state loss;
- per-instance networks cannot support the selected first application;
- the ingress design needs a different Docker network topology; or
- enforcing SELinux cannot support the approved storage and helper access without a broader policy than the threat model permits.

## References

- [Docker Engine API](https://docs.docker.com/reference/api/engine/) documents direct HTTP access, API versioning, and negotiation.
- [Docker Engine security](https://docs.docker.com/engine/security/) explains why daemon control and arbitrary container parameters can become host control.
- [Docker object labels](https://docs.docker.com/engine/manage-resources/labels/) documents labels on containers, volumes, and networks, reverse-DNS key guidance, filtering, and label immutability.
- [Docker port publishing](https://docs.docker.com/engine/network/port-publishing/) documents default exposure, host bindings, and network behavior.
- [Docker firewall behavior](https://docs.docker.com/engine/network/firewall-nftables) documents nftables, iptables, firewalld, forwarding, and Docker network interaction.
- [Docker image pull by digest](https://docs.docker.com/reference/cli/docker/image/pull/) documents immutable digest references and their update tradeoff.
- [OCI image manifest specification](https://specs.opencontainers.org/image-spec/manifest/) defines content-addressed images and multi-platform indexes.
- [Red Hat SELinux documentation](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/pdf/using_selinux/creating-selinux-policies-for-containers_using-selinux) covers SELinux policy for container mounts, ports, capabilities, and processes.
