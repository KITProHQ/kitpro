# GPU-accelerated application catalog expansion — 2026-09-15

Status: **PASS**.

## Product and architecture

Settings now reports normalized vendor/model identity and NVIDIA container-runtime readiness. Catalog cards distinguish CPU support, optional/required GPU policy, NVIDIA certification, and current availability. Installed applications report the helper-owned assignment as the exact GPU model, CPU fallback, or required-GPU unavailable; raw device paths remain absent from normal UI and the bounded helper response.

The helper operation `GetHardwareAssignment` returns only installation-scoped component, class, mode, vendor, stable identity, model, and generation. It validates installation identity and never returns stored raw mappings. Startup now fails stale `accepted` operations closed by an API restart instead of leaving them ambiguous. Regression tests cover the bounded response, certified-model lookup, raw-path omission, and fail-closed recovery.

## Candidate decisions

| Candidate | Decision | Reason |
|---|---|---|
| Open WebUI plus Ollama | REJECT FOR CURRENT MODEL | Cross-installation networks are isolated and no trusted discovery primitive exists; host networking and blind URL injection were rejected. |
| ComfyUI | REJECT FOR CURRENT MODEL | No stable official production image suitable for an immutable trusted release was found. |
| Jellyfin | REJECT FOR CURRENT MODEL | Useful deployment requires media-library mounts; KITPro has no trusted external-media storage class. |
| Frigate | REJECT FOR CURRENT MODEL | Camera streams, shared-memory sizing, and Coral/USB/PCI needs exceed registered classes. |
| whisper.cpp server | REJECT FOR CURRENT MODEL | Official images track branches/commits and startup needs a trusted model bootstrap contract. |
| InvokeAI | REJECT FOR CURRENT MODEL | Official GPU tags track main/commit builds rather than a stable trusted release identity. |
| LocalAI 4.9.0 | REJECT FOR CURRENT MODEL after live test | Its pinned official outer image fetched `quay.io/go-skynet/local-ai-backends:latest-gpu-nvidia-cuda-12-llama-cpp` and reported no signature verification during model installation. End-to-end runtime provenance was mutable. |

Authoritative sources: [LocalAI 4.9.0](https://github.com/mudler/LocalAI/releases/tag/v4.9.0), [ComfyUI](https://docs.comfy.org/), [Jellyfin](https://jellyfin.org/docs/general/installation/container/), [Frigate](https://docs.frigate.video/frigate/installation/), [whisper.cpp](https://github.com/ggml-org/whisper.cpp), and [InvokeAI](https://github.com/invoke-ai/InvokeAI/blob/main/docker/README.md).

The LocalAI linux/amd64 image digest was `sha256:2d77509be9033dea24c3a8200ea9cca53579f75203d759a9e38dcc8a4679ca69`. Its synthetic container, model, storage, network, records, and image were removed after rejection.

## NVIDIA live matrix

The RTX A2000 12GB at Proxmox PCI identity `0000:01:00.0` was assigned to one VM at a time. Every detach occurred with the source stopped. It was restored to VM 107 as `hostpci0: 0000:01:00,x-vga=1`; the guest reported `NVIDIA RTX A2000 12GB, 12282 MiB`.

| Platform | Alpha 9 result |
|---|---|
| Debian 13 VM 500 | PASS. Package active; GPU/toolkit detected; authenticated Ollama recreation advanced generation 5 and stored `device|nvidia|0000:06:10.0:10de:2571`. Docker had one NVIDIA request, no raw devices or ports, and `Privileged=false`. |
| Ubuntu 26.04 VM 502 | PASS smoke. Package upgraded; services active; RTX A2000 and 12,282 MiB visible within the previously certified driver/toolkit boundary. |
| Arch linux-lts VM 503 | PASS. Native package active; authenticated UI/API reported the RTX A2000 and toolkit; Ollama recreation advanced generation 4 and the page reported `NVIDIA RTX A2000 12GB`. |

After Debian GPU removal, authenticated Ollama recreation advanced generation 6, persisted `gpu.nvidia|cpu`, and Docker reported `DeviceRequests=null` and `Devices=null`. Installation identity and managed model storage remained.

No second app was admitted, so cross-application contention and multi-container GPU scoping were not applicable. KITPro still grants access without scheduling or VRAM reservation. Existing component tests prove hardware does not spread to undeclared components.

The LocalAI pull interruption exposed a stale `accepted` operation and drove the generic recovery fix. Its large image also required safe headroom; Debian VM 500's virtual/root disk was expanded from 40 GiB to 80 GiB. Existing application data was preserved.

## Security, regression, and documentation

Raw Docker flags, host networking, privileged mode, arbitrary capabilities/groups/devices, and external host storage remain unrepresentable. LocalAI was rejected despite fitting the outer device model because transitive provenance failed. FreshRSS, Paperless-ngx, Open WebUI, IT-Tools, and Ollama survived package/GPU lifecycle work.

The gstack document-generate pass added missing assignment/recovery and candidate evidence. The document-release pass synchronized architecture, hardware how-to, catalog/manifest reference, limitations, support matrix, changelog, and this result. Unsupported combinations are not advertised.

Two independent alpha9 builds were byte-identical for the Debian package, Arch package, and CycloneDX SBOM. They are validation artifacts prepared from checkpoint `176b404dab780203f4ed9d78d6378889e8586334` plus the reviewed working-tree change; no release was published.

| Artifact | SHA-256 |
|---|---|
| `kitpro-server_0.1.0~alpha9_amd64.deb` | `11f23cf5fbdc73341372d38c5391958d7caabc6873b2bc088f85c7790cade54a` |
| `kitpro-server-0.1.0_alpha9-1-x86_64.pkg.tar.zst` | `60c3cb68d5f611e6f0aee2c789a0f1ec488bbb85203ac4579f9fca0e6f90d082` |
| `kitpro-server_0.1.0~alpha9_amd64.cdx.json` | `74998a75fcfef7b941b6d37d3a20488f11a7f2072e174421d3956d1a5e77ee16` |

## Gate

The GPU UX, Ollama experience, three-platform NVIDIA matrix, CPU fallback, drift/security tests, regression, documentation, and packaging pass. No additional app was admitted because each candidate had a concrete trusted-runtime, storage, device, or network incompatibility; the milestone gate explicitly permits this safe result.

**GPU-ACCELERATED APPLICATION CATALOG EXPANSION: PASS**
