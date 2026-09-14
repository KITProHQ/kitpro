# First vertical slice implementation checkpoint

The initial Go source tree now exists under `software/server/`. It is intentionally limited to a development-only dashboard, host/Docker inspection, and a typed helper protocol with a constrained test-workload create request. The API binds to loopback by default and has no authentication; it must not be exposed beyond a private validation network.

The helper validates a configured API UID with `SO_PEERCRED`, owns its receipts and resource records in a separate database, and uses direct Docker Engine HTTP over the Unix socket. No Docker CLI, Compose, shell execution, arbitrary Docker IDs, or caller-selected host paths are accepted.

This checkpoint has not yet been deployed to Debian. Integration, restart/reconciliation, and persistent-data preservation remain the next verification work. No production package or application catalog is included.
