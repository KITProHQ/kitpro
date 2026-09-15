# Use media and data-heavy applications

KITPro keeps application state separate from imported libraries. Register a
dedicated storage root in **Settings**, then select it on the catalog card.

## Stream music with Navidrome

1. Register a music directory as read-only.
2. Select that root under **Music library** on the Navidrome card.
3. Install the application and open its web service through a controlled
   loopback or LAN exposure.
4. Create the first administrator in Navidrome. The source music remains
   read-only; the database and cache live in KITPro-managed storage.

## Stream audiobooks with Audiobookshelf

Register the audiobook library read-only. KITPro keeps `/config` and
`/metadata` locally in managed storage; do not relocate the SQLite database to
a network filesystem. Complete the first-user setup in the web interface.

## Manage files with SFTPGo

Register a disposable or backed-up directory with **Read and write** access.
Select it under **Managed files**, install SFTPGo, and create the first
administrator at `/web/admin/setup`. SFTPGo can create, rename, and delete
files inside that root, so keep an independent backup.

KITPro allows only one installation to hold a writable root. Readers may share
a root with other readers, but no reader can join an active writer.

## Verify isolation

Open the installed application's **Storage** panel. It shows the logical root,
mode, and availability. Recreating or removing the runtime preserves both
managed state and imported data. Deleting imported data is never part of the
application lifecycle.

If a mount disappears or changes identity, KITPro stops or blocks the app and
shows **Storage unavailable**. Restore the original mount or register a new
root; never create an empty directory in place of a missing NAS mount.
