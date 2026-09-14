# Go helper production-shape validation — Debian 13 — 2026-09-13

Status: **PARTIAL**. This record is immutable and separates executed probes from VM/MAC work not run.

## Executed

| Case | Status | Evidence |
| --- | --- | --- |
| Go helper build | PASS | Go 1.27 development toolchain; CGO-disabled build completed. |
| Helper startup and UDS | PASS | Disposable helper started on Debian and accepted a framed `Ping` over `/run/kitpro-go-helper/helper.sock`. |
| Typed request hash/receipt | PASS | Response returned stable SHA-256 request hash; helper SQLite receipt was written. |
| Direct Docker Engine HTTP-over-Unix | NOT RUN | The probe was not run with root Docker access in this pass. Existing fixture/API evidence remains authoritative. |
| `openat2` in compiled helper | NOT RUN | Local Go runtime probe passed; compiled helper path operation was not exercised on the VM. |
| Realistic API/helper identity separation | NOT RUN | Probe currently authorizes its own UID; production-shaped fixed API UID wiring remains to be added. |

## Not executed

| Case | Status | Reason |
| --- | --- | --- |
| Go binary under Debian AppArmor | NOT RUN | Existing Python AppArmor feasibility passed; a Go-specific profile run remains required. |
| Full systemd hardening with Go binary | NOT RUN | Disposable service was not installed for this probe. |
| Debian reboot/WAL recovery | NOT RUN | No reboot performed in this probe. |
| Negative MAC probes | NOT RUN | Requires the Go-specific AppArmor profile and service. |

The static probe characteristics were recorded separately: approximately 16 MiB, ELF amd64, statically linked with `CGO_ENABLED=0`. No production host configuration or service was changed; temporary probe files were removed.
