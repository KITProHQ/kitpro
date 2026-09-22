# Privilege-boundary test results

Use one immutable result file per environment and run. Never edit a failure into a pass. Attach a later rerun as a separate result.

Every case uses one status:

- `PASS`: the stated command ran in the named environment and met its assertion.
- `FAIL`: the command ran and violated its assertion.
- `NOT RUN`: nobody ran the case in the named environment.
- `BLOCKED`: an attempted case could not run because a stated environmental dependency or control prevented it.
- `OBSERVATION`: measured context that is not itself an acceptance test.

Reference-host acceptance requires results from the actual disposable Debian 13
and Rocky Linux 10 VMs. Fake runtime tests and development-host results validate
adapter logic, not Docker Engine, Podman/Quadlet, or distribution behavior.

Capture each target run with the process in [`tools/platform-validation/README.md`](../../../tools/platform-validation/README.md). Keep the raw terminal transcript and a SHA-256 checksum, then complete a new timestamped copy of the matching Markdown template. Record the fixture revision, VM and snapshot identifiers, resolved image digest, every command, relevant output, and each unrun case. Never replace a prior failed record with a rerun.

Completed package-platform certifications:

- [Ubuntu Server 26.04 LTS](2026-09-13-ubuntu2604-package-certification.md)
- [Arch Linux with `linux-lts`](2026-09-14-arch-linux-platform-certification.md)

Current feature-milestone validation:

- [Video-ready product baseline](2026-09-15-video-ready-product-baseline.md)
- [Media and data-heavy catalog expansion](2026-09-15-media-data-heavy-catalog-expansion.md)
