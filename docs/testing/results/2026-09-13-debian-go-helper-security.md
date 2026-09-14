# Debian 13 Go helper security validation — 2026-09-13

Status: **PARTIAL**. The compiled Go helper was built and exercised through a disposable root-run socket. This record does not claim AppArmor/systemd acceptance.

| Case | Status | Result |
| --- | --- | --- |
| Compiled Go helper startup/UDS | PASS | Framed `Ping` succeeded on Debian. |
| Canonical request hash and receipt | PASS | Stable SHA-256 hash and helper SQLite receipt observed. |
| API/helper identities and DAC | PASS (partial matrix) | Temporary identities 987/986 were created; neither belonged to Docker group. API could not use Docker directly; helper accepted the API UID and wrote helper receipts. Parent-directory replacement and WAL/SHM mutation cases remain NOT RUN. |
| AppArmor enforcing Go helper | NOT RUN | Existing Python profile passed; Go-specific profile remains. |
| Full systemd hardening | NOT RUN | No Go-specific unit installed. |
| Go `RestrictSUIDSGID`/`openat2` | NOT RUN | Existing fixture finding remains unchanged. |
| Full Debian combined chain | NOT RUN | Requires Go AppArmor/systemd unit and API client. |
| VM reboot/WAL recovery | NOT RUN | Not performed in this probe. |
