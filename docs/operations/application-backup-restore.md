# Back up and restore an application

KITPro application backups protect one installed application's managed state.
The public Debian and Arch baseline uses Docker. The same archive format has an
Experimental Podman implementation for Rocky Linux, but that does not expand
the public support promise.

This workflow is separate from `/api/v1/backup`. That older endpoint backs up
the KITPro control and helper databases. It does not back up application data.

## Before you create a backup

Check these conditions:

- The installation is present in KITPro and matches the current trusted
  catalog release.
- The backup filesystem has enough free space for a cold staging copy and the
  archive.
- You have an authenticated administrator session and a current CSRF token.
- You have a separate backup for every imported storage root. KITPro records
  imported bindings but does not copy their content.

The initial supported destination is
`/var/lib/kitpro-helper/application-backups`. The package creates this
root-owned directory with mode `0700`. The API cannot select another path.
Future destination adapters can move completed archives without changing the
archive format.

## Create a backup

Send an authenticated `POST` request to:

```text
/api/v1/installations/<installation-id>/backup
```

The request has no body. The API returns the backup ID, filename, archive
SHA-256, creation time, and detected SQLite database count. Keep the backup ID.
Restore accepts the ID, not an arbitrary server path.

For a cold backup, KITPro stops the application's running components in reverse
dependency order. It stages the declared managed directories, then restarts
the components in dependency order. A metadata-only application remains
running. KITPro reports failure if it cannot restart a component that was
running before backup.

Application-created relative symlinks are accepted only when they resolve to
regular files inside the same managed storage root. KITPro stages their content
as ordinary files, so the archive and restored tree do not preserve symlink
identity. Absolute, escaping, broken, directory, socket, device, and FIFO links
fail closed.

Confirm that the archive is root-owned and has mode `0600`:

```sh
sudo ls -l /var/lib/kitpro-helper/application-backups
sudo sha256sum /var/lib/kitpro-helper/application-backups/<archive-name>
```

The helper database stores the same archive SHA-256. Restore checks that value
before it extracts the archive.

## Restore a backup

Restore format version 1 into the same existing installation. The application
ID, installation ID, release, runtime generation, component images, managed
storage declarations, and imported storage bindings must still match.

Send an authenticated `POST` request to:

```text
/api/v1/installations/<installation-id>/restore
```

Use this JSON body:

```json
{
  "backup_id": "op-<32 lowercase hexadecimal characters>"
}
```

KITPro completes these checks before it stops the application:

1. The backup belongs to the target installation.
2. The archive SHA-256 matches the helper inventory.
3. The archive uses a supported format and stays within extraction limits.
4. The destination has space for extraction and a new managed tree.
5. The manifest matches the current trusted installation.
6. Every file checksum passes, and every SQLite database has an accepted
   recorded verification result.

`passed` means the generic SQLite engine completed full integrity and
foreign-key checks. `structural-pages-passed` means the application database
requires a vendor SQLite extension that KITPro does not load; KITPro instead
accounted for every database and freelist page and completed the foreign-key
check. Any other SQLite integrity error fails the backup or restore.

KITPro copies the restored tree beside the current managed tree. It then stops
the application, moves the current tree to a rollback name, installs the new
tree, restores generated secrets in one database transaction, and starts the
components. KITPro deletes the rollback tree only after every previously
running component reports a running runtime state.

Before the first path swap, the helper creates a durable restore journal that
records the fencing token, exact device/inode identity, active, staged, and
rollback paths, prior and restored secret sets, prior running components, and
each prepared, dispatched, and confirmed swap. Other lifecycle mutations for
the installation are blocked while that journal needs recovery.

If activation fails, KITPro moves the failed restored tree to a path with the
suffix `.kitpro-restore-failed-<operation-id>`. It restores the prior managed
tree and generated secrets, then tries to restart the prior runtime state. The
API reports failure even when rollback succeeds.

After a helper or host restart, KITPro inspects the journal and exact path
identities. It completes a proven forward state, restores a proven prior state,
or records cleanup debt. If the tree layout is mixed or identity cannot be
proved, the restore becomes `action_required`; KITPro does not guess which tree
is authoritative. Failed restored trees can remain under
`.kitpro-restore-failed-<operation-id>` for investigation and require an
explicit retention or cleanup decision.

## Verify a restore

Check both the runtime and the application data:

```sh
sudo systemctl status kitpro-api.service kitpro-helper.socket
sudo journalctl -u kitpro-helper.service --since "10 minutes ago"
```

On Docker hosts, inspect the application's containers with `docker ps`. On
Experimental Rocky hosts use `podman ps` and check the generated Quadlet units with
`systemctl status 'kitpro-*'`.

Open the application and verify the restored record, file, or upload. A running
container alone does not prove that the application's data is correct.

On Rocky Linux, also verify SELinux after restore:

```sh
getenforce
sudo find /srv/kitpro/apps/<application>/<installation> -maxdepth 3 -printf '%Z %p\n'
sudo ausearch -m AVC,USER_AVC -ts recent
```

`getenforce` must return `Enforcing`. Do not relabel imported storage as part
of this workflow.

## Protect the archive

Application archives contain generated secrets and user data. KITPro stores
local archives with mode `0600` in a `0700` directory. Format version 1 does
not encrypt archives.

Use an established authenticated-encryption or encrypted-storage system before
you copy an archive off the host. Do not put archives in a world-readable
directory. Do not send archive contents or `secrets/generated.json` to logs or
support channels.

## Know what is not included

Format version 1 does not provide bare-host disaster recovery. It cannot create
a missing installation, change an installation to another release, or restore
an archive into a different installation ID. Host-to-host restore is
unsupported. Restore also cannot reverse an irreversible schema migration that
the upstream application already applied to its data.

KITPro does not include these items:

- imported NAS, media, music, audiobook, or SFTPGo file content;
- Docker or Podman internal storage;
- container IDs, runtime sockets, or Quadlet files;
- host firewall configuration;
- custom external databases;
- application data outside declared KITPro-managed storage.

See [application backup format version 1](https://github.com/KITProHQ/kitpro/blob/v0.1.0-alpha.12/docs/architecture/application-backup-format-v1.md)
for the frozen archive contract and [the frozen assessment](https://github.com/KITProHQ/kitpro/blob/v0.1.0-alpha.12/docs/architecture/application-backup-restore-assessment.md)
for the per-application strategy.

## Troubleshoot failures

| Error | Meaning | Action |
| --- | --- | --- |
| `insufficient backup storage space` | The destination cannot hold the required staging data. | Free space on the filesystem that contains `/var/lib/kitpro-helper/application-backups`, then retry. |
| `backup archive hash mismatch` | The archive differs from the file that KITPro recorded. | Do not restore it. Recover an untampered copy. |
| `backup is incompatible with target installation` | Identity, release, generation, or strategy changed. | Restore the matching installation state. Format version 1 does not migrate releases. |
| `backup topology does not match target installation` | Components, managed storage, or imported bindings differ. | Restore or reassociate the original topology before retrying. |
| `SQLite verification failed` | A staged database failed full verification and was not eligible for the vendor-extension fallback, or failed structural page accounting or foreign-key checks. | Preserve the archive and application logs. Do not overwrite the current application. |
| `application runtime did not become healthy` | The current error string means a component did not return to the running runtime state; it does not prove application readiness. | Inspect the helper journal and runtime logs. Verify application data before another restore. |
| `RestoreRecoveryRequired` | A prior restore journal has not reached a safe terminal state. | Stop issuing lifecycle mutations. Inspect helper logs and follow the [lifecycle recovery runbook](lifecycle-recovery.md). |
