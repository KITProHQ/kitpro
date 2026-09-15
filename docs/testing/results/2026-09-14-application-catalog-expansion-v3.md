# Application Catalog Expansion V3

- Date: 2026-09-14 (America/Los_Angeles)
- Prepared package: `0.1.0~alpha7`
- Gate: `APPLICATION CATALOG EXPANSION V3: PASS`

No public release or tag was created. Candidate incompatibility did not block
the work.

## Candidate decisions

| Candidate | Current stable | Model | Security, persistence, bootstrap, and network fit | Decision |
| --- | --- | --- | --- | --- |
| Open WebUI | 0.11.3 (0.11.2 update source) | Single | Official GHCR amd64 image; HTTP 8080; `/app/backend/data`; browser-first administrator; fixed generated secret; normal bridge egress; no device or host network | ACCEPT WITH GENERIC CAPABILITY |
| n8n | 2.38.7 | Single SQLite | Official `docker.n8n.io/n8nio/n8n`; HTTP 5678; `/home/node/.n8n`; image user cannot initialize root-owned KITPro storage; persisted encryption state must remain private | REJECT FOR CURRENT MODEL |
| Flowise | 3.1.4 | Single SQLite | Official `flowiseai/flowise`; HTTP 3000; persistent state and credential key; image user cannot initialize root-owned KITPro storage; current authentication/bootstrap contract needs further product work | REJECT FOR CURRENT MODEL |
| Stirling PDF | 2.14.3 | Single | Official image; HTTP 8080; persistent configs/logs/custom files; current login initialization falls back to known `admin`/`stirling` credentials if explicit credentials are absent | REJECT FOR CURRENT MODEL |
| IT-Tools | 2024.10.22-7ca5933 | Single, stateless | Official CorentinTh image; HTTP 80; no storage, secret, capability, device, host-network, or dependency need | ACCEPT |
| Securo | 0.15.1 | Multi | Official backend/frontend images plus PostgreSQL, Redis, migrations, Celery worker and beat; only frontend exposable; needs readiness-gated bootstrap and safely assembled shared connection secrets | REJECT FOR CURRENT MODEL |

All six upstreams publish linux/amd64 artifacts. Resolved candidate platform
digests were:

- Open WebUI: `sha256:9cd136effce6bb12a6a1988a35ab3b82cb40c48a6768fceeb17c83baf7cfac9c`
- Open WebUI 0.11.2 update source: `sha256:4fdd422b9464921d4bef176046c47326b778219408aaee2f4bd8a21c10789f87`
- n8n: `sha256:ed31254f79d45ad1f565f3e9cca17c1b3a1710498c608ea856c2f69a2b443dcc`
- Flowise: `sha256:e4ac7d4f6a09a3be888e0a630ba666075dcbc0135d48195788f9b52f49f1eb3d`
- Stirling PDF: `sha256:272c154bf8d2b18934eb05b7c63d73f8f401d7bf03ccef89b29b90bf18ca0bf1`
- IT-Tools: `sha256:6f177c156b9466610e0f2093e24668b78da501c66f0054f98bccb582b74ab26b`
- Securo backend: `sha256:9a4a943c62aa198f0e0468d7b7a5a2c426f92150fdb45c4750bde086f1865957`
- Securo frontend: `sha256:4572ebd280f4e50c8eb361afb475649584c4b6028c425098d57cc6c7e96780ff`

Licenses reviewed from upstream repositories: Open WebUI license, n8n
Sustainable Use License, Flowise Apache-2.0, Stirling PDF MIT, IT-Tools
GPL-3.0, and Securo AGPL-3.0. Optional bank synchronization and AI-agent
credentials were not supplied or enabled. No financial data was created.

## Generic capability and security review

The manifest now supports only one generated-secret form:
`secret=true`, `required=true`, `generate=random-hex-32`. The root helper uses
the operating system CSPRNG, inserts once by installation/component/name,
reselects the stable value, and injects it only into the container environment.
The API and public catalog never receive the value. The helper database remains
root-only and its existing pre-upgrade backup path preserves the secret.

Docker image pulls now use a separate 30-minute transfer timeout and consume
the full progress response. Ordinary Docker operations retain the prior
two-minute client timeout. This was required because the official Open WebUI
image exceeded two minutes on the reference host. No capability, device,
privileged mode, host path, host network, wildcard exposure, or raw environment
configuration was added.

## Debian 13 primary acceptance

VM 500 (`10.10.0.115`, amd64) installed the alpha7 package and migrated helper
schema 4 to 5 with integrity-checked backups. Open WebUI and IT-Tools were both
installed through authenticated, CSRF-protected API requests using the exact
catalog digests. Each was installed twice. All four installations had distinct
installation identities, storage/network ownership, and runtime names.

Open WebUI reached Docker health `healthy` on both instances and returned HTTP
200. IT-Tools returned HTTP 200. Open WebUI remained usable without a configured
LLM backend. A synthetic `.invalid` administrator account survived KITPro
runtime recreation and a package-host reboot; the installation identity stayed
stable and runtime generation advanced. IT-Tools has no persistent state by
design.

Both apps passed Private, exact `127.0.0.1`, and exact `10.10.0.115` LAN modes.
An authorized Ubuntu peer received HTTP 200 from both LAN endpoints. Docker
showed no wildcard IPv4 or IPv6 binding. The final state is Private: four
containers, no published ports, and reconciliation `exact` for every instance.

Four Open WebUI secret rows were observed during testing, all distinct and all
64 characters. Live and latest pre-upgrade backup queries returned the same
shape; values were not printed. Docker, API, and helper restarts plus a full
reboot restored both Open WebUI containers healthy and both IT-Tools containers
running without publication.

A real trusted-release lifecycle moved the synthetic-data installation from
0.11.3 to 0.11.2 at runtime generation 5, then back to 0.11.3 at generation 6.
The installation ID, persistent database, synthetic account, generated secret,
and Private exposure survived. The final container reached healthy, returned
HTTP 200, and used the 0.11.3 platform digest. New installs generically select
the last, newest trusted release rather than an application-specific version.

Observed failure paths included the original bounded pull timeout, an attempted
untrusted registry identity, stale helper configuration after a LAN-address
change, and the n8n/Flowise bind-storage ownership failures. Each failed closed.
The helper retained an empty capability set.

FreshRSS and Paperless-ngx remained running during the new-app matrix, proving
single- and multi-container coexistence. Their persisted controlled exposures
were unchanged. Full simultaneous installation of every catalog entry was not
attempted because duplicate Open WebUI images and existing document services
made a grouped run the responsible fit for the 40 GiB reference VM.

## Network and AI behavior

Application bridges retain Docker's current ordinary outbound access. Open
WebUI can therefore reach configured remote AI APIs, and its first boot was
observed retrieving its upstream embedding model. KITPro adds no egress policy,
API credential, host-gateway alias, or host-network access. A model service
bound only to host loopback is not reachable from the application bridge.
Remote or LAN-reachable OpenAI-compatible endpoints can be configured inside
Open WebUI after installation.

Ollama was not added. Although upstream documents a CPU-only bundled variant,
bundling it would combine UI and model lifecycle and bypass the deliberate
future device/hardware architecture. The independent Open WebUI deployment is
useful without weakening that boundary.

## Secondary platforms

Ubuntu VM 502 (`10.10.0.116`, Ubuntu 26.04.1 LTS amd64) upgraded from alpha2
through `dpkg`. Both catalog entries were visible. Open WebUI and IT-Tools
installed and recreated through authenticated requests, reconciled `exact`,
used their immutable digests, returned HTTP 200, and finished Private with no
host bindings. Two generated secrets were distinct and 64 characters.

Arch VM 503 (`10.10.0.119`, x86_64 under the documented linux-lts/AppArmor
boundary) installed the native package through `pacman -U`. Both apps installed
and recreated through the authenticated path, reconciled `exact`, used their
immutable digests, and finished Private. IT-Tools ran and Open WebUI reached
healthy with HTTP 200. Two isolated Open WebUI installations also demonstrated
duplicate identity, storage, network, runtime, and generated-secret separation.

Interrupting the first Arch client during a long pull left an accepted helper
receipt without ownership, container, or secret. The helper rejected an unsafe
recreation rather than adopting incomplete state. A fresh normal installation
then completed and reconciled exactly. This is retained as representative
client-interruption and trusted-state failure evidence.

## Prepared reproducible artifacts

Two independent builds of each native package were byte-identical. The
prepared, unpublished artifacts are:

- Debian/Ubuntu `kitpro-server_0.1.0~alpha7_amd64.deb`:
  `4d90cd8ba6ceb6f45cc8307a04ac5a53d2f5274e25289c1fd4d451451ffc1a3c`
- Arch `kitpro-server-0.1.0_alpha7-1-x86_64.pkg.tar.zst`:
  `9084caf402dd188d7259d4936e7a0b815ab25221040887d1b20d9741a2986687`
- CycloneDX SBOM `kitpro-server_0.1.0~alpha7_amd64.cdx.json`:
  `c463c5c3fae61f08156bd3868ea1b41bb1ed646eef01904c997daef03582d99d`

The prepared packages were installed by their native package managers; no
public release was created.

APPLICATION CATALOG EXPANSION V3: PASS
