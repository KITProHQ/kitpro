# Alpha.15 System Health and unified administration dashboard

Status: Implemented and validated on supported Ubuntu and KITPro Linux installations. Rocky Linux remains Experimental.

## Health model

System Health extends the existing `hostadmin` subsystem. It evaluates a bounded current snapshot from Host Overview, Services, Storage, Networking, Application Management, ingress, certificates, backups, security enforcement, time synchronization, the privileged helper, the container runtime, and the explicitly gated journal provider. It adds no daemon, monitoring database, privileged operation, shell, or configuration writer.

Every finding has a stable identifier, component, state, severity, bounded summary, explanation, bounded evidence, observation timestamps, optional recommended action, an allowlisted UI destination, and a capability state. Health states are `healthy`, `warning`, `critical`, `degraded`, `unavailable`, `unsupported`, and `not_configured`. Unsupported and intentionally unconfigured optional capabilities do not make the overall host unhealthy. Missing required observations remain unavailable rather than becoming healthy values.

The overall aggregation order is critical, degraded, warning, unavailable, then healthy. The engine applies documented thresholds to sampled CPU, load per logical CPU, available memory, swap use, Linux memory pressure, filesystem utilization, and certificate expiry. Failed essential services, failed applications, readiness failures, read-only protected filesystems, and certificate failures are explicit findings. A stopped application is not treated as failed solely because its desired state is stopped. A missing default route or resolver is `not_configured`; isolated hosts are not assumed broken.

The response is limited to 512 findings and reports `truncated: true` if the ceiling is reached. Text fields have server-side length ceilings. The current snapshot has `evaluated_at` and `fresh_until` timestamps. KITPro stores no health history in this milestone.

## Collection and safety

Existing providers retain their own deadlines, record ceilings, fixed executable identities, controlled arguments, bounded output, and error classification. Health collection runs independent providers concurrently under the existing four-second Host Administration deadline and caches the assembled snapshot for five seconds. The Dashboard and System Health endpoints reuse that snapshot, avoiding duplicate expensive probes.

Additional host signals use fixed paths and commands only: bounded `/proc/pressure/memory`, bounded `/proc/self/status`, and fixed `/usr/bin/timedatectl show --property=NTPSynchronized --value`. No shell or caller input enters these probes. Journal evidence is collected only when `KITPRO_HOST_LOGS_ENABLED=1`; health receives a bounded count of recent priority 0 through 3 entries and never returns journal messages.

## APIs

- `GET /api/v1/host/health` returns the complete bounded snapshot.
- `GET /api/v1/host/health/checks` returns findings and freshness metadata.
- `GET /api/v1/host/health/{component}` returns one discovered component and its findings.
- `GET /api/v1/dashboard` returns the shared health summary, host identity, current resource values, application and service counts, Storage and Networking summaries, and at most eight findings requiring attention.

Every endpoint requires the existing administrator session, accepts only GET or HEAD, rejects query parameters, returns `Cache-Control: no-store`, and exposes no mutation. Component names resolve only from the collected bounded map. Responses exclude credentials, session values, journal messages, application secrets, certificate private keys, and arbitrary filesystem content.

## Interface

System Health is a Host Administration page with the overall state, freshness, component summaries, and safely rendered findings. Recommended actions link only to an allowlisted existing KITPro view. The unified Dashboard replaces static placeholder health data with the shared live snapshot, current resources, application state, essential services, Storage, Networking, and the highest-priority findings. It refreshes every 15 seconds only while visible. Desktop and mobile layouts reuse existing KITPro components and tokens.

The interface has no repair, restart, storage, network, firewall, package, or application lifecycle action. System-provided strings are inserted through DOM text content. Unavailable, unsupported, and not-configured states remain distinct.

## Platform and limitations

The core model is distribution-independent. systemd service and time observations require a supported systemd environment. Linux pressure information requires pressure stall information in procfs. AppArmor and SELinux are interpreted through the existing security collector. Optional facilities report their observed capability instead of being inferred from a distribution name.

The milestone is a current-state evaluator rather than an alerting system. It has no historical trends, notification delivery, incident acknowledgement, automatic remediation, SMART command execution, internet-connectivity assumption, or privileged mutation. Future monitoring can persist normalized findings behind a separate retention and sensitive-data contract.
