# Disposable AppArmor profile

This directory contains a test-only AppArmor profile for the Debian 13 privilege-boundary fixture. It is not a production package or a production policy.

The experiment copies the distribution Python interpreter to `/opt/kitpro-privilege-boundary-test/bin/kitpro-pb-helper`. The fixed executable path lets AppArmor attach one profile without confining every process that uses `/usr/bin/python3`. This choice does not select Python for KITPro.

The profile permits the copied runtime, fixture code, helper state, approved test storage, the inherited helper socket, and Docker's local Unix socket. It denies IP networking and omits access to unrelated home, root, system configuration, application-data, package-manager, device, and Docker-state paths.

During the Debian experiment, the confined copied Python runtime required an explicit read rule for `/usr/local/lib/python3.13/dist-packages`; the narrow compatibility adjustment was validated with the profile still enforcing.

Use the [MAC validation runbook](../MAC-RUNBOOK.md). Do not install this profile on a production host.
