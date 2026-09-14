# KITPro Server first vertical slice

This plan is documentation only. Production source creation begins in the next
milestone.

## Flow

```text
Browser → kitpro-api → control.db → operation resource
        → helper UDS → kitpro-helper → helper.db / receipt → Docker Engine
```

## Scope

- server-rendered dashboard shell;
- host and Docker health/version observation;
- one constrained Docker-managed harmless workload;
- durable operation creation in `control.db`;
- typed helper request and helper-owned receipt/ownership state;
- inspect/start/stop/remove for the KITPro-owned workload;
- persistent-data preservation on uninstall;
- restart reconciliation and operation status in the UI.

Authentication is a development placeholder only. Cloud, multi-host, Rocky
production support, hardware, KITPro OS, arbitrary Compose, public ingress,
automatic TLS, application-data backup, advanced networking, large catalogs,
RBAC, mobile, and AI are out of scope.

## Initial implementation shape

Create `software/server/` with separate `kitpro-api` and `kitpro-helper` Go
commands, shared typed protocol code, control/helper SQLite state packages,
Docker Engine adapter, ownership/reconciliation packages, migrations, embedded
web assets, Debian packaging metadata, and integration tests.

## Acceptance gates

The slice is complete only when API/helper identity separation, AppArmor and
systemd contracts, deterministic ownership, receipt durability, restart
reconciliation, digest-pinned workload creation, safe lifecycle operations,
bounded logs, and persistent-data preservation are covered by automated tests.
