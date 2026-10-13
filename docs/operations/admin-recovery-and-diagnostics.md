# Recover an administrator and collect diagnostics

## Reset an administrator password

Run the reset command from a root shell on the KITPro host:

```text
kitpro-server admin reset-password [username]
```

The command requires a real local root identity. The HTTP API and helper
protocol do not expose password reset. The command reads the new password from
the terminal, updates the password hash, advances the global session epoch,
and revokes all existing sessions. It does not write the plaintext password to
logs or operation records.

Sign in again on every browser after a reset. A session created before the
reset is invalid even if its cookie has not expired.

## Generate a support bundle

Use the authenticated diagnostics endpoint to create a support bundle. The
bundle contains build and OS identity, runtime status, safe installation and
binding metadata, ingress and certificate status, provider health, readiness,
and bounded operation outcomes.

KITPro excludes passwords, application secret values, API tokens,
certificate private keys, cookies, session material,
authorization headers, and sensitive environment variables. Generation fails
closed when the redaction scan finds a private-key marker or a synthetic known
secret.

A support bundle is not a host backup. It cannot restore applications,
certificates, provider credentials, or databases.
