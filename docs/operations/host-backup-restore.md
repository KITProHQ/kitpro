# Back up and restore alpha.14 host state

`POST /api/v1/backup` creates a control database backup and a root-owned helper
archive. The API response marks the backup as containing secrets and states
that remote Cloudflare resources are not included.

## Backup contents

The host backup contains durable local state:

- control and helper database records;
- ingress routes and generation metadata;
- hostname and certificate metadata;
- provided certificate files and private keys;
- ACME account and renewal state;
- Cloudflare provider configuration and the local credential file;
- setup state and stored readiness observations.

Generated nginx files are recreatable runtime state. Remote Cloudflare DNS,
tunnel, and route resources remain remote provider state. Imported application
storage stays outside a host-state archive and follows the application backup
contract.

The archive is mode `0600` and is not encrypted by KITPro. Anyone who obtains
the archive can obtain its private keys and provider credentials. Store and
transport it as a secret.

## Restore safely

Stop the API, helper, ingress, ACME, and Cloudflare services before an offline
host-state restore. Run the packaged restore command only against the selected
backup and use its explicit confirmation that services are stopped.

Restore in this order:

1. Restore core installation state.
2. Restore trusted storage and application state.
3. Restore the application runtime.
4. Verify application readiness.
5. Restore certificate and provider configuration.
6. Build an ingress candidate.
7. Validate the candidate.
8. Activate ingress.
9. Verify local ingress and optional provider state.

The helper verifies archive paths, types, sizes, and hashes before replacement.
It writes a durable restore journal and can recover an interrupted swap. The
control database restore uses an atomic exchange and retains the previous
database. It rejects a database with active WAL or shared-memory sidecars.

Restored routes start in a fail-closed bootstrap generation and retain an
ingress hold until application restore and readiness succeed. A failed
application restore keeps the hold. The desired route remains recorded for a
reviewed retry.
