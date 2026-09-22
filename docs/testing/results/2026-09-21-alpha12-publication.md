# Alpha.12 publication verification

Date: 2026-09-21

KITPro Server `v0.1.0-alpha.12` was published from commit
`ada9555ab680613f8b56f6f0762abce6f0955670`, tree
`196206df49ec0f21df509c011f9c06f6c56ab788`. The GitHub release is an alpha
prerelease.

## Public filename normalization

GitHub replaces `~` with `.` in uploaded asset names. The published Debian
package is therefore named `kitpro-server_0.1.0.alpha12_amd64.deb`, while its
internal Debian version remains `0.1.0~alpha12`. The SBOM and Debian build
metadata use the same public filename normalization. Artifact bytes were not
rebuilt or replaced.

The package-bound `kitpro-debian-upgrade` wrapper completed its test-mode
preflight and simulated package transaction when given the normalized public
filename. The wrapper checked the frozen SHA-256, package name, version, and
architecture. It does not depend on the package basename.

## Public asset verification

All release assets were downloaded into a new empty directory. The corrected
`kitpro-alpha12-SHA256SUMS` uses the public filenames and includes both build
metadata files. `sha256sum -c kitpro-alpha12-SHA256SUMS` passed for every
listed file.

| Public asset | SHA-256 |
| --- | --- |
| `kitpro-server_0.1.0.alpha12_amd64.deb` | `29541da98fdba7fed0c4f7facec66d936c2da2487b8fedb562e289655fa8ac25` |
| `kitpro-debian-upgrade` | `8ddffc0070aec9576a7704a7b3f588ea7d0939dbed1c787a3c19ebcfd5489bb0` |
| `kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst` | `2fe6b51c370c2e9399da9291c31ad2af6bb09934c1a872f502db0c8c615d12f6` |
| `kitpro-server-0.1.0_alpha12-1-upgrade.sh` | `0eab8578715fcaed71b27739036961e0e20fa48c5571a87e3351048513963f67` |
| `kitpro-server_0.1.0.alpha12_amd64.cdx.json` | `2f365f08afeb81c693ae7b4b0af59452b1220f08c432aa47a99a02a805bf6f27` |
| `kitpro-server_0.1.0.alpha12_amd64.deb.build.json` | `a7ffbe35e4ddc448077ae4da3ff3a3fc57811525b7caeac16ed0081f16b0842f` |
| `kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst.build.json` | `0ced35a74321fc9786775a64b644b56abc018f1773a3635bd604e5e4e8fd1856` |

The alpha.11 release and tag were not changed. Rocky Linux and Podman remain
Experimental.
