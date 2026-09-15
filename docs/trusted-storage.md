# Add trusted storage and install Jellyfin

## Add a storage location

1. Mount local, NFS, or SMB storage with the operating system. Confirm the
   mount is available before registering it.
2. Open **Settings**, then find **Storage**.
3. Enter a clear name such as `Movies NAS` and its absolute host path.
4. Select **Read only** unless an application must modify the imported files.
5. Select **Add storage location**.

KITPro validates the path immediately. Protected system paths, symbolic-link
aliases, missing mounts, and duplicate locations are rejected. Locations must
be dedicated directories below `/mnt`, `/media`, `/data`, or `/srv`.

## Install Jellyfin

1. Add a trusted root containing media and allow read-only access.
2. Open **Catalog** and locate **Jellyfin**.
3. Under **Media library**, choose the approved root.
4. Install Jellyfin, choose a controlled access mode, and complete Jellyfin's
   browser setup.

Jellyfin config and cache are managed by KITPro. Media is imported read-only.
Removing Jellyfin does not remove the media library.

## Troubleshooting

**Storage unavailable** means the directory, filesystem, mount source, mount
point, device identity, or root inode no longer matches registration. Restore
the original mount, then recreate the application. Do not create an empty
directory as a substitute for a missing NAS mount.

**Permission denied** means the container process cannot read the imported
files or the root was deliberately mounted read-only. Adjust host filesystem
permissions without granting write access unless the application and root both
require it.

**Mount identity changed** after replacing storage means KITPro cannot know
whether the new filesystem is intended. Register it as a new trusted root and
explicitly rebind the application.

Control-plane backups preserve root definitions, policies, and application
bindings. They do not back up imported media or files; those remain the storage
owner's responsibility.
