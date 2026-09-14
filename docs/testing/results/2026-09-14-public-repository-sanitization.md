# Public repository sanitization

Date: 2026-09-14

## Remote and tree state

- Private canonical remote: `origin` → private Forgejo (`10.10.0.20:2222`)
- Public mirror remote: `github` → `https://github.com/KITProHQ/kitpro.git`
- Branch: `main`
- Pre-push HEAD matched private `origin/main`.
- Working tree was clean before the mirror operation.
- The private `v0.1.0-alpha.1` tag was not pushed.

## Sanitization results

- Current-tree sensitive-pattern scan: PASS. No credentials, tokens, cookies,
  private keys, certificates, database files, sockets, or environment files
  are tracked.
- Full Git-history scan: PASS. No temporary validation password, private key,
  bearer token, cloud credential, or authorization header was found.
- Internal-reference review: PASS. Historical VM/Proxmox addresses and
  usernames remain only in immutable validation evidence or test fixtures;
  public onboarding contains no private Forgejo URL or VM credential.
- Personal-information review: PASS. No private contact details or unrelated
  personal data are present.
- Large-file review: PASS. The repository is 301 tracked files and the Git
  object database is under 1 MiB packed; no ISO, VM image, database dump, or
  obsolete package artifact is tracked.
- License review: no project license file is currently present. No license was
  invented or changed in this task; this is a follow-up public-release issue.
- README/docs review: PASS. README, support matrix, quickstart, security,
  contribution, catalog, and known-limitations docs are linked and public
  facing. The package/release docs distinguish prepared alpha artifacts from
  the private tag.
- GitHub template review: PASS. Bug, app-request, platform, and security
  redirect templates are present; the security template directs private
  reporting rather than public disclosure.
- CI review: no GitHub Actions workflow exists, so no private runner, secret,
  or publish configuration is exposed. CI can be added after repository setup.
- Relative Markdown-link validation: PASS (0 broken links).
- JSON validation and `git diff --check`: PASS.

## AI and development-process reference review

- Current-tree audit: PASS. Outside this audit record, no references to
  ChatGPT, OpenAI, Codex, gstack, Claude, Gemini, LLMs, prompts, or
  AI-assisted authorship remain in public documentation or source comments.
  The remaining uses of “agent” describe
  technical actors or validation tooling, and “AI features” describes an
  explicitly out-of-scope product capability; both are legitimate project
  terminology rather than development provenance.
- Historical audit: two early architecture commits contain the word `prompt`
  in ordinary security guidance, and two early commits contain `LLM` in
  product-scope/architecture text. These are legitimate technical or product
  references, not coding-assistant attribution. Historical validation notes
  previously named the local sandbox tool; those current files were generalized
  to “validation sandbox” without changing their measured results.
- No commit subject or author trailer contains coding-agent attribution. The
  two superseded validation notes in the early history named the local Codex
  sandbox; those references are internal development-process detail, not a
  secret. No history rewrite is warranted: removing them would require
  rewriting private canonical history and would add risk without improving
  credential or security posture. If a sanitized public history is later
  required by policy, create it as a separately reviewed mirror branch rather
  than rewriting `origin` in place.

## Mirror policy

Only `main` is mirrored to GitHub. Tags, including the private
`v0.1.0-alpha.1`, are excluded. No GitHub release is created in this task.
