# Debian first production vertical slice — 2026-09-13

Result: **FAIL (partial integration)**

The compiled Go API and helper were deployed to Debian 13 VM 500. Dashboard, host/Docker inspection, socket activation, AppArmor confinement, systemd hardening, API-to-helper peer authentication, helper receipts, and a real Docker Engine create operation passed. The API is loopback-only and has no authentication by design.

Stop/remove routing was corrected and exercised successfully after redeployment. Two disposable BusyBox resources were created during testing; one was removed through the API. Full persistent-data recreation, reboot, Docker-restart reconciliation, foreign-resource negative testing, and complete cleanup were not completed in this run. The implementation therefore remains a review checkpoint, not a release candidate.

Image used: `busybox:1.37.0@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0`.
