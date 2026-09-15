# KITPro Server threat model

## Typed accelerator boundary

Accelerators add a device-mediated path into a container, so the helper treats them as privileged owned resources. Only registered classes are representable. The API supplies no host path, and the helper compares its request with the embedded manifest before fresh sysfs/procfs discovery. Trusted state records stable identity and exact mappings. Device disappearance, renumbering, vendor mismatch, extra mapping, component spread, and restore onto different hardware fail closed or reconcile as security drift. KITPro does not install drivers or vendor runtimes.

## 1. Purpose

This document defines the security threats that KITPro Server must address before implementation. It turns [ADR-0018](../decisions/0018-security-boundaries.md), the [privileged-helper protocol](../decisions/0003-privileged-helper-protocol.md), the [Docker integration](../decisions/0004-docker-integration.md), the [durable state and reconciliation model](../decisions/0016-durable-state-and-reconciliation.md), and [ADR-0019](../decisions/0019-helper-mandatory-access-control.md) into concrete attacker paths, expected controls, and unresolved questions.

This is a design threat model, not a security assessment of running software. KITPro has no application implementation to test yet. Each control remains a requirement until tests prove it on the supported host.

## 2. Scope

The model covers the Phase 1 workflow on Debian 13, the primary/reference host. Rocky Linux 10 is a secondary experimental host and must preserve the same trust boundaries:

- installation and first-run setup;
- authenticated local dashboard and API access;
- host discovery;
- catalog manifest loading and validation;
- one OCI application deployment;
- application health, start, stop, logs, update, rollback, and uninstall;
- persistent-data preservation;
- privileged host and container-runtime operations;
- local state, configuration, secrets, and audit history;
- backup and restore design, even if full backup automation follows the first slice; and
- the boundary that any future KITPro cloud service must respect.

The model assumes that application containers and local-network devices may be hostile. It does not assume that the home LAN is safe.

## 3. Assets

| Asset | Why it matters | Protection goal |
| --- | --- | --- |
| Host root privileges | Root controls every local workload, file, identity, and network setting | No browser-facing component or catalog workload receives general root authority |
| Container-runtime socket | Rootful Docker control can create host-affecting containers and mounts | Only the privileged helper can reach it |
| Application data | It may contain personal files, account data, media, and databases | Prevent cross-application access, unintended deletion, and silent corruption |
| Host filesystem | It contains the operating system, KITPro, and unrelated user files | Limit writes to owned paths and named operations |
| Application secrets | They may unlock applications, databases, or external providers | Restrict each value to its intended consumer and keep it out of normal output |
| KITPro authentication credentials | They authorize administrative actions | Prevent disclosure, offline recovery of plaintext values, fixation, and reuse after invalidation |
| TLS private keys | They authenticate the dashboard endpoint and protect traffic | Limit access to the TLS endpoint and certificate-management operation |
| Backup credentials | They may grant broad read or write access to backup storage | Isolate them from applications and ordinary dashboard reads |
| Configuration | It controls listeners, storage, applications, and security policy | Track ownership, validate changes, and preserve recovery copies where practical |
| Application manifests | They request workload privileges and host resources | Verify provenance and enforce a constrained local policy |
| Update metadata | It selects code and images that may run with high authority | Verify publisher, integrity, freshness, platform, and version policy |
| Control-plane state | It records desired state, administrator requests, and user-facing operation metadata | Protect integrity without allowing it to grant privileged ownership |
| Helper state | It records trusted ownership, privileged receipts, leases, observed external IDs, and helper audit events | Protect integrity and durability because its loss removes destructive authority |
| Host network configuration | It determines which services an attacker can reach | Prevent silent exposure and preserve administrator-owned policy |
| Logs | They may contain personal data, paths, tokens, and failure details | Bound access and retention, and redact secrets before writing |
| Audit history | It explains privileged actions and supports incident response | Record at the privilege boundary and resist ordinary modification |

Root can read or alter most local assets. Encryption with a key on the same host does not change that fact. The design uses encryption, file permissions, and process separation to reduce exposure to lesser compromises and accidents.

## 4. Actors

### Unauthenticated local-network user

This actor can reach any KITPro listener exposed on the LAN. The actor may scan endpoints, attempt authentication, submit browser requests, exploit parsers, or exhaust bounded resources. The actor has no administrative authority.

### Authenticated KITPro administrator

This actor may request supported lifecycle operations. Authentication proves an administrative identity, not that each input is safe. Server-side authorization, manifest policy, path checks, and destructive-action confirmation still apply.

### Normal non-admin host user

This actor has a local account but no KITPro administrative role. The actor may inspect permitted processes and files, connect to local sockets, race file operations, or try to join groups. KITPro service identities, sockets, state, and secrets must not be readable or writable through ordinary account access.

### Malicious or compromised container

This actor controls code inside one managed application. It can use every mount, secret, network path, namespace, device, and capability granted to that container. It may attack the kernel, KITPro listeners, other applications, the LAN, and internet services.

### Malicious application manifest

This actor controls structured application input. It may request host mounts, privileged mode, host networking, devices, capabilities, commands, environment values, or resource settings that convert a catalog deployment into host compromise.

### Compromised application image

This actor controls the image entry point and all image code. Image provenance identifies an artifact. It does not make the code safe. The image may steal its own application secrets, corrupt mounted data, probe the network, or exploit the kernel and runtime.

### Compromised KITPro frontend

This actor runs script in the KITPro browser origin through a malicious dependency, injection flaw, or altered frontend asset. It can act with the administrator's active browser permissions. It must not possess helper credentials, runtime access, or bulk secret-read access.

### Compromised KITPro backend

This actor executes code as the unprivileged KITPro service identity. It can process API traffic and may request allowed helper operations. The privilege boundary must prevent arbitrary root commands, runtime access, arbitrary host writes, and unrestricted secret access.

### Compromised privileged helper

This actor executes inside the component allowed to make root-level changes and reach the container runtime. This is a root-equivalent failure. The project reduces its likelihood and audit difficulty by keeping the helper small, local, allowlisted, and free of browser or internet parsing.

### Attacker with local shell access

This actor has the permissions of a compromised local account. If the account has sudo, root, or Docker access, it is already root-equivalent. Otherwise, the actor must remain outside KITPro administration and privileged sockets.

### Attacker with root access

This actor controls the host. Preventing or containing an attacker who already has root is not a Phase 1 security goal. Detection, audit export, credential rotation, backup isolation, and recovery may still reduce later damage.

### Remote attacker reaching an exposed service

This actor can reach the dashboard, a managed application, or another host service through LAN routing, port forwarding, VPN, IPv6, or misconfigured firewall policy. The actor may compromise an application and then attack local services from the container.

### Compromised optional future KITPro cloud service

This actor controls cloud-side data, responses, notifications, and commands. It must not gain a reusable general root credential or direct access to the privileged helper.

## 5. Trust boundaries

| Boundary | Data crossing it | Required treatment |
| --- | --- | --- |
| Browser to KITPro API | Credentials, requests, form data, operation approvals, displayed secrets and logs | Authenticate, authorize, protect transport, enforce origin and CSRF policy, validate size and type |
| Frontend asset to browser session | Code that can make authenticated requests and render sensitive data | Verify delivery integrity, apply CSP, encode output, minimize exposed secrets |
| Unprivileged service to privileged helper | Framed JSON operation, semantic target, expected state, and claimed administrator or session correlation | Use a protected Unix socket, verify `SO_PEERCRED`, reject unknown schema, prevent replay, enforce helper policy, and treat caller-supplied human context as untrusted audit data |
| Privileged helper to Docker Engine | Allowlisted versioned API calls, images, mounts, networks, secrets, health, logs, and untrusted responses | Keep the socket inside the helper, construct requests from safe types, and permit no raw API proxy |
| KITPro to application containers | Environment, files, secrets, health probes, signals, logs, and networks | Grant per-application access, distrust output, and isolate applications from KITPro |
| Container to host | Kernel calls, mounts, devices, namespaces, network, and runtime interfaces | Deny broad grants, apply runtime isolation, and assume a kernel escape remains possible |
| KITPro to filesystem | Configuration, state, data, temporary files, sockets, and logs | Use owned roots, safe path resolution, atomic changes, strict modes, and mount checks |
| KITPro to network and firewall configuration | Listeners, ports, routes, names, certificates, and filtering rules | Detect existing ownership, preview changes, bind narrowly, and fail on unknown policy |
| KITPro to update sources | Metadata, packages, manifests, images, signatures, and revocation data | Verify source, authorization, integrity, freshness, compatibility, and rollback policy |
| KITPro to backup systems | Credentials, snapshots, archives, retention, and restore data | Isolate credentials, verify backup and restore integrity, and authorize destructive writes |
| KITPro to future cloud services | Device identity, status, commands, telemetry, and remote access | Minimize data, authenticate both ends, enforce local policy, and provide revocation and offline operation |

The privileged helper treats the unprivileged service as a hostile client. The runtime and kernel remain external high-trust dependencies even when both run locally.

## 6. Entry points

Phase 1 and later designs must account for these entry points:

- dashboard HTTP and HTTPS listeners;
- API routes, authentication, first-run bootstrap, password or key recovery, and logout;
- cookies, authorization headers, CSRF tokens, Origin, Host, proxy, and Fetch Metadata headers;
- URLs, route parameters, query values, request bodies, uploaded files, and streamed logs;
- privileged-helper socket and operation messages;
- catalog metadata and application manifests;
- container image registries, image configuration, entry points, health checks, and labels;
- Docker Engine API responses, pull progress, health data, logs, labels, and error text;
- filesystem paths, symbolic links, mount points, permissions, quotas, and removable storage;
- application ports, container networks, host listeners, DNS, IPv4, IPv6, and firewall state;
- package repositories, update metadata, signing keys, packages, and rollback data;
- backup destinations, backup credentials, archive metadata, and restore inputs;
- local environment variables and service configuration;
- optional future cloud connections, commands, notifications, and telemetry; and
- diagnostic bundles and exported audit data.

## 7. Threat scenarios

The scenarios use STRIDE categories: spoofing, tampering, repudiation, information disclosure, denial of service, and elevation of privilege.

### Scenario: compromised KITPro web process

Categories: elevation of privilege, information disclosure, tampering.

Attack path:

1. An attacker exploits a request parser, session flaw, template, or web dependency.
2. The attacker gains code execution as the web and API service identity.
3. The attacker tries to open the Docker socket, call the helper, read all secrets, or write `/etc`.

Required answers:

- Can it obtain arbitrary root execution? No by design. It can request only fixed helper operations, which the helper revalidates.
- Can it directly reach the Docker socket? No.
- Can it modify arbitrary host files? No.
- Can it retrieve all application secrets? No. Secret access is partitioned and normal read-back is absent.

Residual risk: the compromised service can request every semantic helper operation authorized to its service identity. The helper limits targets, paths, Docker grants, destructive behavior, and argument values. It does not prove human intent. Administrator or session metadata remains an untrusted audit claim unless an independent trusted component verifies it.

Prototype evidence: the repository fixture proves that a fixed operation vocabulary can reject caller-supplied Docker mechanisms and arbitrary targets, and that the helper can obtain the connecting UID through `SO_PEERCRED`. This is partial evidence only. Root/systemd isolation and real Docker denial remain untested on the candidate hosts.

### Scenario: malicious application definition

Categories: elevation of privilege, tampering, information disclosure.

Attack path:

1. An attacker publishes or imports a manifest that appears to describe a useful application.
2. The manifest requests `/:/host` as a bind mount, privileged mode, host networking, dangerous capabilities, a runtime socket, or a device.
3. A naive installer forwards the manifest to Docker.
4. The application gains host control or access to unrelated data.

Required outcome: normal catalog manifests cannot express these grants. Both the manifest parser and privileged helper reject them. The effective deployment plan shows every approved mount, port, capability, secret, and image before the administrator approves it.

Residual risk: a catalog reviewer or policy defect may approve a dangerous combination of individually allowed fields. Tests must cover combinations, not only single fields.

### Scenario: compromised container

Categories: elevation of privilege, information disclosure, denial of service.

Attack path:

1. An attacker exploits Jellyfin, Immich, Nextcloud, or another managed application.
2. The attacker reads the container's secrets and mounted data.
3. The attacker probes the KITPro API, other containers, LAN services, kernel interfaces, and any accidental host mount.
4. The attacker attempts a kernel or runtime escape.

Required outcome: compromise of one application does not grant access to KITPro state, the runtime socket, the helper, unrelated application data, or host namespaces. Each workload receives only its own mounts, networks, secrets, resources, and reviewed capabilities.

Residual risk: the attacker controls the compromised application's data and secrets. Kernel and runtime isolation reduce risk but do not prove that escape is impossible. Network egress limits and per-application networks remain open decisions.

### Scenario: malicious browser request

Categories: spoofing, tampering, elevation of privilege.

Attack path:

1. An authenticated administrator visits an attacker-controlled website.
2. The site submits a form, script request, image request, WebSocket connection, or DNS-rebinding request to KITPro.
3. The browser attaches an ambient KITPro session or reaches the local address.
4. KITPro installs, removes, updates, restores, or exposes an application without the administrator's intent.

Required outcome: state changes require non-safe methods, an authenticated session, server-side authorization, CSRF protection, and an accepted origin. CORS rejects unlisted origins. Host validation resists DNS rebinding. Framing protections prevent clickjacking.

Residual risk: script execution within the KITPro origin can make legitimate-looking requests. CSP, dependency control, output encoding, session design, recent-authentication checks, and clear confirmations reduce that risk.

### Scenario: compromised KITPro frontend

Categories: spoofing, information disclosure, tampering.

Attack path:

1. A frontend dependency or served asset is altered, or an injection flaw executes script in the KITPro origin.
2. The script reads displayed data and invokes API actions with the active session.
3. The script attempts to steal persistent credentials or bypass confirmations.

Required outcome: the frontend has no helper or runtime credential. Session secrets are unavailable to script where the authentication design permits. APIs do not return stored application or backup secrets. High-impact operations require server-enforced checks that UI code cannot remove.

### Scenario: exposed container-runtime socket

Categories: elevation of privilege, tampering, information disclosure.

Attack path:

1. A web process, container, local user, or diagnostic tool gains access to the rootful Docker socket.
2. The attacker creates a container that mounts `/` or accesses a host device.
3. The attacker changes host files, credentials, services, or network policy as root.

Required outcome: only the privileged helper can reach the local Unix socket. KITPro does not enable the runtime TCP API. The installer does not add administrators or the web-service user to the `docker` group.

### Scenario: forged Docker ownership

Categories: spoofing, tampering, elevation of privilege.

Attack path:

1. A compromised API submits the Docker ID of a user-owned container as an uninstall target.
2. A local Docker administrator copies KITPro labels onto a foreign container or creates a second object with the same labels.
3. A weak helper treats the ID, name, or labels as ownership proof and deletes the object.

Required outcome: the protocol accepts only a semantic application-instance target. Before any mutation, the helper requires its protected authority record, exact labels, the expected Docker object kind, the Docker daemon identity, and current-state preconditions to agree. Missing, conflicting, duplicate, and label-only objects remain untouched and appear as repair cases.

Residual risk: an attacker with Docker access is already root-equivalent and can alter Docker objects or the helper's view of the host. KITPro's checks prevent accidental and API-originated deletion. They do not contain host root.

### Scenario: replayed or ambiguous helper request

Categories: spoofing, tampering, repudiation.

Attack path:

1. A compromised or restarted API resends an operation after losing its response.
2. An attacker reuses an operation ID with a different target or body.
3. A naive helper performs the mutation twice or attributes the second action to the first request.

Required outcome: each mutation binds an operation ID to the canonical request digest and caller identity in a protected receipt before the first external change. An exact replay returns the recorded state. Reuse with different content fails. Unknown protocol fields and versions fail before mutation.

Residual risk: a compromised API can create new valid operation IDs for every operation allowed to its service identity. High-impact human-authorization proof remains open and cannot be replaced by API-supplied metadata.

### Scenario: Docker changes between observation and mutation

Categories: tampering, elevation of privilege.

Attack path:

1. The helper observes a valid KITPro-owned container or network.
2. A local Docker administrator, compromised process, or concurrent KITPro executor replaces or changes the object.
3. The helper acts on the stale observation and mutates the replacement.

Required outcome: cached observations never authorize mutation. The helper serializes each instance mutation with a durable fenced lease. It rechecks the current Docker daemon, object ID, labels, effective configuration, and security-sensitive fields immediately before dispatch. Destructive operations validate the whole target set before the first delete.

Residual risk: Docker and host root can change state after the final check. KITPro reduces that race with deterministic targets, Docker preconditions where available, short check-to-use windows, and postcondition inspection. It cannot contain an attacker who already controls root or Docker.

### Scenario: compromised API fabricates recovery state

Categories: spoofing, tampering, elevation of privilege.

Attack path:

1. A compromised API changes desired state or inserts a false ownership claim in its database.
2. It asks the helper to adopt, remove, or replace a foreign Docker object.
3. A helper that trusts control-plane state grants destructive authority.

Required outcome: the helper owns an independent protected store for ownership intent, privileged receipts, leases, and observed Docker identifiers. A control-plane row never proves ownership. Recovery or adoption requires fresh full inspection, explicit administrator intent, and a helper-produced audit event.

### Scenario: operation receipt tampering or rollback

Categories: tampering, repudiation, elevation of privilege.

Attack path:

1. An attacker changes a running receipt to succeeded, changes its request hash, reuses a lease, or restores an older helper-store backup.
2. The helper skips a required phase or applies an operation to the wrong generation.
3. Later cleanup deletes a foreign or newer resource.

Required outcome: only the helper and controlled recovery tooling can write the helper store. Transactions bind the operation ID, canonical request hash, phase, ownership intent, lease, fencing token, and audit event. Startup detects corrupt or inconsistent state and blocks mutation. Restore invalidates cached observations and requires cross-store reconciliation.

Residual risk: the exact tamper-evidence, backup generation, and mandatory-access-control mechanisms depend on later storage and confinement decisions.

### Scenario: reconciliation amplifies malicious drift

Categories: tampering, denial of service, elevation of privilege.

Attack path:

1. An attacker or administrator changes a KITPro-managed object, attaches a foreign network, or creates a labeled replacement.
2. An autonomous controller treats the difference as routine drift.
3. The controller adopts the object, overwrites the change, repeatedly restarts it, or deletes another resource.

Required outcome: startup and periodic reconciliation are observation-only. Only an existing receipt with a proven safe continuation may resume automatically. Ownership conflicts, user modifications, security drift, and state corruption block mutation and require an explicit audited recovery action.

### Scenario: unsafe resource adoption

Categories: spoofing, elevation of privilege.

Attack path:

1. A labeled container remains after helper state loss, or an attacker creates a convincing replacement.
2. An administrator uses a future adoption workflow without reviewing its effective grants.
3. KITPro records the malicious container as trusted and later operates on it.

Required outcome: adoption is a separate high-impact operation. It compares the immutable image digest, complete effective configuration, mounts, namespaces, capabilities, devices, ports, network membership, storage identity, and installation identity. The interface shows every mismatch, requires fresh authorization, and records the decision at both trust boundaries. Security drift cannot be waived through a generic adoption command.

### Scenario: path substitution during a privileged operation

Categories: tampering, elevation of privilege.

Attack path:

1. A local user or compromised service supplies an allowed-looking application path.
2. After validation, the attacker replaces a path component with a symbolic link or changes the mount.
3. The helper writes, changes ownership, archives, or deletes a protected host path.

Required outcome: the helper uses approved roots, descriptor-relative operations where practical, no-follow semantics, mount identity checks, and operation-specific path types. It rechecks mutable facts at the point of use.

### Scenario: compromised privileged helper

Categories: elevation of privilege, tampering, information disclosure.

Attack path:

1. An attacker exploits helper parsing, a runtime response, a library, or a logic error.
2. The attacker executes within the privileged boundary.
3. The attacker controls the host or runtime.

Required outcome: no architecture claim treats this failure as contained. The helper has no network listener, no general command interface, few dependencies, small parsers, strict inputs, systemd hardening, and a platform MAC profile or domain. Audit export and off-host backups may help recovery, but they do not prevent compromise. Docker socket access remains a residual host-root path because MAC cannot constrain every action taken by dockerd after an allowed API request.

### Scenario: malicious or stale update

Categories: spoofing, tampering, elevation of privilege.

Attack path:

1. An attacker compromises a mirror, registry account, build job, signing key, or update channel.
2. KITPro receives a valid-looking package, manifest, image, or older vulnerable version.
3. The privileged helper installs it.

Required outcome: update policy verifies publisher authorization, digest, freshness, platform, version progression, revocation, and compatibility. Discovery does not equal approval to install. Rollback uses a recorded known-good artifact and states what happens to persistent data.

Residual risk: an authorized publisher may intentionally or accidentally sign harmful code. Source and build provenance, review, reproducibility, staged rollout, and revocation reduce this risk. A signature alone does not.

### Scenario: poisoned backup or restore

Categories: tampering, information disclosure, denial of service.

Attack path:

1. An attacker steals backup credentials, alters an archive, or supplies paths that escape the restore target.
2. KITPro restores stale, malicious, or incomplete data over a running application.
3. The application corrupts state, executes attacker-controlled content, or loses newer data.

Required outcome: backup credentials have narrow access. Restore verifies archive identity and integrity, blocks path traversal and unsafe links, checks application compatibility, stops dependent writers, previews replaced data, and records the operation. Restore never follows a normal uninstall automatically.

### Scenario: compromised future cloud service

Categories: spoofing, tampering, elevation of privilege, information disclosure.

Attack path:

1. An attacker compromises KITPro cloud infrastructure or a cloud operator credential.
2. The attacker sends commands to connected servers or steals stored device credentials.
3. A local agent forwards cloud input to the privileged helper.

Required outcome: cloud identity is not a root credential. The local unprivileged service verifies message authenticity, freshness, device scope, and user policy. The privileged helper still accepts only fixed operations and local policy. Destructive or privilege-expanding remote actions require a separately approved model. Revoking cloud access leaves local operations intact.

### Scenario: hostile local user

Categories: elevation of privilege, information disclosure, repudiation.

Attack path:

1. A non-admin user enumerates KITPro processes, files, and Unix sockets.
2. The user attempts to connect to the helper, replace an operation file, read secrets, or alter audit records.
3. The user races a privileged path operation or reuses a captured request.

Required outcome: file ownership and socket peer checks deny access. Messages bind to an authenticated service identity and reject replay. The helper does not trust path ownership or prior validation after a race window.

### Scenario: public application compromise becomes a host pivot

Categories: elevation of privilege, information disclosure.

Attack path:

1. Router forwarding, IPv6, a reverse proxy, or Docker port publishing exposes an application to the internet.
2. A remote attacker compromises the application.
3. The container reaches local management services, unrelated applications, or a cloud-instance metadata endpoint.

Required outcome: exposure is explicit and visible. Runtime ports bind only to the intended address. Application networks and host listeners do not grant trust. The dashboard remains authenticated and is not exposed publicly in Phase 1. Cloud metadata and egress policy require explicit treatment before public-cloud support.

## 8. Security invariants

The canonical list is in [`docs/security/invariants.md`](invariants.md). In summary:

- The web and API service is unprivileged and has no runtime socket.
- The browser cannot address the privileged helper.
- The helper accepts only framed, versioned, typed operations from the fixed API UID and enforces security policy itself.
- The helper runs under an enforcing AppArmor profile on Debian or a dedicated SELinux domain on supported RHEL-family hosts. Policy-load failure never falls back silently to an unconfined helper.
- The helper does not require arbitrary shell execution, Docker CLI, Compose CLI, or general outbound IP networking.
- Docker mutation requires a protected ownership record and matching labels, kind, daemon identity, and state.
- Unknown Docker outcomes reconcile through deterministic identity and fresh inspection before retry.
- Durable fenced leases prevent two executors from committing mutations for the same instance.
- Reconciliation observes ambiguous drift but does not adopt, rewrite, or delete it automatically.
- Catalog manifests cannot request arbitrary host authority.
- Containers cannot access KITPro control paths, unrelated application data, or another instance's KITPro network by default.
- The dashboard and administrative operations require authentication.
- Browser-origin controls protect every state change.
- Secrets do not appear in normal logs, audit events, URLs, or read APIs.
- Updates require source, integrity, freshness, compatibility, and rollback checks.
- Catalog releases deploy fully qualified images by immutable digest rather than tag alone.
- Normal uninstall preserves persistent application data.
- KITPro does not disable SELinux or embed distribution package and firewall behavior in the helper protocol.
- Local operation does not trust or require KITPro cloud services.
- A KITPro cloud compromise does not automatically imply host-root compromise.

An implementation that violates an invariant must stop release or propose a new ADR. It must not document the violation as an ordinary exception.

## 9. Privilege model

### Unprivileged service

The web and API service owns browser authentication, product workflow, operation planning, and user-visible state. It runs as a dedicated host identity with no login shell and no broad administrative groups. Its writable files do not overlap helper executables, helper policy, update trust roots, or protected audit output.

The service may request an operation by stable identifiers and typed values. It does not send shell text, arbitrary environment blocks, unrestricted paths, Docker object IDs as targets, or raw runtime payloads.

### Privileged helper

The helper is a root-owned, systemd socket-activated daemon. It accepts length-prefixed typed JSON over a protected Unix stream socket. It verifies the fixed API UID with `SO_PEERCRED` before it parses an operation.

The helper owns enforcement at the root boundary. It verifies the request schema, caller authorization, target ownership, current host state, path and mount safety, catalog policy, and operation preconditions. Socket caller authentication does not replace these checks. Administrator or session metadata from the API does not prove human intent.

The helper should have no network listener. It should not fetch manifests, images, packages, web content, or cloud messages directly unless a later ADR proves that no safer ownership exists. Network parsing belongs outside the root boundary.

The helper stores operation and phase intent before each external mutation. It owns accepted privileged work across API disconnects and restarts. Exact replays return the recorded operation state. Conflicting reuse fails. Unknown external outcomes reconcile before retry. The helper emits events for acceptance, each phase, uncertainty, recovery, and the final result.

The helper calls only the allowlisted Docker Engine API subset in ADR-0004 over Docker's local Unix socket. It does not invoke Docker or Compose through a command shell.

Platform packages add an AppArmor profile or an SELinux domain, peer-context rule, file contexts, and container-storage rules. The core messages carry semantic access intent rather than raw security-policy options. KITPro treats an AppArmor or SELinux denial as a policy or packaging failure. It never weakens mandatory access control to complete an operation. The helper profile or domain cannot replace semantic Docker validation because the Docker daemon retains its own host authority.

An Enforcing host is not sufficient evidence by itself. Validation must also prove that the helper enters its intended confined domain and that Docker assigns normal process and mount labels to application containers. A helper in `unconfined_service_t` or a container in `spc_t` is an unconfined-boundary finding even when no AVC is emitted.

### Root and sudo

Installation, repair, upgrade, and removal require root or sudo. The browser never triggers an interactive sudo prompt. A broad passwordless sudo rule for a shell, package manager, container CLI, or user-controlled arguments is forbidden.

### Failure posture

If identity, policy, mount state, runtime compatibility, update trust, or audit recording cannot be verified, the privileged operation fails closed. Read-only health information may remain available when it does not expose secrets.

## 10. Container and runtime risks

Containers share the host kernel. Namespaces and cgroups constrain processes, but neither turns hostile code into a virtual machine. A runtime or kernel flaw can cross the boundary.

Normal catalog workloads start from these restrictions:

- no privileged mode;
- no host PID, IPC, user, or network namespace;
- no runtime socket or KITPro control socket;
- no devices unless a later application-specific security review approves one;
- no added Linux capabilities unless a reviewed feature requires a named capability;
- no disablement of the selected seccomp or mandatory-access-control policy;
- no mounts outside application-owned data, reviewed shared data, and narrow read-only system facts;
- no access to another application's network or data by default;
- one KITPro-owned user-defined bridge network per application instance;
- explicit CPU, memory, process, and log bounds; and
- a non-root container user when the application supports one.

Root inside a container is not host root, but broad mounts, capabilities, devices, host namespaces, or runtime control can collapse that distinction. KITPro must present effective grants, not reassuring labels.

Runtime events, labels, logs, names, and error text are also untrusted input. The helper and web service must bound and encode them before storage or display.

## 11. Application-manifest risks

The manifest model is a policy language. Every feature it can express expands the authority available to catalog publishers and attackers.

Phase 1 needs a small schema with closed sets, typed values, size limits, versioning, and deny-by-default handling of unknown fields. Expansion, templates, defaults, and substitutions occur before the privileged policy check. A safe source document can otherwise produce an unsafe effective workload.

Catalog review and signatures establish who published a manifest. The local validator decides what the manifest may do. Both are required.

The manifest must not contain secret values. It may declare named secret needs and how the application consumes them. The administrator or another authorized local source provides the values.

Raw Compose support is a separate advanced feature because Compose can express host-affecting runtime options. Importing Compose into the normal catalog would either create hidden incompatibility or expose too much authority. The privileged helper does not accept a Compose document, Docker argument list, raw Engine API request, arbitrary host path, or caller-supplied Docker deletion target.

## 12. Network exposure

KITPro manages systems on networks it does not own. Existing routes, interfaces, resolvers, firewalls, VPNs, reverse proxies, router forwarding, and IPv6 may change effective exposure.

Phase 1 treats every listener as a security decision:

- show the protocol, bind address, port, purpose, and expected reach before deployment;
- publish no runtime application port by default;
- permit only an explicit, policy-approved, unprivileged loopback binding until the accepted ingress design replaces this rule;
- never infer safety from `ufw` alone when Docker publishes a port;
- test IPv4 and IPv6 separately;
- detect port conflicts before mutation;
- reject an unknown firewall state rather than disable or replace it;
- do not expose the Docker Engine API on TCP; and
- do not support public dashboard exposure.

The dashboard's LAN listener still requires authentication and TLS. Exact certificate and ingress choices remain open.

DNS rebinding deserves explicit tests. The server must reject unexpected Host values and validate the source and target origin for state-changing browser traffic. A private IP address does not prevent a public site from causing a browser to contact it.

## 13. Authentication and session risks

The main threats are first-run takeover, password guessing, credential theft, session fixation, session reuse, CSRF, clickjacking, cross-origin mistakes, XSS, and recovery bypass.

Required behavior includes:

- no anonymous dashboard data or actions;
- a first-run flow that cannot be claimed by the first LAN visitor;
- server-side authorization for every operation;
- no state changes through `GET`, `HEAD`, or `OPTIONS`;
- CSRF protection plus origin checks on state changes;
- exact CORS allowlists only when cross-origin use is required;
- secure session transport and script-access controls;
- session rotation after authentication and privilege changes;
- idle and absolute expiry;
- logout and administrative invalidation;
- bounded authentication attempts and useful audit events;
- no credentials in URLs or browser storage that any same-origin script can read; and
- server-enforced confirmation or recent authentication for selected high-impact operations.

The exact credential, recovery, session, and multi-factor design remains for ADR-0008. Local account integration is not assumed.

## 14. Secrets

Secrets need an inventory, owner, consumer list, creation source, rotation path, deletion behavior, and backup policy. A value without these properties is unmanaged.

The web process should receive a secret only when its product responsibility needs the plaintext. The frontend should not receive stored secret values. The helper receives only secrets required for the current privileged operation, and managed containers receive only their declared application secrets.

Secret redaction happens before log and audit serialization. A display-time filter leaves copies in files and databases. Diagnostic exports apply the same rule.

Environment variables may be visible through runtime inspection and process metadata. File mounts, runtime secret facilities, or application-specific inputs may be safer, but the project must decide per supported application. No method makes a secret safe after its intended container is compromised.

Backup credentials require separate treatment because they can expose many applications and historical data. Managed applications must not receive them.

## 15. Update and supply-chain risks

The supply chain includes source control, build workers, dependencies, release signing, package repositories, catalog publication, image registries, mirrors, trust-root distribution, and the local installer.

Threats include:

- a compromised maintainer or publisher account;
- a malicious or compromised dependency;
- altered build output;
- forged, stale, frozen, or rolled-back metadata;
- a mutable image tag that changes after review;
- an image for the wrong CPU or operating system;
- key theft and failed revocation;
- dependency confusion or repository substitution;
- partial installation after power loss; and
- a signed update that is incompatible or intentionally harmful.

KITPro must identify installed artifacts by version and digest. A catalog release names the registry, repository, platform, and immutable digest. Tags support display and discovery but never identify the deployed image. The helper records the Docker image ID and repository digest after inspection. It retains approved index and platform-manifest digests from catalog metadata when provided.

KITPro must verify authorization and integrity before privileged work. Update checks may run automatically, but installation requires the policy and intent defined by ADR-0011. A digest proves content identity. It does not prove that the publisher or image is safe.

Rollback is not one operation. Restoring a package, manifest, or image does not restore application data changed by a migration. The interface and audit record must describe each recovery boundary separately.

## 16. Logging and auditing

Operational logs help diagnosis. Audit events establish what the privileged boundary did. They are related but not interchangeable.

Normal logs must avoid credentials, authorization headers, cookies, CSRF tokens, private keys, full manifest secrets, backup credentials, and unnecessary application content. Error messages cross from the helper and runtime into the web interface as untrusted data.

Privileged audit events record:

- the authenticated actor or calling service;
- source context and request identifier;
- operation and target;
- policy decision and reason;
- effective non-secret change summary;
- artifact version and digest where relevant;
- start and completion time; and
- result, interruption, recovery, or rollback.

The helper must emit the authoritative privileged event. Containers, frontend code, and normal users cannot modify the event store. Retention, export, cryptographic tamper evidence, and off-host copies remain open.

Logs are also a denial and storage risk. The system needs per-source size and rate bounds so that a noisy container cannot fill the host or hide relevant events.

## 17. Backup and restore risks

Backup design must cover confidentiality, integrity, availability, and recovery. A backup that has never passed a restore test is unverified.

Threats include stolen destination credentials, broad write permissions that permit ransomware, unencrypted off-host data, incomplete database snapshots, silent retention failure, corrupted archives, path traversal during restore, incompatible application versions, and restoration over newer data.

Required behavior includes:

- define each application's protected data set;
- separate backup credentials from application credentials;
- prefer destination authority that cannot destroy all prior recovery points;
- encrypt data when it crosses or rests outside the trusted host boundary;
- verify archive identity and integrity before restore;
- coordinate application consistency and stop writers where required;
- preview destructive restore effects;
- treat restore as a separately authenticated and audited operation; and
- test recovery on an isolated target.

The backup provider, format, encryption, schedule, retention, and credential technology remain open in ADR-0014.

## 18. Future cloud boundary

Cloud services remain optional. Local workloads continue running and local administrators retain control when the cloud is unreachable or removed.

Any future cloud connection must start from these limits:

- the server initiates the connection unless a later design proves another path safe;
- cloud identity and update-signing trust use separate credentials and policy;
- the cloud does not store a general helper, runtime, root, or SSH credential;
- every command has a device scope, expiry, replay defense, and local authorization decision;
- the helper accepts only its fixed local operations regardless of command origin;
- destructive and privilege-expanding remote operations require a separate approved model;
- the user can revoke the cloud relationship locally; and
- the product documents every class of data sent off-host.

A cloud compromise may expose cloud-held metadata and permitted messages. It must not automatically imply root compromise of connected hosts. Preserving this invariant later will be harder than adding a remote shell, which is why the boundary is set now.

## 19. Explicit non-goals

Phase 1 does not claim to defend against:

- an attacker who already controls host root;
- physical attacks, hostile firmware, malicious hardware, or an untrusted hypervisor;
- unknown kernel or container-runtime escapes;
- a malicious administrator who intentionally approves a clearly described destructive action;
- confidentiality of data from the application that legitimately owns and processes it;
- public-internet exposure of the KITPro dashboard;
- multi-user roles, tenant isolation, cluster security, or fleet administration;
- raw Compose or unrestricted manual workload safety;
- optional cloud remote administration; or
- every denial-of-service condition caused by an application exhausting shared host resources.

These non-goals do not permit silent insecure defaults. KITPro must state when an operation leaves the modeled boundary.

## 20. Questions remaining

The following questions remain open and belong in later ADRs:

- How does the helper receive proof of administrator authorization without trusting a compromised backend to mint it?
- Which storage engines meet the transaction, crash-recovery, fencing, corruption, migration, backup, and local-operation requirements in ADR-0016?
- How will helper-store backup generations coordinate with control-plane backups without silently restoring stale destructive authority?
- Which receipt and audit mechanism provides useful tamper evidence without making recovery depend on KITPro cloud services?
- Which exact systemd hardening and AppArmor or SELinux policy controls work without blocking required helper operations?
- Which policy packaging, profile reload, file-context restoration, and upgrade behavior can fail closed on Debian and Rocky?
- What authentication, recovery, session, and recent-authentication model will ADR-0008 choose?
- How will ADR-0009 establish locally trusted TLS without teaching users to ignore warnings?
- Which exact Host and origin values are valid across IP addresses, local DNS, and a future reverse proxy?
- What ingress and egress policy applies between per-instance networks, KITPro, the LAN, and the internet?
- Which manifest fields and combinations will the catalog allow?
- What final data-root layout and storage-slot policy will ADR-0006 choose?
- Where do secret values live, and which component decrypts or reads each class?
- Which exact Docker Engine and API version range will release acceptance support?
- Which reverse-DNS label namespace does KITPro own permanently?
- How will update trust roots, delegation, expiry, revocation, and offline recovery work?
- Which audit store resists tampering without becoming a new source of lock-in?
- Which backup format and credentials allow independent restore without KITPro?
- What event, metric, and log retention fits small hosts?
- Which actions require recent authentication, separate confirmation, or physical presence?
- How will a future cloud connection prove that cloud compromise cannot become general host control?

These questions block the relevant implementation work. They do not require selecting a backend language, frontend framework, database, reverse proxy, authentication library, or TLS library in this milestone.
