# Complete first run

KITPro Server stores first-run progress in the control database. You can close
the page, restart the services, or reboot the host without losing completed
steps.

## Before you start

Install KITPro with the native package for your Supported platform. Configure
Docker with an operator-selected address pool before you install an
application. KITPro reports the current configuration and conflicts, but it
does not edit `/etc/docker/daemon.json` or restart Docker.

## Complete the guided setup

1. Open the KITPro control service and create the first administrator through
   the one-time `/setup` page.
2. Sign in. KITPro sends an incomplete installation to `/first-run`.
3. Resolve each **Attention** result. The page rechecks the helper, container
   runtime, trusted storage, LAN address, Docker address pool, certificates,
   and installed applications against current state.
4. Select **Local only** or **LAN**. KITPro records the selection only when the
   active service listener matches it. Change the service listener and restart
   the control service when the page reports a mismatch.
5. Configure HTTPS, or defer it. Deferring HTTPS does not enable HTTP ingress.
6. Install the first application through the normal catalog lifecycle.

Critical steps cannot be marked complete by a browser request. The page derives
their state from the host, helper, runtime, storage, and application records.
After the first administrator exists, `/setup` no longer accepts bootstrap
registration.

## Read the readiness result

`GET /api/v1/system/readiness` reports separate dimensions for the helper,
runtime, Docker address pool, storage, disk capacity, LAN address, detectable
firewall state, ingress, and HTTPS. A disabled optional service is
not the same state as a failed service.

For Docker remediation, follow the
[Docker address-pool prerequisite](../docker-address-pool-prerequisite.md).
