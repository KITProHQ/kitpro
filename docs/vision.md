# KITPro vision

## The problem

Self-hosting gives people more control over their applications and data, but the practical entry cost remains high. A new operator must often learn Linux administration, containers, networking, certificates, reverse proxies, storage, backups, and application-specific recovery procedures at the same time.

That work is reasonable for an experienced administrator. It is a poor prerequisite for someone who wants a dependable service at home or in a small organization.

## The intended outcome

KITPro aims to make personal infrastructure understandable and manageable without hiding the standards underneath it. A user should be able to complete common operations through a clear interface, then inspect the generated configuration and use ordinary Linux tools when deeper control is useful.

The product succeeds when it reduces the knowledge needed to begin without limiting what the user can do later.

## The first user

The initial user has a Linux server or mini PC and wants to run self-hosted applications. The user may understand basic computing concepts but should not need prior experience with container engines, reverse proxies, certificate management, storage layout, or backup tooling.

The alpha focuses on one supported Linux host and a deliberately curated
catalog. Supporting many environments or applications before the lifecycle is
safe and understandable would hide design problems behind catalog size.

## Phase 1 product

KITPro Server provides a local web interface for installing and operating
self-hosted applications. The complete alpha slice covers installation, host
discovery, trusted single- and multi-container applications, health, start and
stop controls, safe updates, controlled access, relevant logs, and removal that
preserves persistent user data by default.

The product must make consequential operations clear. Before an update or removal, the user should understand what will change, what data will remain, and what recovery path exists.

## Long-term product areas

KITPro may grow through four related areas:

- `software/` contains KITPro Server and later local software.
- `os/` reserves space for possible operating-system or platform work.
- `hardware/` reserves space for possible hardware products.
- `cloud/` reserves space for optional services such as remote access, monitoring, and off-site backup support.

These areas share a mission, not a required dependency chain. The local product must remain useful if KITPro never ships the other three areas.

## What KITPro is not

KITPro is not a new container ecosystem, a hosted control plane that happens to manage local machines, or an attempt to conceal the host from its owner. It should use established Linux facilities and open standards, while reducing the manual coordination those tools demand.

Phase 1 excludes custom hardware, manufacturing, KITPro OS, mandatory accounts, hosted SaaS control planes, proprietary container runtimes, vendor lock-in, large application catalogs, and unnecessary AI features.

## Measures of success

Before wider scope is added, the Phase 1 slice should demonstrate that:

- a target user can install KITPro Server on a documented host;
- the dashboard describes the host and application state in plain language;
- lifecycle operations produce predictable, inspectable changes;
- an interrupted or failed update does not silently leave the application in an unknown state;
- uninstalling the application preserves persistent user data unless the user makes a separate, explicit deletion choice;
- core operations continue without a KITPro account or KITPro cloud connection; and
- an experienced operator can inspect, export, and operate the underlying standard workload outside KITPro.
