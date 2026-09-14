# Disposable SELinux helper domain

This directory contains a test-only SELinux module for the Rocky Linux 10 privilege-boundary fixture. It is not a production policy package or a final file-context layout.

The policy creates a dedicated helper domain and separate types for the copied test executable, helper state, runtime socket, and approved test storage. It uses Rocky's installed `docker_stream_connect` policy interface instead of granting broad container administration.

The policy grants no generic TCP or UDP networking and no permission to execute shells or unrelated binaries. On Rocky, the disposable test required a temporary systemd `SELinuxContext` drop-in to enter the dedicated domain and narrow `init_t` socket-directory permissions; these are experiment observations, not production packaging decisions. Use the [MAC validation runbook](../MAC-RUNBOOK.md). Keep SELinux Enforcing throughout.
