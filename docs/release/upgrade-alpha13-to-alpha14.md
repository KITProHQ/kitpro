# Upgrade published alpha.13 to alpha.14

Use only the published alpha.13 package and its verified checksum to create the
upgrade baseline. A local alpha.13 build is not equivalent release evidence.

## Prepare the upgrade

1. Record the installed package version and checksum source.
2. Confirm that all expected applications, bindings, generated secrets,
   external-storage references, backup metadata, operation failures, and
   sessions exist.
3. Create and verify an application backup for critical applications.
4. Record that no managed ingress route, HTTPS certificate, or Cloudflare
   provider exists in the alpha.13 state.
5. Install the alpha.14 native package through the platform package manager.
6. Wait for the schema migration and service restart.
7. Verify the recorded application and state baseline.
8. Confirm that ingress remains empty and disabled, HTTPS remains disabled,
   and Cloudflare remains unconfigured.

Schema 18 adds alpha.14 control and helper state without changing the existing
application exposure policy. The migration does not infer hostnames or create
routes. Any application that becomes newly reachable after the package upgrade
is a release blocker.

Complete this procedure separately on Debian 13, Ubuntu 26.04 LTS, and Arch
Linux. Do not infer Ubuntu results from Debian. Rocky Linux 10 remains an
Experimental regression gate.
