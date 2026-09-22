# KITPro Server

KITPro Server is an open-source platform for operating self-hosted
applications without manually coordinating all of the infrastructure behind
them. It manages a trusted application lifecycle on your Linux server while
the workloads, persistent data, and host remain under your control.

## Why KITPro Server exists

Installing an application is often the easy part. Complexity accumulates when
you update it, replace a runtime, preserve storage, diagnose a failure, recover
from an interrupted operation, take a backup, or decide what is safe to
delete. Each tool can work as designed while the owner still has to coordinate
their combined state.

KITPro coordinates that work through a local browser interface and a narrowly
privileged helper. It verifies state before changing it, preserves durable
installation identity and data when it replaces disposable runtime machinery,
records uncertainty, and stops when it cannot prove a safe next step.

Standard Linux, systemd, Docker, and application data remain inspectable. KITPro
is not a generic Docker or Compose dashboard, and it does not hide the server
from its owner.

## What alpha.12 changes for the user

KITPro Server `v0.1.0-alpha.12` provides one consistent path to:

- install applications from a trusted, digest-pinned catalog;
- preserve installation identity and managed storage across runtime
  replacement;
- start, stop, recreate, expose, and update declared application services;
- reconcile recorded intent with observed runtime and storage state;
- apply a bounded repair chosen from fresh evidence;
- back up and restore managed application storage within a strict
  same-installation boundary; and
- inspect durable operations and release provenance.

Debian 13 and Arch Linux are the supported public baseline. Ubuntu has
development and validation evidence only. Rocky Linux 10 and Podman remain
Experimental.

## Alpha limitations

KITPro Server is active alpha software. Breaking changes and incomplete
workflows may occur. Alpha.12 does not provide application-aware readiness,
imported-storage backup, host-to-host restore, bare-host recovery, automatic
rollback of irreversible upstream schema changes, destructive application-data
deletion, clustering, automatic failover, or public TLS and domain automation.
Some repair and recovery actions require the API or root-local tooling rather
than a complete browser workflow.

Read the [current-state reference](docs/product/kitpro-server-current-state.md)
and [known limitations](docs/release/known-limitations.md) before using KITPro
with important data.

![KITPro Server dashboard](docs/assets/screenshots/kitpro-server-dashboard.png)

## Try alpha.12

> Review the alpha limitations and keep an independent backup of important
> data. Alpha software can contain breaking changes and incomplete recovery
> paths.

For a fresh supported host, follow the [alpha.12 quickstart](docs/release/quickstart.md).
An existing alpha.11 installation must use the documented transition wrapper.
Raw `apt install`, `dpkg -i`, and `pacman -U` transitions from alpha.11 are
unsupported because they bypass KITPro's application-level safety checks.

## Documentation

### Start here

- [What alpha.12 does today](docs/product/kitpro-server-current-state.md)
- [Known limitations](docs/release/known-limitations.md)
- [Platform support matrix](docs/support-matrix.md)

### Installation and first use

- [Alpha.12 quickstart](docs/release/quickstart.md)
- [Install on Debian 13](docs/install-debian-package.md)
- [Install on Arch Linux](docs/install-arch-package.md)
- [Complete first use](docs/release/quickstart.md#first-run)

### Operate, back up, and recover

- [Operations index](docs/operations/README.md)
- [Back up and restore an application](docs/operations/application-backup-restore.md)
- [Recover application lifecycle state](docs/operations/lifecycle-recovery.md)
- [Upgrade or remove the Debian package](docs/upgrade-uninstall-debian-package.md)

### Concepts and reference

- [Architecture](docs/architecture.md)
- [Operating principles](docs/principles.md)
- [Security boundary](docs/security/current-security-boundary.md)
- [API reference](docs/api-reference.md)
- [Roadmap](docs/roadmap.md)
- [Complete documentation index](docs/README.md)

The implementation entry point is [software/server/README.md](software/server/README.md).
KITPro is licensed under the [Apache License 2.0](LICENSE).

## Release provenance

The alpha.12 release includes SHA-256 checksums, a CycloneDX JSON SBOM, build
metadata, a release manifest, and a frozen source revision. These artifacts let
an owner verify downloaded bytes, inspect packaged components, trace the
release to reviewed source, and distinguish the official frozen files from an
unverified replacement. They do not claim that every build is reproducible or
provide a formal supply-chain guarantee.

## Development transparency

KITPro uses AI-assisted development tools as part of a human-directed
engineering workflow. Project ownership, architecture, security decisions,
review, and release approval remain human responsibilities. The source is
available in this repository, and the project accepts reports through its
public issue tracker. Read [CONTRIBUTING.md](CONTRIBUTING.md) before proposing a
change.
