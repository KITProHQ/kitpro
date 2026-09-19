# KITPro application backup format version 1

This page defines the on-disk application archive format. It is a reference for
KITPro developers and recovery-tool authors.

## Container

The file suffix is `.kitpro-backup.tar.gz`. The file is a gzip-compressed POSIX
tar archive with one top-level directory named `kitpro-backup`.

```text
kitpro-backup/
├── manifest.json
├── metadata/
│   └── installation.json
├── application/
│   └── <component>/<storage-id>/...
├── secrets/
│   └── generated.json
└── checksums/
    └── sha256.json
```

Writers create the final archive with mode `0600`. They write a `.partial`
file, sync it, rename it atomically, and sync the containing directory.

The archive contains no Docker or Podman storage layout. Runtime-specific IDs,
sockets, generated Quadlet files, and named volumes are not part of the format.

## `manifest.json`

The manifest uses strict JSON. Unknown fields, duplicate fields, trailing JSON,
and unsupported versions are invalid.

| Field | Type | Meaning |
| --- | --- | --- |
| `format` | string | `kitpro-application-backup` |
| `format_version` | integer | `1` |
| `kitpro_version` | string | KITPro build version that created the archive |
| `created_at` | RFC 3339 timestamp | UTC archive creation time |
| `application_id` | string | Trusted catalog application ID |
| `installation_id` | string | Stable KITPro installation ID |
| `release_id` | string | Trusted catalog release |
| `runtime_generation` | integer | Exact runtime generation accepted by restore |
| `strategy` | string | One of the strategies listed below |
| `components` | array | Component IDs, digest-pinned images, and dependencies |
| `storage` | array | Managed storage IDs, dispositions, archive paths, and numeric owners |
| `imported_storage` | array | External binding identity with `content_included: false` |
| `databases` | array | Detected SQLite files and their verification result |
| `secrets` | array | Secret names and inclusion status, never secret values |
| `checksum` | object | `sha256` and `checksums/sha256.json` |

An included storage path is
`application/<component>/<storage-id>`. The normalized component ID for a
single-container application is `app`.

An excluded storage entry uses `exclude-ephemeral` and has no archive path.
Imported storage uses the reason `external-administrator-managed`. Its record
contains the trusted root ID, access mode, filesystem, device and inode
identity, and network-backed flag. The archive does not contain the imported
path, credentials, or content.

## Strategies

| Strategy | Behavior |
| --- | --- |
| `metadata-only` | Records installation identity and runtime state. It has no managed data payload and does not stop the application during backup. |
| `cold-filesystem` | Stops running components, copies included managed storage, and restarts the prior running set. |
| `cold-sqlite-filesystem` | Performs a cold filesystem backup, detects SQLite files by header, and runs integrity and foreign-key checks on the staged copy. |

Format version 1 has no PostgreSQL or MariaDB strategy. A future format must
add trusted typed dump and import implementations before the catalog can deploy
an application that uses either engine.

## Generated secrets

`secrets/generated.json` is strict JSON with this shape:

```json
{
  "version": 1,
  "values": [
    {
      "component": "app",
      "name": "EXAMPLE_SECRET",
      "value": "<64 lowercase hexadecimal characters>"
    }
  ]
}
```

The helper accepts only generated secrets declared by the trusted catalog. It
rejects missing, extra, duplicate, malformed, or renamed secret values.

## Checksums

`checksums/sha256.json` lists every regular payload file except the checksum
index itself. Each record contains the canonical archive path, SHA-256, byte
size, permission bits, UID, and GID. The extractor rejects a missing, extra,
duplicate, or mismatched record.

The helper also records the SHA-256 of the complete compressed archive in its
`application_backups` table. The full-archive hash binds tar directory metadata
and the checksum index to the backup inventory.

## Extraction rules

The extractor uses bounded entry, per-file, and total-size limits. It accepts
regular files and directories only. It rejects these archive forms:

- absolute, non-canonical, backslash-containing, or traversal paths;
- content outside `kitpro-backup/`;
- duplicate entry names;
- symlinks and hard links;
- devices, FIFOs, sockets, and other special files;
- negative or oversized entries;
- unsupported format versions;
- invalid manifests or checksum indexes.

Extraction occurs in a new `0700` directory. A failed extraction removes that
directory. KITPro copies validated content into new managed directories rather
than unpacking over a running installation.

## Compatibility

Version 1 supports exact existing-installation restore. Restore requires the
same application ID, installation ID, release ID, runtime generation,
component image digests, storage policy, and imported storage identity.

The format does not promise cross-release data migration, host-to-host trust
store reconstruction, or creation of a missing installation. Those workflows
need a later compatibility contract.

## Confidentiality

The archive contains application data and generated secrets. Version 1 relies
on owner-only filesystem permissions and does not define encryption. Operators
must use standard authenticated encryption or encrypted storage when an
archive leaves the host. KITPro does not define custom cryptography.
