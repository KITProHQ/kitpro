# Known alpha limitations

- Only the platforms in the support matrix are certified.
- Rootful Docker and enforcing AppArmor are required.
- The catalog is intentionally limited; arbitrary Compose and user manifests
  are not accepted.
- Application-data backup, HA, clustering, and automatic disaster recovery
  are not implemented.
- Application updates are trusted-catalog and administrator initiated; native
  package managers remain authoritative for KITPro updates.
- Rollback of irreversible schema migrations is not promised.
- LAN exposure binds one configured host address; wildcard/public-Internet
  exposure, reverse proxy, domains, and TLS automation are out of scope.
