# Active-WAL backup matrix — 2026-09-13

**PASS** on Debian with `modernc.org/sqlite`, WAL, foreign keys, and
`busy_timeout=250ms`.

Ten readers and one writer remained active during five independent `VACUUM INTO`
runs. All five backups passed generation-consistency and `integrity_check`.
Writer commits and reader operations continued during every backup; no busy
events occurred. Backup durations ranged from 0–127 ms in the repeated Debian
runs. Captured generations were valid snapshots and sometimes lagged the live
generation, as expected.

Phase 1 primitive: **VACUUM INTO**. Raw active-WAL file copying remains
prohibited; backups require integrity/foreign-key validation and sufficient free
space.
