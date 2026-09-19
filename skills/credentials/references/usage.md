# cred — complete usage guide

`cred` stores service credentials in a **passphrase-encrypted vault file** and
injects them into a command's environment without ever printing a value. There
is no `get` verb: a secret value exists only inside the child process. It is
cross-platform — it needs `bash`, `awk`, `find`, `mktemp` and `openssl`, plus the
`cred-run` binary for `run`.

- Deployable entrypoint: `skills/credentials/scripts/cred.sh` (and the compiled
  `scripts/cred-run` binary it delegates `run` to).
- Source (not deployed): `src/credentials/` — a build project, exposed as
  `packages.credentials` in the repo flake. Build and install it with
  `./bin/build-project.sh credentials` from the repo root, which runs
  `nix build .#credentials` and installs the artifact into `scripts/`.

## 1. Setup (once)

```bash
cred init
```

Creates:

- the encrypted vault `~/.config/cred/vault` (passphrase you set now; relocks
  300s after unlock),
- the profile dir `~/.config/cred/profiles/`.

> ⚠️ The passphrase you type here is the only thing protecting the vault. Pick a
> real one — there is no default. Anyone who knows it (and can read the vault
> file) can decrypt every secret.

## 2. Commands

| Command | Who runs it | What it does |
|---|---|---|
| `cred init` | human, once | create encrypted vault + profile dir |
| `cred add <profile> <VAR>` | human | store a secret (hidden prompt) |
| `cred set <profile> <VAR> <value>` | human | store a non-secret var |
| `cred list [profile]` | anyone | names only, never values |
| `cred unlock` | human | decrypt into a holder process for 5 min |
| `cred lock` | human | close that window now |
| `cred run <profile> -- <cmd> [args]` | agent or human | inject + exec + scrub |

`cred help` / `cred --help` / `cred -h` print the same summary. `cred help run`
delegates to `cred-run --help`.

### `cred init`

```bash
cred init
```

Idempotent: refuses to touch an existing vault. Prompts for the passphrase
twice (hidden) to set it.

### `cred add <profile> <VAR>`

```bash
cred add github GH_TOKEN
# secret for github/GH_TOKEN (hidden):  <paste, press enter>
```

- Reads the secret with echo off (`read -s`) — never appears in the transcript.
- Rejects empty secrets.
- Prompts for the vault passphrase (hidden) and re-encrypts the whole vault.
- Writes `VAR=@secret` into the profile (a *reference*, not the value).
- Verifies the stored value by reading it back and comparing (without printing).
- Known limit: the plaintext secret is written to a 0600 temp file for ~ms.

### `cred set <profile> <VAR> <value>`

```bash
cred set github GH_API https://api.github.com
```

For non-secret vars (base URLs, regions, config). Stored plaintext in the
profile — visible to anyone with file access, by design.

### `cred list [profile]`

```bash
cred list              # all profile names
cred list github       # vars in one profile (values shown, secrets as @secret)
```

Never prints a secret value — secret vars render as `@secret`.

### `cred unlock` / `cred lock`

```bash
cred unlock   # prompts for the vault passphrase (needs a TTY)
cred lock
```

`unlock` decrypts the vault **in memory** and hands it to a detached holder
process (`cred-run hold`) that keeps it for 300s and serves every `cred run`
until the window closes. `lock` kills the holder immediately.

- **Nothing decrypted is written to disk.** There is no plaintext cache file —
  the holder's memory is the only place a secret exists between `unlock` and the
  window closing.
- The window is a fixed 300s from unlock; it does not extend on use.
- Every `cred run` reuses the same holder: you unlock once, not per command.
- An agent **cannot** unlock it (needs a TTY); if `cred run` reports locked,
  tell the human.
- `cred add` while a holder is live replaces it, so the new secret is visible.

### `cred run <profile> -- <cmd> [args]`

```bash
cred run github -- gh api user
cred run github -- sh -c 'curl -s -H "Authorization: Bearer $GH_TOKEN" https://api.github.com/user'
```

- `cred run` is a thin client of the holder: it sends `{profile, command,
  terminal}` and relays the answer. Loading the profile, checking `allow =`,
  injecting, and scrubbing all happen inside the holder — so any client that can
  open the socket, not just `cred run`, gets the same allow-list and the same
  scrubbing.
- Resolves every `@secret` from the vault held in the holder's memory and
  injects all vars as environment variables.
- Replaces every secret value with `***` in stdout **and** stderr before it
  reaches you.
- Enforces the profile's `allow =` list on the command's first word.
- Gives the command your terminal as stdin when you have one, so an interactive
  command can still prompt; without a terminal it gets `/dev/null`.
- Fails fast if the window is not open — no holder, a holder that has closed its
  window, and a socket left behind by a killed one all read as locked. It does
  not hang.
- Needs the window open even when the profile has no secrets: a run that
  silently proceeded without credentials would be worse than a refusal.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | command ran |
| 1 | usage / generic error |
| 2 | vault locked |
| 3 | secret not found |
| 5 | profile missing an `allow =` line |
| 6 | command not in the allow-list |

## 3. Profile format

`~/.config/cred/profiles/<name>.env` — plaintext, agent-readable by design.

```ini
# a secret: resolved from the vault (profile=<profile>, var=<VAR>)
GH_TOKEN = @secret

# a non-secret var: passed straight through
GH_API   = https://api.github.com

# the command allow-list: cred run refuses anything else
allow    = gh git curl
```

Rules:

- `allow =` is **mandatory**. `cred run` fails closed (exit 5) without it.
- `@secret` means "look up vault entry `profile=<profile>, var=<VAR>`".
- Non-secret values are plain `KEY=value` lines. `#` starts a comment — a whole
  line, or anything after whitespace on a value line (`KEY = value # note`).

## 4. The safety model (what `cred run` guarantees)

- Secrets go into the child via `envp` — the one Unix channel `ps` cannot see.
  They never appear in argv, in this process's output, or in the transcript.
- **Secrets never leave the holder.** The holder is a separate process holding
  the decrypted vault; the client sends a command and gets scrubbed output back.
  The protocol has no request that returns a value, so a client that is not
  `cred run` cannot ask for one — and still gets scrubbed, allow-listed output.
- Output is scrubbed on both streams, inside the holder. Values shorter than 6
  chars are **not** scrubbed (redacting them would mangle ordinary text) — don't
  store short secrets.
- A closed window is a real boundary: the vault exists only as ciphertext, and
  the holder's memory is gone. `cred run` converts that into a fast, clear
  failure.

## 5. Known limits (do not paper over)

1. **Same-uid is not a boundary, and the holder does not pretend to be one.**
   While the window is open the decrypted vault is in the holder's memory. Any
   process running as you can talk to the holder's socket
   (`~/.config/cred/hold.sock`, 0600) and run commands through it — the same
   authority `cred run` grants — and a debugger or `ptrace` gets the memory.
   What the holder does buy is that the vault is **never written to disk**: a
   plain `cat` of a cache file no longer yields every secret, and nothing
   survives the window. It is not proof against a determined same-user agent.
2. **The scrub stops accidents, not intent.** It replaces literal values in the
   child's output. `cred run svc -- sh -c 'base64 <<< "$TOKEN"'` prints the
   secret in a form the scrubber cannot match. Coming through `cred run` does
   not make output safe to re-publish.
3. **`cred add` writes the plaintext secret to a 0600 temp file for ~ms**, and
   the passphrase travels via `openssl`'s environment (never argv). A sibling
   same-user process polling that instant could catch either. `cred unlock`
   creates no temp file — it decrypts straight into the holder's stdin.
4. **Allow-list checks only the first word.** `cred run github -- gh api repo …`
   is allowed; arguments are not screened. Keep the list tight.
5. **Secrets shorter than 6 chars are not scrubbed.** They can leak into
   command output.

## 6. Examples

GitHub:

```bash
cred add github GH_TOKEN
cred set github allow "gh git"
cred run github -- gh api user
```

npm:

```bash
cred add npm NPM_TOKEN
cred set npm allow "npm"
cred run npm -- sh -c 'npm publish --dry-run'
```

Multiple vars in one profile:

```bash
cred add aws AWS_SECRET_ACCESS_KEY
cred set aws AWS_REGION us-east-1
cred set aws AWS_ACCESS_KEY_ID AKIA...
cred set aws allow "aws"
cred run aws -- aws sts get-caller-identity
```

## 7. Troubleshooting

| Symptom | Fix |
|---|---|
| `cred run` → `vault is LOCKED` | human runs `cred unlock` |
| `cred unlock` → `the holder did not come up` | wrong passphrase, or a stale build: `./bin/build-project.sh credentials` |
| `cred run` → `cred-run binary not built` | `./bin/build-project.sh credentials` |
| `cred run` → `profile has no 'allow =' line` | add `allow = …` to the profile |
| `cred run` → `'X' not in profile allow-list` | add `X` to `allow =`, or don't run `X` |
| `cred run` → `no secret for svc/VAR` | `cred add svc VAR` first |
| secret still shows in output | it's <6 chars (not scrubbed), or the command printed it in a form the scrubber can't match |
