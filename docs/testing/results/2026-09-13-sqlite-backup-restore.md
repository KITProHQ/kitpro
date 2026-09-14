# SQLite driver and backup/restore probe — 2026-09-13

Status: **PARTIAL**. The disposable Go probe used `modernc.org/sqlite` v1.37.0.

## Results

| Case | Status | Evidence |
| --- | --- | --- |
| Pure-Go driver build | PASS | `CGO_ENABLED=0` static helper build succeeded. |
| WAL mode | PASS | `PRAGMA journal_mode=WAL` returned `wal`. |
| Foreign keys and uniqueness | PASS | Invalid foreign-key insert rejected; primary-key receipt identity enforced. |
| Transaction commit/integrity | PASS | Committed row reopened and `PRAGMA integrity_check` returned `ok`. |
| Concurrent readers/writers | PASS (bounded probe) | Four readers and two writers completed with `busy_timeout=250` ms; high-contention production load remains open. |
| Crash before/after commit | NOT RUN | Requires process-kill harness. |
| Consistent online backup/restore | PASS (disposable probe) | `VACUUM INTO` produced a pre-mutation generation that reopened and passed integrity checks. Active-reader/writer backup stress remains NOT RUN. |
| Disk-full simulation | NOT RUN | Not attempted on a host filesystem. |

## Requirement

Production backups must use a SQLite-consistent backup mechanism or coordinated checkpoint/read transaction. Copying only `helper.db` while `helper.db-wal` exists is not valid. The final backup spike must verify restored generation, integrity, foreign keys, and helper/control ownership relationships.
