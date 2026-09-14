# Application catalog expansion v1 — 2026-09-13

Status: **PASS**

This immutable follow-up records the expansion of KITPro's trusted catalog from
FreshRSS to four real applications. Tests used the production manifest,
operation, helper, Docker Engine, installation-storage, exposure, and
reconciliation paths. No Docker CLI was used by KITPro; operator-side Docker
inspection and disposable drift resources were used only as test evidence.

## Selection and immutable identities

| Candidate | Decision | Release | `linux/amd64` image digest | Reason |
| --- | --- | --- | --- | --- |
| FreshRSS | Existing baseline | 1.29.1 | `docker.io/freshrss/freshrss@sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21` | Existing accepted single-container app |
| Uptime Kuma | ACCEPT | 2.3.1 | `docker.io/louislam/uptime-kuma@sha256:92fd01c488771d1bcb0b299770255c06994ab7e4f079b7c7fcf52b8e08789a67` | Official single-container image, browser setup, `/app/data`, HTTP 3001 |
| Linkding | REJECT FOR CURRENT MODEL | 1.46.2 evaluated | Not deployed | Initial administrator requires interactive `createsuperuser` or password-bearing environment; KITPro has no accepted secret/bootstrap operation |
| Mealie | ACCEPT | 3.24.0 | `ghcr.io/mealie-recipes/mealie@sha256:3d2384661634e954c12ec27bb5b25a0263832f9e39044f145d726d388e9f8268` | Official single-container SQLite deployment, `/app/data`, HTTP 9000 |
| Memos | ACCEPT substitute | 0.30.0 | `docker.io/neosmemo/memos@sha256:51a4cef418b1f173ac37139ad99de08da5b8662136007231d3ac8a0498a3095a` | Official single-container browser setup, `/var/opt/memos`, HTTP 5230 |

Gitea was considered before Memos. A disposable rootful-image probe could not
traverse KITPro's root-owned `0750` installation root. The catalog did not add
an app-specific ownership change or weaken the privileged storage policy.

Official source URLs, licenses, configuration, and release provenance are in
[Application catalog](../../application-catalog.md). They were retrieved on
2026-09-13.

## Static and trust-boundary validation

- All real manifests passed strict parsing, schema-version, digest, platform,
  storage, environment, service, restart, and resolved-plan assertions.
- Catalog IDs are unique and deterministically ordered.
- Each real plan passed the same helper validator. Searches found no
  application-ID branch in helper, Docker, storage, or reconciliation code.
- Plans contain no secret values, privileged mode, host namespace, Docker
  socket, device, capability, arbitrary host path, or host publication.
- The helper independently derives storage identity and verifies service and
  exposure fields. Labels remain observation evidence, not ownership authority.

## Debian 13.7 acceptance — VM 500 (`10.10.0.115`)

| Case | Result | Measured evidence |
| --- | --- | --- |
| Catalog and install | PASS | Authenticated API listed all entries; Uptime Kuma `inst-8e1847b4a729137b`, Mealie `inst-445578d3befff00f`, Memos `inst-43a4e3290dc18349`, and FreshRSS `inst-c4a544a45dabdaac` installed from pinned digests |
| Internal baseline | PASS | No host bindings; same-network HTTP succeeded; reconciliation returned `exact` for all four |
| Loopback exposure | PASS | Exact bindings were `127.0.0.1:20001`, `:20002`, `:20003`, and `:20004`; all returned HTTP 200; LAN-address requests failed |
| LAN exposure | PASS | Exact bindings were `10.10.0.115:20001` through `:20004`, without IPv4/IPv6 wildcard bindings; all returned HTTP 200 locally and from Ubuntu peer `10.10.0.116` |
| Persistent recreation | PASS | Runtime remove/recreate retained every installation and storage root, advanced generations, returned `exact`, and preserved marker SHA-256 `55e46969df8114375c81abc1ec4f66029a1f542bcff46acf605d92b18f9a5e20` |
| Simultaneous operation | PASS | Four applications plus a second Memos installation ran together with isolated names, networks, roots, and ports; all returned HTTP 200 and `exact` |
| Duplicate installation | PASS | Second Memos `inst-146fc603e2757528` used a separate root/network/port; recreating the first did not interrupt the second |
| Restart | PASS | API, helper, and Docker restarts retained assignments and resources; Mealie completed its slower normal startup without repair |
| Reboot | PASS | All five runtimes, exact bindings, marker checksums, AppArmor, Docker, API, and helper recovered without duplication or manual repair |
| Missing | PASS | External Uptime runtime removal classified `missing`; supported recreation recovered to `exact` |
| Network drift | PASS | A stopped foreign member classified `security_drift`; after the generic fix, start/remove mutations failed closed until the foreign object was removed |
| Ownership conflict | PASS | A foreign same-name replacement for the second Memos runtime classified `ownership_conflict`; it was not adopted and supported recreation recovered |
| Port collision | PASS | A foreign listener held `10.10.0.115:20005`; KITPro left it running and allocated `20006` to a disposable third Memos installation |
| Authentication | PASS | Anonymous install/exposure returned 401; missing CSRF and bad Origin returned 403; authenticated requests succeeded |
| FreshRSS regression | PASS | Install, internal service, loopback/LAN exposure, persistent recreation, exact reconciliation, restart, and reboot remained successful |

The generic defect found during drift testing was that reconciliation detected a
stopped foreign network member while start still proceeded. The Docker adapter
now observes both network endpoints and every stopped container's configured
networks before lifecycle mutation. Regression tests cover the omitted-stopped-
endpoint case.

## Ubuntu 26.04.1 smoke — VM 502 (`10.10.0.116`)

The same alpha3 package and embedded catalog were used. A separate production-
migrated control database and random temporary administrator were created via
the real setup/login flow; the original certified state was not overwritten.

| Case | Result | Measured evidence |
| --- | --- | --- |
| Catalog visibility | PASS | Uptime Kuma, Mealie, and Memos appeared with safe descriptions and pinned releases |
| Install and internal startup | PASS | All three installed through the API and reached usable HTTP state; the first Uptime image transfer was slow, then the exact digest installed normally |
| Controlled exposure | PASS | Exact loopback bindings were `127.0.0.1:20000`, `:20001`, and `:20002`; each returned HTTP 200 and reconciled `exact` |
| Persistence/recreation | PASS | All three removed and recreated with the same installation, storage, and port; generation advanced from 2 to 3; marker SHA-256 remained `d7cbde92b7dc5488c46fbcd756439c81887b781f2322cb39c5004f7bef4499a5` |
| Safe final state | PASS | Exposures were disabled, validation runtimes removed, persistent data retained, temporary credentials deleted, and the original API configuration restored |

The slow first Uptime pull also exposed a generic control-state edge case: a
failed first install was recorded at generation 1 even though the helper had no
runtime ownership, making recreate request generation 2. An explicit helper
rejection now retains the installation/data in `runtime_removed` at generation
0, so a supported recreate begins at generation 1. An ambiguous transport
failure retains generation 1 until reconciliation establishes the outcome,
avoiding control/helper generation divergence. Focused API regression tests
cover both paths.

## Package and cleanup

The catalog ships in `kitpro-server_0.1.0~alpha3_amd64.deb` (7,947,900 bytes),
SHA-256 `656d256cd1fb19a12e94d86df90bde76545facdc1c2b818ff11a17097babc151`.
The CycloneDX SBOM SHA-256 is
`c35f5251ed7f2f45baba761816ef59fccdcecd4115526eb2b164973e18486e26`.
Build metadata records source checkpoint
`bd0abd56ea3eba36f32358022ace9fdd359bca91`, Go 1.27, CGO disabled, and
reproducible build flags.

Two clean alpha3 builds matched the retained package byte-for-byte. The exact
retained artifact and hash above were installed on Debian VM 500 and Ubuntu VM
502 after final review; both reported alpha3 build metadata and active API and
helper socket units.

Debian validation runtimes were removed through KITPro after exposures returned
to internal; persistent markers remain. Ubuntu validation runtimes and temporary
authentication state were removed. Diagnostic containers/listeners and plaintext
temporary credential files were deleted. Docker and both VMs were preserved.

## Decision

`APPLICATION CATALOG EXPANSION V1: PASS`
