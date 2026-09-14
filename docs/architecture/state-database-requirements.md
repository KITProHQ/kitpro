# State-store requirements (engine intentionally open)

ADR-0016 deliberately does not select a database. Any production control-plane or helper state store must support:

- local-only operation with no cloud dependency;
- ACID transactions and WAL or equivalent crash recovery;
- uniqueness and foreign-key constraints for operation, instance, resource, and lease identities;
- durable fencing/lease updates and concurrent readers/writers;
- append-only audit/event records with sensitive-value minimization;
- schema migrations, version compatibility, and rollback planning;
- backup, restore, integrity checking, and corruption detection;
- bounded administration and resource overhead on the supported host; and
- independent recovery procedures for control-plane and helper-owned state.

The engine decision follows implementation-language and packaging evaluation. A prototype receipt file or in-memory adapter is not production approval.
