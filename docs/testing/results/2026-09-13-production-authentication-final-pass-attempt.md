# Production authentication/session security final pass attempt — 2026-09-13

Result: PASS

This record follows the earlier partial authentication record and captures the
remaining Debian integration evidence. It does not replace historical records.

## Implementation policy

- bcrypt cost: 10 (`PasswordHashCost`); bcrypt input is limited to 72 UTF-8 bytes.
- Passwords longer than 72 bytes are rejected before hashing/comparison; no truncation occurs.
- Sessions are opaque, server-side, HttpOnly, SameSite=Strict, with 30-minute idle and 12-hour absolute limits.
- Login throttling is server-side and keyed by a hash of remote peer address and normalized username. It uses exponential delays from 250 ms to an 8 s cap and expires state after 15 minutes.
- Development HTTP cookies omit Secure only when `KITPRO_SECURE_COOKIES` is not enabled. HTTPS deployments set Secure with the same HttpOnly/SameSite/Path policy.

## Debian evidence

| Case | Result | Evidence |
| --- | --- | --- |
| Login and authenticated dashboard | PASS | HTTP login `303`; dashboard `200` |
| Bcrypt 72-byte boundary | PASS | 72-byte input accepted; 73-byte input rejected; multibyte byte limit tested |
| Password change and rotation | PASS | Browser-form POST `303`; old password `401`; new password `303`; original credential restored |
| Cross-session revocation | PASS | Session B redirected after Session A password change |
| Missing/wrong CSRF | PASS | State-changing request `403` |
| Malicious Origin | PASS | State-changing request `403` |
| Logout CSRF | PASS | Missing token `403`, bad Origin `403`, valid POST `303`, subsequent dashboard `302`; GET is non-mutating (`405`) |
| Host validation | PASS | unexpected Host `400` |
| Idle timeout | PASS | disposable `2s` timeout: protected request `302` after expiry |
| Absolute lifetime | PASS | disposable `8s` lifetime expired despite activity refreshes |
| Login throttling | PASS | wrong-password and nonexistent-user requests both return generic `401`; second rapid attempt includes bounded `Retry-After: 1` |
| API restart session persistence | PASS | same cookie remained authenticated (`200`) after `systemctl restart kitpro-api` |
| VM reboot session persistence | PASS | same cookie stored in persistent `/var/tmp` remained authenticated (`200`) after VM reboot; Docker/API active |
| Authenticated workload | PASS | anonymous mutation `401`; authenticated create/stop/remove `202` |

## Automated evidence

- `GOCACHE=/tmp/kitpro-gocache go test ./...`: PASS
- `GOCACHE=/tmp/kitpro-gocache go vet ./...`: PASS
- `gofmt` and `git diff --check`: PASS

## Scope note

The service uses the durable control database, so restart persistence is
intentional and was verified across API restart and VM reboot. Authentication
remains private-network/development-only until broader deployment controls are
reviewed.
