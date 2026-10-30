# State-store requirements

ADR-0016 originally left the engine open. The current implementation selects
two independent local SQLite databases: the unprivileged control database and
the root-owned helper database. SQLite is an implementation choice, not a
collapse of the trust boundary. Each process opens only its own database.

The production state stores must support:

- local-only operation with no cloud dependency;
- ACID transactions and WAL or equivalent crash recovery;
- uniqueness and foreign-key constraints for operation, instance, resource, and lease identities;
- durable fencing/lease updates and concurrent readers/writers;
- append-only audit/event records with sensitive-value minimization;
- schema migrations, version compatibility, and rollback planning;
- backup, restore, integrity checking, and corruption detection;
- bounded administration and resource overhead on the supported host; and
- independent recovery procedures for control-plane and helper-owned state.

The helper database at schema 13 owns durable privileged operations, operation
events, installation leases and fencing tokens, runtime generations and
components, component-step evidence, reconciliation results, legacy ownership
and receipt evidence, and restore journals. The control database owns desired
installation state and user-facing operation projections. Helper success is
authoritative for privileged completion; a control projection can be repaired
afterward without repeating the mutation.

Schema migrations run sequentially in one SQLite transaction. The alpha.11
schema-7 path is a release requirement: migrations preserve `receipts`,
`ownership`, and `component_ownership`, then backfill generation evidence
without treating a label or control row as ownership. Pre-upgrade `VACUUM INTO`
backups and integrity/foreign-key checks remain mandatory. A prototype receipt
file or in-memory adapter is not production state.

SQLite cannot make container-runtime or filesystem operations transactional.
The helper therefore commits intent before dispatch, records phase evidence,
observes the external result, and uses reconciliation for unknown outcomes.
