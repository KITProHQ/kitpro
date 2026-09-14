# ADR-0018: Security boundaries

- Status: Accepted
- Date: 2026-09-12
- Owners: Unassigned
- Approval required: Josh
- Related principles: Local-first, No lock-in, Open foundations, Secure defaults, Inspectability, Reversibility, Standard workloads, Optional cloud
- Related decisions: ADR-0003, ADR-0004, ADR-0005, ADR-0007, ADR-0008, ADR-0009, ADR-0010, ADR-0011, ADR-0013, ADR-0014, ADR-0015, ADR-0016, ADR-0017
- Detailed model: [`docs/security/threat-model.md`](../security/threat-model.md)
- Required invariants: [`docs/security/invariants.md`](../security/invariants.md)

## Context

KITPro Server will accept browser requests and translate them into container, storage, network, update, backup, and host operations. Some of those operations require root-equivalent authority. A flaw in KITPro can therefore affect the whole host and every managed application.

The web process is exposed to the local network and parses complex input. It is not a suitable place to hold root privileges or unrestricted container-runtime access. Docker documents that a client with daemon control can mount the host root filesystem into a container and alter it without restriction. Podman documents that its API grants full engine functionality and arbitrary code execution as the service user.

"Local network" does not mean "trusted request." Browsers send ambient credentials, compromised applications can reach local addresses, and home networks often contain devices with uneven security. Authentication, browser-origin checks, and narrow host interfaces remain necessary.

## Decision

KITPro will use privilege separation as an architectural boundary:

```text
Administrator's browser
        |
        | authenticated HTTPS request
        v
KITPro web and API service
dedicated unprivileged host identity
        |
        | local, authenticated, versioned, allowlisted operation
        v
KITPro privileged helper
minimal root authority and no network listener
        |
        | validated host or runtime action
        v
systemd, filesystem, network controls, and container runtime
```

The diagram describes trust and responsibility. It does not choose a language, web framework, internal protocol, process supervisor, or database.

The privileged helper is the only KITPro component allowed to reach the rootful container-runtime socket or make privileged host changes. The web and API service must not run as root, join the `docker` group, receive the runtime socket, execute unrestricted sudo commands, or write arbitrary host paths.

The helper will expose a small set of typed KITPro operations. It will not expose a shell, generic command execution, raw Compose submission, arbitrary runtime API forwarding, arbitrary paths, or arbitrary systemd unit content. The helper must authenticate its local peer, authorize each operation, validate security policy independently of the web service, and emit its own audit event.

Normal catalog application manifests will use a constrained, versioned, allowlisted model. They may request only features that KITPro has modeled, reviewed, and can explain before deployment. Raw Docker Compose and unrestricted runtime options are not catalog manifest formats. A future manual or advanced workload mode requires a separate trust model and is outside Phase 1.

The dashboard is authenticated. First-run setup must establish administrative access before the dashboard accepts LAN administration. Local-network placement is not authentication. ADR-0008 will select the credential and session implementation.

The browser security model must include same-origin state changes, CSRF protection, a restrictive CORS policy, strict session handling, framing protection, and defenses against credential theft. The specific authentication and TLS libraries remain open.

Updates are authenticated supply-chain input. KITPro must verify the source, integrity, version, platform compatibility, and rollback policy for KITPro packages, application manifests, application images, and security metadata before privileged installation.

Every privileged operation must produce an audit event that excludes secret values. The helper must produce an event even if the web process fails after it requests the operation.

An optional future KITPro cloud service must not receive a general host-control credential. A cloud compromise must not automatically grant root control over a customer's server. Local operation must not depend on the cloud.

This decision is proposed, not accepted. ADR-0003 must still define the helper protocol and operating-system controls. ADR-0008 and ADR-0009 must still define authentication and local TLS.

## Security objectives

The design aims to preserve these outcomes:

- An unauthenticated LAN user cannot read the dashboard or invoke administrative operations.
- A malicious website cannot use an administrator's browser session to change KITPro state.
- A compromised frontend cannot communicate with the privileged helper.
- A compromised web or API service cannot execute arbitrary root commands, reach the runtime socket, or write arbitrary host files.
- A catalog manifest cannot request unrestricted host mounts, host namespaces, devices, Linux capabilities, or privileged mode.
- A compromised application container cannot read KITPro secrets, reach the runtime socket, modify KITPro state, or access unrelated application data by design.
- A compromised update or cloud service cannot bypass local verification and operation policy.
- An administrator can determine who requested a privileged operation, what KITPro authorized, what changed, and whether it succeeded.

The architecture reduces the effect of a web-service compromise. It cannot make a compromised privileged helper safe. The helper remains a root-equivalent security boundary and must be kept small enough to review and test thoroughly.

## Actors and authority

The threat model covers these actors:

| Actor | Expected authority | Security posture |
| --- | --- | --- |
| Unauthenticated local-network user | Network access to an exposed KITPro listener | Untrusted |
| Authenticated KITPro administrator | Approved administrative actions through the dashboard or API | Trusted for intent, but requests still require validation |
| Normal non-admin host user | Local shell and access to that user's files and processes | Untrusted for KITPro administration |
| Malicious or compromised container | Code execution inside one managed workload | Untrusted |
| Malicious application manifest | Structured deployment input | Untrusted, even if it appears in a catalog |
| Compromised application image | Code execution with the container's granted identity, mounts, network, and capabilities | Untrusted |
| Compromised KITPro frontend | Script execution in the KITPro browser origin | Untrusted |
| Compromised KITPro backend | Code execution as the unprivileged KITPro service identity | Untrusted and partly contained |
| Compromised privileged helper | Code execution inside the privileged boundary | Root-equivalent failure |
| Attacker with local shell access | Permissions of the compromised host account | Untrusted unless the account already has root-equivalent rights |
| Attacker with root access | Full host control | Outside the prevention boundary |
| Remote attacker reaching an exposed service | Network access to KITPro or a managed application | Untrusted |
| Compromised optional KITPro cloud service | Control of cloud data and messages sent from that service | Untrusted by the privileged boundary |

## Assets

The protected assets include:

- host root privileges;
- the container-runtime socket and equivalent API credentials;
- application data and the host filesystem;
- application secrets, backup credentials, KITPro authentication credentials, and TLS private keys;
- KITPro and application configuration;
- application manifests and their approval status;
- update metadata, signing trust, packages, and image identities;
- the state database and operation state;
- host routes, listeners, firewall policy, and name resolution;
- logs and audit history; and
- backup content and restore authority.

## Trust boundaries

The design recognizes these boundaries:

- browser to KITPro API;
- KITPro frontend code to the administrator's browser session;
- unprivileged KITPro service to privileged operations;
- KITPro to Docker or Podman;
- KITPro to application containers;
- container to host;
- KITPro to the host filesystem;
- KITPro to network and firewall configuration;
- KITPro to application manifests and catalog metadata;
- KITPro to update sources and image registries;
- KITPro to backup systems and restore inputs; and
- KITPro to optional future cloud services.

Every boundary must validate the identity, authorization, shape, size, and expected state of incoming data. Internal code may trust only the typed result of that validation. The privileged helper repeats security-critical policy checks because a compromised web process cannot be trusted to have performed them.

## Least privilege

### Security advantages

- Remote request parsing, templates, and browser-facing dependencies run without root.
- The web process cannot use the runtime socket as an indirect root shell.
- The helper can reject operations outside KITPro's defined lifecycle even when the caller is compromised.
- Separate identities and file ownership limit which component can read secrets or alter state.
- The local helper interface is smaller than a container engine API or a general sudo rule.
- The helper can create an audit record at the point where privileged work occurs.

### Operational costs

- The project must design, version, authenticate, and test an internal protocol.
- Installation must create service identities, permissions, sockets, and systemd policy correctly.
- Long-running operations need clear ownership when either process restarts.
- Debugging crosses a process boundary and must preserve useful errors without exposing secrets.
- Updates must keep both components compatible and recover safely from partial installation.
- The helper can become a policy duplicate if ownership is unclear. Security checks belong at its boundary, while product workflow belongs in the unprivileged service.

These costs are justified because direct root or runtime access in the web process turns common web flaws into immediate host compromise.

## Container-runtime access

Access to the rootful Docker socket is root-equivalent authority in the KITPro threat model. A client can create privileged containers, mount `/`, access devices, alter networks, and execute host-affecting workloads. File permissions on the socket are an authorization boundary, not evidence of low privilege.

KITPro will mediate runtime access through the privileged helper:

- The web and API service never receives the socket path or file descriptor.
- The browser never submits raw runtime API requests.
- The helper translates a fixed KITPro operation into the minimum required runtime calls.
- The helper constructs privileged runtime parameters from validated internal types. It does not forward manifest fields blindly.
- The helper rejects unknown runtime versions or capabilities until compatibility policy permits them.
- The Docker API remains on its local Unix socket. KITPro does not enable a TCP listener.

This containment does not make the Docker daemon low risk. A compromised helper or daemon may still imply host compromise. The runtime and helper require prompt security updates, narrow filesystem access, and separate audit coverage.

## Application-manifest policy

Catalog manifests are untrusted input. Publication, signatures, or repository ownership may improve provenance, but none removes the need for policy validation.

The normal catalog model must reject or omit:

- privileged containers;
- host PID, IPC, user, or network namespaces;
- the Docker or Podman socket;
- bind mounts outside approved application-specific roots;
- a bind mount of `/`, `/etc`, `/proc`, `/sys`, `/dev`, `/run`, runtime state, KITPro state, or unrelated application data;
- arbitrary device passthrough;
- arbitrary Linux capabilities or security-profile disablement;
- arbitrary systemd units, host commands, hooks, and package installation;
- arbitrary environment inheritance from the KITPro process;
- unbounded resource requests;
- arbitrary published interfaces or ports; and
- configuration that disables image or update verification.

A catalog application may receive a reviewed subset of mounts, capabilities, commands, environment values, ports, health checks, and resources. KITPro must show the effective grants before deployment. The helper validates the effective form after defaults and templates are resolved.

An advanced mode that accepts raw Compose or equivalent definitions would give its author much of KITPro's host authority. Such a mode needs separate warnings, authorization, storage rules, and support expectations. It is deferred.

## Secrets

The concrete secret store remains open. Any design must meet these requirements:

- Store secret values under access controls that limit each value to the components and workloads that need it.
- Encrypt secrets at rest when encryption protects against the identified threat. Document that a key stored on the same host cannot protect secrets from an attacker with root.
- Never place secrets in normal logs, audit records, URLs, process arguments, image layers, manifest files, or error details.
- Do not return stored secret values through normal read APIs. A write-only or replacement workflow is preferred.
- Do not expose a general secret-listing operation to the frontend.
- Pass a secret to a container through the narrowest runtime mechanism that the selected application supports. Record unavoidable environment-variable exposure as an application risk.
- Keep backup credentials outside application data and inaccessible to managed containers.
- Limit TLS private-key access to the component that terminates TLS and any narrowly defined certificate-management operation.
- Support rotation and revocation without requiring application-data deletion.
- Redact values before log serialization rather than relying on a later display filter.

## Authentication and browser security

ADR-0008 will choose the authentication method. ADR-0009 will choose local TLS. This ADR sets their minimum behavior:

- The dashboard and API are not anonymous.
- First-run setup establishes an administrator before LAN administration is available. Bootstrap requires local proof or possession of a short-lived installer secret.
- Every state-changing operation requires an authenticated administrator and server-side authorization.
- Safe HTTP methods do not change server state.
- The server validates CSRF protection and request origin for state-changing browser requests.
- CORS is disabled by default. Any allowed origin is exact and cannot use a wildcard with credentials.
- Session credentials use transport, script-access, and cross-site restrictions appropriate to the selected session design.
- Sessions have expiry, logout, invalidation, and rotation after authentication or privilege changes.
- High-impact actions may require recent authentication. ADR-0008 will define the set and user experience.
- The server rejects unknown Host and origin values to reduce DNS-rebinding and proxy-confusion attacks.
- Responses prevent framing. The default policy is Content Security Policy `frame-ancestors 'none'`, with a compatible fallback where required.
- The frontend uses a restrictive Content Security Policy and output encoding to reduce script injection and credential theft.

CSRF tokens, SameSite cookies, Origin checks, and Fetch Metadata can support one another. No single browser signal is sufficient for every deployment. The implementation ADR must choose and test a complete policy.

## Update and supply-chain boundary

KITPro updates, catalog manifests, and application images cross a privileged supply-chain boundary. The update design must:

- use explicitly trusted sources;
- verify integrity and publisher authorization before installation;
- identify packages, manifests, and OCI images by immutable versions or digests;
- reject metadata that is expired, rolled back, inconsistent, or intended for another platform;
- define trust-root rotation and emergency revocation;
- separate update discovery from permission to install;
- check version and data-format compatibility before changing the host;
- stage enough state to recover from interruption;
- preserve the last known-good artifact when rollback is claimed;
- record the source, version, digest, policy decision, and result without recording credentials; and
- treat build provenance as evidence, not as a substitute for publisher policy or local verification.

ADR-0011 and ADR-0012 must select the update and rollback mechanisms. This ADR does not choose a signing system, registry, build service, or update framework.

## Auditability

The privileged helper must emit an audit event for each accepted, rejected, started, completed, failed, recovered, or rolled-back privileged operation. Events cover:

- application installation and removal;
- configuration changes;
- application and KITPro updates;
- rollback;
- backup and restore;
- network exposure and network-policy changes;
- storage ownership or mount-sensitive changes;
- secret creation, replacement, use authorization, and deletion without the secret value;
- authentication and authorization changes; and
- every privileged-helper request.

An event records the time, actor or service identity, source context, request identifier, operation, target, policy result, effective non-secret change summary, artifact versions or digests, and final result. The event store must resist modification by containers, the frontend, and normal host users. Retention, export, tamper evidence, and the state database remain for ADR-0013 and ADR-0016.

## Consequences

### Benefits

- A web vulnerability does not automatically become arbitrary root code execution.
- The runtime socket and privileged host APIs stay outside the browser-facing process.
- Catalog applications receive a supportable subset of container features.
- Security policy can be tested at one privileged boundary.
- Audit records correspond to actual privileged actions rather than interface intent alone.
- Future cloud features start without an assumed path to host root.

### Costs

- Privilege separation adds protocol, packaging, lifecycle, and recovery work.
- Some applications will not fit the normal catalog policy.
- Manual Compose import cannot be presented as equivalent to a reviewed catalog application.
- Strict browser-origin and Host checks add setup cases for local names, IP addresses, and future proxies.
- Secret separation and update verification add operational recovery requirements.
- The helper and runtime remain high-value targets even after the boundary is narrowed.

## Risks

| Risk | Effect | Required response |
| --- | --- | --- |
| The helper interface grows into a remote shell | A backend compromise becomes arbitrary root execution | Reject generic commands and paths; require a security review for every new privileged operation |
| The helper trusts web validation | A compromised backend bypasses manifest or path policy | Revalidate security-critical policy inside the helper |
| Runtime abstraction hides Docker-only authority | Engine access spreads into the web service or application model | Keep one runtime adapter behind the helper and test that the socket is unreachable elsewhere |
| Browser protections assume the LAN is trusted | CSRF, DNS rebinding, or a stolen session triggers host changes | Require authentication, exact origins, Host validation, CSRF controls, and safe methods |
| Secrets share one readable store | One component compromise reveals every application and backup credential | Partition access by consumer and avoid list or read-back APIs |
| Signed updates are treated as safe by definition | A compromised signer ships harmful but valid artifacts | Enforce publisher scope, version policy, compatibility, local authorization, and revocation |
| Audit events come only from the web service | A compromised or failed web process can hide privileged work | Make the helper emit its own events |
| Cloud remote management gains a root bearer token | A cloud breach compromises every connected host | Keep local policy enforcement and prohibit general host-control credentials |

## Alternatives considered

### Run the whole service as root

This design is operationally direct. Every parser, template, session handler, dependency, and network endpoint would share root authority. A web flaw would become a host compromise, so this option is rejected.

### Give the unprivileged service the Docker socket

Unix permissions make this arrangement look constrained, but Docker control can mount and modify the host. Membership in the `docker` group or possession of the socket is root-equivalent in this threat model. This option is rejected.

### Put the web service in a container with the runtime socket mounted

The container boundary does not constrain an application that controls the host runtime. Broad host mounts would make the situation worse. This option is rejected.

### Use one unprivileged service with selected sudo commands

Narrow sudo rules can work for a few static commands. KITPro operations carry structured application, storage, network, and update state. Shell argument handling and tool-specific options make a command allowlist hard to keep narrow. A typed helper is preferred, while exact use of sudo or systemd to start it remains open.

### Use rootless containers and no privileged helper

Rootless runtime operation reduces the authority of the container engine. KITPro still needs privileged installation, service, storage, network, and update operations. Rootless containers remain worth evaluating after the first lifecycle, but they do not remove the need to design a host privilege boundary.

## Validation plan

ADR-0018 cannot be accepted from a diagram alone. Before application implementation expands, the project must prove these properties on the ADR-0017 reference host:

1. Enumerate every Phase 1 privileged operation and the exact host resources each one needs.
2. Build a disposable boundary prototype with no application UI or product logic.
3. Prove that the web-service identity cannot open the runtime socket, write protected KITPro state, alter systemd units, change network policy, or read helper-only secrets.
4. Prove that the browser cannot address the helper and that the helper has no network listener.
5. Send malformed, oversized, stale, duplicate, unauthorized, and out-of-order helper requests. Confirm that they fail without partial host changes.
6. Attempt path traversal, symlink replacement, mount substitution, command injection, and time-of-check to time-of-use changes against every privileged path operation.
7. Attempt catalog manifests with `/:/host`, privileged mode, host networking, host namespaces, devices, dangerous capabilities, runtime sockets, arbitrary commands, and out-of-policy ports. Confirm rejection in both the unprivileged and privileged boundaries.
8. Compromise a test container and attempt to reach KITPro state, the helper, the runtime socket, unrelated application data, the dashboard, host metadata services, and LAN services.
9. Test CSRF, CORS, hostile Origin and Host headers, DNS rebinding, clickjacking, session fixation, session theft, logout, expiry, and concurrent destructive requests.
10. Test update metadata and artifacts that are unsigned, signed by the wrong authority, expired, rolled back, digest-mismatched, incompatible, revoked, or interrupted during installation.
11. Insert known secret markers into every accepted input and verify that normal logs, audit events, API responses, errors, process arguments, and backups do not reveal them.
12. Interrupt each privileged operation and verify recovery plus an accurate helper-generated audit trail.
13. Model a compromised cloud command channel and prove that it cannot invoke arbitrary helper operations or bypass local policy.
14. Perform an independent security review of the helper protocol and privileged implementation before release.

Tests that mutate the host must run only on disposable systems. They are not authorized by this ADR on a development or production host.

## Review conditions

Revisit this ADR when any of these conditions occurs:

- Josh does not approve the separate privileged-helper boundary or the catalog restrictions.
- ADR-0003 cannot define a narrow authenticated helper interface.
- The selected runtime requires socket access in the web and API process.
- A supported application needs host privileges outside the catalog policy.
- Remote administration or a cloud command channel enters scope.
- Multi-user roles enter scope.
- KITPro begins importing raw Compose or other user-authored workload definitions.
- A new backup, restore, update, plugin, extension, or third-party integration crosses a privileged boundary.
- The host support matrix adds rootless engines, Podman, multiple engines, or non-systemd hosts.
- A security test, incident, or dependency change invalidates an invariant.

## Evidence reviewed

- [Docker Engine security](https://docs.docker.com/engine/security/) describes daemon authority, arbitrary host mounts, namespaces, cgroups, and the danger of provisioning containers through a web API.
- [Protect the Docker daemon socket](https://docs.docker.com/engine/security/protect-access/) states that a client credential can give root access to the daemon host.
- [Podman system service](https://docs.podman.io/en/latest/markdown/podman-system-service.1.html) states that the API grants full Podman functionality and arbitrary code execution as the service user.
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) documents origin checks, Fetch Metadata, CORS constraints, CSRF tokens, and SameSite limits.
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) documents secure cookie and session controls.
- [OWASP Clickjacking Defense Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Clickjacking_Defense_Cheat_Sheet.html) documents `frame-ancestors` and framing controls.
- [OCI image manifest specification](https://specs.opencontainers.org/image-spec/manifest/) defines content-addressed OCI images and multi-platform image indexes.
- [SLSA provenance specification](https://slsa.dev/spec/v1.2/provenance) defines verifiable information about where, when, and how a software artifact was produced.
