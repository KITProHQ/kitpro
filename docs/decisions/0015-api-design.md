# ADR-0015: Control-plane API style

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related decisions: ADR-0001, ADR-0002, ADR-0016

## Context

The local dashboard and a future CLI need a debuggable contract for host state, applications, long-running operations, logs, and audit events.

## Decision

Use a versioned REST/JSON API served by the local control plane. Model mutations as idempotent operation resources with explicit status and audit references. Keep the API same-origin with the dashboard in Phase 1; future streaming may use SSE without changing mutation semantics.

## Alternatives

- Generic RPC: strong schemas, but less inspectable and less convenient for browser tooling.
- GraphQL: flexible reads, but unnecessary query complexity and authorization surface for the first slice.

## Consequences

REST/JSON is easy to inspect, test, document, and consume from a browser or CLI. It does not authorize helper operations; the control plane still uses the typed local helper protocol and the helper independently validates every request.

## Review conditions

Revisit if measured operation streaming or a separately trusted integration requires a generated RPC contract.
