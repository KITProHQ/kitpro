# ADR-0022: Application manifest and catalog model

- Status: Accepted
- Date: 2026-09-13
- Owners: Josh

## Context

The first production slice used a single BusyBox definition embedded in API
logic. KITPro needs a small, reviewable way to describe supported applications
without turning the helper into a generic Docker configuration service.

## Decision

Phase 1 uses strict JSON manifests with an explicit schema version. Catalog
content is embedded and shipped with KITPro; entries are parsed and semantically
validated at startup. A manifest is a constrained declarative application
definition, not Docker Compose and not a raw Docker API document. It cannot
carry arbitrary Docker fields, host paths, credentials, capabilities, devices,
network IDs, or shell commands.

The API resolves a validated manifest release into a typed execution plan. The
helper receives only that plan and independently revalidates image, identity,
storage, network, command, environment, and restart constraints before Docker
execution. Catalog trust and Docker image trust remain separate: shipped
catalog content is product-trusted, while every release is still pinned to an
immutable digest. Application IDs are stable and distinct from release IDs and
Docker object names.

Schema version 1 permits only logical storage declarations, one KITPro-owned
per-instance bridge, internal container-port metadata without host publication,
bounded fixed argv, typed environment entries, and the constrained restart
enum `no`/`unless-stopped`. It has no privileged mode, arbitrary capabilities,
devices, host namespaces, Docker socket mounts, sysctls, security-option
overrides, or shell command form. Phase 1 catalog releases target linux/amd64
and Docker Hub or GHCR.

## Alternatives

- YAML: more convenient for hand editing, but duplicate keys and implicit types
  make strict, fail-closed parsing less predictable.
- TOML: readable but less natural for bounded nested release structures and
  less common for catalog distribution.
- Raw Compose/Docker JSON: rejected because it would transfer privileged
  policy to callers and make helper validation incomplete.

## Consequences

JSON is inspectable, deterministic, and supported by the standard library.
Future schema changes require a new explicit version; unknown versions and
fields fail closed. User configuration and secret storage remain separate
follow-up concerns. Remote catalog distribution and signing are not included
in Phase 1; future updates must add signed catalog/release verification.

## Review conditions

Revisit if a measured catalog size, distribution workflow, or schema-generation
requirement justifies a different format or signed package metadata.
