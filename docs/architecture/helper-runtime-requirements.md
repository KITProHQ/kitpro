# Privileged-helper runtime requirements

This is the language-neutral contract for the future helper implementation. It is input to stack selection, not a language recommendation.

The runtime must support:

- Unix-domain sockets with reliable `SO_PEERCRED`, bounded framing, timeouts, cancellation, and concurrent clients;
- direct Docker Engine HTTP over a Unix socket without shelling out;
- strict typed decoding, canonical serialization, request hashing, operation IDs, and version negotiation;
- `openat2(2)` with safe descriptor-relative resolution, or a demonstrably equivalent containment mechanism;
- atomic state writes with `fsync`, durable rename semantics, file locking, and crash recovery;
- timestamps, signals, graceful shutdown, restart, randomness, and bounded resource use;
- durable leases/fencing and safe concurrent operations;
- a small dependency surface and predictable systemd integration;
- compatibility with Debian AppArmor and Rocky SELinux policy boundaries;
- no shell dependency, no required child process, and no outbound IP networking; and
- a maintainable Debian package/update story.

Garbage collection and runtime-managed background threads are not disqualifiers, but their memory, shutdown, syscall, and MAC behavior must be measured. The final choice must be based on these requirements plus maintainability and security evidence, not popularity.
