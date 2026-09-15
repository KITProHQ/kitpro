# GPU and device access architecture validation — 2026-09-15

Status: **PASS**.

## Architecture and threat model

Manifest schema v3 accepts only `gpu.nvidia`, `gpu.amd`, `gpu.intel.render`, and `video.vaapi`. Raw device paths, unknown classes, arbitrary groups, capabilities, privileged mode, host networking, and runtime arguments are not representable. The API sends the trusted declaration only. The helper reloads the embedded catalog, requires an exact match, discovers sysfs/procfs and Docker runtime state, resolves devices internally, and creates Docker mappings.

NVIDIA requires one unambiguous PCI GPU, a loaded driver, and registered Docker `nvidia` runtime. AMD requires exact `/dev/kfd` plus the matching render node. Intel and VAAPI receive only one vendor-matched render node. DRM device type, major 226, and render minor range are validated. No driver or toolkit is auto-installed.

Helper schema v6 records installation, component, class, CPU/device mode, vendor, stable identity, resolved nodes, and generation. Reconciliation detects missing/changing hardware, vendor or stable-identity mismatch, extra/missing mappings, and GPU request drift. Required ambiguity fails closed; optional CPU fallback requires explicit manifest support. Restore retains intent and re-resolves against the destination host.

The existing AppArmor profile needed no change. It retained read-only `/sys/**` and `/proc/**`, Docker socket access, child-execution denial, user-home denial, and internet-network denial.

## Host discovery

| Host | Result |
|---|---|
| Debian VM 500, 10.10.0.115 | Passed-through RTX A2000 12GB, driver 550.163.01, NVIDIA Container Toolkit 1.20.0, rootful Docker, and enforcing AppArmor. KITPro normalized stable identity `0000:06:10.0:10de:2571`. |
| Ubuntu VM 502, 10.10.0.116 | Passed-through RTX A2000 12GB, server driver 580.178.04, NVIDIA Container Toolkit 1.20.0, rootful Docker, and enforcing AppArmor. KITPro normalized stable identity `0000:06:10.0:10de:2571`. |
| Arch VM 503, 10.10.0.119 | Passed-through RTX A2000 12GB, `linux-lts`, open DKMS driver 615.71.09, NVIDIA Container Toolkit 1.20.0, rootful Docker, and enforcing AppArmor. KITPro normalized stable identity `0000:06:10.0:10de:2571`. |
| Local Arch workstation | NVIDIA RTX 3060 Ti (`0000:01:00.0`, renderD128) and AMD PCI device 164e (`0000:39:00.0`, renderD129 plus KFD) detected. Docker 29.7.2 is usable. NVIDIA driver 610.57.04 is healthy, but NVIDIA Container Toolkit/runtime is absent. AppArmor is installed only by the test package and disabled in the running kernel (`/sys/module/apparmor/parameters/enabled=N`). |

The same physical GPU was assigned exclusively to one VM at a time. Each supported platform passed driver/runtime discovery, helper-side class resolution, container visibility, inference, recreation, and reboot. The device was removed from all test VMs after validation and restored to VM 107 as `hostpci0: 0000:01:00,x-vga=1`.

## Ollama

Official upstream release v0.34.0 uses `docker.io/ollama/ollama`. The trusted linux/amd64 manifest digest is `sha256:aa6f86f01fee264c81f1edd9083ebfb07c8116d95d8bedd1ad470874b66a40b4`. The container uses API port 11434 and persistent `/root/.ollama`. NVIDIA acceleration requires NVIDIA Container Toolkit; AMD uses a distinct upstream ROCm image and was not conflated with the CPU image.

Debian first passed CPU-only installation through the authenticated KITPro API. Pulling `smollm2:135m` created a 270,898,672-byte synthetic model, and recreation plus reboot preserved it. With the RTX A2000 attached, authenticated recreation advanced the same installation to generation 3. Docker inspect showed the exact image digest, no host publication, `Devices=null`, one NVIDIA GPU request, and `Privileged=false`. Helper state recorded `gpu.nvidia|device|nvidia|0000:06:10.0:10de:2571|[]|3`; Ollama reported `100% GPU`. Reboot preserved the assignment, model, and GPU inference. Removing the GPU and recreating advanced to generation 4, retained the model, removed all device requests, and reported `100% CPU`.

## Platform smoke

- Ubuntu 26.04.1: alpha8 upgrade backup/migration passed from schema 5 to 6; AppArmor stayed active. Authenticated Ollama installation with the RTX A2000 produced one NVIDIA GPU request, no raw device mapping, no publication, and `Privileged=false`. The model ran `100% GPU`; reboot preserved both model and acceleration.
- Arch Linux: the native alpha8 package upgraded after a full supported `pacman -Syu` under `linux-lts` and enforcing AppArmor. Authenticated recreation produced generation 2 with the exact trusted NVIDIA assignment and `100% GPU` inference. Reboot preserved the model and assignment. After GPU removal, generation 3 had no device request and the preserved model ran `100% CPU`.
- Initial image downloads were I/O-bound. Debian required removal of unused Docker images only; no container, volume, KITPro state, or application data was deleted.

## Device and failure tests

Tests cover unknown/raw classes including `/dev/sda`, `/dev/mem`, `/dev/kvm`, input, USB, and TTY forms; optional/required rules; NVIDIA driver/runtime requirements; ambiguous multi-NVIDIA rejection; single vendor-matched VAAPI resolution; exact Docker mappings without privileged mode; CPU runtime rejection of unexpected `/dev/sda`; exact helper/catalog requirement matching; and component-local assignment without privilege spread.

On the local Arch workstation, the production discovery code resolved `gpu.amd` to stable identity `0000:39:00.0:1002:164e` with exactly `/dev/kfd` and `/dev/dri/renderD129`. It resolved `video.vaapi` to only `/dev/dri/renderD129`; `gpu.nvidia` correctly remained unavailable because Docker has no NVIDIA runtime. Two ephemeral containers proved the resolved AMD and VAAPI mappings were usable and that `/dev/dri/renderD128`, both DRM card nodes, and KFD in the VAAPI-only case were absent. Both containers were removed automatically.

The second device-based catalog app is deferred. Jellyfin requires a trusted external media storage/import primitive; arbitrary media mounts would weaken the storage boundary. Generic `video.vaapi` and component-scoping tests provide the permitted synthetic alternative: live render-device scoping passed on the local AMD GPU, and tests prove that only the declaring component receives the assignment.

During Debian live acceptance, Docker creation succeeded but helper assignment persistence failed because the SQL statement had one surplus placeholder. The helper rolled the runtime back. The statement was corrected and a regression test now verifies every trusted identity field. The complete Go suite passed afterward, and the corrected package passed on all three platforms.

## Validation and artifacts

`go test ./...` and `go vet ./...` pass. Debian and Arch packages were built twice from the same source state and matched byte-for-byte. The SBOM was generated twice and matched.

| Artifact | SHA-256 |
|---|---|
| Debian/Ubuntu `kitpro-server_0.1.0~alpha8_amd64.deb` | `525811f50237c3de63846496411fa80313beec76a0aef640ff3a34524636ec67` |
| Arch `kitpro-server-0.1.0_alpha8-1-x86_64.pkg.tar.zst` | `2283bd30957b5c9b0cedbf35211a29491c82b304b28044c9b0551ac1ba17ac6c` |
| CycloneDX SBOM | `efd4757931317562c8275d5604f5584b3dfd64cd5bfcaa00b4b5c1e9f55eb8bf` |

These artifacts are validation-only and record a dirty source tree against checkpoint `d9d3656e`; they are not public-release artifacts.

## Gate

Typed declarations, helper-side resolution, ownership, reconciliation, optional and required semantics, negative device tests, per-component scoping, CPU Ollama, live NVIDIA inference, reboot persistence, supported-platform smoke, and the security review pass. AMD compute and Intel workloads remain explicitly unvalidated and are not part of the supported claim.

**GPU + DEVICE ACCESS ARCHITECTURE: PASS**
