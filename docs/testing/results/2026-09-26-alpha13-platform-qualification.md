# Alpha.13 platform qualification

Date: 2026-09-26

This record preserves the live platform results used to prepare
`v0.1.0-alpha.13`. It does not authorize publication. Final artifact identity
is recorded separately after the documentation freeze and final rebuild.

## Source under live qualification

- Branch: `develop/0.1.0-alpha.13`
- Runtime-qualified commit before the documentation pass:
  `66bf26bea87f68d9450cf891f5679996fec0f5ce`
- Tree: `b83e1d83b156cff6e64c2160e1e479ad5ba5d7dc`

Artifacts built from this source became superseded when the release
documentation changed. They must not be published as the final release set.

## Focused Syncthing result

`SYNCTHING_FOCUSED_PASS`

The Supported Docker-host run covered installation, offline identity
generation, bounded application-configuration ownership handoff, identity-file
preservation, lifecycle, recreation, reconciliation, backup, and restore. Two
manually paired devices used explicit TCP addresses with discovery, relays,
NAT traversal, QUIC, and UDP disabled. A transferred test file matched its
SHA-256 value. Restore returned the managed configuration, device
identity, peer and folder state, and TCP-only policy while the external
`/sync` tree remained excluded and unchanged.

## Supported platforms

`ALPHA13_DEBIAN_SUPPORTED_PASS`

Debian 13 passed fresh install, alpha.12 to alpha.13 migration, schema 13 to
14 migration, package preflight and paired backup behavior, the 20-application
catalog, representative functional and lifecycle checks, runtime removal,
networking, credential reveal, backup and restore, reboot recovery, AppArmor,
and final security review.

`ALPHA13_UBUNTU_SUPPORTED_PASS`

Ubuntu 26.04 LTS passed the full Supported-host gate with the qualified Debian
package. The run covered systemd, the helper socket, Docker, AppArmor, catalog
and schema integrity, original and alpha.13 applications, lifecycle, external
storage, networking, credential reveal, backup and restore, reboot recovery,
and security review.

`ALPHA13_ARCH_SUPPORTED_PASS`

Arch Linux passed on a fully updated `linux-lts` host with the native package,
pacman pre-transaction upgrade hook, rootful Docker, systemd, and AppArmor.
The run covered original and alpha.13 applications, lifecycle, runtime removal,
networking and conflict handling, credential reveal, backup and restore,
reconciliation repair, reboot recovery, and security review.

Each Supported host used a qualification-only, locally selected Docker address
pool. The CIDR was host configuration, not a KITPro default. The preflight also
preserved negative evidence for missing or insufficient address-pool
configuration.

## Rocky Linux 10 Experimental result

`ALPHA13_ROCKY_EXPERIMENTAL_LIMITATION`

Rocky Linux 10.2 passed native RPM and SELinux-package installation, source
identity, schema and database integrity, rootful Podman 5.8.2, Quadlet, crun,
systemd, SELinux Enforcing, firewalld, trusted read-only and read-write root
registration, and reboot recovery. No recent AVC, orphan runtime, or managed
network remained after the failed application attempt.

The first normal authenticated application install reached the helper and
failed with the staged-generation runtime-contract rejection. The Podman
adapter does not implement `containers.LifecycleRuntime`. The request created
no container, network, or committed runtime generation. A retained generation
zero installation record remained available for the product's normal failed
installation recovery model.

This is isolated to the Experimental Podman path. Docker Supported hosts use
the implemented lifecycle contract. Alpha.13 documents the Rocky limitation
instead of adding a larger Podman staged-lifecycle architecture during the
release freeze.

## Security findings

- Authentication, CSRF, Host, and Origin enforcement remained active.
- Normal catalog, installation, HTML, and operation responses contained no
  application secret values.
- Credential reveal remained limited to the authorized installation and
  credential ID, with `Cache-Control: no-store`.
- Supported hosts retained enforcing AppArmor. Rocky retained SELinux
  Enforcing and firewalld.
- Managed runtimes used no unexpected host networking, devices, capabilities,
  or wildcard publication.
- Generated application secrets remain root-bound plaintext in helper state
  and restrictive backups. This is a documented alpha.13 limitation, not an
  encryption-at-rest claim.
