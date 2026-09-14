# Go build, dependency, and reproducibility record — 2026-09-13

| Check | Status | Evidence |
| --- | --- | --- |
| CGO-disabled static build | PASS | amd64 static ELF, approximately 16 MiB. |
| Module inventory | PASS | `go version -m` and `go list -m all` captured for the disposable helper. |
| Reproducible second build | NOT RUN | Two independent clean-environment builds were not completed. |
| govulncheck | NOT RUN | Tool is not installed; no global installation performed. |
| SBOM | PARTIAL | Go module inventory and artifact SHA-256 captured; formal SBOM generation remains open. |

The current module graph includes `modernc.org/sqlite` v1.37.0 and its pure-Go transitive dependencies. Production release work must add vulnerability scanning, license review, SBOM generation, and byte-reproducible build verification.
