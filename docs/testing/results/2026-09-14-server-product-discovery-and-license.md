# KITPro Server product discovery and license

## Website source identification

On 2026-09-14, `https://os.kitpro.us/` returned HTTP 200 with a Next.js-rendered
`KITpro OS` page. Its HTML, navigation, dark visual treatment, and named image
assets match the remote `https://github.com/keepittechie/kitpro-os` repository,
inspected in an isolated temporary checkout. That repository is the OS/ISO
source and does not contain the web application's Next.js pages.

The canonical Next.js services-site repository is the private `kitpro-site`
repository (`ssh://git@10.10.0.20:2222/josh/kitpro-site.git`). Its package
scripts and route structure identify the framework and build workflow, and the
new `/server` route is implemented there. The existing deployment script
requires explicit approval; production routing was not changed in this pass.

## Product page and discovery

The `/server` page presents KITPro Server as a local-first product, with the
trusted catalog, persistence, access modes, updates, Paperless-ngx, security,
platform support, and alpha call to action. Metadata includes a descriptive
title, description, canonical URL, and Open Graph fields. Navigation links to
the page from the services site, while the KITPro README links to the product
page and public documentation.

The page follows the live site's dark graphite surfaces, blue accent actions,
rounded cards, muted supporting text, compact navigation, and local typography.
It uses no remote fonts, analytics, or third-party runtime assets.

## Apache License 2.0

The canonical Apache-2.0 text is now in `LICENSE`. Debian and Arch package
metadata identify Apache-2.0, and public README/server documentation link to
the license. A focused third-party review found no incompatible bundled
material. Go dependencies retain their own licenses and are represented in the
SBOM. A separate `NOTICE` file is not required; the decision is documented in
`THIRD_PARTY_NOTICES.md`.

## Validation

- KITPro: Go tests, vet, formatting, package static checks, diff check, and
  sensitive-data scan completed after the documentation/license changes.
- Website: `npm run validate` and `npm run build` completed in the isolated
  site checkout; `/server` rendered with the expected metadata and links.
- Responsive/accessibility review: semantic headings, labelled links, visible
  focus styles, local assets, and responsive card/layout rules were retained.
- Live deployment: not performed. The site repository's deployment workflow
  requires an explicit approved production action; no public claim is made that
  `/server` is live until that action is authorized and verified.

## Remaining limitation

The current `os.kitpro.us` hostname serves the OS/ISO page. Routing the new
Server page to that hostname requires the existing site deployment/routing owner
to approve and execute the established deployment workflow.
