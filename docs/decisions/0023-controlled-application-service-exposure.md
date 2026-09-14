# ADR-0023: Controlled application service exposure

- Status: Accepted
- Date: 2026-09-13

Catalog manifests declare internal services (HTTP, HTTPS, or TCP) and never
host bindings. Exposure is installation state controlled by KITPro. Phase 1
supports `internal` (default), loopback, and one explicitly configured LAN
address. Host ports are allocated from 20000-29999 and persist per
installation/service across runtime generations. Wildcard, privileged, and
arbitrary host bindings are rejected. Exposure changes require controlled
runtime recreation; public Internet exposure, firewall automation, and
reverse-proxy/TLS configuration are out of scope.

The helper independently validates service identity, container port, mode,
approved address, and allocated host port before constructing Docker bindings.
Manifest content is not Docker port-binding configuration.
