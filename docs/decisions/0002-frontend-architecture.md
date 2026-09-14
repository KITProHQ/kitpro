# ADR-0002: Phase 1 frontend architecture

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related decisions: ADR-0001, ADR-0008, ADR-0009, ADR-0015

## Context

The first slice needs a responsive local dashboard for authentication, host/app status, operation progress, logs, and settings. It does not require a public SaaS-style application shell or offline multi-page application.

## Decision

Use server-rendered HTML with progressively enhanced, small local JavaScript modules. Embed or ship the assets locally with `kitpro-api`; do not require a CDN. Use JSON endpoints from the same API for targeted updates and future CLI compatibility. A visual/accessibility review remains an implementation gate, not a reason to introduce a SPA now.

## Alternatives

- React/Vue/Svelte SPA: richer client state, but larger build/runtime surface and unnecessary first-slice complexity.
- Full server-rendered HTML without enhancement: simplest, but weaker operation-progress and log-refresh ergonomics.

## Consequences

The dashboard remains inspectable, local-first, and easy to package. The API must provide stable page and JSON contracts, and the frontend must avoid embedding authorization decisions in JavaScript. A later SPA remains possible if measured workflows justify it.

## Review conditions

Revisit if accessibility, live operation progress, or log presentation cannot be delivered without excessive page complexity, or if measured user testing demonstrates a substantial need for a richer client application.
