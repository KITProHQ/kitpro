# Debian 13 helper AppArmor validation — 2026-09-12

Status: **PASS (test-only feasibility)**. This immutable record covers the disposable helper profile experiment on Debian 13. It does not establish production package, upgrade, or final-runtime compatibility.

## Environment

- Debian GNU/Linux 13 (trixie), amd64; kernel `6.12.107+deb13-amd64`.
- Existing validated Docker Engine 29.8.0, API 1.56; Docker reported AppArmor and seccomp.
- AppArmor service active. `apparmor-utils` was installed as disposable validation tooling because parser/status commands were absent initially.
- Existing fixture identities/state were reused; no clean snapshot was modified.

## Results

| Case | Status | Evidence |
| --- | --- | --- |
| Profile loads and remains enforcing | PASS | `apparmor_parser -r`; `aa-enforce`; `aa-status` showed `kitpro-pb-test-helper (enforce)`. |
| Protected socket and peer credentials | PASS | API UID 988 ping succeeded; `SO_PEERCRED` and fixed-UID policy remained active. |
| Runtime/Docker inspection | PASS | Engine 29.8.0 inspection succeeded through the helper; API identity had no Docker socket access. |
| Container/network lifecycle | PASS | Disposable internal bridge and container create/start/stop/remove succeeded with no published ports. |
| Approved state/storage access | PASS | `prepare-directory` and helper state operations succeeded under profile. |
| Helper restart and Docker outage/recovery | PASS | Socket activation recovered; bounded Docker-unavailable result followed by successful recovery. |
| Negative shell/IP/unrelated-storage probes | PASS | Confined probe received `EACCES` for `/bin/sh`, outbound IP, and disposable unrelated storage. |
| Profile compatibility denials | OBSERVATION → PASS | Initial required reads of `/usr/local/lib/python3.13/dist-packages` were denied; narrow read rules were added, profile reloaded, and positive probes produced no new denials. |
| Full filesystem race/mount stress suite | NOT RUN | Existing fixture does not provide a meaningful high-frequency race harness under MAC. |
| Production package/upgrade/rollback | NOT RUN | Deliberately outside this disposable experiment. |

The profile also denied DAC capability-assisted negative probes (`cap_dac_read_search`/`cap_dac_override`), providing defense in depth. No AppArmor disablement or permissive mode was used. Test-only profile, copied interpreter, unit, and disposable resources were removed after evidence capture; the standard fixture unit was restored.

## Conclusion

Debian AppArmor is technically viable as a helper defense-in-depth boundary. A production release should require a profile tied to the packaged helper and pass package/upgrade tests before acceptance.
