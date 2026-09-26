# Architecture decisions

This directory contains architecture decision records for KITPro. Proposed records require owner approval and validation before they become accepted implementation constraints.

## Decision register

| ID | Decision | Status |
| --- | --- | --- |
| [ADR-0001](0001-production-implementation-stack.md) | Production implementation stack | Accepted: Go for API and helper |
| [ADR-0002](0002-frontend-architecture.md) | Phase 1 frontend architecture | Accepted: server-rendered HTML with local progressive enhancement |
| [ADR-0003](0003-privileged-helper-protocol.md) | Privileged helper protocol | Accepted; production helper and supported-platform validation passed |
| [ADR-0004](0004-docker-integration.md) | Docker integration | Accepted; production adapter and supported-platform validation passed |
| [ADR-0016](0016-durable-state-and-reconciliation.md) | Durable state and reconciliation | Accepted; disposable state-machine fixture passed |
| [ADR-0017](0017-phase-1-host-compatibility.md) | Phase 1 host compatibility | Accepted architecture decision. Alpha.13 designation: Debian, Ubuntu, and Arch Supported; Rocky and Podman Experimental. |
| [ADR-0018](0018-security-boundaries.md) | Security boundaries | Accepted |
| [ADR-0019](0019-helper-mandatory-access-control.md) | Helper mandatory-access-control confinement | Accepted; Debian, Ubuntu, and Arch require enforcing AppArmor; Rocky requires an enforcing SELinux domain before experimental promotion |
| [ADR-0015](0015-api-design.md) | Control-plane API style | Accepted: versioned REST/JSON with operation resources |
| [ADR-0020](0020-production-state-database.md) | Phase 1 production state database | Accepted: two physically separate SQLite databases |
| [ADR-0021](0021-helper-protocol-serialization.md) | Helper protocol serialization | Accepted: strict length-framed JSON |
| [ADR-0024](0024-multi-container-application-architecture.md) | Multi-container application architecture | Accepted: typed bounded components and dependency ordering |

The remaining open decisions are listed in [`docs/architecture.md`](../architecture.md).

## File convention

Name each record `NNNN-short-description.md`. Use the identifiers listed in [`docs/architecture.md`](../architecture.md) when the record answers one of the current open decisions.

## Record states

A record has one of these states:

- `Proposed`: ready for review but not approved for implementation.
- `Accepted`: approved and part of the current architecture.
- `Superseded`: replaced by a later record.
- `Rejected`: considered and not selected.

## Record template

```markdown
# ADR-NNNN: Decision title

- Status: Proposed
- Date: YYYY-MM-DD
- Owners: Unassigned
- Related principles: List the applicable principles

## Context

State the problem, requirements, and verified constraints.

## Options considered

Describe at least two meaningfully different options when the decision has real alternatives.

## Decision

State the selected option. Do not use this section while the record remains exploratory.

## Consequences

Describe benefits, costs, operational effects, security effects, and work that the decision defers.

## Validation

State how the project will test the decision against a real artifact.

## Review conditions

List the evidence or requirement changes that would reopen the decision.
```

Decision records must distinguish requirements, measured evidence, and judgment. They must not select a technology because it is popular or familiar.
