# Operate managed ingress

KITPro uses a dedicated nginx service named `kitpro-ingress`. It generates the
nginx configuration from authenticated route records. Application containers
remain bound to loopback, and nginx is the only managed path from a hostname to
an eligible HTTP service.

## Choose an access mode

- Use LAN HTTP only on a trusted network when transport encryption is not
  required.
- Use a provided certificate when your organization already manages the key
  and certificate.
- Use managed ACME when the hostname and HTTP-01 prerequisites are under your
  control.

KITPro accepts only catalog-authorized HTTP or HTTPS services. It rejects an
invalid hostname, a duplicate hostname, a wildcard hostname, a non-loopback
backend, and an unknown `Host` header.

## Install a provided certificate

Submit the hostname, certificate chain, and matching private key through the
authenticated certificate endpoint. KITPro validates the PEM data, key match,
hostname, validity period, and lifetime before installation. It stores the
private key under the root-owned ingress state with mode `0600`.

The API, UI, logs, audit events, and support bundles never return the private
key. KITPro protects the key with permissions and mandatory access control. It
does not claim encryption at rest.

## Issue and renew an ACME certificate

Select `acme`, a catalog-authorized installation and service, the exact
hostname, a contact address, and either the Let's Encrypt production or staging
directory. Accept the subscriber terms explicitly.

KITPro checks the hostname, DNS resolution, route authorization, challenge
path, and listener prerequisites before it asks the CA to issue a certificate.
The dedicated ACME worker performs the network exchange. The helper validates
the issued key and certificate before it activates a new nginx generation.

The renewal scheduler checks certificates every 12 hours. Certificate state is
`requested`, `issued`, `active`, `expiring`, `renewal-pending`,
`renewal-failed`, or `invalid`. The certificate list includes the expiration,
last renewal, next renewal, and a bounded error code. A failed or invalid
replacement leaves the last known-good certificate active.

Public CA access and public inbound TCP/80 are operational dependencies for
issuance and renewal. KITPro does not modify a router or firewall and cannot
bypass an ISP block. A failed HTTP-01 attempt reports a bounded error and tells
the administrator to verify public DNS and TCP/80 reachability. Use the staging
directory to validate that path before production issuance.

When public TCP/80 is unavailable, import a certificate obtained through an
external client, DNS-01 workflow, or upstream reverse proxy instead.

## Recover a failed generation

KITPro writes each candidate to a new generation, validates it with nginx, and
reloads only after validation succeeds. If validation, reload, or post-reload
verification fails, the helper restores the previous active generation. A
failed candidate cannot replace the current symlink or certificate.

After a restore, KITPro places affected routes on hold. Restore the application,
verify its readiness, and call `POST /api/v1/ingress/reconcile`. KITPro then
builds and validates a candidate before it removes the hold. It never redirects
a hostname to a different installation as a recovery shortcut.
