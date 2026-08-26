---
name: credentials
description: Store and inject service credentials so agents can reach authenticated APIs without a secret value ever landing in the transcript. Use when a task needs an API token, key, password, or OAuth credential for a service (GitHub, AWS, npm, Docker, a web API), when an agent must run a command that talks to an authenticated endpoint, or when setting up a new service's credentials. Triggers include "credential", "API key", "token", "auth", "authenticate", "access a service", "connect to a service", "log in to a service", ".env", "secrets". Do NOT use for general app code, or for hashing/encryption logic — only for storing and injecting credentials.
metadata:
  audience: personal
  domain: credentials
---

# credentials

`cred` stores secrets in a **passphrase-encrypted vault file**
(`~/.config/cred/vault`) and injects them into a command's environment without
printing them. There is deliberately **no `get` verb** — a secret value exists
only inside a child process, never in your output. It is cross-platform: the
only dependency is `openssl`.

The entrypoint is `scripts/cred.sh` (a thin dispatcher for the interactive
verbs) plus the compiled Rust binary `scripts/cred-run` (resolve + inject +
scrub — the `run` verb). Executing them is the whole point: run them, do not
read their values into context.

The `cred-run` binary is gitignored — it is built from source in
`src/credentials/` (outside this skill, so deploy only ships the binary and
`cred.sh`, not the Rust source). Build it once with
`nix develop -c bash src/credentials/build.sh`. If `cred run` reports
"cred-run binary not built", run that command.

## Working rules (non-negotiable)

1. **Reference, never value.** A profile holds `@secret` references; `cred run`
   resolves them at execution time. Never put a real secret in a profile, in a
   command you type, or in output.
2. **No `get`.** If you are about to print, `echo`, or `cat` a credential, stop.
   The only verbs are below. There is no read-to-output path.
3. **`cred run`, not a manual `env` dance.** Never do
   `export TOKEN=... && curl ...` yourself — you will put the value in the
   transcript. `cred run` injects via `exec`'d environment, which is not
   visible in `ps` argv.
4. **Never pass a secret as a CLI argument.** `curl -H "Authorization: Bearer $TOKEN"`
   is fine because `$TOKEN` is expanded by the shell inside `cred run`; a literal
   token in argv is not — argv *is* visible to `ps`. Read env vars in the command,
   don't interpolate a value you have seen.
5. **Locked = ask the human.** If `cred run` reports `vault is LOCKED`, tell
   the user to run `cred unlock` themselves. You cannot do it (it needs a TTY).
   Do not retry, do not fall back to reading anything.

## Commands

```bash
cred init                         # one-time: create the encrypted vault (human)
cred add <profile> <VAR>          # store a secret — prompts, no echo (human)
cred set <profile> <VAR> <value>  # store a NON-secret var (e.g. API base URL)
cred list [profile]               # names only — never values
cred unlock                       # human unlocks for a 5-min window
cred lock                         # re-lock now
cred run <profile> -- <cmd> ...   # inject + exec + scrub
```

Where `cred` is `bash <repo>/skills/credentials/scripts/cred.sh` unless the
script is on `PATH`. `cred --help` / `cred help` print a quick summary;
`cred help run` shows the run-specific help.

**Read [references/usage.md](references/usage.md) when** you need the full
command reference — every flag, exit codes, profile format, worked examples,
and troubleshooting.

## Profile format

`~/.config/cred/profiles/<name>.env` — plaintext, agent-readable by design:

```ini
# a secret: resolved from the vault (profile=<profile>, var=<VAR>)
GH_TOKEN = @secret

# a non-secret var: passed straight through
GH_API   = https://api.github.com

# the command allow-list: cred run refuses anything else
allow    = gh git curl
```

The `allow` line is mandatory. Set it to exactly the commands the profile's
service needs. `cred run` fails closed if it's missing, and refuses any command
whose first word isn't listed.

## What `cred run` guarantees

- Secrets are injected as environment variables (safe from `ps`), never argv.
- Every secret value is replaced with `***` in stdout *and* stderr before it
  reaches you. Values shorter than 6 chars are skipped (redacting them would
  mangle ordinary text) — treat those as effectively unredacted.
- A locked vault fails fast with `vault is LOCKED`, it does not hang.

## Known limits (do not paper over)

- **The lock is the only real boundary.** At rest the vault is AES-256-CBC
  (PBKDF2, `-iter 200000`) behind a passphrase the human sets at `cred init` —
  there is no default password. While *unlocked*, the plaintext cache
  `~/.config/cred/unlocked` (0600) is readable by any same-user process,
  including an agent. The protection is that a *locked* vault has no such
  cache and is encrypted. This stops accidental leaks and idle exfiltration; it
  is not proof against an agent that reads the cache during an open window.
- **`cred add` writes the plaintext secret to a 0600 temp file for ~ms** and
  passes the passphrase via `openssl`'s environment (never argv). A sibling
  same-user process polling during that instant could catch either. `cred run`
  and `cred unlock` never put the *secret* in argv or env (only the passphrase
  briefly in `openssl`'s env on `add`/`unlock`).
- **The allow-list checks only the command's first word.** `cred run github -- gh api repo ...`
  is allowed; arguments are not screened. Keep the list tight to the tools you
  trust.
- **A secret shorter than 6 chars will not be scrubbed from output.** Don't
  store secrets under 6 characters, or accept that they can leak into output.

## Interaction style

- If credentials are needed and none are configured, say so and offer
  `cred add`, but never ask the user to paste a secret into the chat.
- If `cred run` says locked, hand the unlock to the user; don't work around it.
- Before reporting a command's output, trust that `cred run` scrubbed it — but
  never re-print a value you happened to observe anyway.
