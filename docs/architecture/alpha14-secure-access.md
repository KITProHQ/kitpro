# Alpha.14 secure access architecture

- Status: Updated for the alpha.14 scope correction
- Release: `0.1.0-alpha.14`
- Source baseline: `v0.1.0-alpha.13` plus `b693722ef93bdebb90fcbb440fdddf9d30fba648`
- Frozen: 2026-09-26

This document is the implementation contract for alpha.14. It extends the
existing catalog, service-binding, generation, helper-operation, backup, and
authentication models. It does not create a second application lifecycle.

Cloudflare Tunnel was evaluated during development but is outside the
alpha.14 product boundary. Tunnel and DNS-01 providers remain possible future
work. The generic nginx Host-isolation and ACME challenge-routing fixes found
during that evaluation remain part of this architecture.

## User-facing operations

The browser and CLI work with KITPro concepts:

```text
create route(installation, service, hostname, exposure, tls policy)
enable route(route id)
disable route(route id)
inspect readiness(host, installation, route, certificate)
resume setup(step input)
generate support bundle()
reset administrator password locally(new password)
```

Callers never submit proxy directives, filesystem paths, commands, systemd
unit names, or ACME commands.

## Existing boundaries retained

- The unprivileged API owns authenticated workflow, desired state, and UI.
- The root helper owns privileged files, runtime mutation, protected evidence,
  and narrowly allowlisted host-service operations.
- Strict framed JSON remains the helper protocol. New requests are typed and
  versioned. Unknown fields and operations fail before mutation.
- Application runtime generations remain authoritative for backend identity.
- Service bindings remain distinct from ingress. A listening HTTP port does
  not create a route.
- Docker remains supported on Debian, Ubuntu, and Arch. Rocky remains an
  experimental Podman and Quadlet adapter.
- Existing alpha.13 installations migrate with ingress and TLS disabled.

## Domain model

### Manifest declarations

Manifest schema 9 adds optional readiness and ingress eligibility.

```text
ReadinessCheck =
  runtime
  docker-health
  tcp(service id, timeout)
  http(service id, path, expected status set, timeout)

IngressEligibility =
  service id
  allowed exposure modes
  allowed upstream protocols
  external access allowed
```

Only HTTP and HTTPS services may be ingress eligible. Paths are fixed catalog
data. They cannot contain a host path, command, template, or shell fragment.
Applications without a readiness declaration continue to work and report
`runtime-only` readiness.

### Ingress route

```text
IngressRoute
  route id
  installation id
  service id
  normalized hostname
  exposure mode: lan | managed-https
  upstream protocol: http | https
  tls policy: none | provided | acme
  certificate id, when applicable
  desired state: disabled | enabled
  applied ingress generation
  created and updated times
```

The database forbids duplicate normalized hostnames. A route points to one
manifest-approved service binding in one installation. The route never stores
an arbitrary upstream address. The helper derives the upstream from protected
runtime-generation binding state and rejects non-loopback backends.

### Ingress generation

```text
IngressGeneration
  monotonically increasing generation
  configuration hash
  status: prepared | validated | activating | active | failed |
          rollback-pending | rolled-back | cleanup-pending
  previous active generation
  operation id and timestamps
  non-secret verification evidence
```

One generation is active. Candidate generation directories are immutable
after validation. Database state never claims a generation is active before
the proxy reload and route verification finish.

### Certificate

```text
Certificate
  certificate id
  hostname
  method: provided | acme
  state: requested | issued | active | expiring | renewal-pending |
         renewal-failed | invalid
  leaf fingerprint
  issuer, not-before, not-after
  last renewal attempt and sanitized error code
```

The control database stores metadata only. The helper stores PEM material by
certificate ID under its protected ingress state. Ordinary APIs never return
PEM bytes or filesystem paths.

### Readiness

Readiness is a set of independent observations, not one boolean:

```text
DesiredState
RuntimeReadiness
ApplicationReadiness
IngressReadiness
TLSReadiness
```

Each observation includes `state`, `checked_at`, a stable reason code, and a
short redacted summary. Unknown and not-configured are distinct from failed.
The API derives the overall presentation but preserves every component.

### Setup state

Setup is a durable state machine:

```text
host-readiness -> administrator -> storage -> network -> access ->
optional-tls -> optional-first-application -> complete
```

Each step records `pending`, `in-progress`, `complete`, `attention`, or
`skipped`, plus non-secret evidence. Replaying a completed step with the same
input is a no-op. Conflicting input requires an explicit replacement action.
After `complete`, unauthenticated setup requests return not found. Existing
alpha.13 hosts migrate to `complete` when an administrator already exists.

## Proxy selection

KITPro uses a dedicated nginx instance named `kitpro-ingress`. It does not
write the host's primary nginx configuration or expose an nginx admin UI.

| Candidate | Strengths | Rejected cost |
| --- | --- | --- |
| Caddy | Strong TLS defaults, native ACME, atomic API reload | Its normal unprivileged service owns writable certificate storage, which conflicts with root-owned private keys. The admin API also adds a second mutation interface to confine. |
| nginx | Deterministic generated config, offline `-t`, graceful reload, old workers survive a bad reload, packaged broadly | KITPro must own ACME and certificate status instead of delegating it. |
| Traefik | Dynamic routing and certificate automation | Its provider model and dashboard are broader than KITPro needs, and constraining dynamic configuration adds more authority than a generated file. |

The dedicated instance uses fixed paths and a fixed binary invocation. The
helper cannot select a different binary, prefix, config root, PID path, log
path, user, or unit. The master reads root-owned keys and binds 80/443; request
workers run as the dedicated `kitpro-ingress` user. Systemd and mandatory
access control restrict both processes.

## Hostname and request routing rules

- Hostnames are lowercased IDNA ASCII, without a trailing dot.
- Wildcards, IP literals, empty labels, underscores, control characters,
  slashes, whitespace, ports, and labels outside DNS length limits fail.
- Route identity uses the normalized hostname. Unicode lookalikes never become
  a second comparison form.
- nginx receives exact `server_name` values. There is no regex server name,
  catch-all proxy, variable upstream, user path matcher, or user header value.
- Unknown Host values terminate without proxying.
- KITPro replaces forwarding headers with fixed values. It does not append
  untrusted client forwarding headers.
- Every backend is `127.0.0.1:<assigned port>` and must match the active runtime
  generation's protected binding record.

LAN routes may use HTTP or a provided certificate. Managed HTTPS uses ACME or
a provided certificate and redirects port 80 to 443. Public DNS and inbound
TCP/80 are required for HTTP-01. KITPro reports failures without changing
router or firewall configuration.

## Transactional ingress activation

The API starts one durable helper operation. The helper serializes all ingress
changes with a global fenced lease because hostnames, ports, and one proxy
configuration are shared state.

```text
resolve desired routes and active runtime bindings
-> render complete candidate generation
-> write candidate files atomically
-> validate nginx syntax and referenced certificates offline
-> record validated generation
-> atomically change the current-generation symlink
-> invoke the one allowlisted graceful reload action
-> verify proxy process and every changed route
-> mark active
-> retain the previous known-good generation
```

If activation or verification fails, the helper restores the prior symlink,
reloads the prior config, verifies it, and records a typed failure. If rollback
cannot be proved, the operation becomes `action_required`; cleanup stops and
the prior files remain. Candidate validation never changes the live config.

Removal disables the route before runtime removal. Runtime recreation updates
the protected backend binding, produces a new ingress generation, and does not
change the hostname or exposure policy.

## Certificate lifecycle

The helper owns a narrow ACME client. It accepts a certificate ID, one already
validated hostname, an allowlisted ACME directory choice, and contact email.
It does not accept a command, challenge type, filesystem path, or arbitrary
URL. Alpha.14 supports HTTP-01 for public DNS names and does not implement
wildcards.

The helper creates the private key with mode `0600` in a root-owned directory,
completes the challenge through a fixed nginx challenge location, validates
the returned chain and hostname, writes replacement files atomically, stages
an ingress generation, reloads, verifies, and only then marks the certificate
active. The API schedules checks before expiry through a typed helper request.
Renewal failure leaves the active certificate and route in place and moves the
certificate to `renewal-failed` or `expiring` as appropriate.

Provided certificates enter through the local privileged CLI. The CLI reads
operator-selected files before entering the helper boundary, validates that
the key matches the leaf and the hostname matches the certificate, and sends a
bounded typed import request. The browser API cannot read or export keys.

## Administrator recovery

`sudo kitpro-server admin reset-password` is a local command. It requires real
UID 0, reads the password from a terminal twice without echo, opens the control
database through the existing state layer, writes the normal bcrypt hash in a
transaction, revokes every administrator session, and appends a non-secret
audit event. Arguments and environment variables cannot carry the password.
There is no web recovery endpoint.

## Diagnostics

The API creates a diagnostics request. The helper gathers an allowlisted data
model and writes a mode-0600 archive. It never archives a directory wholesale.
Collectors return typed values for build identity, OS, runtime, address pool,
disk, installations, bindings, readiness, routes, certificate metadata,
operation outcomes, mandatory-access-control status, and a
bounded log window.

Redaction happens before serialization. It removes authorization and cookie
headers, known secret values, token-like environment fields, PEM private-key
blocks, session hashes, and application secret values.
The archive is scanned again for registered synthetic test secrets and private
key markers before it is committed. A match deletes the candidate and fails
the operation.

## Backup, restore, and removal

Application backup format v1 remains application scoped. Alpha.14 adds a
separate host-access state payload to the paired KITPro control/helper backup:

- route desired state and hostname;
- ingress generation metadata and generated configuration;
- certificate metadata and root-owned certificate/key files;
- setup state and readiness history needed for recovery.

Application restore keeps its current storage transaction, then verifies the
application before enabling a restored route:

```text
restore core installation state -> restore storage -> restore runtime ->
verify runtime and application -> restore route desired state ->
validate certificate -> activate ingress -> verify route
```

Failure before route verification leaves the route disabled. Runtime removal
disables ingress and preserves local certificate material. Uninstall-preserve
preserves certificate state.

## Privileged helper authority

New helper operations are limited to:

- prepare, validate, activate, verify, roll back, and inspect one complete
  ingress generation;
- import, issue, renew, inspect, and revoke one modeled certificate;
- gather the fixed diagnostics schema.

The helper gains no arbitrary write, command, path, URL, proxy directive,
systemd unit, or service name. nginx subprocess arguments are constant except
for helper-created paths beneath fixed roots. Every mutation
uses the existing operation coordinator, canonical request hash, durable
event log, and recovery disposition.

## Failure and reboot behavior

- The last active ingress generation and certificate remain on disk.
- systemd starts the dedicated proxy after local filesystems. It starts the API
  independently of ACME availability.
- On startup, the helper verifies the active generation hash, proxy process,
  route backends, and certificate metadata.
- Interrupted candidate work resumes from durable phase evidence. Unknown
  activation outcome requires reconciliation before another mutation.
- An unavailable application produces `application=not-ready` and
  `ingress=backend-unavailable`; it does not retarget the hostname.

## Alternatives rejected

One shared lifecycle for application and ingress generations was rejected.
Application generations are per installation, while one proxy configuration
contains routes for many installations. Coupling them would require unrelated
applications to share a lifecycle lease.

Deriving routes from open HTTP ports was rejected. It would expose existing
alpha.13 applications during migration and would erase administrator intent.

Letting nginx configuration be user editable was rejected.
KITPro could no longer validate routing isolation, restore known-good state, or
describe effective authority.

## Release-blocking proofs

Alpha.14 cannot become a local release candidate until tests prove:

- alpha.13 migration creates no route or certificate state;
- hostname collision and cross-installation backend substitution fail;
- a bad candidate and failed reload preserve the known-good route set;
- invalid and mismatched certificates never activate;
- key, password, cookie, authorization, and application secrets do not
  appear in API responses, logs, operation details, or support bundles;
- password reset rejects the old password and every prior session;
- restore never exposes an unverified application;
- Debian, Ubuntu, and Arch pass supported-host gates;
- Rocky passes its documented experimental regression gate without weakening
  SELinux or substituting Docker assumptions.

## Known alpha.14 limits

No wildcard certificates, custom proxy directives, other DNS providers,
router automation, HA ingress, multi-node routing, full Rocky staged runtime
rewrite, general permanent-delete redesign, or backup-management redesign is
included. Certificate and generated application keys use restrictive host
permissions but are not encrypted at rest. The product and support bundle must
say that plainly.
