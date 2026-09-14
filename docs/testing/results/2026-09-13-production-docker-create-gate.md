# Production Docker create gate — 2026-09-13

Result: **PASS**

Two concrete adapter defects were found and fixed:

1. Docker rejected network creation because requests omitted `Content-Type: application/json`. The adapter now sets it on every request and captures structured daemon error bodies.
2. Docker returns HTTP 304 for an already-stopped container. The adapter now treats 304 as a successful idempotent lifecycle response.

The production API then created a real BusyBox workload through the API → helper UDS → Docker Engine path. Observed resource:

- image: `busybox:1.37.0@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0`
- deterministic container/network names
- `com.kitpro.managed=true`, instance, and resource labels
- `Privileged=false`
- user-defined bridge network
- no mounts and no published ports

Stop and remove then succeeded through the production API. The persistent marker remained after runtime removal. The earlier API false-success defect remains fixed: helper `OK=false` responses are now surfaced as failed API operations.
