# ADR-0020: Phase 1 production state database

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related decisions: ADR-0016, ADR-0001

## Context

Phase 1 is a one-node local product. It needs durable transactions, WAL/crash recovery, uniqueness, foreign keys, fencing, migrations, append-only events, backups, and low administration burden. ADR-0016 requires separate trust ownership for control-plane and helper state.

## Decision

Use SQLite for Phase 1, with two physically separate database files: one owned and writable by the unprivileged control plane, and one owned and writable only by the privileged helper. Configure WAL, busy/timeout handling, foreign keys, integrity checks, and transactional migrations as implementation requirements. The disposable Go probe passed WAL, foreign-key, transaction, and integrity checks; consistent backup remains an implementation requirement.

PostgreSQL is the fallback when measured concurrency, retention, backup, or multi-node requirements exceed the one-node model. Phase 1 does not justify a database server or its operational dependency.

The Go probe validated `modernc.org/sqlite` v1.37.0 with `CGO_ENABLED=0`, WAL mode, foreign keys, transactions, and integrity checks. Its pure-Go build avoids a libc/CGO dependency but brings a larger transitive module graph; that graph must remain pinned and included in SBOM and vulnerability review.

## Consequences

SQLite minimizes services, supports local-only operation, and simplifies snapshots and recovery. Separate files preserve the trust boundary but require coordinated diagnostics and backup documentation. WAL backups must use a SQLite-consistent backup procedure, not an arbitrary copy of only the main file.

## Alternatives

- PostgreSQL: stronger multi-client concurrency and operational tooling, but adds a daemon, credentials, upgrades, and recovery surface.
- One shared SQLite file: simpler schema joins, but violates the helper-state write boundary.

## Review conditions

Revisit if representative operation concurrency causes unacceptable write contention, if helper durability cannot be packaged safely, or if requirements expand to multi-node coordination.
