# cred — complete usage guide

`cred` stores service credentials in a **passphrase-encrypted vault file** and
injects them into a command's environment without ever printing a value. There
is no `get` verb: a secret value exists only inside the child process. It runs on
macOS and Linux — the Keychain path (`cred remember`) is macOS-only — and needs a
POSIX shell plus the usual Unix tools (`bash`, `awk`, `find`, `mktemp`, `openssl`,
`head`, `od`, `tr`, `date`, `seq`, `ps`, `sort`, `chmod`, `nohup`, `kill`,
`basename`) and the `cred-run` binary for `run`.

- Deployable entrypoint: `skills/credentials/scripts/cred.sh` (and the compiled
  `scripts/cred-run` binary it delegates `run` to).
- Source (not deployed): `src/credentials/` — a build project, exposed as
  `packages.credentials` in the repo flake. Build and install it with
  `./bin/build-project.sh credentials` from the repo root, which runs
  `nix build .#credentials` and installs the artifact into `scripts/`.

Nothing installs `cred` on your `PATH` — the skill deploys as a folder, so every
example below assumes this definition in the shell that runs it (from the repo
root that holds this skill; use an absolute path otherwise):

```bash
cred() { bash skills/credentials/scripts/cred.sh "$@"; }
```

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
| `cred remember` | human, once | store the vault passphrase in the Keychain |
| `cred add <profile> <VAR>` | human | store a secret (hidden prompt) |
| `cred set <profile> <VAR> <value>` | human | store a non-secret var |
| `cred list [profile]` | anyone | names and non-secret values; secrets as @secret |
| `cred unlock` | human or agent | open a window (approve a dialog, or type on a TTY) |
| `cred lock` | anyone | close that window now |
| `cred run <profile> -- <cmd> [args]` | agent or human | open a window if closed, then inject + run + scrub |

`cred help` / `cred --help` / `cred -h` print the same summary. `cred help run`
delegates to `cred-run --help`.

### `cred init`

```bash
cred init
```

Idempotent: refuses to touch an existing vault. Prompts for the passphrase
twice (hidden) to set it.

### `cred remember`

```bash
cred remember
# vault passphrase (hidden):  <type it one last time>
```

Stores the vault passphrase in a Keychain item created with **`-T ""`** — no
trusted applications at all. Every later read therefore raises a dialog that
macOS itself draws, and this is the point: an agent cannot impersonate that
dialog and cannot read the passphrase out of it. Only a human approving it gets
the read.

Approving is **not** a bare click. macOS asks you to authorize the read, which
means typing your **login keychain password** into the dialog (your macOS
password — *not* your vault passphrase). That password is what lets macOS modify
the item's access list, so it cannot be skipped. The dialog offers Allow,
Always Allow and Deny; see §5.6 before touching Always Allow.

That is what lets `cred run` open a window with no terminal involved. It also
settles the phishing question structurally:

> **After `cred remember`, cred never asks you to type the vault passphrase
> again.** Approving a read asks for your *login password*; setup asks for the
> vault passphrase in a *terminal*. So a dialog asking for your vault passphrase
> is not cred — cred only ever asks for it on a TTY, during `init`/`add`/`remember`.

- The value reaches `security` on **stdin**, hex-encoded — never argv (readable
  by any same-user process via `ps`) and never a file.
- It verifies the passphrase against the vault *before* storing, so a typo can't
  be remembered.
- macOS-only. Without it, everything still works exactly as before via the TTY.

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
- Known limit: the whole decrypted vault is written to a 0600 temp file in
  `$TMPDIR` for the duration of the command (~0.4 s measured) and removed at exit.

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
cred unlock              # opens a window — 300s by default
cred unlock --for 1800   # or ask for a longer one
cred lock                # close it now
```

`unlock` decrypts the vault **in memory** and hands it to a detached holder
process (`cred-run hold`) that keeps it for the window and serves every
`cred run` until it closes. `lock` kills the holder immediately.

- **Nothing decrypted is written to disk.** There is no plaintext cache file —
  the holder's memory is the only place a secret exists between `unlock` and the
  window closing.
- The window is a fixed 300s by default and does not extend on use; `--for`
  asks for a different one. A longer window is a real trade — the decrypted
  vault stays in memory for all of it — but it also buys the one thing that
  costs typing, since approving a read needs your login password. A command
  already running when the window closes still finishes; only new ones are
  refused.
- Every `cred run` reuses the same holder: you unlock once, not per command.
- The passphrase comes from the Keychain if you ran `cred remember` — an approval
  dialog, no terminal — and otherwise from a hidden TTY prompt. `cred run` opens
  the window itself when it finds it closed.
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
- Opens the window itself when it is closed, so an agent never has to ask first:
  with `cred remember` that is the Keychain approval; on a TTY it is a hidden
  prompt. A refusal — or no remembered passphrase and no terminal — fails as
  `vault is LOCKED` rather than running the command without credentials. A run
  never proceeds on a profile with no secrets unless the window is open, because
  a run that silently went ahead without credentials would be worse than a
  refusal.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | command ran |
| 1 | usage / generic error |
| 2 | vault locked |
| 3 | secret not found |
| 5 | profile missing an `allow =` line |
| 6 | command not in the allow-list |

An empty result is not a failure: re-running `cred init` on an existing vault is
an intentional no-op that exits 0, and `cred list` with no profiles prints
`no profiles yet` and also exits 0. A *named* profile that does not exist exits 1.

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

- Secrets go into the child via `envp`, never argv. argv is world-readable; the
  environment is readable only by the same uid — and same-uid is not a boundary
  (§5.1). They never appear in this process's output or in the transcript.
- **The vault passphrase is never typed after setup.** With `cred remember`,
  opening a window is an *approval*: you enter your **login keychain password**
  so macOS will authorize the read. The vault passphrase itself only ever goes
  into a terminal prompt, during `init`/`add`/`remember`.
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
3. **`cred add` writes the whole decrypted vault to a 0600 temp file in
   `$TMPDIR` for the duration of the command** (two 200 000-iteration `openssl`
   passes; ~0.4 s measured warm, longer under load) and removes it at exit. The
   passphrase also travels via `openssl`'s environment (never argv). A sibling
   same-user process polling that window could catch either. `cred unlock`
   creates no temp file — it decrypts straight into the holder's stdin.
4. **Allow-list checks only the first word.** `cred run github -- gh api repo …`
   is allowed; arguments are not screened. Keep the list tight.
5. **Secrets shorter than 6 chars are not scrubbed.** They can leak into
   command output.
6. **⚠️ Never click "Always Allow" on the passphrase dialog.** That one button
   adds `security` to the item's trusted list, after which any process can read
   the passphrase **silently, forever** — worse than the plaintext cache this
   design replaced. `Allow` and `Deny` are per-read and are the only safe answers.
7. **The approval dialog asks for your login password, not a fingerprint.**
   Biometric gating needs `kSecAccessControlUserPresence`, which only the
   Security API can set — the `security` CLI has no flag for it. So approving a
   read costs a password entry; if the dialog itself offers Touch ID, that is
   macOS's own setting and not something cred controls.
8. **Window openings are logged.** `~/.config/cred/unlock.log` (0600) records a
   timestamp and what opened the window. That is the defence against a prompt
   you did not expect: a prompt cannot be made unforgeable, but it can be made
   auditable.

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
cred set npm allow "npm sh"   # the sh -c wrapper below runs `sh`, which the allow-list checks first
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
| `cred run` → `vault is LOCKED` | nothing remembered and no terminal: run `cred unlock` yourself, or `cred remember` once |
| a dialog asks for the passphrase **text** | that is not cred — `cred` only asks for the vault passphrase on a TTY, during setup. Don't type it |
| the approval dialog wants a password | that's your **login keychain password** (macOS), which authorizes the read — not your vault passphrase |
| you clicked Always Allow | delete the item and run `cred remember` again: `security delete-generic-password -s cred-vault-passphrase -a cred` |
| `cred unlock` → `the holder did not come up` | wrong passphrase, or a stale build: `./bin/build-project.sh credentials` |
| `cred run` → `cred-run binary not built` | `./bin/build-project.sh credentials` |
| `cred run` → `profile has no 'allow =' line` | add `allow = …` to the profile |
| `cred run` → `'X' not in profile allow-list` | add `X` to `allow =`, or don't run `X` |
| `cred run` → `no secret for svc/VAR` | `cred add svc VAR` first |
| secret still shows in output | it's <6 chars (not scrubbed), or the command printed it in a form the scrubber can't match |

The scrub list in §5 is deliberately blunt about what this is not. Read it before
trusting a new workflow to it.
