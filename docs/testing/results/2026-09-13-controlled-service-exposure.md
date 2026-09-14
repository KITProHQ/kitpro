# Controlled service exposure validation

Date: 2026-09-13

## Result

`CONTROLLED SERVICE EXPOSURE GATE: FAIL`

The typed Phase 1 foundation is implemented: manifests now declare constrained
internal services, exposure modes are represented by a dedicated package, the
approved unprivileged allocation range is 20000-29999, and helper requests
reject wildcard/invalid addresses and out-of-range bindings. FreshRSS declares
its HTTP service on container port 80 and remains internal by default.

## Remaining concrete blockers

- Docker `PortBindings` wiring is not yet connected to the production
  install/recreate operation and persistent exposure state is not yet exposed
  by API routes.
- Debian loopback/LAN publication, collision, restart/reboot persistence, and
  exposure-drift integration evidence has not been executed.

No host port or wildcard binding was introduced. No public exposure, firewall
rewriting, reverse proxy, or TLS behavior was added.
