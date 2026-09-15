# GPU and device access architecture

KITPro represents accelerator access as trusted intent, never as a Docker host path. A catalog manifest names a registered class. The helper discovers the host, resolves the class, creates the exact Docker configuration, and records what it assigned.

## Trust flow

```text
trusted manifest (class + optional/required policy)
  -> API request (same typed declaration; no paths)
  -> helper revalidates manifest and class
  -> sysfs/procfs discovery + Docker runtime inventory
  -> exact device/runtime plan + trusted assignment record
  -> Docker create -> inspect -> hardware-aware reconciliation
```

The API cannot send device paths, supplementary groups, capabilities, runtime arguments, privileged mode, or host networking. Unknown JSON fields and unknown classes fail validation.

## Discovery

The helper reads PCI vendor and device identifiers from `/sys/class/drm`, follows the kernel's DRM relationship to card and render nodes, and records stable PCI identity when available. It detects `/dev/kfd`, the NVIDIA driver record under `/proc/driver/nvidia`, IOMMU groups, and Docker's registered runtimes. Normal API output contains only normalized accelerator information.

Shell commands are not used for discovery. KITPro does not install drivers or a vendor container runtime.

## Device-class registry

| Class | Resolution | Container access |
|---|---|---|
| `gpu.nvidia` | Exactly one NVIDIA PCI GPU, driver present, Docker NVIDIA runtime registered | One NVIDIA Docker GPU request; multi-GPU hosts fail as ambiguous |
| `gpu.amd` | One AMD GPU with `/dev/kfd` and its vendor-matched render node | Exact KFD and render mappings, read/write |
| `gpu.intel.render` | One Intel vendor-matched render node | Exact render mapping, read/write |
| `video.vaapi` | One Intel or AMD vendor-matched render node | Exact render mapping, read/write |

DRM nodes must be character devices with major 226; render minors must be at least 128. KITPro does not map card nodes for these classes. It adds neither the API account nor the helper to `video` or `render`, and accepts no arbitrary supplementary groups.

## Optional and required acceleration

An optional declaration is usable without hardware only when `cpu_fallback: true`. The helper records `mode=cpu`. A required declaration fails installation if it cannot resolve exactly. A manifest cannot claim CPU fallback for required acceleration.

## Ownership, reboot, and restore

Helper schema version 6 adds `hardware_assignments`. Each record binds installation, component, class, mode, vendor, stable identity, resolved nodes, and runtime generation. The helper database is part of existing backups.

Reconciliation compares trusted state with fresh discovery and Docker inspect. Missing hardware, changed identity, missing or extra mappings, and unexpected GPU requests are security drift. After reboot or restore, KITPro re-discovers hardware and never treats a stale render-node number as sufficient proof or silently substitutes a different device.

Hardware is component-scoped. A web component receives no accelerator merely because its worker declares one.

## AppArmor

No AppArmor permission was added. The packaged helper already had read-only `/sys/**` and `/proc/**` inspection plus Docker-socket access for bounded lifecycle operations. It still has no arbitrary child execution, internet socket access, or user-home access. Docker, not the helper process, opens a resolved container device.

## Threat review

| Threat | Mitigation |
|---|---|
| Manifest names a path | No path field; unknown class rejected |
| Compromised API changes a class | Helper reloads the embedded catalog and requires an exact match |
| Node renumbering | Stable PCI identity and freshly resolved nodes are compared |
| Extra Docker device | Exact count and paths are inspected; mismatch is security drift |
| Group or capability abuse | Neither is representable |
| Privilege spreads across components | Each component has its own declaration and assignment |
| Restore to another host | Intent is restored, then resolved against new inventory |
| NVIDIA runtime missing | Class unavailable; KITPro never installs it automatically |

## Trade-offs and limits

The closed registry requires code for each future device class, but makes every grant auditable. NVIDIA is restricted to one unambiguous GPU until stable per-device toolkit identifiers are supported. An RTX A2000 12GB passed live container, inference, recreation, fallback, and reboot tests on Debian, Ubuntu, and Arch. AMD compute and Intel accelerated workloads remain unclaimed.

Jellyfin was not added because useful acceptance requires a trusted media-import/storage design. Arbitrary host media mounts would weaken the storage boundary. Per-component `video.vaapi` behavior has schema/helper coverage and live exact-node scoping on a local AMD host, but not an accelerated media workload.

See the [manifest reference](../application-manifest.md), [user guide](../hardware-acceleration.md), and [threat model](../security/threat-model.md).
