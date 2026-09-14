# Disposable stack-evaluation notes

This directory is reserved for narrow, non-production implementation spikes. It must not become KITPro Server source code.

The existing privilege-boundary and reconciliation fixtures already provide measured evidence for Unix sockets, `SO_PEERCRED`, bounded JSON, `openat2`, Docker Engine semantics, durable receipts, locking, restart behavior, and ownership checks. No additional language-specific spike is required to make the proposed stack recommendation: the remaining uncertainty is implementation-specific and belongs in the first post-selection spike.

Before production code, the selected Go stack must add equivalent probes for:

- `SO_PEERCRED` and `openat2` wrappers;
- direct HTTP-over-Unix Docker calls;
- canonical JSON hashing;
- SQLite WAL, locking, and consistent backup;
- atomic `fsync`/rename receipts; and
- systemd graceful shutdown and the accepted hardening matrix.

The fallback Rust stack must pass the same probes. Results belong in immutable test records.
