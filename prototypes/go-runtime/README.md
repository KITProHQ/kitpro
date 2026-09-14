# Disposable Go runtime probes

These probes validate selected Go/Linux assumptions only. They are not KITPro production code and do not choose the final module layout.

Run with `go test ./...`. The probe uses Go 1.27, `golang.org/x/sys/unix`, and `modernc.org/sqlite` (pure Go). It demonstrates Unix peer credentials, framed-socket prerequisites, `openat2` containment, SQLite WAL/foreign keys/integrity, atomic receipt writes, canonical hashing, and graceful HTTP shutdown.

The canonical-hash test is intentionally small: production should hash a typed canonical representation rather than arbitrary maps. `TestDockerEngineUnixSocket` runs only when the local Docker socket exists and uses direct HTTP-over-Unix; otherwise it is skipped for later VM execution. MAC/systemd execution remains a disposable-host concern; no probe grants the API socket access.
