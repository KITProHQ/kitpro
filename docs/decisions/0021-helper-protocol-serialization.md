# ADR-0021: Helper protocol serialization

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related decisions: ADR-0003, ADR-0004, ADR-0016

## Context

The fixture uses length-framed strict JSON. Production needs bounded parsing, canonical request hashing, schema evolution, diagnostics, and safe cross-language compatibility without exposing a generic RPC surface.

## Decision

Retain strict length-framed JSON for the local helper protocol, with explicit protocol version, operation ID, request hash, deadlines, typed operation envelopes, duplicate-key rejection, bounded message size, and canonical serialization for hashing. Do not expose raw Docker or Compose documents. Existing fixture tests and the Go framing/hash probe passed these assumptions.

## Alternatives

- CBOR or protobuf: smaller typed payloads, but add tooling and reduce inspectability before payload size is a demonstrated issue.
- Unframed JSON lines: easier to inspect, but weaker bounded parsing and message delimiting.

## Consequences

Operators can inspect requests during troubleshooting, and Go/Rust/Python tooling can interoperate. Canonicalization must be implemented once and tested against adversarial JSON. Binary serialization can be revisited if measured volume or compatibility requirements change.

## Review conditions

Revisit if framing overhead, canonical JSON limitations, or a future independently trusted component requires a formally generated schema language.
