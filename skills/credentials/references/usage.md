# cred — complete usage guide

`cred` stores service credentials in a **passphrase-encrypted vault file** and
injects them into a command's environment without ever printing a value. There
is no `get` verb: a secret value exists only inside the child process. It is
cross-platform — the only dependency is `openssl`.

- Deployable entrypoint: `skills/credentials/scripts/cred.sh` (and the compiled
  `scripts/cred-run` binary it delegates `run` to).
- Source (not deployed): `src/credentials/` — build with
  `nix develop -c bash src/credentials/build.sh`.

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
| `cred unlock` | human | decrypt vault into a 5-min cache |
| `cred lock` | human | wipe the cache now |
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

`unlock` decrypts the vault into a plaintext cache (`~/.config/cred/unlocked`,
0600) that auto-expires 300s after unlock; `lock` wipes it immediately. An agent
**cannot** unlock it (no TTY); if `cred run` reports locked, tell the human.

### `cred run <profile> -- <cmd> [args]`

```bash
cred run github -- gh api user
cred run github -- sh -c 'curl -s -H "Authorization: Bearer $GH_TOKEN" https://api.github.com/user'
```

- Reads the profile, resolves every `@secret` from the unlocked cache, injects
  all vars as environment variables, and `exec`s the command.
- Replaces every secret value with `***` in stdout **and** stderr before it
  reaches you.
- Enforces the profile's `allow =` list on the command's first word.
- Fails fast if the vault is locked (no unlocked cache, or its 300s TTL has
  expired) — it does not hang.

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
- Output is scrubbed on both streams. Values shorter than 6 chars are **not**
  scrubbed (redacting them would mangle ordinary text) — don't store short
  secrets.
- A locked vault is a real boundary: the secrets exist only as ciphertext, and
  the plaintext cache is gone. `cred run` converts that into a fast, clear
  failure.

## 5. Known limits (do not paper over)

1. **The lock is the only real boundary.** At rest the vault is AES-256-CBC
   (PBKDF2, `-iter 200000`) behind the passphrase you set at `init` — there is
   no default. While *unlocked*, the plaintext cache `~/.config/cred/unlocked`
   (0600) is readable by any same-user process, including an agent. A *locked*
   vault has no cache and is ciphertext. This stops accidental leaks and idle
   exfiltration; it is not proof against an agent that reads the cache during
   an open window.
2. **`cred add` writes the plaintext secret to a 0600 temp file for ~ms**, and
   the passphrase travels via `openssl`'s environment (never argv). A sibling
   same-user process polling that instant could catch either.
3. **Allow-list checks only the first word.** `cred run github -- gh api repo …`
   is allowed; arguments are not screened. Keep the list tight.
4. **Secrets shorter than 6 chars are not scrubbed.** They can leak into
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
| `cred run` → `cred-run binary not built` | `nix develop -c bash src/credentials/build.sh` |
| `cred run` → `profile has no 'allow =' line` | add `allow = …` to the profile |
| `cred run` → `'X' not in profile allow-list` | add `X` to `allow =`, or don't run `X` |
| `cred run` → `no secret for svc/VAR` | `cred add svc VAR` first |
| secret still shows in output | it's <6 chars (not scrubbed), or the command printed it in a form the scrubber can't match |
