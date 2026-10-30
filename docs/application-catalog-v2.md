# Application catalog reference

The current trusted catalog is maintained in
[Application catalog](application-catalog.md). This page records the V2
single-container expansion decisions and remains useful as provenance for the
alpha release.

## V2 decisions

Actual Budget and Vaultwarden were accepted as bounded single-container
applications. Forgejo was reviewed during V2 but was not part of that shipped
inventory; alpha.13 later added a constrained web-only, SQLite profile after a
fresh upstream review. Linkding was rejected because its initial
administrator bootstrap requires secret-handling capabilities that KITPro does
not expose. Home Assistant was evaluated as a safe replacement during the V2
review; the current catalog inventory is authoritative for what ships today.

All accepted entries use strict manifests, immutable linux/amd64 image digests,
logical storage IDs, typed services, and internal-only defaults. Host paths,
host ports, namespaces, capabilities, devices, secrets, and Docker arguments
remain outside catalog authority.

Multi-container applications are represented by the separate schema-v2 model;
Paperless-ngx is documented as one logical application with internal broker
components and only its web service eligible for controlled exposure.

See [application manifest reference](application-manifest.md) for field-level
constraints and [application catalog](application-catalog.md) for the full
current inventory and upstream provenance.
