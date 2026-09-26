# KITPro Server technical entry point

KITPro Server is a Go control plane and narrowly privileged helper for trusted self-hosted applications. The API owns authentication, desired state, the browser UI, and operations. The mandatory-access-control-confined helper independently validates typed plans and is the only KITPro process permitted to operate the container runtime or approved host storage.

## Supported production boundary

- Debian 13 amd64: `.deb`, rootful Docker, enforcing AppArmor.
- Ubuntu 26.04 LTS amd64: `.deb`, rootful Docker, enforcing AppArmor.
- Arch Linux x86_64: native package, `linux-lts`, fully updated official repositories, rootful Docker, enforcing AppArmor.
- Rocky Linux 10 amd64 and Podman: Experimental. The native package and host
  boundary pass, but alpha.13 application lifecycle is unavailable because the
  Podman adapter does not implement staged generations.

See the definitive [support matrix](../../docs/support-matrix.md).

## Runtime model

- Strict, schema-versioned catalog manifests resolve to typed single- or multi-container plans. Nineteen of the 20 catalog apps are single-container; Paperless-ngx owns a web component and internal Redis broker.
- Installation identity, generated secrets, managed storage, exposure policy, and external-storage bindings persist independently of disposable runtime generations.
- Generated secrets remain internal unless the trusted manifest marks one as an administrator credential. A protected POST action can reveal only that credential; ordinary APIs and initial HTML remain secret-free.
- Services begin internal-only unless a schema-v8 manifest declares a constrained initial exposure. Pi-hole uses exact-address TCP and UDP port 53, while the experimental Syncthing profile uses exact-address TCP port 22000; both keep administration on a dynamic loopback UI. The trusted binding set supports TCP, UDP, multiple services, dynamic ports, and manifest-authorized fixed ports. Wildcard publication and host networking are rejected.
- Syncthing's typed configuration bootstrap lets the official image generate its identity offline, then enforces the reviewed manual-peer TCP-only policy before the networked runtime is created. It is not a general template or command facility.
- Hardware requests use bounded device classes. NVIDIA acceleration is certified for Ollama on the published test boundary; CPU fallback remains available.
- External data uses administrator-registered trusted roots and manifest-declared slots. Bind sources and targets are resolved by the helper; imported data is never deleted with an app.
- App updates use trusted release pairs, preserve identity and storage, and back up control state. Imported data and full application-data disaster recovery remain administrator responsibilities.

## Build and test

From this directory:

```sh
go test ./...
go vet ./...
gofmt -l .
./packaging/tests/package_static_test.sh
./packaging/tests/arch_package_static_test.sh
```

Build packages with the version documented in the release manifest:

```sh
./packaging/build-package.sh 0.1.0~alpha13
./packaging/build-arch-package.sh 0.1.0_alpha13
```

Do not infer publication from a source version. Alpha.13 packages become
official only after the release tag and GitHub release are published. Until
then, use the [latest published prerelease](https://github.com/KITProHQ/kitpro/releases/latest)
for public artifacts.

## Contributor references

- [Architecture](../../docs/architecture.md)
- [Manifest reference](../../docs/application-manifest.md)
- [API reference](../../docs/api-reference.md)
- [Security invariants](../../docs/security/invariants.md)
- [Catalog](../../docs/application-catalog.md)
- [Package quickstart](../../docs/release/quickstart.md)
- [Known limitations](../../docs/release/known-limitations.md)
- [Contributing](../../CONTRIBUTING.md)
- [Security policy](../../SECURITY.md)
