# alpha.15 controlled systemd service management

Status: Implemented with a curated, action-scoped catalog, scoped local-administrator authorization, unit-specific audit history, and explicit uncertain-outcome handling. Disabled by default.

## Boundary

KITPro extends the existing Host Administration Services view and root helper. It does not add a command runner, accept systemd flags, edit unit files, create transient units, or expose arbitrary D-Bus methods. The API remains unprivileged. Only the helper connects to the system systemd manager for mutations.

Service management requires both `KITPRO_HOST_SERVICE_MANAGEMENT_ENABLED=1` and an exact unit in `KITPRO_HOST_SERVICE_MANAGEMENT_UNITS`. Entries can scope actions with `unit.service=start|restart|reload`; semicolons separate entries. A bare unit retains all six actions for compatibility. The deterministic `kitpro-qualification.service` fixture is the sole built-in eligible identity. Catalog membership never overrides protection. The API and helper read the same package configuration and independently apply the same policy.

Each service is classified as KITPro protected infrastructure, OS critical, networking/access, storage/filesystem, container runtime, approved noncritical, administrator-reviewed custom, or unknown. Unknown units remain read-only. A manageable unit must resolve to an exact canonical identity and a regular root-owned definition beneath `/usr/lib/systemd/system`, `/lib/systemd/system`, or `/etc/systemd/system` that is not writable by group or others. Symlinks are resolved before this check. Template definitions require an exact reviewed instance.

## Request and operation lifecycle

`POST /api/v1/host/services/{unit}/actions` accepts only:

```json
{"action":"restart","confirmation_unit":"example.service"}
```

`GET /api/v1/host/services/{unit}/actions/{action}/preview` is read-only. It reports the current state, action eligibility, expected transition, dependency concerns, confirmation requirement, and uncertainty. Preview neither reserves state nor grants authority.

The action is one of `start`, `stop`, `restart`, `reload`, `enable`, or `disable`. The unit must pass the bounded service-name grammar and the confirmation must exactly equal the requested unit. The existing session guard enforces administrator authentication, same-origin requests, and CSRF.

Local administrators have explicit permissions:

- `host.read` reads Host Administration inventory and health.
- `host.logs.read` reads the separately gated journal API.
- `host.services.manage` permits service mutations only when paired with an exact unit/action scope, unless the principal also has `host.security.admin`.
- `host.audit.read` reads bounded unit operation history.
- `host.security.admin` retains the existing full-administrator service scope and permits local security administration.

Existing administrators receive the compatibility permission set during the auth-table migration. A newly created secondary administrator starts with `host.read` only. Root-only `kitpro-server admin permissions` and `kitpro-server admin service-scope` commands replace the complete permission or scope set, require an exact confirmation argument, write an audit event, and revoke every active session for the affected administrator. `host.read` and the final security administrator are protected. The current principal can inspect its own effective contract at `GET /api/v1/account/permissions`; the response never enumerates other accounts.

The API resolves current state and action eligibility, creates an operation ID, persists the initial audit record, then reloads the principal permission and exact unit/action scope immediately before sending a protocol-v2 `ManageSystemService` request. Revocation during the preview-to-dispatch interval therefore rejects the request before it reaches the helper. The helper records the semantic request in its durable operation coordinator, serializes operations by `host-service:{unit}`, resolves the unit again, repeats policy evaluation immediately before dispatch, and uses fixed go-systemd D-Bus methods. Lifecycle actions wait for the systemd job result. Enable and Disable wait for their D-Bus method and all actions reread the resulting state. A request is successful only when the observed state matches its requested effect.

The control operation record captures the request and final status for Activity. Before helper dispatch, the API must persist a `system-service-operation` audit event. If that write fails, dispatch is refused. The event is updated with authorization and policy outcomes, previous/resulting states, systemd result, final status, failure reason, and correlation ID. Audit content excludes credentials, cookies, session tokens, journal messages, environment values, and unit contents.

`GET /api/v1/host/services/{unit}/history` requires `host.audit.read`. It uses an exact validated unit, stable newest-first ordering, a maximum page size of 50, an offset ceiling of 10,000, and optional RFC 3339 time, action, and result filters. It returns only service-operation events for that unit. Retention follows the existing audit store; the query does not create a second activity database.

Helper operation records and events retain the bounded semantic request, API socket caller UID, previous/resulting service objects, systemd result, timestamps, and failure state. The helper accepts requests only from the configured API Unix peer through `SO_PEERCRED`. The privileged protocol carries no administrator identity or permission fields, so neither an HTTP caller nor a compromised request payload can forge audit attribution or helper authority. The API derives the administrator identity from the live session and writes it only to the control-plane audit record. The helper independently repeats the feature gate, exact catalog, canonical-unit, critical-service, dependency, and resulting-state checks.

## Eligibility and protection

Eligibility is calculated per action. Start is unavailable for an active unit; Stop and Restart require an active unit; Reload requires an active unit that advertises `CanReload`; Enable and Disable require compatible unit-file states.

The protection policy checks requested and canonical identities plus aliases. It denies KITPro units, SSH, D-Bus, essential systemd units, common network managers and resolver services, firewall services, Docker/containerd/Podman, managed ingress, AppArmor, storage and mount infrastructure, ZFS, iSCSI, NFS, and related aliases. Unknown, malformed, missing, incomplete, or inaccessible metadata fails closed. Action-specific analysis covers Requires, Wants, Requisite, BindsTo, PartOf, Conflicts, RequiredBy, WantedBy, triggers, socket identities, and StopWhenUnneeded metadata. Traversal is cycle-safe and bounded to 64 service nodes. Protected, unreviewed, unavailable, and over-limit consequences are denied. The helper repeats the analysis immediately before dispatch. There is no maintenance bypass.

The helper AppArmor profile permits the system-bus socket, the D-Bus client handshake and match-subscription methods, the exact systemd `JobRemoved` completion signal, and communication with the `org.freedesktop.systemd1` peer. The installed Ubuntu run demonstrated that both the bus handshake and unique-name completion signal need explicit rules; no bus-name ownership or unrelated peer was added. The systemd unit retains `NoNewPrivileges`, its existing capability bounding set, filesystem protections, and AF_UNIX-only networking. The API gains no capabilities and never runs as root.

## Interface

The Services list and details present actual state, catalog category, management state, unit-definition trust, dependencies, action-specific decisions, and policy explanations. Filters cover running, stopped, failed, eligible, protected, and custom services. Permission-denied actions remain disabled and explain that the current administrator lacks authority. Confirmation identifies the exact unit and action, obtains a fresh preview, and displays known dependency concerns. Stop and Disable additionally require typing the unit name.

Service details include a bounded Service Activity section with action/result filters and pagination. Repeated activity records use a compact semantic desktop table and dense mobile rows, consistent with the permanent administrative-data design rule. It safely renders administrator, action, status, state evidence, systemd outcome, correlation ID, and bounded failure text. Completion refreshes Services, System Health, and the unified Dashboard. The health evaluator never performs automatic remediation.

## Failure and recovery

Systemd rejection, non-`done` job results, timeouts, D-Bus loss, disappearing units, concurrent requests, malformed helper receipts, and resulting-state mismatches have distinct results. Requests rejected before dispatch remain `rejected`. A confirmed non-`done` job or resulting-state mismatch is `failed`. A bounded wait that expires is `timed_out`. A dispatch transport error, missing completion signal, lost helper response, post-dispatch metadata loss, malformed receipt, or failed final audit update is `outcome_unknown` unless durable helper reconciliation proves a stronger state. API or helper restart does not turn acceptance into success.

The durable operation ID is the replay key. The helper returns the existing semantic receipt for an identical replay and rejects different contents. Per-unit helper leases and in-process controller locks reject conflicting concurrent work. KITPro never automatically replays or inverses an operation with an uncertain result.

## Platform notes and limitations

The implementation uses systemd D-Bus on Ubuntu and Arch-family systems, including KITPro Linux. Distribution-specific service names are handled by canonical identities, aliases, a conservative cross-platform protected-name set, and the explicit allowlist.

Rocky Linux remains Experimental. RPM packaging requires SELinux Enforcing, `container-selinux`, the KITPro policy package, the root-owned helper socket, and keeps `KITPRO_HOST_SERVICE_MANAGEMENT_ENABLED=0` by default. Static validation rejects permissive domains, `setenforce 0`, disabled SELinux, broad privileged containers, and a service-management default-on configuration. An installed Rocky Linux 10.2 test passed scoped authorization, all six fixture actions, history, revocation, and cleanup with SELinux Enforcing and no KITPro AVC denials. The API and ingress processes were nevertheless observed in `unconfined_service_t`; the current module does not yet provide dedicated API/helper domains. Functional evidence therefore does not promote Rocky beyond Experimental. Dedicated domain, socket, D-Bus, job-observation, audit-storage, and upgrade policy qualification remain required without broad `init_t`, D-Bus, or filesystem allowances.

Permission administration is intentionally root-local in alpha.15. A browser role editor is deferred until it can reuse the same final-security-administrator protection, exact confirmation, session revocation, and audit transaction without expanding the identity system. This milestone does not provide protected-service maintenance, unit-file management, automatic remediation, or a privilege bypass. systemd can still discover runtime side effects after preview, so the catalog remains narrow and helper revalidation is authoritative.
