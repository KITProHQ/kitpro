# ADR-0023: Controlled application service exposure

- Status: Accepted
- Date: 2026-09-13

Catalog manifests declare bounded services using HTTP, HTTPS, TCP, or UDP.
Exposure is installation state controlled by KITPro. The current model
supports `internal` (default), loopback, and one explicitly configured LAN
address. Ordinary host ports are allocated from 20000-29999 and persist per
installation/service across runtime generations. Schema version 8 may instead
authorize one exact `fixed_host_port` for a trusted service; no API or UI field
accepts an arbitrary fixed port. Wildcard and arbitrary host bindings are
rejected. Exposure changes require controlled
runtime recreation; public Internet exposure, firewall automation, and
reverse-proxy/TLS configuration are out of scope.

The API and helper carry the complete, bounded service-binding set through
install, replacement, lifecycle, reconciliation, and repair. Each binding
records service identity, TCP or UDP transport, container port, exposure mode,
host address, and host port. The helper independently validates that set
against the embedded manifest before constructing runtime bindings. Scalar
protocol-v2 exposure fields are rejected.

Binding conflicts are transport-aware: the uniqueness key is host address,
host port, and transport, so TCP and UDP may share a number. A wildcard address
conflicts with exact addresses in the same IP family. Preflight checks KITPro
reservations plus Linux TCP and UDP socket tables in `/proc/net`; container
creation/start remains the final authority because state can change after a
point-in-time check. The inspection requires no shell, firewall changes, or
additional helper privilege.

HTTP and HTTPS endpoints may be rendered as browser links. TCP and UDP
endpoints are rendered as address, port, and transport only.
