# KITPro Server application visual-design pass

Date: 2026-09-14
Final package source: `f998fc1c0ab110c2324324fde85d981affa8cefe`

## Product Design audit

The rendered production UI was reviewed before implementation on Debian. The
baseline was functional but used browser-default forms, a single long page,
weak hierarchy, raw operation language, and visually unrelated setup and login
screens. The design retained server-rendered workflows, native form controls,
the private-by-default access model, and one logical presentation for
multi-container applications.

The KITPro OS site was the primary brand reference. Its navy canvas, elevated
blue-black surfaces, blue action color, restrained borders, compact type, and
quiet spacing were translated into an operational interface. Atlas UI was a
secondary interaction reference: sidebar navigation, status badges, option
cards, activity rows, and collapsed technical details were adapted. Its React
architecture, command palette, remote assets, and dense inspector patterns were
rejected.

## Implemented design

- A persistent desktop shell and responsive navigation separate Dashboard,
  Applications, Catalog, Updates, and Settings.
- The dashboard leads with one server-health statement, then compact metrics
  and product-oriented application rows.
- Catalog cards show purpose, category, trusted release, and install state. The
  internal BusyBox validation workload is no longer a public catalog choice.
- Installed applications group access, persistent storage, updates, lifecycle,
  and collapsed technical details. Paperless-ngx remains one logical app.
- Access uses native radio cards for Private, This server only, and Local
  network, with precise data-preservation language.
- Setup, login, password change, progress, failure, activity, and empty states
  now share the same local design system and human-facing language.
- Concurrent lifecycle controls on one application are disabled while an
  operation is active, preventing accidental rapid-action races.

The source-backed catalog currently presents Actual Budget, FreshRSS, Home
Assistant, Mealie, Memos, Paperless-ngx, Uptime Kuma, and Vaultwarden. Forgejo
is not presented because it remains outside the accepted bootstrap-secret
model; the UI does not claim an unavailable app.

## Screenshot comparison

Before the pass, captured login and dashboard views showed unstyled browser
controls and an undifferentiated document. The refreshed deterministic set in
the screenshot automation project contains:

- `kitpro-dashboard.png` — branded health-first dashboard with three healthy apps.
- `kitpro-catalog.png` — curated trusted catalog.
- `kitpro-application.png` — Memos as one managed product.
- `kitpro-access-controls.png` — focused three-mode access selector.
- `kitpro-paperless.png` — Paperless-ngx as one logical application.
- `kitpro-updates.png` — plain-language update and activity view.
- `kitpro-dashboard-mobile.png` — 390 by 844 responsive dashboard.

All final captures use real Debian state. Technical disclosures are collapsed;
no credentials, session data, installation IDs, image digests, internal
hostnames, or private endpoints are visible. The first over-tall application
capture was rejected and recaptured with focused viewport framing.

## Live user journey

### Debian 13

PASS. A disposable administrator was created through setup and used through the
real authenticated UI. FreshRSS installed private, changed to an exact Local
network endpoint, opened successfully, and updated from one trusted catalog
release to the next with installation identity, persistent data, and assigned
port preserved. Memos exercised stop, start, and recreate; Paperless-ngx
installed with web and background components while remaining one UI product.
After host reboot, Docker, KITPro services, AppArmor, all three applications,
their storage, and access state recovered without duplicates or repair.

The live trusted-update run exposed an operation-name mismatch at the helper
boundary. It was fixed generically: the helper now accepts only a catalog-
validated `UpdateApplication` transition for a pinned release change, while an
ordinary recreation still cannot change release or image identity.

### Ubuntu 26.04 LTS

PASS. The current Debian package upgraded through native package hooks, created
pre-upgrade backups, loaded the branded login, completed disposable setup and
authenticated navigation, and reported the correct Ubuntu, amd64, and apt/dpkg
context. Docker, the helper socket, API, and enforcing AppArmor remained active.

### Arch Linux

PASS. The native package upgraded through pacman hooks, created pre-upgrade
backups, loaded the branded login, completed disposable setup and authenticated
navigation, and reported the correct Arch, x86_64, and pacman context. Docker,
the helper socket, API, `linux-lts`, and enforcing AppArmor remained active.

Arch validation found that an unquoted tilde replacement embedded a builder
home path in binary version output. The PKGBUILD now preserves a literal tilde,
and a static regression check protects it. The corrected binary reports
`0.1.0~alpha1` without build-host path leakage.

## Responsive and accessibility review

- Desktop was reviewed at 1440 by 900; laptop and tablet compositions retain
  hierarchy; the 390 by 844 capture has no horizontal overflow.
- Landmarks, sequential headings, associated native inputs, descriptive
  actions, visible focus rings, text-plus-color statuses, skip navigation, and
  an `aria-live` operation region were verified.
- Controls meet a 40-pixel baseline and stack at narrow widths. Reduced-motion
  preferences disable smooth scrolling and transitions.
- Clean browser runs produced no console or runtime errors.

## Final Product Design decision

1. Recognizably KITPro: **yes** — the implemented OS-site palette and visual
   character carry through without copying its marketing layout.
2. Substantially more polished: **yes** — the before/after change is material
   across shell, hierarchy, forms, states, and application management.
3. Atlas improved interaction: **yes** — it informed bounded operational
   patterns rather than decoration or framework adoption.
4. Understandable without Docker knowledge: **yes** — normal workflows use app,
   access, storage, and update language.
5. Advanced details remain available but secondary: **yes** — identity,
   generation, and components live in a collapsed disclosure.
6. Suitable as a permanent home-lab interface: **yes** — state, action, and data
   consequences are visible, calm, and consistent.

## Artifacts and reproducibility

Two clean builds of each package matched byte-for-byte. Both build records mark
the source tree clean and identify the final package source above.

| Artifact | Size | SHA-256 |
| --- | ---: | --- |
| `kitpro-server_0.1.0~alpha1_amd64.deb` | 7,994,532 bytes | `175fde7b207d94decf4c3f3eb3cef23da7f0af4c4ee19499287211412108532d` |
| `kitpro-server-0.1.0_alpha1-1-x86_64.pkg.tar.zst` | 5,772,075 bytes | `afe76ba569a1a3d6908a0e9e295ec86cbcf52f3574d62c7af8d6db6b10fd2657` |
| CycloneDX SBOM | — | `fb42c1444ed1bb89c6c061149723960b5c3a08a76d0939154d1b4606bb12c873` |

The SBOM is deterministic, identifies `0.1.0~alpha1` and the current package
source, and contains no stale private-tag source identifier.

## Security and performance

Authentication, server-side sessions, CSRF, Origin, Host, helper validation,
trusted catalog, exact exposure, and AppArmor regression tests pass. Assets are
embedded and served only after Host validation with CSP, nosniff, and referrer
headers. The API remains unprivileged, and no remote font, stylesheet, script,
icon service, analytics, or telemetry was introduced.

The application adds 23,627 bytes of uncompressed CSS and 4,664 bytes of local
JavaScript. It loads no product images or third-party resources, uses no client
framework, and shows no measured layout overflow or blocking external request.

## Remaining visual limitations

- App identities use consistent initials because a redistributable icon set has
  not been approved.
- Top-level views use hash navigation rather than individually addressable
  routes.
- Settings reports AppArmor as a requirement; exact kernel enforcement detail
  remains in operator diagnostics.
- Failure guidance is human-readable, but expanded raw diagnostic detail is not
  yet exposed in the normal UI.
