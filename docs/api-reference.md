# KITPro API reference

## Hardware inventory

`GET /api/v1/hardware` requires an authenticated administrator session. It returns normalized accelerators, vendor/model identity, render and compute availability, NVIDIA integration state, and IOMMU presence. It never returns raw `/dev` paths or unrelated hardware.

The API is local to the KITPro control plane. Mutating endpoints require an
authenticated administrator session, a valid CSRF token, and the expected
Origin and Host headers. The public alpha does not promise a remote or
machine-to-machine API contract.

## Read-only endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/version` | Authenticated KITPro and platform build metadata |
| GET | `/api/v1/catalog` | Trusted application catalog and release metadata |
| GET | `/api/v1/installations` | Installed application identities and desired state |
| GET | `/api/v1/installations/{id}/services` | Declared services and safe access state |
| GET | `/api/v1/operations` | Operation history and status |
| GET | `/api/v1/storage-roots` | Trusted roots, access ceilings, filesystem type, and availability |

Image digests and installation identifiers are available for administration and
support, but clients must not turn them into arbitrary Docker requests.

## Lifecycle operations

Lifecycle mutations are represented as durable operations. The exact operation
resource returned by a mutation reports accepted, running, succeeded, or failed
state. A failed operation is not success and should be inspected before retry.

The dashboard is the supported client for installing an application, starting
or stopping a runtime, removing a runtime while keeping its data, and
recreating a runtime with the same installation identity and storage.

## Service exposure

`POST /api/v1/installations/{id}/services/{service}/exposure` accepts only a
policy mode (`internal`, `loopback`, or `lan`). Host addresses, host ports,
container ports, protocols, and Docker binding objects are not client inputs.

`DELETE /api/v1/installations/{id}/services/{service}/exposure` returns the
service to `internal` and retains its assigned port for stable re-enable.

## Application updates

Application update requests select a trusted catalog release. KITPro preserves
the installation and persistent storage, recreates the runtime when required,
and records the result as an operation. Native package managers remain
authoritative for KITPro software updates.

For first use, follow the [quickstart](release/quickstart.md) rather than
calling the API directly.

## Trusted storage roots

`POST /api/v1/storage-roots` is the only application-management workflow that
accepts a host path. It accepts `name`, `path`, and a bounded `mode` of
`read-only` or `read-write`. The helper canonicalizes and independently
validates the path before returning a generated root ID.

Application install forms send `storage_<slot>=<root-id>`. They never send a
host path, container target, bind mode, or Docker option. `DELETE
/api/v1/storage-roots/{id}` refuses roots that remain attached to an
installation.
