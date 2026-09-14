# KITPro Server product UI direction

## Second-pass Product Design review

### Source of truth

The intended brand reference is [os.kitpro.us](https://os.kitpro.us). A direct
inspection was attempted on 2026-09-14, but the reference site was not
reachable from the validation environment (DNS resolution failed). The server
UI therefore keeps the visual language already established for KITPro: a calm,
light surface, a blue primary action, soft borders, rounded cards, generous
spacing, and local system typography. Exact brand color and font claims are
deliberately deferred until the reference can be inspected reliably.

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
- No invented brand hex values or external font dependency while the live
  reference is unavailable.

### Validation focus

The second pass checks setup/login copy, dashboard hierarchy, catalog and
installed-app actions, access language, update affordances, error/empty states,
keyboard focus, semantic status text, and narrow-screen wrapping. Technical
IDs, image digests, component topology, and storage paths remain available only
in advanced details or diagnostics.
