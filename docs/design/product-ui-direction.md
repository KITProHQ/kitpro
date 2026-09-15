# KITPro Server product UI direction

The implemented application design system and second-pass audit are documented in [application-ui-system.md](application-ui-system.md).

## Second-pass Product Design review

### Source of truth

The intended brand reference is [os.kitpro.us](https://os.kitpro.us). A direct
inspection on 2026-09-14 returned the live KITpro OS page (Next.js-rendered,
dark graphite surfaces, blue actions, rounded cards, white headings, muted gray
copy, and a compact sticky navigation). The server UI should share that calm,
technical visual language while retaining its operational density and offline,
local-font behavior.

### What is already good

- The server-rendered UI works offline and keeps the security-sensitive flows
  in the existing authenticated backend.
- The dashboard already separates catalog, installed applications, and recent
  activity.
- Access choices use understandable labels and start private by default.
- Paperless-ngx is presented as one installed application rather than a list of
  containers.
- The first-pass layout has responsive cards, semantic headings, and visible
  keyboard focus.

### Findings

- The prior page was visually consistent but too dense: navigation, controls,
  and service details competed for attention.
- Technical status values and lifecycle actions needed clearer hierarchy and
  reassurance about data preservation.
- Catalog cards needed a stronger product-style title/action relationship.
- Access controls needed short explanations of who can connect and a clear
  warning that changing access may recreate the runtime.
- Installation identity and generation are useful for support but should be
  secondary technical details.
- Empty states and activity needed more breathing room and live status semantics.
- Small screens needed wrapped navigation and full-width primary controls.

### Direction accepted

- Use semantic design tokens for background, surfaces, text, borders, actions,
  and status colors.
- Keep a compact, trustworthy operations dashboard rather than a marketing
  hero or cloud-console density.
- Make the primary question visible first: whether the server is ready and
  what the user can do next.
- Treat installed apps as products with Access, Lifecycle, and Technical
  details groupings.
- Use `Private`, `This server only`, and `Local network` consistently, with an
  endpoint only when one actually exists.
- Preserve progressive enhancement and offline operation; no remote fonts,
  icon CDNs, JavaScript frameworks, or analytics.

### Rejected changes

- No wholesale frontend framework or client-side routing: the current server
  rendered model is reliable and keeps the package self-contained.
- No marketing-style dashboard, charts, decorative animation, or dense cloud
  resource tables: they obscure the operational tasks of a local server.
- No new configuration fields or raw environment-variable controls: the
  existing typed manifest and trusted update model remain the authority.
- No invented brand hex values or external font dependency; the live reference
  establishes the direction, while the server keeps its local token system.

### Website source identification

The live site HTML, copy, assets, and Next.js build match the recovered
`kitpro-os-site` source repository. It is the canonical development source for
`os.kitpro.us`; its `/server` route is the
KITPro Server product page. This application pass uses the site's implemented
tokens and visual character as its brand reference without changing or
redeploying the website.

### Validation focus

The second pass checks setup/login copy, dashboard hierarchy, catalog and
installed-app actions, access language, update affordances, error/empty states,
keyboard focus, semantic status text, and narrow-screen wrapping. Technical
IDs, image digests, component topology, and storage paths remain available only
in advanced details or diagnostics.
