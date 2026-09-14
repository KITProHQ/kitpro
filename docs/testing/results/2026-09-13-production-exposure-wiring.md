# Production exposure wiring

Date: 2026-09-13

## Result

`PRODUCTION EXPOSURE WIRING: FAIL`

The production model now carries typed service exposure fields through the
manifest, protocol, helper validation, and Docker container-plan layers.
Exposure assignments have an installation/service state table and a bounded
allocator (`20000-29999`) with loopback/LAN address validation.

The authenticated API path can resolve a declared service, allocate or reuse a
port, persist the assignment, and construct a typed recreation request. The
helper derives Docker `PortBindings` only for validated loopback or LAN plans;
internal mode has no host binding. DELETE selects internal mode and retains
the assignment.

## Not yet closed

The Debian loopback smoke deployment was not executed in this run. Exposure
reconciliation, collision integration, lifecycle audit events, and complete
UI controls remain follow-up work. Consequently no claim is made for actual
host publication or LAN reachability.

Local production Go tests and vet pass. No wildcard or public binding was
introduced.
