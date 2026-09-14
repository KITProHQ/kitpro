# KITPro software

This area contains KITPro software products. Phase 1 focuses on KITPro Server, a local interface for managing self-hosted applications on a supported Linux host.

KITPro Server is a tentative product name. The repository contains planning documents only. Application implementation must wait until the blocking architecture and security decisions in [`docs/architecture.md`](../docs/architecture.md) are accepted.

## Phase 1 scope

The first complete slice covers:

- installation on one supported Linux host;
- an authenticated local dashboard;
- host detection and compatibility reporting;
- deployment of one supported application;
- runtime health, start, and stop controls;
- safe application updates with defined recovery behavior;
- relevant local logs; and
- application uninstall that preserves persistent user data by default.

Phase 1 does not include KITPro OS, custom hardware, manufacturing, mandatory KITPro accounts, a hosted control plane, a proprietary runtime, a large application catalog, or unnecessary AI features.

## Design boundary

KITPro Server will coordinate standard Linux infrastructure. Managed applications must remain normal OCI containers, standard host services, or both. Users must be able to inspect the changes KITPro makes and continue using their applications and data without KITPro.

See the [product principles](../docs/principles.md) and [Phase 1 roadmap](../docs/roadmap.md) before adding code to this directory.
