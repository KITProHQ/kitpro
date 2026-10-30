# Alpha.15 read-only Services and Logs

Status: Implemented and installed-service qualified on Ubuntu and KITPro Linux. Rocky Linux remains Experimental.

## Service inventory

The API uses the upstream `go-systemd/v22/dbus` binding on the system bus. It calls read-only systemd manager methods to list service unit files and loaded units, then joins their observed state. This includes inactive, disabled units that are absent from `ListUnits`. Detail requests retrieve bounded Unit and Service properties, including dependency names, main PID, and non-secret failure result/exit status. No unit file content, process environment, or command line is returned. No systemd mutator is imported or exposed through the HTTP API.

The service name must match a conservative `.service` identifier, with no path separators, wildcard, or arbitrary D-Bus path. A single response contains at most 50 services; the backend rejects inventories above 5,000 units and requests time out after four seconds. Search and state filters run on the server. `501` means no systemd runtime, `503` means the bus or collector is unavailable, and `404` means the requested unit is absent. D-Bus observations can race with service state changes. PID and detailed properties are omitted when not available; they are not fabricated.

## Journal access boundary

The production Debian package builds the API with `CGO_ENABLED=0`, so the libsystemd `sd-journal` C binding cannot be used without a separate packaging change. This milestone uses a dedicated, fixed `/usr/bin/journalctl` adapter with `--output=json`. It invokes no shell, accepts no arbitrary executable, and constructs only server-selected arguments from validated unit, priority, time, and cursor values. The adapter captures at most 4 MiB, scans at most 500 entries, and returns at most 100 entries with messages truncated to 2 KiB. Binary or malformed fields are omitted; journal text is treated as untrusted and rendered with `textContent`.

The journal endpoint requires an authenticated administrator session **and** `KITPRO_HOST_LOGS_ENABLED=1` in the API service environment. The latter is off by default, independent of Host Overview access. The unprivileged `kitpro-api` account also needs OS journal read permission. On standard systemd hosts, membership in `systemd-journal` is the minimal conventional way to grant all system-journal reads; it still exposes sensitive records to that account and requires operator review. No root permission, journal group membership, or policy relaxation is added by this milestone. Without the gate the API returns `403`; with the gate but insufficient OS permission it returns a sanitized `403` or `503`, depending on the host's journalctl error. The API never records query text, journal output, or raw backend errors in its own logs or support bundles.

The API accepts only a time range of up to 30 days, defaults to the last 24 hours, and performs case-insensitive literal message search within a bounded scan window. Cursor pagination uses journald's opaque cursor and reverse order; clients must not parse the cursor. Because `journalctl` forbids combining `--since` with `--cursor`, cursor pages enforce the original lower time bound in the backend after reading the bounded window. A search can yield fewer than the requested page size even when older matching entries exist. Journal rotation may invalidate a cursor; the client can start a fresh search. This adapter is a read-only bridge to journald's documented machine-readable interface, not a generic command executor.

## Platform and qualification

Debian 13, Ubuntu 26.04, supported Arch-family systems, and KITPro Linux are supported when systemd D-Bus and journald are present. Rocky Linux 10 remains Experimental. The API account's bus and journal permissions, systemd versions, SELinux/AppArmor policy, package dependencies, and actual cursor behavior must be qualified separately on each platform. A host without systemd reports unsupported for services. Log access is denied until explicitly configured and OS-permitted. No KITPro Linux package or Installer 1.1 RC1 integration artifact changes are made here.

Tests use fake D-Bus and journal backends for filtering, pagination, invalid input, missing units, unavailable facilities, authorization, and safe rendering. Live service actions, storage changes, network changes, and destructive tests are outside this milestone. Platform acceptance must use a recoverable nonproduction environment and the installed service identity.

## Interface presentation contract

The main Services inventory is a semantic desktop table with one compact row per unit and columns for canonical identity, description, active state, startup state, PID, management classification, and a compact action selector. Search, policy/state filters, bounded offset pagination, row selection, service details, eligibility previews, confirmations, audit history, and associated Logs navigation remain available. Detail panels may use grouped cards because they describe one selected unit.

The main Logs inventory is a semantic table with timestamp, severity, unit/source, and message columns. Messages use monospaced text and a bounded two-line collapsed presentation; expanding a row reveals the full safely rendered message and structured metadata. Existing priority, time, unit, text, and cursor controls remain authoritative. Journal text is inserted through `textContent`, never interpreted as HTML.

At phone widths both tables become dense separated row lists: Services retains identity, status, management classification, and actions; Logs retains timestamp, severity, source, and the bounded message. This responsive transformation must not reintroduce large per-record cards. Explicit frontend assertions protect the table/list contract and pagination behavior.
