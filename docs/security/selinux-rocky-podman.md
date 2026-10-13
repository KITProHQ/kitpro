# SELinux on Rocky Linux with Podman

SELinux must remain `Enforcing`. A failure under Enforcing is a validation
failure to diagnose, not permission to use `setenforce 0`, a permissive domain,
`--privileged`, or broad capabilities.

## Shipped policy scope

The separate `kitpro-selinux` RPM installs one persistent file context:

```text
/srv/kitpro/apps(/.*)? -> container_file_t
```

The module has no allow rules and changes no boolean. Managed private mounts
also use Podman's `:Z` handling, which assigns an MCS label for the container.
This is sufficient for the current exclusive application directories if live
validation shows no related AVCs.

Imported paths under `/mnt`, `/media`, `/data`, or `/srv` are not relabeled by
KITPro. Never relabel one of those parent trees. If a specific imported root
needs container access, review its sharing model first and add an exact
administrator-owned file-context rule only for that root.

## Inspect labels

```sh
getenforce
ls -Zd /srv/kitpro/apps
podman inspect --format '{{.ProcessLabel}} {{.MountLabel}}' CONTAINER
ps -eZ | grep container_t
```

Normal application processes should have a confined `container_t` label and a
non-empty MCS category. `spc_t`, an empty process label, or an empty mount label
fails the Rocky security gate.

## Investigate an AVC

Record the exact validation window, then inspect it without changing policy:

```sh
sudo ausearch -m AVC,USER_AVC -ts recent -i
sudo journalctl -t setroubleshoot --since '-10 minutes'
sudo matchpathcon -V /srv/kitpro/apps
```

Classify the denial against the expected operation and path. A wrong file label
should be fixed with `restorecon` or an exact `semanage fcontext` rule. Do not
pipe production AVCs directly into `audit2allow` and install the result.

## When custom policy is justified

Use custom policy only when a required operation cannot be expressed with the
standard container labels and exact file contexts. Udica may provide a starting
point for a custom container policy, but its output still requires reduction,
review, packaging, and a clean Enforcing test. Every permission must correspond
to a documented KITPro operation.

A future dedicated helper domain must be measured against Podman inspection,
systemd control, state, runtime secrets, and storage preparation. It must not
grant generic service administration or unrestricted host filesystem access.
