# KITPro API reference

## Contract status

The alpha.14 API is an authenticated local control-plane interface for the
shipped dashboard and operator diagnostics. It is public source, but it is an
alpha contract. Endpoint shapes and state names may change before a stable
release. Internal helper socket operations are not public HTTP APIs.

All endpoints require an authenticated local administrator session. Mutating
endpoints also require a valid CSRF token and the expected Origin and Host
headers. The API does not promise a remote or machine-to-machine contract.

Alpha.14 ingress, certificate, setup, provider, host-backup, readiness, and
diagnostics endpoints follow the same authentication, Origin, Host, and CSRF
rules. Secret inputs are write-only. The API never returns a certificate
private key, ACME account key, password, session value, or
application secret through those endpoints.

## Hardware inventory

`GET /api/v1/hardware` requires an authenticated administrator session. It returns normalized accelerators, vendor/model identity, render and compute availability, NVIDIA integration state, and IOMMU presence. It never returns raw `/dev` paths or unrelated hardware.

## Read-only endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/version` | Authenticated KITPro and platform build metadata |
| GET | `/api/v1/host/overview` | Alpha.15 read-only host facts and per-collector availability; no actions |
| GET | `/api/v1/host/services` | Paginated systemd service inventory with `search`, `state`, `offset`, and `limit` |
| GET | `/api/v1/host/services/{unit}` | One validated `.service` unit and bounded details |
| GET | `/api/v1/host/logs` | Explicitly gated journal query with `unit`, `priority`, `since`, `until`, `search`, `cursor`, and `limit` |
| GET | `/api/v1/host/storage` | Read-only block-device, filesystem, mount, capacity, protection, LVM, and ZFS inventory |
| GET | `/api/v1/host/health` | Bounded current System Health snapshot and component findings |
| GET | `/api/v1/host/health/checks` | Bounded System Health findings with freshness metadata |
| GET | `/api/v1/host/health/{component}` | One discovered health component and its findings |
| GET | `/api/v1/dashboard` | Unified current administration summary built from the shared health snapshot |
| GET | `/api/v1/apps` | Trusted application catalog and release metadata |
| GET | `/api/v1/installations` | Installed application identities and desired state |
| GET | `/api/v1/installations/{id}/services` | Declared services and safe access state |
| GET | `/api/v1/installations/{id}/reconciliation` | Last helper reconciliation result for the installation |
| GET | `/api/v1/operations` | Operation history and status |
| GET | `/api/v1/operations/{id}` | One control operation, refreshed from helper authority when pending |
| GET | `/api/v1/storage-roots` | Trusted roots, access ceilings, filesystem type, and availability |
| GET | `/api/v1/ingress` | Managed route state without backend secrets |
| GET | `/api/v1/certificates` | Typed certificate, expiry, and renewal state |
| GET | `/api/v1/exposure-providers` | Provider identity and health without credentials |
| GET | `/api/v1/system/readiness` | Separate host and optional-service readiness dimensions |
| GET | `/api/v1/setup` | Durable first-run step state and safe evidence |
| GET | `/api/v1/diagnostics` | Support bundle history and inclusion policy |

Image digests and installation identifiers are available for administration and
support, but clients must not turn them into arbitrary Docker requests.

## Managed ingress and certificates

`POST /api/v1/ingress` creates an authorized hostname route. The request names
an installation, catalog service, normalized hostname, exposure mode, upstream
protocol, TLS policy, and optional certificate ID. The server
derives the loopback backend. A client cannot supply an upstream address or
port.

`DELETE /api/v1/ingress/{route_id}` removes the managed route.
`POST /api/v1/ingress/reconcile` retries held routes after a restore and
activates only a validated candidate whose application is ready.

`POST /api/v1/certificates` accepts either a provided certificate and private
key or an ACME request. ACME requests also name the authorized installation and
service, contact email, allowlisted directory, and explicit terms acceptance.
`POST /api/v1/certificates/{certificate_id}/renew` requests a renewal. `GET
/api/v1/certificates` returns metadata and typed state only.

## Setup, readiness, backup, and diagnostics

`GET /api/v1/setup` refreshes critical steps from current system state and
returns the durable nine-step workflow. `POST /api/v1/setup` can record only the
local or LAN access choice and an explicit deferral of optional HTTPS. It
cannot mark a critical prerequisite complete.

`GET /api/v1/system/readiness` returns typed dimensions. Clients must not
collapse `disabled`, `unknown`, `degraded`, and `ready` into one Boolean.

`POST /api/v1/backup` creates both the control database backup and the protected
helper-state archive. Its response states `contains_secrets: true`.

`POST /api/v1/diagnostics` creates a redacted support bundle. `GET
/api/v1/diagnostics` lists bundle metadata and states the inclusion and
exclusion policy.

## Lifecycle operations

Lifecycle mutations are represented as durable operations. Each API operation
ID is also the semantic helper operation ID. A separate request ID identifies
one socket exchange and can change when the API polls or retries transport.
The helper binds the semantic ID to a canonical, secret-free request hash on
first acceptance. Exact replay returns the recorded operation; reuse of the ID
with different semantic content is rejected.

Helper operation states are `accepted`, `executing`, `reconciling`,
`succeeded`, `failed`, `action_required`, `cancelled`, or `superseded`. The API
can temporarily report `helper_succeeded_projection_pending` when privileged
work committed but its unprivileged projection still needs repair. A browser
timeout or disconnected HTTP request does not cancel helper execution. Poll the
operation resource instead of issuing a different destructive request.

The helper serializes mutations per installation with a durable lease and
monotonic fencing token. Mutations for unrelated installations can proceed
independently. A failed or action-required operation is not success and must be
reconciled before repair or retry.

The dashboard is the supported client for installing an application, starting
or stopping a runtime, removing a runtime while keeping its data, and
recreating a runtime with the same installation identity and storage.

Runtime removal retains the installation record, managed storage,
external-storage bindings, and generated secrets. The next supported recovery
uses the same installation identity. The API has no destructive
application-data deletion endpoint.

For a catalog `network-service` whose lifecycle notice requires
acknowledgement, the API returns `428 Precondition Required` unless stop,
restart, update, recreate, runtime removal, or exposure replacement includes
an explicit acknowledgement. JSON requests use `"acknowledged": true`; browser
forms use `acknowledgement=accepted`. Ordinary applications are unaffected.

`POST /api/v1/installations/{id}/credentials/{credential}/reveal` deliberately
reveals one administrator credential authorized by the installed application's
embedded manifest. The public credential ID is not an environment-variable
name. The endpoint uses the normal authenticated session, Origin, Host, and
CSRF checks and returns `Cache-Control: no-store`. No GET, catalog,
installation, lifecycle, or operation response contains the value. Reveal is
repeatable; refresh discards the displayed value from the page. Credential
enumeration and reset are not supported.

`GET /api/v1/operations` returns up to 20 recent operation records. `GET
/api/v1/operations/{id}` returns one record with this public shape:

```json
{
  "id": "op-...",
  "type": "UpdateApplication",
  "requested_at": "2026-09-21T12:00:00Z",
  "status": "accepted",
  "instance_id": "inst-...",
  "summary": "privileged operation continues in helper"
}
```

Poll the same operation ID after a lost response. Do not create a second
destructive request. `accepted` means the helper may still be working or the
API may be repairing its projection. `succeeded` means the helper committed the
operation. `failed` means the operation exists but needs review. The helper's
more detailed durable states include `action_required`, `cancelled`, and
`superseded`.

## Reconciliation and repair

`GET /api/v1/installations/{id}/reconciliation` returns separately modeled
runtime and reconciliation state. Reconciliation states are `consistent`,
`repairable`, `degraded`, `action_required`, `runtime_missing`,
`runtime_unknown`, and `cleanup_pending`. A `running` runtime state does not
mean the application is ready; readiness requires an explicit manifest-defined
check, which the current catalog does not yet provide.

A reconciliation response has this shape:

```json
{
  "installation_id": "inst-...",
  "checked_generation": 2,
  "reconciliation_state": "runtime_missing",
  "runtime_state": "missing",
  "observed_at": "2026-09-21T12:00:00Z",
  "mismatch_codes": ["active_container_missing"],
  "recommended_action": "recreate_generation"
}
```

`POST /api/v1/installations/{id}/reconciliation` records a fresh helper
observation. `POST /api/v1/installations/{id}/repair` accepts one helper-chosen
bounded action: `start_active`, `recreate_generation`, `cleanup_resources`, or
`acknowledge_retained_missing`. Recreation uses the constrained installation
lifecycle path; the other actions execute through the helper repair operation.
Clients must not invent a repair action that the latest reconciliation result
did not recommend.

Docker can remove a stopped container's live network attachment when a start
fails while programming a host port. If the helper proves that the container,
immutable configuration, configured network, and surviving network identity
are otherwise exact, reconciliation reports `active_container_detached` and
recommends `recreate_generation`. Other network or configuration drift remains
`action_required` and is not adopted.

Controlled recreation does not label the missing or detached source generation
as a usable rollback. After the replacement commits, the helper removes only
the exact unusable source resources and records that generation as removed. A
normal verified update continues to retain its valid prior generation. If an
older build already recorded an exact detached source as retained, fresh
reconciliation recommends `cleanup_resources`; the bounded repair removes only
that proven unusable retained generation.

The reconciliation request has no body. The repair request is JSON:

```json
{"action":"recreate_generation"}
```

For a network service whose lifecycle notice requires acknowledgement, include
`"acknowledged":true` in the same repair request. The acknowledgement is
revalidated by the normal recreate route; repair does not bypass the network
service notice.

Both mutation responses contain an operation `id`, its current `status`, and
the latest reconciliation `result` when available. Repeating a request is not
a client-side idempotency guarantee because the API creates the durable
operation ID. After an uncertain response, poll the operation that already
exists or reconcile the installation before taking another action.

## Service exposure

`POST /api/v1/installations/{id}/services/{service}/exposure` accepts only a
policy mode (`internal`, `loopback`, or `lan`). Host addresses, host ports,
container ports, protocols, and Docker binding objects are not client inputs.
The trusted manifest can authorize dynamic TCP, multiple bindings, fixed TCP,
fixed UDP, the same numeric TCP and UDP port, or non-HTTP TCP. Wildcard
publication and host networking remain invalid.

`DELETE /api/v1/installations/{id}/services/{service}/exposure` returns the
service to `internal` and retains its assigned port for stable re-enable. A
network service requiring acknowledgement uses
`?acknowledgement=accepted` on this DELETE endpoint.

## Application updates

Application update requests select a trusted catalog release. KITPro preserves
the installation and persistent storage, prepares a new runtime generation,
cuts over in dependency order, and makes that generation authoritative only
after exact runtime verification. The prior generation is retained stopped
when recovery may need it. Cleanup failure after commit is recorded as debt and
does not turn a verified replacement into a false failure. Native package
managers remain authoritative for KITPro software updates.

For first use, follow the [quickstart](release/quickstart.md) rather than
calling the API directly.

## Application backup and restore

`POST /api/v1/installations/{id}/backup` creates a version 1 application
archive in the helper's configured local backup directory. The request has no
body. A completed response contains a backup ID, filename, SHA-256, creation
time, and detected database count. It never returns archive data or secret
values.

The backup request has no body. A completed response resembles:

```json
{
  "backup_id": "op-...",
  "filename": "application-backup-op-....tar",
  "sha256": "...",
  "created_at": "2026-09-21T12:00:00Z",
  "databases": 1
}
```

`POST /api/v1/installations/{id}/restore` accepts only
`{"backup_id":"op-..."}`. The helper resolves that ID from its root-owned
backup inventory. The API cannot supply an archive path, storage path, runtime
command, or database command.

Both operations require the existing authenticated administrator, CSRF,
Origin, and Host checks. Restore format version 1 targets the same existing
installation, release, runtime generation, component images, managed storage,
and imported storage bindings.

The backup and restore endpoints return the helper result directly. They do not
promise the common `{id, status}` operation envelope. The API creates a durable
control operation before helper dispatch, but an accepted or uncertain response
does not always expose that operation ID to the client.

If the response does not identify the operation, inspect `GET
/api/v1/operations` for the recent backup or restore operation for the target
installation. Poll `GET /api/v1/operations/{id}` after you identify it. Do not
issue a second backup or restore request to discover the first request's
outcome. If a restore becomes action-required, preserve every active, staged,
rollback, and failed tree and follow the [lifecycle recovery guide](operations/lifecycle-recovery.md).

Restore writes a helper-owned journal before filesystem swaps. An unresolved
restore blocks other installation mutations. Helper startup classifies the
journal and either completes a proven forward state, restores the prior tree,
records cleanup debt, or leaves `action_required`; it never blindly repeats a
path mutation whose outcome is unknown.

See [Back up and restore an application](operations/application-backup-restore.md)
for the operator workflow.

## Trusted storage roots

`POST /api/v1/storage-roots` is the only application-management workflow that
accepts a host path. It accepts `name`, `path`, and a bounded `mode` of
`read-only` or `read-write`. The helper canonicalizes and independently
validates the path before returning a generated root ID.

Application install forms send `storage_<slot>=<root-id>`. They never send a
host path, container target, bind mode, or Docker option. `DELETE
/api/v1/storage-roots/{id}` refuses roots that remain attached to an
installation.

The helper rejects a read-write binding if another installation already uses
the root, and rejects a reader beside an existing writer. Multiple read-only
bindings are allowed. Runtime UID/GID and managed-directory ownership are
trusted manifest data and are never API inputs.

## Alpha.15 host overview

`GET /api/v1/host/overview` returns KITPro Server version, installed KITPro Linux release metadata where present, hostname, OS, kernel, architecture, CPU model and sampled utilization, uptime, load averages, memory and swap, root filesystem capacity, interfaces, and explicit collector states. Missing data is omitted and its collector reports `unavailable`. `systemd` and `security` describe detection only; this endpoint does not claim service health. It requires the existing authenticated administrator session, accepts only GET/HEAD, and sends `Cache-Control: no-store`. Host Administration mutations remain disabled; detailed network inventory is provided by the separate Network endpoints.

## Alpha.15 storage inventory

`GET /api/v1/host/storage` returns a bounded read-only snapshot with `summary`, `capabilities`, `devices`, `filesystems`, `lvm_volumes`, `zfs_pools`, and `zfs_datasets`. The summary covers unique mounted local persistent filesystems and excludes bind duplicates, overlay, memory-backed, and network filesystems. Device entries include parent relationships, category, type, size, transport, filesystem metadata, mount state, and protected-resource markers. Filesystem entries include source, type, mount, total/used/available bytes, usage percentage, read-only state, class, warnings, and collection status.

The endpoint accepts no query parameters. It requires the existing authenticated administrator session, accepts only GET/HEAD, and sends `Cache-Control: no-store`. Unknown query parameters return HTTP 400. Collection uses fixed executable identities and arguments, bounded `/proc` reads, a four-second deadline, 4 MiB output ceilings, and 4,096-record limits. It exposes no path reads, shell, or storage mutation. Optional LVM and ZFS providers report explicit capability states when their fixed read-only tools are missing, denied, or unavailable. See [read-only Storage Inventory](architecture/alpha15-storage-inventory.md).

### System Health and Dashboard

`GET /api/v1/host/health` returns a current health summary, per-component summaries, freshness timestamps, and at most 512 typed findings. `/checks` returns the finding list, while `/{component}` resolves only a component present in the collected snapshot. States remain distinct: `healthy`, `warning`, `critical`, `degraded`, `unavailable`, `unsupported`, and `not_configured`. A ceiling hit sets `truncated: true`.

`GET /api/v1/dashboard` reuses the same five-second cached snapshot and returns host identity, current resource observations, application and service counts, Storage and Networking summaries, and no more than eight findings requiring attention. These endpoints accept no query parameters, require the administrator session, accept only GET/HEAD, and send `Cache-Control: no-store`. They provide no remediation action. Journal evidence is limited to a count and is collected only when the independent Logs feature gate is enabled; raw messages are excluded. See [System Health and unified administration dashboard](architecture/alpha15-system-health-dashboard.md).

## Alpha.15 network inventory and diagnostics

`GET /api/v1/host/network` returns the bounded network summary, capability and backend states, interfaces, routes, DNS configuration, and traffic counters. `/interfaces`, `/interfaces/{name}`, `/routes`, `/dns`, and `/stats` provide typed read-only views. Interface names are validated against the discovered inventory. Every read requires the administrator session, accepts only GET/HEAD with no query parameters, and sends `Cache-Control: no-store`.

`POST /api/v1/host/network/diagnostics` accepts a JSON `operation` of `dns`, `tcp`, `route`, `icmp`, or `gateway`, plus bounded `target`, `port`, and `interface` fields where applicable. It also requires same-origin CSRF authorization. DNS performs resolution only. Other operations require a configured gateway/resolver target or an operator allowlist in `KITPRO_HOST_DIAGNOSTIC_TARGETS`; TCP also requires an allowed port (53, 80, or 443 by default, replaceable through `KITPRO_HOST_DIAGNOSTIC_PORTS`). The server permits two concurrent checks, ten starts per minute, four seconds per request, 4 KiB requests, bounded command output, and no shell or arbitrary arguments. See [read-only Network Administration](architecture/alpha15-network-administration.md).

## Alpha.15 services and logs

Services use the systemd system bus. `state` is `all`, `active`, `inactive`, or `failed`; `limit` is 1–50. The response has `items`, `total`, and optional `next_offset`. Unit detail returns loaded, active, substate, enabled state, PID and failure result when observed, plus bounded dependencies. The unit identifier must be a validated `.service` name.

Logs require the ordinary administrator session and the separate `KITPRO_HOST_LOGS_ENABLED=1` operator gate plus OS journal read permission. `priority` is 0–7, `limit` is 1–100, `since`/`until` are RFC3339 timestamps within a 30-day range, `search` is a literal substring of at most 100 bytes, and `cursor` is an opaque journal cursor. The response has `entries` and optional `next_cursor`; each entry includes timestamp, unit when known, priority, identifier, message, and cursor. Content may be sensitive. Raw journal data is not included in support bundles. `400` means invalid query, `403` denied access, `501` unsupported, and `503` unavailable. See [read-only Services and Logs](architecture/alpha15-services-logs.md) for capability and permission limits.
