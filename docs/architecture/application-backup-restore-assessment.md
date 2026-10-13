# Application backup and restore assessment

Status: discovery baseline with implementation follow-up, 2026-09-16

This assessment records the application state that KITPro must protect. The
repository implements the three strategies and format version 1 described
below. Automated tests cover a filesystem fixture, Open WebUI-shaped SQLite and
secret state, and Paperless-shaped multi-container state. Live acceptance also
covers Ollama, Open WebUI, and Paperless-ngx on Podman and SFTPGo on Docker. See
the [2026-09-16 acceptance record](../testing/results/2026-09-16-application-backup-restore-acceptance.md).

The existing `/api/v1/backup` operation still protects only the control and
helper SQLite stores. Application backups use the installation-scoped backup
and restore operations.

## Scope and evidence

The assessment covers the 20 applications currently visible in the KITPro
catalog. The hidden BusyBox lifecycle fixture is considered separately because
it is not a public application. The repository manifests are authoritative for
the state KITPro mounts and the image releases KITPro runs. Upstream application
documentation is used to identify the content inside those mounts and its
consistency requirements.

The deployed catalog currently has these properties:

- Every persistent application uses runtime-independent host bind storage below
  `/srv/kitpro/apps/<application>/<installation>/`.
- There are no Docker or Podman named volumes to export.
- There is no catalog deployment of PostgreSQL or MariaDB/MySQL.
- SQLite is the default database for the applications that need a relational
  database. Some upstream applications can be configured for an external
  database, but the current KITPro manifests do not deploy one.
- Paperless-ngx has an internal Redis component. Redis is a broker/cache, not
  the authoritative document database.
- Audiobookshelf, Jellyfin, Navidrome, and Plex import read-only external libraries.
  SFTPGo and Syncthing import read-write external file roots. These paths are approved by
  logical storage references and are not KITPro-managed application data.
- Catalog-generated secrets are stored by the privileged helper, not in the
  catalog manifest or API database. Only a manifest-authorized credential may
  be revealed through the protected credential action.

## Backup behavior at discovery time

`internal/backup.Vacuum` uses SQLite `VACUUM INTO` and verifies integrity and
foreign keys. The API calls it for the control database and asks the helper to
do the same for the helper database. Package upgrades and application updates
also create these bounded control-state backups.

That behavior is safe for the two KITPro databases, but it leaves five gaps:

1. No operation enumerates or captures an installation's managed storage.
2. No operation quiesces application writers before copying mutable state.
3. No application database is identified, dumped, or checked.
4. Generated application secrets and storage bindings are not packaged with an
   application recovery point.
5. There is no archive format, checksum validation, safe extractor, restore
   transaction, runtime recreation, or post-restore health gate.

The container runtime interface intentionally has no general-purpose `exec` or
socket pass-through. That is a useful security boundary: backup must not add an
arbitrary command channel merely to invoke application-specific utilities.

## Strategy classes

The first format needs three typed strategies:

| Strategy | Meaning | Consistency rule |
| --- | --- | --- |
| `metadata-only` | The app has no managed persistent storage. | Capture installation/catalog/runtime metadata; no application stop is needed. |
| `cold-filesystem` | Managed persistent storage is authoritative, with no supported relational database in the current plan. | Stop every application component, confirm it is stopped, copy selected managed storage, then restart if it was running. |
| `cold-sqlite-filesystem` | Managed persistent storage includes one or more SQLite databases plus files/configuration. | Stop every component, confirm it is stopped, copy selected managed storage, discover and verify declared SQLite data, then restart if it was running. |

Cold backup is the conservative common primitive for the current catalog. It
provides a clean SQLite shutdown boundary and one consistency point across a
database and adjacent uploads/files. Short controlled downtime is preferable
to an archive that combines database state and user files from different
points in time.

Application-native backup is deliberately not a fourth generic command field.
Several applications have useful native exports, but they are incomplete for a
KITPro recovery point, version-sensitive, or omit secrets and files. A future
native strategy must be a reviewed, typed implementation in KITPro rather than
catalog-supplied shell.

PostgreSQL and MariaDB/MySQL strategies are required before a future catalog
manifest may deploy those engines. They should use typed logical dump/import
implementations (`pg_dump`/`pg_restore` and `mariadb-dump`/`mariadb`) and must
never be represented as arbitrary manifest commands. They are not needed by
the current visible catalog.

## Visible catalog strategy matrix

Every visible manifest has a validated schema version 6 policy. Representative
round trips exercise all three strategies and both container backends. An
application marked "strategy coverage" below is covered by schema, archive,
and strategy tests but has not received its own live data round trip.

| Application | Strategy | Automated representative | Live restore |
| --- | --- | --- | --- |
| Actual Budget | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Audiobookshelf | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Forgejo | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| FreshRSS | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Home Assistant | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| IT-Tools | `metadata-only` | Direct no-downtime test | Strategy coverage |
| Jellyfin | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Mealie | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Memos | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Navidrome | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Nextcloud | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Ollama | `cold-filesystem` | Filesystem fixture | PASS: Rocky/Podman |
| Open WebUI | `cold-sqlite-filesystem` | Direct SQLite and secret round trip | PASS: Rocky/Podman |
| Paperless-ngx | `cold-sqlite-filesystem` | Direct multi-component round trip | PASS: Rocky/Podman |
| Plex | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| SFTPGo | `cold-sqlite-filesystem` | Live opt-in Docker round trip | PASS: Docker |
| Syncthing | `cold-filesystem` | Shared strategy tests | Strategy coverage |
| Uptime Kuma | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |
| Vaultwarden | `cold-sqlite-filesystem` | Shared strategy tests | Strategy coverage |

### Actual Budget

- Components: one `actual-server` container.
- Managed storage: `data` at `/data`.
- Database and files: the server index is SQLite (`server-files/account.sqlite`)
  and budget documents live below `user-files`; server configuration also lives
  in the data directory.
- Configuration/secrets: server configuration is managed inside `/data`; there
  is no KITPro-generated secret in the manifest.
- User content: budget files and sync state below `/data`.
- Imported storage: none.
- Ephemeral data: container filesystem and ordinary transient process state.
- Native backup: Actual can export individual budget files, but that is not a
  complete server installation backup.
- Consistency: stop required to align the account database, sync state, and
  budget files.
- KITPro strategy: `cold-sqlite-filesystem`, include managed `data`.

Reference: [Actual server configuration and data paths](https://actualbudget.org/docs/config/).

### Audiobookshelf

- Components: one Audiobookshelf container running as UID/GID 1000.
- Managed storage: `config` at `/config`; `metadata` at `/metadata`.
- Database and files: SQLite database/migrations in `/config`; metadata, cover
  images, logs, and application-created backup material in `/metadata`.
- Configuration/secrets: configuration and accounts are in the managed paths;
  no KITPro-generated secret.
- User content: metadata, progress, collections, cover art, and related state.
- Imported storage: required read-only `audiobooks` at `/audiobooks`.
- Ephemeral data: logs and rebuildable caches may coexist with authoritative
  metadata; the first format includes both managed mounts rather than relying on
  unstable internal exclusions.
- Native backup: built-in backups cover the database and cover images, but do
  not protect the imported audiobook library.
- Consistency: stop required so SQLite and metadata files share one point in
  time.
- KITPro strategy: `cold-sqlite-filesystem`, include `config` and `metadata`;
  record but do not copy the imported audiobook binding.

Reference: [Audiobookshelf container storage](https://www.audiobookshelf.org/docs/documentation/install/docker/).

### FreshRSS

- Components: one FreshRSS container.
- Managed storage: `data` at `/var/www/FreshRSS/data`; `extensions` at
  `/var/www/FreshRSS/extensions`.
- Database and files: the catalog default uses SQLite data under per-user data
  directories; application configuration, users, feeds, and state live in
  `data`. Third-party extensions live in `extensions`.
- Configuration/secrets: FreshRSS configuration is in managed `data`; no
  KITPro-generated secret.
- User content: subscriptions, categories, read/star state, and extension data.
- Imported storage: none.
- Ephemeral data: FreshRSS cache content under `data` is rebuildable but shares
  the authoritative mount.
- Native backup: FreshRSS provides CLI backup/restore. Its own documentation
  still identifies `data` and installed extensions as the complete filesystem
  boundary.
- Consistency: stop web and scheduled writers before copying the two managed
  mounts.
- KITPro strategy: `cold-sqlite-filesystem`, include `data` and `extensions`.

Reference: [FreshRSS backup guidance](https://freshrss.github.io/FreshRSS/en/admins/05_Backup.html).

### Home Assistant

- Components: one Home Assistant container.
- Managed storage: `config` at `/config`.
- Database and files: default recorder database
  `/config/home-assistant_v2.db` (SQLite), YAML configuration, `.storage`
  registries, automations, dashboards, credentials, and locally stored media
  reachable through this mount.
- Configuration/secrets: configuration and application credentials live in the
  managed mount; no separate KITPro-generated secret.
- User content: history, automations, dashboards, and local configuration.
- Imported storage: none in the KITPro manifest.
- Ephemeral data: caches and logs below the config tree are not separated by the
  current manifest.
- Native backup: Home Assistant offers native backup functionality, but KITPro
  Container exposes only `/config`; a KITPro archive must not depend on an
  unmounted upstream backup directory.
- Consistency: stop required. The upstream application can be configured to use
  a remote database from `configuration.yaml`; v1 restore must reject or flag a
  detected non-SQLite recorder URL because that data is outside the declared
  plan.
- KITPro strategy: `cold-sqlite-filesystem`, include `config`.

References: [Home Assistant recorder database](https://www.home-assistant.io/integrations/recorder/), [Home Assistant backups](https://www.home-assistant.io/common-tasks/general/#backups).

### IT-Tools

- Components: one stateless web container.
- Managed storage: none.
- Database, configuration, secrets, and uploads: none declared.
- Imported storage: none.
- Ephemeral data: browser-local data and container filesystem only.
- Native backup: not applicable to server-side state.
- Consistency: no stop required for data capture.
- KITPro strategy: `metadata-only`.

### Jellyfin

- Components: one Jellyfin container.
- Managed storage: `config` at `/config`; `cache` at `/cache`.
- Database and files: SQLite database and configuration below `/config`, along
  with accounts, library metadata, plugins, images, and operational settings.
- Configuration/secrets: managed in `/config`; no KITPro-generated secret.
- User content: watched state, users, playlists, metadata, subtitles, and other
  server-generated library state. Source media is external.
- Imported storage: required read-only `media` at `/media`.
- Ephemeral data: `/cache` is rebuildable and should be excluded from v1 backup
  content while its mapping is recorded.
- Native backup: Jellyfin 10.11+ has an online backup facility. Manual upstream
  guidance requires stopping Jellyfin before copying its data/config. The
  catalog release is 12.1, but KITPro still needs a runtime-neutral recovery
  format rather than embedding a Jellyfin archive.
- Consistency: stop required for a cold copy of `/config`.
- KITPro strategy: `cold-sqlite-filesystem`, include `config`, exclude `cache`,
  and record but do not copy the imported media binding.

References: [Jellyfin backup and restore](https://jellyfin.org/docs/general/administration/backup-and-restore/), [Jellyfin database and paths](https://jellyfin.org/docs/general/administration/configuration/).

### Mealie

- Components: one Mealie container.
- Managed storage: `data` at `/app/data`.
- Database and files: default SQLite database plus recipes, images, generated
  application backups, and site state in `/app/data`.
- Configuration/secrets: catalog environment plus database/application state;
  no KITPro-generated secret.
- User content: recipes, images, users, groups, meal plans, and settings.
- Imported storage: none.
- Ephemeral data: process caches outside the data mount.
- Native backup: Mealie provides full-site backup/restore, but upstream states
  that stopping the container and backing up `/app/data` is the preferred
  complete SQLite deployment backup.
- Consistency: stop required.
- KITPro strategy: `cold-sqlite-filesystem`, include `data`.

Reference: [Mealie backup and restore](https://docs.mealie.io/documentation/getting-started/usage/backups-and-restoring/).

### Memos

- Components: one Memos container.
- Managed storage: `data` at `/var/opt/memos`.
- Database and files: default SQLite database `memos_prod.db` and resources in
  the managed data directory.
- Configuration/secrets: application settings are database-backed; no
  KITPro-generated secret.
- User content: memos, users, resources/attachments, and settings.
- Imported storage: none.
- Ephemeral data: process state outside the data mount.
- Native backup: no complete native export is relied upon by KITPro.
- Consistency: stop required before copying the SQLite database and resources.
- KITPro strategy: `cold-sqlite-filesystem`, include `data`.

Reference: [Memos SQLite deployment evidence](https://github.com/usememos/memos/issues/3101).

### Navidrome

- Components: one Navidrome container running as UID/GID 1000.
- Managed storage: `data` at `/data`.
- Database and files: SQLite database, playlists, artwork/cache, and application
  configuration/state in `/data`.
- Configuration/secrets: managed data and catalog environment; no
  KITPro-generated secret.
- User content: users, play counts, favorites, playlists, scans, and metadata.
- Imported storage: required read-only `music` at `/music`.
- Ephemeral data: artwork cache is rebuildable but resides in the authoritative
  managed mount.
- Native backup: Navidrome has database backup/restore commands; upstream notes
  that they do not include music or configuration.
- Consistency: stop required for a complete database-plus-files recovery point;
  upstream also requires the service stopped for native restore.
- KITPro strategy: `cold-sqlite-filesystem`, include `data`; record but do not
  copy the imported music binding.

Reference: [Navidrome backup behavior](https://www.navidrome.org/docs/usage/admin/backup/).

### Ollama

- Components: one Ollama container.
- Managed storage: `models` at `/root/.ollama`.
- Database and files: no relational database; manifests, model blobs, locally
  created models, and Ollama identity material reside in the managed tree.
- Configuration/secrets: no KITPro-generated secret.
- User content: downloaded and locally created models. Although downloaded
  blobs can be fetched again, local model state is not assumed rebuildable.
- Imported storage: none.
- Ephemeral data: in-memory model state and container filesystem.
- Native backup: no complete native backup/restore interface is used.
- Consistency: stop required to avoid capturing an in-progress pull or model
  creation.
- KITPro strategy: `cold-filesystem`, include `models`. Administrators must plan
  capacity because archives can be very large.

### Open WebUI

- Components: one Open WebUI container.
- Managed storage: `data` at `/app/backend/data`.
- Database and files: `webui.db` (SQLite), uploads, vector database, generated
  content, audit data, and cache in the managed tree.
- Configuration/secrets: `WEBUI_SECRET_KEY` is generated and retained by the
  KITPro helper; application settings and credentials also live in the database.
- User content: users, chats, knowledge files, uploads, generated media, and
  vector index state.
- Imported storage: none.
- Ephemeral data: cache/audit content is mixed into the managed tree. It is
  included initially to avoid an incomplete recovery point.
- Native backup: database export alone omits uploads and vector data. Upstream
  full-volume guidance captures all data and requires a stopped application for
  a safe SQLite copy.
- Consistency: stop required across SQLite, uploads, and vector data.
- KITPro strategy: `cold-sqlite-filesystem`, include `data` and the generated
  secret record.

References: [Open WebUI database export](https://docs.openwebui.com/tutorials/maintenance/database/), [Open WebUI persistent data inventory](https://docs.openwebui.com/tutorials/maintenance/backups/).

### Paperless-ngx

- Components: `broker` (Redis) and `web` (Paperless-ngx).
- Managed storage: broker `data` at `/data`; web `data` at
  `/usr/src/paperless/data`, `media` at `/usr/src/paperless/media`, `consume` at
  `/usr/src/paperless/consume`, and `export` at `/usr/src/paperless/export`.
- Database and files: default SQLite database in web `data`; documents,
  thumbnails, and other user media in `media`; incoming documents in `consume`;
  user/native exports in `export`.
- Configuration/secrets: application settings are in managed state and catalog
  environment; no KITPro-generated secret.
- User content: database metadata, original/archived documents, thumbnails,
  pending consumed documents, and export artifacts.
- Imported storage: none.
- Ephemeral data: Redis broker state is non-authoritative and is recreated from
  the primary application state. Its managed mount is excluded from backup
  content but its component/storage mapping is recorded.
- Native backup: `document_exporter`/`document_importer` covers documents,
  thumbnails, metadata, and database content, but it omits API tokens and is
  tied to the exact Paperless version. KITPro cold backup is more complete for
  the current SQLite topology.
- Consistency: stop the web component first to stop producers, then stop Redis;
  copy all four web storage roots at one cold point. Restore starts Redis before
  the web component using the normal dependency order.
- KITPro strategy: `cold-sqlite-filesystem`, include all web storage; exclude
  broker `data` as ephemeral.

Reference: [Paperless-ngx backup and exporter behavior](https://github.com/paperless-ngx/paperless-ngx/blob/dev/docs/administration.md#making-backups-backup).

### SFTPGo

- Components: one SFTPGo container running as UID/GID 1000.
- Managed storage: `config` at `/var/lib/sftpgo`.
- Database and files: default SQLite provider database `sftpgo.db`, global
  configuration, host keys, credentials, and provider backup material.
- Configuration/secrets: keys and provider state live in the managed mount; no
  KITPro-generated secret.
- User content: virtual users, folders, groups, policies, host keys, and related
  control state.
- Imported storage: required exclusive read-write `files` at
  `/srv/sftpgo/data`. It may contain very large or externally managed user data.
- Ephemeral data: in-memory sessions and transfer state.
- Native backup: SFTPGo can dump provider data through its API/EventManager, but
  that does not make a complete copy of configuration, host keys, or external
  user files.
- Consistency: stop required for the managed SQLite/config tree. In-flight file
  transfers are interrupted. The imported file root is never copied implicitly.
- KITPro strategy: `cold-sqlite-filesystem`, include managed `config`; record
  the imported read-write binding and mark its content excluded.

References: [SFTPGo data-provider configuration](https://github.com/sftpgo/docs/blob/main/docs/config-file.md#data-provider), [SFTPGo provider backup example](https://github.com/drakkan/sftpgo/blob/main/examples/backup/README.md).

### Uptime Kuma

- Components: one Uptime Kuma container.
- Managed storage: `data` at `/app/data`.
- Database and files: default SQLite database `kuma.db`, monitor history,
  notifications, status pages, certificates, and application state.
- Configuration/secrets: notification tokens and other sensitive settings are
  application data; no KITPro-generated secret.
- User content: monitors, history/events, incidents, maintenance windows,
  status pages, and notification configuration.
- Imported storage: none.
- Ephemeral data: process state outside `/app/data`.
- Native backup: JSON export is not sufficient because it omits history and
  event data. Upstream supports MariaDB as an alternative, but the KITPro
  manifest deploys SQLite only.
- Consistency: stop required before copying `/app/data`.
- KITPro strategy: `cold-sqlite-filesystem`, include `data`.

References: [Uptime Kuma data directory and database options](https://github.com/louislam/uptime-kuma/wiki/Environment-Variables), [Uptime Kuma persistent mount](https://github.com/louislam/uptime-kuma/wiki/%F0%9F%94%A7-How-to-Install).

### Vaultwarden

- Components: one Vaultwarden container.
- Managed storage: `data` at `/data`.
- Database and files: `db.sqlite3`, attachments, Sends, `config.json`, RSA
  signing keys, and icon cache.
- Configuration/secrets: sensitive admin/SMTP configuration and private signing
  keys are in `/data`; no KITPro-generated secret.
- User content: encrypted vault records in SQLite plus file attachments and
  Send attachments.
- Imported storage: none.
- Ephemeral data: icon cache is rebuildable, but the first format includes the
  whole managed mount; temporary upload content is not relied upon.
- Native backup: Vaultwarden's backup command protects its SQLite database but
  not the complete attachments/config/keys tree.
- Consistency: stop required to capture the database, attachments, and keys as
  one recovery point and to avoid mismatched WAL sidecars.
- KITPro strategy: `cold-sqlite-filesystem`, include `data`.

Reference: [Vaultwarden backup inventory and restore rules](https://github.com/dani-garcia/vaultwarden/wiki/Backing-up-your-vault).

### Nextcloud

- Components: one official Nextcloud Apache container.
- Managed storage: `html` at `/var/www/html`.
- Database and files: the default SQLite database is in `data` alongside user
  files; configuration, custom applications, themes, and generated instance
  state are also below the managed tree.
- Configuration/secrets: browser setup writes the administrator account and
  generated application secrets into Nextcloud state. KITPro does not collect
  the administrator password or inject database credentials.
- User content: default-layout uploads below `/var/www/html/data`.
- Imported storage: none.
- Ephemeral data: process and temporary state outside `/var/www/html`.
- Native backup: Nextcloud documents configuration, data, database, and themes
  as required restore inputs. The bounded SQLite profile keeps all four inside
  the one managed tree.
- Consistency: stop required before copying the SQLite database and adjacent
  files as one recovery point. This is not an online transactional backup.
- KITPro strategy: `cold-sqlite-filesystem`, include `html`.

References: [official image persistent-data layout](https://github.com/nextcloud/docker#persistent-data), [Nextcloud restore requirements](https://docs.nextcloud.com/server/stable/admin_manual/maintenance/restore.html).

### Syncthing

- Components: one official Syncthing container.
- Managed storage: `config` at `/var/syncthing`, including device identity,
  certificates, configuration, GUI state, peer definitions, folder
  definitions, and application-generated index state.
- Imported storage: one required read-write root mounted at `/sync`.
- User content: synchronized files below `/sync`; these are not copied into the
  application backup and must be protected separately by the administrator.
- Consistency: stop required before copying the managed tree so configuration,
  identity, and index state share one filesystem point.
- KITPro strategy: `cold-filesystem`, include managed `config`; preserve the
  external binding as metadata and exclude the external root's contents.

Reference: [official container storage layout](https://github.com/syncthing/syncthing/blob/main/README-Docker.md).

## Hidden validation fixture

BusyBox has one managed `/data` mount and no database. If used to validate the
backup engine, it uses `cold-filesystem`. Its results do not determine public
catalog coverage.

## Implemented format version 1

The archive describes application state, never Docker or Podman storage. A
gzip-compressed POSIX tar archive contains one top-level directory:

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

`manifest.json` contains:

- format name and integer format version;
- KITPro version and archive creation time in UTC;
- application/catalog ID, installation ID, release ID, and runtime generation;
- every component, exact digest-pinned image, dependency, and selected strategy;
- every managed storage mapping, content disposition (`included` or
  `excluded-ephemeral`), ownership, and archive prefix;
- imported storage logical binding ID, access mode, mount identity evidence,
  and an explicit `content_included: false` marker, but not NAS credentials;
- database inventory and validation result;
- generated secret inventory without values;
- component dependency data used for ordering;
- checksum algorithm and checksum index path.

Generated secret values are stored only in `secrets/generated.json`. They must
not appear in manifest fields, filenames, process arguments, ordinary logs, or
API responses.

The archive and every temporary workspace use owner-only permissions. Version 1
supports a local administrator-controlled destination. Encryption is not part
of version 1: weak custom cryptography is worse than a clear boundary. Archives
therefore remain sensitive root-owned files with mode `0600`, and documentation
must require encrypted storage/transport when they leave the host. A future
destination layer can wrap the same archive with a standard authenticated
encryption tool without changing its internal schema.

## Minimal catalog schema change

Backup policy belongs in the trusted catalog because only the catalog knows
which declared storage is authoritative. The minimum addition is typed data,
not commands:

```json
"backup": {
  "strategy": "cold-sqlite-filesystem",
  "storage": [
    {"component": "app", "id": "data", "disposition": "include"}
  ]
}
```

Allowed strategies and dispositions are a closed set. Every persistent storage
declaration must be named exactly once as `include` or `exclude-ephemeral`.
Components without an explicit ID use the normalized `app` component. External
storage never appears in this list because it is always reference-only in
format version 1.

SQLite files are discovered inside included managed roots after quiescence by
file signature and recorded in the archive manifest. KITPro first runs full
SQLite integrity and foreign-key checks on a private writable copy so copied
WAL state can recover safely. If the database schema requires an unavailable
application-provided collation, tokenizer, function, or virtual-table module,
KITPro requires extension-independent structural page accounting plus a
successful foreign-key check and records `structural-pages-passed` instead of
`passed`. Other integrity errors still fail closed. Discovery avoids brittle
application path globs while validating the databases actually present. A
catalog can add a future explicit database declaration when a non-SQLite
engine is deployed.

## Backup lifecycle

The privileged helper owns the filesystem snapshot boundary. The unprivileged
API owns authentication, intent, operation records, catalog selection, and the
control-plane projection. A backup request names an installation and a
destination directory registered/configured by the administrator; it cannot
carry an arbitrary source path or command.

1. API verifies the installation and obtains its exact normalized plan.
2. Helper independently validates the plan against its embedded catalog and
   ownership records.
3. Helper resolves every managed directory beneath `/srv/kitpro/apps` with the
   existing no-follow/path-ownership controls.
4. Helper checks destination capacity against source size plus bounded staging
   overhead, creates a `0700` workspace, and reserves a non-existing final name.
5. Helper records which components were running, stops them in reverse
   dependency order, and verifies they are stopped.
6. Helper copies included managed roots without crossing into imported
   storage. A relative symlink may be normalized to an ordinary staged file
   only when its resolved target is a regular file inside the same managed
   root; all other symlinks fail closed.
7. Helper discovers and verifies SQLite files in the cold copy.
8. Helper captures generated secrets and trusted storage binding metadata from
   its own store, and installation/release/exposure intent supplied by the API.
9. Helper writes the manifest, hashes every regular payload entry, writes the
   checksum index, and uses stable ordering within each archive section.
10. Helper `fsync`s and atomically renames the `0600` partial archive to its
    final name.
11. In a deferred cleanup path, helper restarts only the components that were
    running before backup, in dependency order, and removes the workspace.
12. API reports success only after the restart/readiness check passes. An
    archive may be reported as created-but-application-unhealthy rather than
    silently called a fully successful backup.

A dump/copy failure must still execute the restart safeguard. Incomplete
archives retain a `.partial` suffix only when preservation is useful for
diagnosis; otherwise they are removed.

## Restore lifecycle and safety boundary

Format version 1 restores only an existing KITPro installation with the same
application ID and a catalog release whose component image digests exactly
match the archive. Cross-version data migration is not implied. Bare-host
disaster recovery can be layered on this archive after coordinated restoration
of the control/helper trust stores is designed; it must not weaken ownership
fences in the first implementation.

1. Measure the archive with size and entry-count limits before extraction.
   Reject absolute paths, `..`, duplicate names, non-canonical names, device
   nodes, FIFOs, sockets, hard links, and symlinks.
2. Verify available space for extraction and the new managed tree.
3. Extract into a `0700` staging directory. Validate strict manifest JSON, the
   checksum index, and every regular payload checksum before using staged data.
4. Reject an unknown format, wrong application or installation, component
   mismatch, image digest mismatch, undeclared storage, or unsupported
   strategy. No archive path becomes a host destination.
5. Stop the application in reverse dependency order and confirm it is stopped.
6. Move the current managed installation tree to a helper-owned rollback area;
   do not overwrite it in place.
7. Materialize restored managed roots with recorded safe modes and numeric
   ownership. Restore generated secrets through the helper store, not an API
   response or environment argument.
8. Keep the existing exact-generation runtime definition. Start its components
   in dependency order after the managed tree and secrets are active.
9. On SELinux hosts, create restored data under the established labeled
   managed-storage tree. Never relabel imported storage as part of restore.
10. Require every component that previously ran to report a running runtime
    state. Live acceptance must also verify application-level readiness.
11. Commit by deleting the rollback tree only after health succeeds. If any
    step fails, preserve diagnostics, restore the prior managed tree and secret
    generation, recreate/start the prior runtime, and report the restore as
    failed even if rollback succeeds.

Restoring a backup never follows the normal uninstall path and never modifies
another installation.

## Imported storage semantics

Imported storage is a reference, not backup payload. Version 1 records:

- the catalog storage slot and component;
- read-only/read-write mode;
- trusted root identity and mount identity needed to detect reassociation;
- whether the binding was available at backup time; and
- `content_included: false` with reason `external-administrator-managed`.

It does not copy files, NAS credentials, mount configuration, or an external
database. Restore requires the administrator to re-establish or explicitly
approve the same trusted binding. Read-only libraries can be absent while
staging but application readiness may require them. SFTPGo's read-write file
root is essential user data that the administrator must protect separately;
KITPro backs up only SFTPGo's managed control/configuration state.

## SELinux and ownership

Managed storage remains below `/srv/kitpro/apps` and uses the existing KITPro
SELinux model. Archive entries record numeric UID/GID and a constrained mode,
not SELinux xattrs. Restore creates files only under the exact managed root,
reapplies declared storage ownership, restores the base managed-storage
context, and recreates the runtime so private container labels are applied.

Imported storage is never extracted into, relabelled, or ownership-modified.
Docker hosts continue to use the same normalized plan. Podman-specific paths,
Quadlet files, container IDs, and MCS categories are not archive payload.

## Failure requirements

The implementation must make these failures explicit and testable:

- insufficient destination or restore space before stopping the app;
- stop/quiesce timeout;
- interrupted copy/database validation;
- invalid, oversized, or traversal-bearing archive;
- checksum mismatch;
- unsupported format version;
- wrong application, installation, release, or image set;
- missing required managed storage/component;
- generated-secret restoration failure;
- runtime recreation failure;
- readiness failure after restore; and
- rollback failure.

Logs may include operation ID, installation ID, strategy, phase, and error
category. They must not include secret values, archive payload, user filenames,
database rows, or imported host paths.

## Implementation and validation status

The repository implements catalog policy, archive creation and extraction,
helper inventory, typed protocol operations, authenticated API operations,
cold managed-storage capture, SQLite verification, generated-secret capture,
capacity checks, staged restore, and rollback.

Automated tests pass for filesystem, SQLite and secret, and Paperless-shaped
multi-component restore. The remaining work is live Docker and Podman parity,
Rocky SELinux restore, application-level state verification, and reboot after
restore.

This sequence changes only the application-state boundary demonstrated by the
acceptance blocker. It does not redesign Docker/Podman orchestration or create a
Rocky-specific backup path.
