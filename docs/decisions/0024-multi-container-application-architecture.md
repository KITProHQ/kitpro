# ADR-0024: Multi-container application architecture

- Status: Accepted
- Date: 2026-09-14
- Owners: Josh

## Decision

KITPro will extend its constrained application manifest with an explicit
schema-version-2 component model. A logical installation may contain a bounded
set of typed components, each with an immutable image release, logical storage,
typed environment, internal services, and named dependencies.

This is not Docker Compose. Manifests cannot contain arbitrary Docker API
objects, host paths, namespaces, devices, capabilities, security options,
wildcard exposure, or user-supplied orchestration. The helper independently
validates every component before any Docker mutation. Components share one
installation-owned bridge network; only manifest-declared user-facing services
may use the existing controlled exposure policy.

Runtime generation applies to the complete installation runtime set. A
recreation advances one generation for all components and preserves the stable
installation identity and its persistent storage. Dependency graphs are
bounded, acyclic, and executed in deterministic dependency-first order. A
failed or uncertain component operation remains visible and is reconciled before
retry; it is never blindly adopted or recreated.

Schema version 1 manifests remain valid and retain the current single-container
execution path. Version 2 is rejected unless all component declarations pass
strict parsing and semantic validation.

## Scope boundary

The first real multi-container validation target is Paperless-ngx only after
official image provenance, database/cache requirements, and a safe typed plan
are verified. Internal database and cache services remain unexposed. Upgrade,
automatic repair, and destructive data deletion remain separate operations.

## Consequences

The manifest package now validates component IDs, releases, typed storage,
environment, services, restart policy, and dependency references, and exposes a
deterministic dependency ordering helper. Docker and helper execution must be
wired to this plan before a version-2 application is catalogued or claimed as
accepted.
