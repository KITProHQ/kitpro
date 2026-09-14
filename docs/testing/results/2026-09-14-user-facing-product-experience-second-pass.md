# User-facing product experience — second pass

Date: 2026-09-14
Scope: KITPro Server public-alpha interface polish, offline assets, and
documentation. No backend trust or exposure contracts changed.

## Product Design review

The live brand reference is [os.kitpro.us](https://os.kitpro.us). The site was
attempted directly during this review, but DNS resolution was unavailable in
the validation environment. We therefore preserved the observable KITPro
direction already present in the product: calm light surfaces, blue actions,
soft borders, rounded cards, system typography, and a restrained operational
tone. The review and accepted/rejected decisions are recorded in
`docs/design/product-ui-direction.md`.

## Changes and measured review

- Setup and sign-in now use complete, labeled, responsive forms with clear
  local-data and password guidance.
- Dashboard hierarchy leads with server readiness, next action, catalog, and
  installed applications. Navigation wraps on narrow screens and marks the
  current page.
- Catalog cards use a product-style title, trusted version badge, purpose copy,
  and a clear named install action.
- Installed applications group status, lifecycle, Access, and technical
  details. Runtime identity and generation are secondary details; Paperless-ngx
  remains one logical application.
- Access choices are `Private`, `This server only`, and `Local network`, with
  truthful endpoint links and a stored-data/restart explanation.
- Empty states are calm and actionable. Recent activity has an `aria-live`
  region. Focus-visible controls, semantic headings, labels, reduced-motion
  handling, and full-width small-screen controls were reviewed.

Responsive and keyboard/text review covered desktop, tablet, and phone-sized
layouts; primary controls wrap without horizontal overflow and status is not
communicated by color alone. No remote fonts, scripts, icons, analytics, or
frameworks were added.

## Platform smoke

The existing Debian full UX acceptance and Ubuntu/Arch smoke evidence remains
valid because the changes are embedded presentation assets. Template parsing,
Go tests, vet, package checks, and security regression tests pass. The final
VM state remains internal-only; no public GitHub publication was attempted.

## Rebuilt candidate artifacts

Both package families were built twice from clean output directories with the
same source epoch and matched byte-for-byte:

- Debian/Ubuntu: `kitpro-server_0.1.0~alpha1_amd64.deb` —
  `23e078c81e43e55d7779caa19ef785edb786f52deb57f5b6efc02cc82895be71`
- Arch: `kitpro-server-0.1.0_alpha1-1-x86_64.pkg.tar.zst` —
  `793a9ec514196f6ccf93fb055eed4b337e612c2ba3cf0f5f5612b160d806f4ed`
- SBOM: `kitpro-server_0.1.0~alpha1_amd64.cdx.json` —
  `0f0108551c69d6d173da4169b44270a28c6ec252b933f2fc1e3b51eb1cd3edb4`

Source commit embedded in build metadata: `57ca023d726a4d89a491f9632f8ab3deaabc519b`.

## Remaining limitations

- The live brand reference needs a follow-up visual comparison when DNS access
  is available; no unsupported exact palette or font claims are made here.
- Operation progress remains backend-phase based; the UI does not invent
  percentage completion.
- Public release publication remains a separate, explicitly authorized step.
