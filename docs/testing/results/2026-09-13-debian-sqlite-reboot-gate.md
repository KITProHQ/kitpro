# Debian SQLite and reboot-recovery gate — 2026-09-13

**DEBIAN SQLITE/REBOOT GATE: FAIL**

This immutable record covers the disposable Debian VM 500 persistence run.

## Measured results

| Area | Status | Evidence |
| --- | --- | --- |
| Production-shaped DB ownership | PASS | `control.db` was owned by `kitpro-api-test`; `helper.db` was root-owned mode 0600. |
| API control DB write | PASS | API identity appended to control DB. |
| API helper DB write/delete | PASS (denied) | Permission denied for write and delete. |
| Helper identity control DB write | PASS (denied) | Permission denied. |
| WAL/SHM creation | PASS | `helper.db-wal` and `helper.db-shm` were created with root ownership and 0600 mode. |
| Pre-reboot helper receipt | PASS | `pre-reboot` Ping receipt was persisted. |
| VM reboot | PASS | SSH returned; Docker remained active; identities and databases persisted. |
| Post-reboot helper replay/Docker | PASS after runtime fix | `/run/kitpro` reverted to root:root; after restoring its disposable group ownership, receipt replay and Docker inspection passed. |
| Runtime directory persistence | FAIL | Runtime socket parent ownership was not recreated for the API identity after reboot. A tmpfiles/package rule is required. |
| Concurrency/backup/migration matrix | NOT RUN | No standalone driver harness was executed in this run. |

## Reboot evidence

After reboot, `control.db` and `helper.db` remained present with their expected
owners. WAL/SHM files had been checkpointed away, which is valid. Docker was
active and AppArmor remained loaded. The first post-reboot API connection failed:

```text
PermissionError: [Errno 13] Permission denied
```

The cause was `/run/kitpro` reverting to `root:root`; after a disposable
`chown root:kitpro-api-test` and mode `0750`, socket activation, receipt replay,
and Docker inspection all passed.

## Gate disposition

The gate is **FAIL**. Required concurrency, active-WAL backup, crash-kill, and
migration failure tests remain NOT RUN, and reboot recovery requires a durable
tmpfiles/package rule for `/run/kitpro` ownership. No production schema or
runtime implementation was changed.

Disposable units, identities, profile, binary, databases, socket, and test
storage were removed. Docker and VM snapshots were preserved.
