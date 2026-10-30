#!/usr/bin/env bash
# Run on a designated disposable installed host as root. Prints no journal entries,
# environment values, credentials, or host network addresses.
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
    printf 'Run as root on a verified disposable qualification host.\n' >&2
    exit 2
fi
for tool in systemctl runuser busctl journalctl id uname ip python3; do
    command -v "$tool" >/dev/null || { printf 'Missing tool: %s\n' "$tool" >&2; exit 2; }
done
id kitpro-api >/dev/null || { printf 'kitpro-api account absent\n' >&2; exit 2; }
systemctl cat kitpro-api.service >/dev/null 2>&1 || { printf 'kitpro-api unit absent\n' >&2; exit 2; }

printf 'OS: '
if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    (. /etc/os-release; printf '%s %s\n' "${ID:-unknown}" "${VERSION_ID:-unknown}")
else
    printf 'unavailable\n'
fi
printf 'Kernel: %s\n' "$(uname -r)"
printf 'Architecture: %s\n' "$(uname -m)"
printf 'systemd: %s\n' "$(systemctl --version | head -n 1)"
printf 'API package: '
if command -v dpkg-query >/dev/null && dpkg-query -W -f='${Version}\n' kitpro-server 2>/dev/null; then :
elif command -v pacman >/dev/null && pacman -Q kitpro-server 2>/dev/null; then :
else printf 'not installed or unsupported package format\n'; fi
printf 'API service account: %s\n' "$(systemctl show kitpro-api.service -p User --value)"
printf 'API service group: %s\n' "$(systemctl show kitpro-api.service -p Group --value)"
printf 'API service state: %s\n' "$(systemctl show kitpro-api.service -p ActiveState --value)"
printf 'API account in systemd-journal group: '
if id -nG kitpro-api | tr ' ' '\n' | grep -Fxq systemd-journal; then printf 'yes\n'; else printf 'no\n'; fi

printf 'Network links as API account: '
if count=$(runuser -u kitpro-api -- ip -json -details link show 2>/dev/null | python3 -c 'import json,sys; value=json.load(sys.stdin); assert isinstance(value,list) and len(value) <= 512; print(len(value))'); then
    printf '%s interfaces (details suppressed)\n' "$count"
else
    printf 'query failed\n'
fi
printf 'Network routes as API account: '
if count=$(runuser -u kitpro-api -- ip -json route show table all 2>/dev/null | python3 -c 'import json,sys; value=json.load(sys.stdin); assert isinstance(value,list) and len(value) <= 4096; print(len(value))'); then
    printf '%s routes (details suppressed)\n' "$count"
else
    printf 'query failed\n'
fi
printf 'Resolver configuration as API account: '
if runuser -u kitpro-api -- test -r /etc/resolv.conf; then printf 'readable (content suppressed)\n'; else printf 'unavailable\n'; fi

printf 'Systemd D-Bus as API account: '
if runuser -u kitpro-api -- busctl --system call \
    org.freedesktop.systemd1 /org/freedesktop/systemd1 \
    org.freedesktop.systemd1.Manager ListUnitFiles >/dev/null 2>&1; then
    printf 'query succeeded\n'
else
    printf 'query failed\n'
fi

# Count bytes only. A zero-byte result is inconclusive: it can mean an empty
# journal or insufficient permission on hosts where journalctl exits zero.
printf 'System journal as API account: '
if bytes=$(runuser -u kitpro-api -- journalctl --system --no-pager --quiet \
    --output=json --lines=1 2>/dev/null | wc -c); then
    if [[ ${bytes} -gt 0 ]]; then printf 'entry readable (content suppressed)\n';
    else printf 'no readable entry (inconclusive)\n'; fi
else
    printf 'query failed\n'
fi
