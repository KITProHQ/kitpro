# KITPro Server technical entry point

KITPro Server is a Go control plane and narrowly privileged helper for trusted self-hosted applications. The API owns authentication, desired state, the browser UI, and operations. The host-confined helper independently validates typed plans and is the only KITPro process permitted to operate the selected container runtime or approved host storage.

## Supported production boundary

- Debian 13 amd64 and Ubuntu 26.04 LTS amd64: `.deb`, rootful Docker, enforcing AppArmor.
- Arch Linux x86_64: native package, `linux-lts`, fully updated official repositories, rootful Docker, enforcing AppArmor.
- Rocky Linux 10 amd64: experimental RPM path, rootful Podman 5, Quadlet, crun, SELinux Enforcing, and firewalld; clean-host matrix not certified.

See the definitive [support matrix](../../docs/support-matrix.md).

## Runtime model

- Strict, schema-versioned catalog manifests resolve to typed single- or multi-container plans. Fourteen catalog apps are single-container; Paperless-ngx owns a web component and internal Redis broker.
- Installation identity, generated secrets, managed storage, exposure policy, and external-storage bindings persist independently of disposable runtime generations.
- Services begin internal-only and may be published only on loopback or one configured LAN address. Wildcard publication and host networking are rejected.
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
./packaging/tests/rpm_package_static_test.sh
```

Build packages with the version documented in the release manifest:

```sh
./packaging/build-package.sh 0.1.0~alpha11
./packaging/build-arch-package.sh 0.1.0_alpha11
./packaging/build-rpm.sh 0.1.0~alpha11
```

Do not infer a public release from a source version. Use the [GitHub releases page](https://github.com/KITProHQ/kitpro/releases) for published artifacts.

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
