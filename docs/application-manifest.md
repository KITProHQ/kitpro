# KITPro application manifest reference

Catalog entries are strict JSON files shipped with KITPro. They are
constrained application definitions, not Docker Compose. Unknown fields,
duplicate keys, oversized documents, unknown schema versions, and invalid
values are rejected before a helper request is created.

## Catalog metadata (schema version 7)

Schema version 7 keeps catalog presentation metadata in the same trusted
manifest as the deployment definition. It also retains the schema-version-6
backup requirement. A version-7 manifest without a valid `backup` policy is
rejected.

```json
{
  "schema_version": 7,
  "category": "Reading",
  "kind": "application",
  "catalog_status": "standard",
  "website_url": "https://example.org/",
  "source_url": "https://github.com/example/project",
  "documentation_url": "https://docs.example.org/",
  "logo": "example-project",
  "limitations": ["Public publishing is not configured automatically."],
  "lifecycle_notice": {
    "install": "Review the application settings before installation.",
    "stop": "Stopping interrupts the service.",
    "remove": "Removing the runtime does not delete stored data.",
    "require_acknowledgement": true
  }
}
```

`category` uses the reviewed values `AI`, `Developer Tools`, `Documents`,
`Files`, `Finance`, `Food and recipes`, `Home automation`, `Media`,
`Monitoring`, `Music`, `Networking`, `Notes`, `Reading`, and `Security`.
`kind` is `application` or `network-service`. The kind describes the
application's operational role. It does not grant new networking or lifecycle
authority.

`catalog_status` is `standard` or `experimental`. It describes the maturity of
the catalog entry. It does not describe operating-system support. Platform
support remains in the [support matrix](support-matrix.md).

Website, source, and documentation links are optional HTTPS URLs. Validation
is offline and never fetches them. User information, fragments, malformed
URLs, and other schemes are rejected.

`logo` is an optional packaged-asset key, not a URL or path. The UI loads a
reviewed SVG from its local catalog assets. If no logo is declared, the UI uses
generated initials. Each packaged logo needs a recorded source and license;
KITPro never downloads catalog artwork at runtime.

`limitations` contains at most eight non-empty, single-line entries of 280
bytes each. Use it only for important behavior that structured storage,
service, hardware, or backup declarations cannot already express.

`lifecycle_notice` is structured display metadata. Schema version 7 validates
its text, but it does not implement acknowledgement or alter lifecycle
behavior. A later phase must add server-side policy before
`require_acknowledgement` can enforce anything.

Schemas 1 through 6 remain parseable. They cannot use schema-version-7
metadata fields. This keeps one metadata contract and prevents older manifests
from silently adopting only part of it.

## Backup policy (schema version 6)

Schema version 6 requires one typed `backup` policy. The strategy is
`metadata-only`, `cold-filesystem`, or `cold-sqlite-filesystem`.

```json
{
  "schema_version": 6,
  "backup": {
    "strategy": "cold-sqlite-filesystem",
    "storage": [
      {"component": "app", "id": "data", "disposition": "include"}
    ]
  }
}
```

The policy must account for every persistent managed storage declaration once.
The disposition is `include` or `exclude-ephemeral`. A single-container
application uses component `app`. The manifest cannot supply a host path,
archive path, dump command, restore command, or shell command.

External storage is absent from the policy because format version 1 never
copies imported data. The backup archive records the trusted binding as an
excluded external reference.

See [application backup format version 1](architecture/application-backup-format-v1.md)
for the archive fields and compatibility rules.

## Runtime identity and managed ownership (schema version 5)

Schema version 5 lets a trusted manifest run a container under one numeric
primary UID/GID and assign that same bounded ownership to KITPro-managed
storage. The values must be 1–65535 and must be declared together. The helper
creates the exact managed directory, changes only that directory's ownership,
and revalidates the identity before creating the container.

```json
{
  "schema_version": 5,
  "run_as": {"uid": 1000, "gid": 1000},
  "storage": [{
    "id": "data",
    "container_path": "/data",
    "persistent": true,
    "owner_uid": 1000,
    "owner_gid": 1000
  }]
}
```

The schema cannot declare user names, supplementary groups, capabilities, an
identity for an imported root, or an ownership change outside KITPro-managed
storage. Runtime identity drift is security drift.

## External storage (schema version 4)

`external_storage` declares imported-data slots. It never contains a host path.

```json
{
  "external_storage": [{
    "id": "media",
    "container_path": "/media",
    "mode": "read-only",
    "required": true,
    "purpose": "Media library"
  }]
}
```

`mode` is `read-only` or `read-write`. A read-write slot still requires a root
whose administrator-approved ceiling is read-write. IDs and container targets
are unique and helper-validated. Optional slots set `required` to false.

Invalid manifests include `host_path`, raw bind syntax, relative or protected
container paths, unrecognized modes, duplicate slots, and external storage in
schema versions 1–3.

## Hardware schema (version 3)

Schema version 3 adds a `hardware` array at the application or component level.

| Field | Type | Required | Constraint |
|---|---|---|---|
| `class` | string | yes | `gpu.nvidia`, `gpu.amd`, `gpu.intel.render`, or `video.vaapi` |
| `optional` | boolean | no | Defaults to false |
| `cpu_fallback` | boolean | no | May be true only when `optional` is true |

Valid declarations include `{"hardware":[{"class":"gpu.nvidia","optional":true,"cpu_fallback":true}]}`. For multi-container applications, place the array on only the component that needs the accelerator.

Raw paths such as `/dev/dri/renderD128`, unknown classes, and CPU fallback without optional acceleration are invalid. Host paths, groups, capabilities, Docker requests, runtime arguments, privileged mode, and host networking are not representable.

See [GPU and device access architecture](architecture/gpu-device-access.md).

```json
{
  "schema_version": 1,
  "id": "busybox",
  "name": "BusyBox validation workload",
  "releases": [{
    "version": "1.37.0",
    "registry": "docker.io",
    "repository": "library/busybox",
    "digest": "sha256:<64 lowercase hexadecimal characters>",
    "platform": "linux/amd64"
  }],
  "storage": [{"id":"data","container_path":"/data","persistent":true,"read_only":false}],
  "restart": "unless-stopped"
}
```

IDs are lowercase, bounded, and stable. Release versions are metadata; the
deployment image is always `registry/repository@sha256:digest`. The
single-container schema allows
only Docker Hub and GHCR image identities and `linux/amd64` releases.

Storage declarations contain logical IDs and container paths only. KITPro
derives host paths under `/srv/kitpro/apps/<application>/<installation>/`; users
cannot provide bind sources. `/`, `/etc`, `/proc`, `/sys`, `/dev`, and Docker
socket paths are rejected.

Environment entries are explicitly named and bounded. A required secret may
declare the bounded `random-hex-32` generator. The helper creates 32 random
bytes, persists the encoded value in its root-only state database, and reuses
the value for recreation, restart, and update. A generated secret cannot also
contain a catalog value. Secret values never appear in logs, receipts, API
responses, or HTML. Helper database backups include generated secrets so a
restored installation does not silently rotate them.

Only `no` and `unless-stopped` restart policies are allowed. Commands, when
needed, are fixed catalog argv arrays; users cannot supply executable text or
shell forms. Ports describe internal container ports only. Host publication,
privileged mode, host namespaces, devices, capabilities, security-option
overrides, sysctls, Docker socket mounts, and arbitrary healthcheck commands
are not part of schema version 1.

The helper receives a resolved typed plan rather than this JSON and repeats
the security validation. Catalog content is trusted product input, but image
trust is independently anchored by digest. Remote catalog downloads,
signatures, and application upgrades are future work.

Catalog services declare only internal ports and protocols. Exposure is an
installation policy: `internal` (default), constrained loopback, or an
administrator-configured LAN address. Manifests never contain host addresses,
host ports, Docker port-binding objects, or firewall rules.

## FreshRSS catalog entry

An installed application has a stable installation identity independent of disposable runtime generations. KITPro derives persistent storage from the application ID and installation ID, never from Docker IDs or runtime generations. Runtime removal preserves the installation and data; explicit recreation reuses that storage with a new runtime generation.

FreshRSS is the baseline catalog application. Its entry uses the
official `docker.io/freshrss/freshrss` image, release `1.29.1`, pinned for
`linux/amd64` to platform manifest digest
`sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21`.
The image tag is display metadata only. The internal HTTP port is 80 and is
not published to the host. Persistent logical storage is declared for
`/var/www/FreshRSS/data` and `/var/www/FreshRSS/extensions`; KITPro derives
both host paths under its application root. The only initial environment
default is typed, non-secret `TZ=UTC`, and the restart policy is
`unless-stopped`.

The release and image metadata were checked 2026-09-13 against official
FreshRSS sources:

- https://hub.docker.com/r/freshrss/freshrss
- https://hub.docker.com/r/freshrss/freshrss/tags/
- https://freshrss.github.io/FreshRSS/en/developers/02_First_steps.html
- https://github.com/FreshRSS/FreshRSS/releases

FreshRSS and the other supported catalog applications remain internal by
default. An authenticated administrator may apply KITPro's constrained
loopback or exact-address LAN exposure policy. That installation policy is
stored separately from the manifest; no catalog entry controls its host
address or host port.

Schema version 2 extends this model with typed components and dependencies for
multi-container applications. Paperless-ngx is the current example; its web
component is exposable while its broker remains internal. The same helper
validation and installation-scoped storage rules apply to every component.

The supported catalog inventory, pinned releases, upstream provenance, and
application-specific limitations are maintained in
[Application catalog](application-catalog.md).
