# KITPro Server security invariants

33. Hardware access uses only the closed trusted device-class registry. Manifests and API requests cannot contain raw device paths, arbitrary groups, capabilities, runtime arguments, privileged mode, or host networking.
34. The helper independently revalidates hardware intent, discovers the host, resolves exact devices, persists assignments, and treats missing, changed, extra, or ambiguous mappings as security drift.

These rules apply to every implementation and supported application. A change that violates one must stop release or propose an explicit replacement through an architecture decision record.

1. The browser-facing KITPro web and API service runs as a dedicated unprivileged host identity, not as root.
2. The browser communicates only with the authenticated KITPro API. It never communicates directly with a privileged helper, container-runtime socket, or host-management interface.
3. The web and API service has no unrestricted Docker or Podman socket access and does not belong to a root-equivalent runtime group.
4. Only a narrow privileged helper may perform root-level KITPro operations. It has no network listener and accepts no shell, arbitrary command, raw runtime request, Docker argument list, Compose document, or unrestricted path.
5. The helper accepts framed, versioned, typed operations over a protected Unix socket. It verifies the fixed API service UID with kernel peer credentials, validates policy independently, rejects unknown input, and records every privileged attempt and result. Compromise of that API identity grants access to every semantic operation authorized to it. Peer identity does not prove human intent, and API-supplied administrator metadata is audit context unless an independent trusted component verifies it.
6. Normal catalog manifests use a versioned allowlist. They cannot request arbitrary host mounts, privileged mode, host namespaces, devices, capabilities, commands, environment inheritance, or network exposure.
7. Managed containers cannot access KITPro control paths, runtime sockets, helper interfaces, backup credentials, or unrelated application data by default.
8. The dashboard is not anonymous. First-run setup establishes administrative access before LAN administration becomes available.
9. Every administrative state change requires authentication, server-side authorization, and browser-origin protection. Safe HTTP methods never change state.
10. CORS denies unlisted origins, responses prevent framing, and the server rejects unexpected Host and origin values.
11. Secrets do not appear in normal logs, audit events, URLs, process arguments, manifest files, image layers, or routine read APIs.
12. Each secret is available only to the components and workloads that need it. A managed application never receives KITPro, TLS, runtime, or backup authority unless its reviewed function requires a narrowly scoped value.
13. KITPro verifies update source, publisher authority, integrity, freshness, platform compatibility, and version policy before privileged installation. Catalog releases deploy fully qualified images by immutable digest. A tag alone cannot identify an approved artifact.
14. Every privileged operation produces an audit event at the privileged boundary without sensitive values. Frontend code, containers, and normal host users cannot alter that event.
15. Destructive Docker operations use semantic KITPro targets, not caller-supplied Docker IDs. The helper requires a protected ownership record, exact labels, object kind, and current-state match. A normal uninstall never deletes persistent application data.
16. KITPro fails closed when it cannot verify identity, authorization, policy, mount state, runtime compatibility, update trust, or the ability to record a privileged event.
17. Local operation does not require KITPro cloud connectivity. A cloud identity is never a general root credential, and compromise of a KITPro cloud service does not automatically grant host-root control.
18. Each application instance receives its own KITPro-owned bridge network. No network is shared across different application instances. Catalog containers join no existing user network, default bridge, or host network unless a later accepted ADR replaces this rule.
19. Catalog containers publish no host ports by default. Any Phase 1 exception is explicit, policy-approved, loopback-only, unprivileged, conflict-checked, and tested for both IPv4 and IPv6.
20. The core helper protocol does not depend on a distribution package manager, firewall CLI, or disabled mandatory access control. KITPro never requires SELinux to be disabled or set to permissive mode.
21. On an SELinux-supported host, `Enforcing` mode alone is not acceptance evidence: the helper and application containers must enter their intended confined domains with non-empty runtime labels.
22. The helper commits phase intent before it dispatches an external mutation. An interrupted or unknown external outcome must reconcile against a fresh observation before retry.
23. No single label, deterministic name, control-plane row, helper record, cached observation, or Docker object ID proves ownership. A privileged mutation requires trusted helper state and matching fresh external evidence.
24. Loss or corruption of helper ownership state removes destructive authority. KITPro cannot reconstruct that authority from Docker labels or control-plane state alone.
25. A cached observation never authorizes a privileged mutation. The helper rechecks security-sensitive state immediately before dispatch and verifies the result afterward.
26. One durable, fenced lease controls mutations for each application instance. A stale executor cannot commit after another executor receives a newer fencing token.
27. Reconciliation cannot automatically adopt, rewrite, or delete an ambiguous, foreign, user-modified, or security-drifted resource.
28. A destructive operation verifies its complete target set before it deletes the first resource. Normal uninstall preserves persistent application data through every recovery path.
29. Privileged receipts and audit events preserve earlier outcomes. Supersession, recovery, and administrator overrides append evidence instead of rewriting history.
30. The helper runs under an enforcing platform MAC profile or domain where the supported host provides one. A policy-load failure never falls back silently to an unconfined helper.
31. The helper does not require shell, Docker CLI, Compose CLI, arbitrary child execution, or general outbound IP networking. Any exception needs an explicit architectural decision.
32. AppArmor and SELinux rules grant only the helper executable, runtime, state, audit, Docker socket, and approved storage access required by the semantic operation set. MAC policy never authorizes arbitrary host paths.
33. MAC confinement does not weaken or replace Docker semantic validation. The helper rejects dangerous Docker capabilities before dispatch even when the helper profile or domain is enforcing.

The detailed rationale, actors, threats, and tests are in the [threat model](threat-model.md), [ADR-0016](../decisions/0016-durable-state-and-reconciliation.md), and [ADR-0018](../decisions/0018-security-boundaries.md).
