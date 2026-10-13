# KITPro Server roadmap

This roadmap separates the published alpha.13 baseline, current alpha.14 work,
and future direction. It does not set delivery dates or turn direction into a
commitment.

## Current state

The published baseline is `v0.1.0-alpha.13`. The current alpha.14 branch adds
secure ingress and recovery features but is not a release candidate until its
live qualification gates pass. KITPro Server coordinates a trusted application
lifecycle on Debian 13, Ubuntu 26.04 LTS, and Arch Linux. The API records
durable operations, and a narrowly privileged helper validates and applies host
and container changes.

KITPro records installation identity, desired state, runtime state, managed
storage identity, and reconciliation evidence separately. A running runtime is
not proof that an application is ready. Read the [current-state reference](product/kitpro-server-current-state.md)
for the full shipped boundary.

## Published foundations

Alpha.13 includes these foundations:

- local administrator setup and a local browser interface;
- a trusted, digest-pinned application catalog;
- single-component and multi-component application installations;
- durable operation identity, request hashing, leases, and fencing;
- runtime generations, staged replacement, and bounded reconciliation;
- explicit repair actions chosen from fresh helper evidence;
- managed persistent storage that survives runtime replacement;
- bounded backup and same-installation restore for managed application storage;
- generated-secret preservation within supported backup and restore;
- exact loopback or LAN service exposure without wildcard publication;
- guarded alpha.12 to alpha.13 package transitions;
- release checksums, an SBOM, build metadata, and frozen source identity;
- native packages for the Supported Debian, Ubuntu, and Arch baselines;
- a 20-application catalog with constrained Forgejo, Plex, Nextcloud, Pi-hole,
  and Syncthing profiles;
- multiple TCP and UDP bindings with trusted fixed-port policy;
- a fail-closed Docker address-pool prerequisite for Supported hosts.

## Alpha.14 development

The current development branch adds these capabilities on top of alpha.13:

- managed nginx ingress with transactional generations;
- provided and ACME HTTP-01 certificates with renewal state;
- optional Cloudflare Tunnel with write-only credentials;
- application readiness checks for catalog releases that define them;
- durable first-run and typed system readiness state;
- host-state backup and fail-closed ingress restore ordering;
- root-local administrator password recovery; and
- redacted support bundles.

Alpha.14 still does not provide imported-storage backup, general host-to-host
restore, destructive application-data deletion, clustering, automatic
failover, router or firewall automation, domain registration, or generic
Docker or Compose administration.

Rocky Linux and Podman remain Experimental. The package and host boundary
passes, but the alpha.13 Podman adapter cannot run the staged-generation
application lifecycle. Ubuntu is part of the Supported alpha.13 baseline.
The [known limitations](release/known-limitations.md) document has the complete
public list.

## Near-term work

Near-term work must qualify the implemented alpha.14 boundary:

- exercise live nginx, TLS, rollback, reboot, and failure recovery;
- qualify AppArmor on Debian and Ubuntu and SELinux on Rocky;
- run fresh-install and published-alpha.13 package upgrades on every Supported
  platform;
- run the Rocky Experimental regression gate; and
- freeze, rebuild, smoke-test, and stage final artifacts without publishing
  them.

## Longer-term direction

Longer-term work may extend backup scope, recovery portability, platform
coverage, and network access. Any such work must preserve installation
identity, user-owned data, inspectable Linux and container foundations, and
explicit handling of uncertainty.

These items are direction, not shipped capability:

- imported-storage backup policies;
- reviewed host-to-host recovery;
- broader hardware and application coverage;
- public TLS and domain integration;
- additional supported operating systems and container runtimes;
- optional remote operations that do not make local use depend on a KITPro
  cloud service; and
- KITPro-owned deterministic IPAM and subnet allocation, if a future design
  can preserve per-generation isolation and host administrator control.

The [historical Phase 1 roadmap](history/phase-1-roadmap.md) remains available
as a record of the plan that led to the current implementation.
