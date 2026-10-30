# KITPro helper confinement matrix

This matrix shows which boundary carries each protection. The intended chain is:

```text
browser input → authentication/authorization → unprivileged control plane
→ typed Unix protocol → DAC + SO_PEERCRED → semantic helper policy
→ trusted ownership state → AppArmor/SELinux → systemd hardening
→ Docker Engine API → Docker container confinement
```

Protocol, semantic validation, ownership state, and path safety are primary controls. DAC, MAC, systemd, and Docker defaults are secondary or defense in depth. No row implies that one control is sufficient. Docker socket authority remains a residual risk even when several controls apply.

| Threat or action | API/helper protocol | Semantic helper policy | Ownership state | Filesystem safeguards | Unix DAC | systemd | AppArmor | SELinux | Docker configuration | Gap or residual risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Arbitrary Docker request | Rejects raw API and CLI-shaped input | Closed operation schema and allowlist | Target must be known | N/A | Socket only to API identity | Helper only | Profile limits helper socket/files | Domain limits helper socket/files | Runtime still has host authority | A compromised helper can abuse any semantic operation it is allowed to request |
| Arbitrary shell | No shell operation | No command field | N/A | N/A | Executable ownership | No child privilege gain | Shell execution denied | Shell execution denied | Container shell is workload-specific | Helper compromise remains high impact |
| `/etc` write | No path input | Approved storage reference only | N/A | Descriptor-relative roots | Root-owned files | `ProtectSystem=strict` | Write omitted or denied | Type access omitted | Docker can alter host only if helper requests it | Docker daemon authority bypasses direct file controls |
| SSH-key read | No secret-read operation | No arbitrary path | N/A | Approved-root containment | `/home` and SSH permissions | `ProtectHome=yes` | Home and root paths denied | Home and root types denied | Container has no host mount | Root or Docker administrators remain trusted |
| Unrelated application-data read | Semantic storage slot only | Slot ownership and path policy | Helper record required | Mount and identity checks | Root ownership | Read paths omitted | Unrelated data denied | Unrelated data type denied | No foreign bind mounts | Shared storage policy remains open |
| Unknown Docker deletion | No Docker ID target | Requires deterministic semantic instance | Helper record plus exact object identity | N/A | Docker socket withheld from API | Helper only | Socket limited to helper | Socket limited to helper | Labels are not authority | A root attacker can alter both Docker and helper state |
| Privileged container request | No unrestricted runtime fields | Rejects privileged, namespaces, devices, capabilities, and raw security options | N/A | N/A | N/A | Helper service only | Profile cannot constrain dockerd result | Domain cannot constrain dockerd result | Default container policy | Helper semantic validation is the primary control |
| Host bind mount | No arbitrary host path | Allowlisted storage references | Ownership of storage slot | `openat2`, mount identity, no-follow | Root paths protected | `ProtectSystem`, `ProtectHome` | Host paths denied to helper | Host types denied to helper | Docker mount still powerful | A compromised helper can ask Docker for a dangerous mount if policy fails |
| Docker socket proxying | API cannot open socket | No proxy operation | N/A | N/A | Socket mode and group | Helper-only unit | Socket path explicit | Docker socket type explicit | No TCP Engine API | Helper access is root-equivalent Docker authority |
| Helper-state tampering | Typed protocol only | Helper validates receipts and fences | Helper-owned store | Root-owned state path | API cannot write state | Writable path narrow | State path only | State type only | N/A | Backup restore and root compromise require recovery controls |
| Network exfiltration | No network operation | No destination input | N/A | N/A | N/A | `RestrictAddressFamilies=AF_UNIX` | IP networking denied | No generic TCP or UDP | Docker networking separate | Runtime libraries may need local name-service reads |
| Helper child-process execution | No command operation | No executable input | N/A | N/A | N/A | Capability and namespace restrictions | Unrelated execution denied | Unrelated execution denied | N/A | Runtime implementation may need narrowly reviewed helpers |
| Reconciliation auto-deletes drift | API cannot force repair | Fresh observation and fail-closed classification | Ownership conflict blocks | Full target verification | N/A | Helper only | N/A | N/A | Foreign members block network removal | Race after final check cannot be eliminated |

## Reading the gaps

The strongest controls are layered. The protocol and semantic policy reduce what a compromised API can ask for. Ownership state prevents labels or caller-provided Docker IDs from granting authority. Filesystem checks protect direct helper mutations. DAC, systemd, AppArmor, and SELinux add host-enforced restrictions. Docker configuration limits the workload after creation.

MAC does not turn Docker socket access into ordinary application access. A helper compromise remains a likely host compromise if the helper can submit a dangerous Engine API request. The profile and domain are still valuable because they reduce direct host reads, writes, child execution, and network access, and because they create independent evidence when a request crosses the helper's intended boundary.

The helper has rootful Docker Engine access by design. Therefore MAC is not complete containment: minimizing helper attack surface, keeping it local-only, using typed operations, rejecting arbitrary Docker configuration and shells, proving ownership, auditing, and systemd isolation remain the primary defense strategy.
