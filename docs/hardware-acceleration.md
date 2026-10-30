# How to use hardware acceleration

KITPro runs trusted applications on the CPU and can grant a registered accelerator class when the host meets that class's requirements.

## Check the host

Open **Settings**. The Hardware acceleration panel reports the detected vendor and model and whether the container runtime is ready. Authenticated technical clients can request `GET /api/v1/hardware`. The response omits raw device paths and reports normalized vendor/model identity, render/compute availability, NVIDIA integration, and IOMMU state.

## Install Ollama

1. Open **Catalog** and select **Install** on Ollama.
2. Wait for the pinned official image to download. The first pull is large.
3. Keep the Ollama API **Private** unless a specific client needs loopback or LAN access.

Ollama starts without a model and works without a GPU. KITPro persists models in managed application storage. It does not download models or add API keys automatically.

The initial trusted release supports CPU fallback and optional NVIDIA acceleration. AMD needs the upstream ROCm image variant and is not enabled for this Ollama release. Intel acceleration is not claimed for Ollama.

## Verify the result

The catalog card distinguishes **CPU supported**, **GPU optional**, **GPU required**, certification, and current availability. Without a usable NVIDIA runtime, the helper records CPU mode for Ollama. The installed-app page reports the helper-owned assignment as the detected GPU, CPU fallback, or required GPU unavailable. Reconciliation must return `exact`; required-hardware failures or security drift need operator attention.

## Troubleshooting

### GPU not detected

Confirm Linux sees the PCI GPU and a DRM render node. QXL and other virtual display adapters do not qualify. KITPro does not install drivers.

### NVIDIA toolkit missing

Follow NVIDIA's official Container Toolkit instructions, restart Docker, and check Settings again. KITPro requires a registered `nvidia` Docker runtime and does not edit Docker configuration.

### Render node unavailable

Confirm the driver created `/dev/dri/renderD*`. KITPro ignores card-only adapters and never substitutes `/dev/dri/card*` for a render class.

### AppArmor or device denial

Review the helper unit and kernel audit log. Do not disable AppArmor. Treat a denial as a policy or packaging defect, not a reason to grant arbitrary access.

### GPU disappears after reboot

KITPro re-discovers and compares stable identity. Required hardware fails closed. Optional CPU fallback occurs only when the manifest explicitly permits it.

### CPU fallback

CPU mode is expected when optional acceleration is unavailable. It is slower but does not weaken isolation, and KITPro never labels it GPU-accelerated.


See the [architecture](architecture/gpu-device-access.md) and [manifest reference](application-manifest.md).
