# KITPro Server application UI system

## Direction

KITPro Server uses the same visual family as `os.kitpro.us`, adapted for an operational application rather than a marketing page. The interface is dark, calm, compact, and explicit about system state. Blue signals primary action and selected navigation; green, amber, and rose communicate status with accompanying text.

The primary product questions are answered in this order:

1. Is the server healthy?
2. Which applications are installed?
3. Does anything need attention?
4. Are updates available?
5. What can the operator do next?

## Product Design audit

### Strengths retained

- Server-rendered HTML and a small local enhancement script keep the interface reliable and offline-capable.
- Application lifecycle, access, storage, and update state already come from the production operation model.
- The access vocabulary—Private, This server only, and Local network—is understandable without Docker knowledge.
- Multi-container applications remain one logical product in the normal interface.

### Problems corrected

- Browser-default typography, controls, and spacing made the product look unfinished.
- Dashboard, catalog, operations, and lifecycle controls had equal visual weight and weak hierarchy.
- Raw operation language and identifiers dominated normal workflows.
- The internal BusyBox validation workload appeared as a user-installable catalog item.
- Navigation was a long page rather than a durable application shell.
- Login and setup looked unrelated to the main application.
- Lifecycle forms were coupled to operation identifiers instead of stable installation identities.

## Brand translation

The source-of-truth brand is the current KITPro OS site. Values below reproduce its visible character conservatively; no remote font, stylesheet, icon service, or script is required.

| Role | Token | Value |
| --- | --- | --- |
| Canvas | `--kit-bg` | `#070d1b` |
| Elevated canvas | `--kit-bg-elevated` | `#0b1426` |
| Surface | `--kit-surface` | `#101b30` |
| Border | `--kit-border` | `#24324a` |
| Primary text | `--kit-text` | `#f8fafc` |
| Muted text | `--kit-text-muted` | `#9aa9bf` |
| Primary action | `--kit-primary` | `#2563eb` |
| Success | `--kit-success` | `#45d39c` |
| Attention | `--kit-warning` | `#f0b65a` |
| Danger | `--kit-danger` | `#fb7185` |

Typography uses an offline system stack with strong, compact headings and quiet metadata. Surfaces use 6–14 pixel radii, border-led separation, and restrained shadows. Motion is limited to brief state transitions and operation progress, and is removed when `prefers-reduced-motion` is enabled.

## Application shell

- Desktop: persistent left navigation, compact server presence, sticky utility header, and a bounded content column.
- Tablet: compact top navigation with a menu control.
- Phone: stacked navigation, full-width actions, single-column catalog and access options, and no horizontal overflow.
- JavaScript enhances view switching and async operations. Without JavaScript, the server-rendered sections and form submissions remain available.

## Core components

- Health hero: one dominant server state with Docker, helper, and update summaries.
- Summary strip: installed, running, private, and attention counts.
- Application row: product identity, plain-language status, access state, and primary action.
- Catalog card: purpose, category, trusted version, and install state; registry and digest details stay out of the primary view.
- Installation workspace: access, storage, updates, and whole-application lifecycle controls.
- Access selector: keyboard-accessible radio cards for Private, This server only, and Local network.
- Activity row: friendly operation name, explicit text status, safe summary, and time.
- Technical disclosure: installation ID, runtime generation, and component list are collapsed by default.
- Operation banner: truthful indeterminate progress and safe success/failure language; no fake percentages.

## State language

| Internal state | Primary UI label |
| --- | --- |
| `running` / `exact` | Healthy |
| `stopped` | Stopped |
| `runtime_removed` | Runtime removed |
| missing or inconsistent runtime | Needs attention |
| security drift | Security issue detected |

Status is never communicated by color alone.

## Atlas UI reference review

Atlas UI is an MIT-licensed secondary interaction reference, not the KITPro visual source of truth.

### Adopted or adapted

- Compact desktop sidebar and clear selected navigation.
- Border-led operational density instead of decorative cards everywhere.
- Radio-card selection for bounded configuration choices.
- Compact badges, activity rows, and disclosure-based technical details.

### Rejected

- React application architecture and client-side state framework: incompatible with KITPro's server-rendered constraint.
- Remote fonts, icon services, or JavaScript: incompatible with offline operation.
- Command palette and three-pane inspector: unnecessary for the current user workflows.
- Generic cloud-console density: conflicts with KITPro's approachable, local-first character.

## Accessibility baseline

- Semantic landmarks and heading order.
- Associated form labels and native radio inputs.
- Visible focus rings and minimum 40 pixel controls.
- Text labels accompany all status colors.
- An `aria-live` operation region reports progress and completion.
- Reduced-motion support and responsive layouts down to 320 pixels.

## Deferred ideas

- User-selectable themes: rejected for this milestone because the brand has one established dark visual direction.
- Per-component lifecycle controls: rejected because multi-container products must remain one logical application.
- Arbitrary icon or logo downloads: rejected because offline availability and licensing are not assured.
- A large frontend framework: rejected because current workflows do not justify its runtime and maintenance cost.
