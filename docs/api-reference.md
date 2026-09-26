# KITPro API reference

## Contract status

The alpha.12 API is an authenticated local control-plane interface for the
shipped dashboard and operator diagnostics. It is public source, but it is an
alpha contract. Endpoint shapes and state names may change before a stable
release. Internal helper socket operations are not public HTTP APIs.

All endpoints require an authenticated local administrator session. Mutating
endpoints also require a valid CSRF token and the expected Origin and Host
headers. The API does not promise a remote or machine-to-machine contract.

## Hardware inventory

`GET /api/v1/hardware` requires an authenticated administrator session. It returns normalized accelerators, vendor/model identity, render and compute availability, NVIDIA integration state, and IOMMU presence. It never returns raw `/dev` paths or unrelated hardware.

## Read-only endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/version` | Authenticated KITPro and platform build metadata |
| GET | `/api/v1/catalog` | Trusted application catalog and release metadata |
| GET | `/api/v1/installations` | Installed application identities and desired state |
| GET | `/api/v1/installations/{id}/services` | Declared services and safe access state |
| GET | `/api/v1/installations/{id}/reconciliation` | Last helper reconciliation result for the installation |
| GET | `/api/v1/operations` | Operation history and status |
| GET | `/api/v1/operations/{id}` | One control operation, refreshed from helper authority when pending |
| GET | `/api/v1/storage-roots` | Trusted roots, access ceilings, filesystem type, and availability |

Image digests and installation identifiers are available for administration and
support, but clients must not turn them into arbitrary Docker requests.

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
