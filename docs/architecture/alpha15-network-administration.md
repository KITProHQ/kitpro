# Alpha.15 Network Administration, diagnostics, and transactional configuration

Status: Read-only inventory, diagnostics, and the bounded systemd-networkd transaction workflow have installed-service evidence on Ubuntu and KITPro Linux. Primary management-interface and non-networkd mutations remain disabled.

## Provider architecture

Networking extends the existing `hostadmin` subsystem and uses the existing administrator session, origin checks, CSRF protection, interface, and API process. It adds no daemon, helper operation, capability, raw socket, or configuration writer.

The required provider invokes the fixed `ip` executable from `iproute2` with allowlisted JSON arguments for links, addresses, IPv4 and IPv6 routes, and policy rules. It never invokes a shell. The executable identity, environment, and argument lists are fixed; output is limited to 4 MiB, collection has a four-second deadline, interfaces are limited to 512, and routes are limited to 4,096. Kernel sysfs files under the discovered interface name supply carrier, speed, duplex, driver, and counters. Interface names must match the Linux name grammar before they enter a fixed sysfs path. `iproute2` is a package dependency on Debian, Arch, and RPM platforms.

Resolver configuration uses a bounded read of the fixed `/etc/resolv.conf` path, reports its symlink target, and identifies systemd-resolved or NetworkManager ownership when the target establishes it. When systemd-resolved's bounded per-link runtime records exist, the provider reports interface DNS servers, routing/search domains, and DNSSEC configuration. Configuration presence is separate from resolution success. It never reads wireless credentials, VPN keys, NetworkManager connection secrets, or arbitrary configuration paths.

Backend detection reports observed NetworkManager and systemd-networkd runtime state plus Netplan and ifupdown configuration-layer presence. A runtime directory does not prove that a backend manages each interface, so per-interface backend labels state that ownership is not asserted. Netplan is always described as a configuration layer.

## Inventory and response model

`GET /api/v1/host/network` returns `summary`, `capabilities`, `backends`, `interfaces`, `routes`, and `dns`. Dedicated read-only endpoints return interface lists and validated detail, routes, DNS, and current statistics.

Interfaces include kernel index and name, link kind, physical/virtual/container classification, MAC address, administrative and operational state, carrier, MTU, speed, duplex, driver, flags, IPv4/IPv6 addresses and scopes, parent/master relationships, backend observation, counters, sampled rates, and observed warnings. VirtIO is virtual. Linux bridge, VLAN, bond, veth, WireGuard, loopback, Docker bridge, tunnel, and other `ip` link kinds retain their kernel kind. An interface can disappear between snapshots; detail then returns not found.

Routes retain address family, destination, next hop, source, interface, metric, table, protocol, scope, and type. Multiple defaults and policy tables remain separate. Container routes are labeled and excluded from the host-default count. The API does not infer a single internet gateway.

Traffic rates are computed between requests from monotonic counter differences. A first sample, counter reset, interface replacement, or interval shorter than 100 ms has no rate. Rates are never negative. The browser requests a bounded current sample every five seconds only while the Networking view is visible. No history is stored and virtual traffic is not added to physical traffic.

Health is an observation. Interfaces plus a non-container default route and configured resolver produce `healthy`, without claiming connectivity. Missing default route or resolver produces `not_configured`; it is not automatically a host failure. Counter errors and drops remain facts on the interface because intentional or old counters lack a universal failure threshold.

## Controlled diagnostics

`POST /api/v1/host/network/diagnostics` accepts one typed operation: `dns`, `tcp`, `route`, `icmp`, or `gateway`. It requires the existing administrator session, a same-origin request, and a valid CSRF token. The JSON body is limited to 4 KiB and rejects unknown fields.

DNS lookup accepts only a syntactically valid bounded hostname or IP and returns at most 32 addresses. It does not connect to returned addresses. TCP, route, and ICMP accept only targets approved by server policy. Configured non-container gateways and resolver addresses are approved automatically. Operators can add exact host/IP values or explicit CIDRs with `KITPRO_HOST_DIAGNOSTIC_TARGETS`. TCP is restricted to ports 53, 80, and 443 by default; `KITPRO_HOST_DIAGNOSTIC_PORTS` replaces that list. These settings are operator configuration, never request fields.

The process allows two concurrent diagnostics and ten starts per minute. Every operation has a four-second context deadline. TCP uses Go's bounded dialer. Route lookup uses fixed `ip -json route get`. ICMP uses fixed `/usr/bin/ping -n -c 1 -W 2 -- TARGET`, with bounded output; if unprivileged ping is unavailable, the capability is unsupported. No shell, caller-provided executable, arbitrary flag, port range, scan loop, persistent probe, or raw-socket capability exists. Audit logs retain only operation type and outcome category, never the target or result payload.

## Transactional configuration boundary

The Networking page preserves compact interface and route tables and dense mobile rows. Eligible interfaces expose typed DHCP or static IPv4, gateway, DNS, and search-domain inputs. The preview shows exact current and candidate digests, protection decisions, recovery availability, and the confirmation deadline. Transaction progress is reported as validating, recovery armed, applying, awaiting confirmation, confirmed, rolling back, rolled back, failed, or outcome unknown. The browser countdown is informational; rollback is controlled by the server-side deadline.

Mutation is disabled by default and requires `KITPRO_HOST_NETWORK_MANAGEMENT_ENABLED=1`, `host.network.manage`, CSRF protection, exact interface confirmation, and a versioned root-owned policy. Each policy entry names one exact kernel interface, one `systemd-networkd` profile, permitted address modes, and permitted fields. NetworkManager and Netplan remain read-only mutation capabilities in alpha.15. Unknown, multiply managed, container, loopback, master/slave, default-route, or management-path interfaces fail closed. The helper repeats live ownership, link, default-route, and policy checks immediately before dispatch.

The helper writes only the exact KITPro-owned `90-kitpro-<resource>.network` profile using typed content and atomic publication. It captures a protected previous-state digest, persists a recovery record, and arms an independent systemd timer before publication. The timer invokes the fixed helper recovery entry point and does not depend on the browser, API process, or changed interface. Apply verifies the exact observed IPv4 address set on two consecutive bounded observations and then waits for an exact transaction confirmation. This prevents asynchronous networkd overlap from becoming false success. Expiry, explicit rollback, and startup recovery restore the previous verified profile only when its digest still matches; concurrent external edits produce an uncertain outcome instead of being overwritten. A typed helper status operation lets API reads reconcile independently completed recovery records after API or helper interruption.

The initial mutation scope excludes primary management interfaces, arbitrary routes, IPv6 mutation, bridges, bonds, VLANs, VPNs, firewall rules, Docker networking, raw backend files, and caller-selected commands. Qualification fixtures must be guest-local, disposable, and disconnected from production bridges. Evidence from a dummy interface does not establish that changing a primary management interface is safe.

## Qualification boundary

Transactional mutation is qualified only for exact systemd-networkd-owned, policy-approved disposable interfaces with independent rollback. Primary management interfaces and other backend mutations remain disabled or unqualified.

## Platform notes

Ubuntu and other Debian-family systems require `iproute2` and optionally expose Netplan, NetworkManager, or systemd-networkd. KITPro Linux and other Arch-family systems use the same kernel and `iproute2` interfaces without requiring Netplan or NetworkManager. Rocky remains Experimental and uses the RPM `iproute` and `iputils` packages. AppArmor/SELinux and the installed API identity must be qualified on each platform. DNS interface detail depends on bounded systemd-resolved runtime data; NetworkManager-specific per-link DNS beyond resolv.conf remains a known limitation.
