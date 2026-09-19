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
only inside a child process, never in your output. It is cross-platform: it
needs only `bash`, `awk`, `find`, `mktemp` and `openssl`, plus the `cred-run`
binary for `run`.

The entrypoint is `scripts/cred.sh` (a thin dispatcher for the interactive
verbs) plus the compiled Rust binary `scripts/cred-run` (resolve + inject +
scrub — the `run` verb). Executing them is the whole point: run them, do not
read their values into context.

Two units, and the split is deliberate: `cred.sh` owns the whole vault/profile
contract — where profiles live, where the holder socket is, the relock window,
and `@secret` resolution — and is the only thing that defines it. `cred-run` in
`src/credentials/` is a build project with two halves that share one wire
protocol:

- **`cred-run hold`** — the executor. `cred unlock` starts it, it reads the
decrypted vault from stdin, and it keeps it **in memory** for the relock window.
  It owns everything that touches a secret: profile loading, the allow-list,
injection into the child's `envp`, and scrubbing. Nothing decrypted is ever
written to disk.
- **`cred-run run`** — a thin client. It sends `{profile, argv, terminal}` to the
holder, relays the scrubbed answer to its own stdout/stderr, and exits with the
holder's code. It never sees a secret value, so a client that is *not* cred-run
still gets scrubbed, allow-listed output — the scrub is not a property of the
caller.

cred.sh exports the contract as environment variables — `CRED_PROFILE_DIR`,
`CRED_HOLD_SOCK`, `CRED_TTL`, `CRED_SECRET_MARKER` to the holder, and only
`CRED_HOLD_SOCK` to the client — rather than letting either half re-derive it.
Each definition exists exactly once. Deploy ships `cred.sh` plus a binary, and
the Rust source never has to agree with the shell by coincidence.

The `cred-run` binary is gitignored — a derived artifact. From the repo root,
`bin/build-project.sh` builds it with nix and installs it into `scripts/`:

```bash
./bin/build-project.sh credentials     # nix build .#credentials, then install
```

If `cred run` reports "cred-run binary not built", run that command.

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
4. **Never pass a secret as a CLI argument** — argv *is* visible to `ps`. Note that
   `curl -H "Authorization: Bearer $TOKEN"` does **not** work: your shell expands
   `$TOKEN` before `cred run` ever sees it, so the header goes out empty. Have the
   command read its own environment instead, which keeps the value off argv:

   ```bash
   # preferred — no shell involved, so `allow = curl` stays tight
   cred run github -- curl -sS --variable %GH_TOKEN \
     --expand-header 'Authorization: Bearer {{GH_TOKEN}}' https://api.github.com/user

   # general fallback — single quotes, so the child shell expands it; needs `sh`
   # in the allow-list, which only checks the command's first word
   cred run github -- sh -c 'gh api user --header "Authorization: Bearer $GH_TOKEN"'
   ```
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

Where `cred` is `bash <repo>/skills/credentials/scripts/cred.sh`. Nothing puts
that on your `PATH` — the skill deploys as a folder, so use the path; in every
example below, `cred` means that command. `cred --help` / `cred help` print a
quick summary; `cred help run` shows the run-specific help.

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
- Secrets never leave the holder process; the client only relays scrubbed output.
- Every secret value is replaced with `***` in stdout *and* stderr before it
  reaches you. Values shorter than 6 chars are skipped (redacting them would
  mangle ordinary text) — treat those as effectively unredacted.
- A closed window fails fast with `vault is LOCKED`, it does not hang. `cred run`
  needs the window open even when the profile has no secrets.

## Known limits (do not paper over)

- **Same-uid is not a boundary, and the holder does not pretend to be one.**
  While the window is open the vault is in the holder's memory, so any process
  running as you can use the holder (that is what `cred run` does) and a
  debugger can read it. What the holder buys is that the vault is **never on
  disk** — no plaintext cache, nothing surviving the window. It is not proof
  against a determined same-user agent.
- **The scrub stops accidents, not intent.** `cred run svc -- sh -c 'base64 <<< "$TOKEN"'`
  prints the secret in a form the scrubber cannot match. Coming through
  `cred run` does not make output safe to re-publish.
- **`cred add` writes the plaintext secret to a 0600 temp file for ~ms** and
  passes the passphrase via `openssl`'s environment (never argv). A sibling
  same-user process polling during that instant could catch either. `cred unlock`
  creates no temp file — it decrypts straight into the holder's stdin.
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
