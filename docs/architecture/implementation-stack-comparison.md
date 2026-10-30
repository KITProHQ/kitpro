# Implementation-stack comparison

This weighted comparison reflects KITPro's priorities: helper security and failure correctness outweigh raw development speed. Scores are qualitative (1 low, 5 high) and are evidence-informed, not benchmark results.

| Category (weight) | Go both components | Rust both components | Go helper + Python control plane |
| --- | ---: | ---: | ---: |
| Helper security and memory safety (25) | 4 | 5 | 4 |
| Linux systems access and crash correctness (20) | 5 | 5 | 3 |
| Low dependency/runtime burden (15) | 5 | 4 | 2 |
| Packaging and upgrades (10) | 5 | 4 | 3 |
| Operational simplicity (10) | 5 | 4 | 2 |
| Maintainability/contributor access (10) | 4 | 3 | 4 |
| Testing/fuzzing/tooling (5) | 5 | 5 | 4 |
| Development velocity (5) | 4 | 3 | 5 |
| **Weighted direction** | **strongest balance** | **security-leading fallback** | **higher operational burden** |

Go's advantage is one low-dependency toolchain for both trust domains, not benchmark speed. Rust remains attractive where compile-time guarantees justify its higher toolchain and contributor cost. Python is credible for UI/API work but materially increases deployment and security-surface complexity beside another helper language.

## Database and protocol comparison

| Choice | Phase 1 assessment |
| --- | --- |
| SQLite | Preferred: embedded, transactional, WAL, low administration; use two files with separate permissions. |
| PostgreSQL | Fallback: excellent concurrency and operations, but unnecessary server dependency for one node. |
| Strict JSON framing | Preferred: inspectable, bounded, cross-language, canonicalizable. |
| CBOR/protobuf | Deferred until payload volume or generated-schema needs justify complexity. |

## Preferred stack

Proposed: Go control plane and helper; two separate SQLite databases; strict framed JSON helper protocol; REST/JSON control-plane API; server-rendered HTML with small local JavaScript; separate `kitpro-api` and `kitpro-helper` binaries from one toolchain; Debian `.deb` packaging with AppArmor/systemd assets.

## Credible fallback

Rust for both binaries, retaining the same database, protocol, API, frontend, and packaging boundaries.
