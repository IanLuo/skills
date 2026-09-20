#!/usr/bin/env bash
#
# cred.sh — store and inject service credentials without ever printing a value.
#
# Usage:
#   cred init                          create the encrypted vault + profile dir
#   cred remember                      store the vault passphrase in the Keychain (once)
#   cred add <profile> <VAR>           store a secret (prompts, no echo)
#   cred set <profile> <VAR> <value>   store a NON-secret var (plaintext)
#   cred list [profile]                list profiles / vars (secrets as @secret)
#   cred unlock                        hold the vault in memory for 5 min
#   cred lock                          close that window now
#   cred run <profile> -- <cmd> [args] run cmd, injecting secrets + scrubbing output
#
# Secrets live in an encrypted vault file (~/.config/cred/vault) at rest, behind a
# passphrase the human sets. `cred unlock` decrypts it into a detached holder
# process (cred-run hold) that keeps it in memory for 300s and owns every secret
# operation; `cred run` is a thin client of that holder and never sees a value. The
# decrypted vault is never written to disk, so nothing readable survives the
# window. Non-secret vars live in a plaintext profile. There is deliberately no
# `get` verb: a secret value is only ever placed into a child process environment,
# never printed. `cred remember` puts the passphrase in a Keychain item with no
# trusted apps, so an agent can ask for a window and a human only has to approve.
#
# Env overrides (used by tests): CRED_DIR, CRED_RUN_BIN.

set -euo pipefail

# ── Paths ────────────────────────────────────────────────────────────────
CRED_DIR="${CRED_DIR:-$HOME/.config/cred}"
export CRED_DIR
PROFILE_DIR="$CRED_DIR/profiles"
VAULT="$CRED_DIR/vault"
HOLD_SOCK="$CRED_DIR/hold.sock"
HOLD_PID="$CRED_DIR/hold.pid"
RUN_BIN="${CRED_RUN_BIN:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/cred-run}"
OPENSSL="${OPENSSL:-openssl}"
HEADER='# cred-vault-v1'
TTL=300

# SECRET_MARKER is the token a profile value uses to mean "resolve this from the
# vault". These definitions are the whole contract between this script and
# cred-run, and each is exported only to the half that uses it instead of being
# re-derived on the other side: the holder gets the profile dir, the socket, the
# window and the marker. The client *requires* only the socket — but `cred run`
# execs it from the same shell that may have just exported the other three, so
# they are present there too. A hash of the vault layout or the relock window in
# the binary would be a second place to change.
SECRET_MARKER='@secret'

# The Keychain item that holds the vault passphrase, and the trail of every window
# this script opens. `keychain_pass` explains why the item is created with -T "":
# that one flag is the whole gate.
KC_SERVICE='cred-vault-passphrase'
KC_ACCOUNT='cred'
UNLOCK_LOG="$CRED_DIR/unlock.log"

err() { printf 'cred: %s\n' "$*" >&2; }
die() { err "$1"; exit "${2:-1}"; }

require_tty() { [ -t 0 ] || die "this verb needs an interactive terminal (run it yourself)"; }
valid_profile() { [[ "$1" =~ ^[A-Za-z0-9_-]+$ ]]; }

# Temp files to remove on exit. EXIT (not RETURN) so it also fires on set -e
# abort and die — a RETURN trap would orphan plaintext vault temp files. An
# array, quoted at the trap: these paths sit under $TMPDIR/$CRED_DIR, and an
# unquoted list word-splits on whitespace, so one space in either path makes rm
# fail and leaves the whole decrypted vault on disk.
#
# The length guard is load-bearing: this trap runs on every exit, and under
# `set -u` bash 3.2 (macOS /bin/bash, which `#!/usr/bin/env bash` resolves to on
# a stock machine) treats `"${TMPFILES[@]}"` on an empty array as an unbound
# variable — so without it every invocation that registered no temp file exits 1.
# `${#TMPFILES[@]}` is 0-safe there.
TMPFILES=()
trap '[ ${#TMPFILES[@]} -eq 0 ] || rm -f "${TMPFILES[@]}"' EXIT

usage() {
  cat <<'USAGE'
cred — store and inject service credentials without ever printing a value.

Usage:
  cred init                          create the encrypted vault + profile dir
  cred remember                      store the vault passphrase in the Keychain (once)
  cred add <profile> <VAR>           store a secret (prompts, no echo)
  cred set <profile> <VAR> <value>   store a NON-secret var (plaintext)
  cred list [profile]                list profiles / vars (secrets as @secret)
  cred unlock [--for <secs>]        hold the vault in memory (default 5 min)
  cred lock                          close that window now
  cred run <profile> -- <cmd> [args] run cmd, injecting secrets + scrubbing output

Setup (once):
  cred init                          # sets the vault passphrase (human)
  cred remember                      # so an agent can open a window by asking (human)
  cred add github GH_TOKEN           # prompts for the token, stores it hidden
  cred set github GH_API https://api.github.com
  cred set github allow "gh git"

Use:
  cred unlock                        # open a 5-min window
  cred run github -- gh api user     # token injected, output scrubbed; opens the
                                     # window itself if closed (asks for approval)
  cred list github                   # names + non-secret values; secrets as @secret

There is no `get` verb — a secret value exists only inside the child process.
Full guide: skills/credentials/references/usage.md
USAGE
}

# Read one value silently (no echo), reject empty, echo it on stdout.
read_secret() { # $1 = prompt label (to stderr)
  local out
  printf '%s' "$1" >&2
  IFS= read -r -s out || { printf '\n' >&2; die "no input read"; }
  printf '\n' >&2
  [ -n "$out" ] || die "empty value"
  printf '%s' "$out"
}

# Encrypt a plaintext file into the vault. Call with CRED_PW=… set (env, never argv).
VENC() { # $1 = plaintext file
  local out="$VAULT.tmp.$$" # unique per process: concurrent adds can't clobber each other's tmp
  "$OPENSSL" enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass env:CRED_PW -in "$1" -out "$out" \
    || { rm -f "$out"; return 1; }
  mv "$out" "$VAULT"
}

# Decrypt the vault to a file ("-" for stdout). Call with CRED_PW=… set.
# NOTE: LibreSSL's `enc -out -` does not write to stdout — omit `-out` for stdout.
VDEC() { # $1 = out file, or "-" for stdout. Wrong passphrase/corrupt vault is caught
  # by the caller via exit code + header check, so openssl's own stderr is quiet.
  if [ "$1" = "-" ]; then
    "$OPENSSL" enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:CRED_PW -in "$VAULT" 2>/dev/null
  else
    "$OPENSSL" enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:CRED_PW -in "$VAULT" -out "$1" 2>/dev/null
  fi
}

profile_file() { printf '%s/%s.env' "$PROFILE_DIR" "$1"; }

# Replace-or-append KEY=VALUE in a profile.
#
# One awk pass rather than grep+sed: `sed -i ''` is BSD-only (GNU sed reads the
# empty suffix as the script and then treats the expression as a filename), and
# interpolating the value into a sed expression made an `&` inside it expand to
# the matched text. The value travels through the environment, so it is never on
# argv and no backslash escape inside it gets reinterpreted.
upsert() {
  local f="$1" key="$2" val="$3"
  TMPFILES+=("$f.new")
  CRED_UPSERT_KEY="$key" CRED_UPSERT_VAL="$val" awk '
    BEGIN { key = ENVIRON["CRED_UPSERT_KEY"]; val = ENVIRON["CRED_UPSERT_VAL"]; done = 0 }
    {
      probe = $0
      sub(/^[[:space:]]+/, "", probe)
      eq = index(probe, "=")
      if (eq > 0) {
        k = substr(probe, 1, eq - 1)
        sub(/[[:space:]]+$/, "", k)
        if (!done && k == key) { print key "=" val; done = 1; next }
      }
      print
    }
    END { if (!done) print key "=" val }
  ' "$f" > "$f.new" && mv "$f.new" "$f"
}

# In a decrypted vault (profile<TAB>var<TAB>value lines), drop any line matching
# (profile,var) and append the new one. Preserves the header/comment lines.
vault_put() { # $1 = file, $2 = profile, $3 = var, $4 = value
  local f="$1" p="$2" v="$3" val="$4"
  awk -v p="$p" -v v="$v" 'BEGIN{FS=OFS="\t"} !($1==p && $2==v)' "$f" > "$f.new" || true
  chmod 600 "$f.new" # shell `>` creates it 0644 — a full-vault plaintext must not sit wider than 0600
  : >> "$f.new"
  printf '%s\t%s\t%s\n' "$p" "$v" "$val" >> "$f.new"
  mv "$f.new" "$f"
}

valid_name() { [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; }

# ── Verbs ────────────────────────────────────────────────────────────────

cmd_init() {
  require_tty
  mkdir -p "$PROFILE_DIR"
  if [ -f "$VAULT" ]; then
    err "vault already exists: $VAULT"
    err "leaving it alone."
    return 0
  fi
  local pw pw2
  pw="$(read_secret 'vault passphrase (hidden): ')"
  pw2="$(read_secret 'confirm passphrase (hidden): ')"
  [ "$pw" = "$pw2" ] || die "passphrases do not match"
  printf '%s\n' "$HEADER" > "$VAULT.plain"
  CRED_PW="$pw" VENC "$VAULT.plain"
  rm -f "$VAULT.plain"
  printf 'created encrypted vault %s\n' "$VAULT"
  printf 'profiles live in %s\n' "$PROFILE_DIR"
  printf 'relocks %ss after unlock — run "cred unlock" to open a window\n' "$TTL"
}

cmd_add() {
  local profile="$1" var="$2"
  valid_profile "$profile" || die "invalid profile name '$profile'"
  valid_name "$var" || die "invalid VAR name '$var'"
  require_tty
  [ -f "$VAULT" ] || die "no vault yet — run 'cred init'"

  local secret
  secret="$(read_secret "secret for $profile/$var (hidden): ")"
  case "$secret" in *$'\n'*) die "secret must be a single line" ;; esac

  local pw tmp
  pw="$(read_secret 'vault passphrase (hidden): ')"
  tmp="$(mktemp "${TMPDIR:-/tmp}/cred.XXXXXX")"
  TMPFILES+=("$tmp" "$tmp.new")
  CRED_PW="$pw" VDEC "$tmp" || die "wrong passphrase (or corrupt vault)"
  [ "$(head -n1 "$tmp")" = "$HEADER" ] || die "wrong passphrase (or corrupt vault)"

  vault_put "$tmp" "$profile" "$var" "$secret"
  CRED_PW="$pw" VENC "$tmp"

  # Verify the value round-trips (readback compare, nothing printed).
  local got
  got="$(CRED_PW="$pw" VDEC -)"
  # In-process compare — never put the secret on a subprocess argv (ps-visible).
  [[ "$got" == *"$profile"$'\t'"$var"$'\t'"$secret"* ]] \
    || die "verification failed — value not stored correctly"

  # A live holder is holding the *old* vault in memory, so it has to be replaced
  # — not refreshed — or `cred run` keeps resolving the previous value.
  if holder_running; then
    start_holder < "$tmp"
    rm -f "$tmp"
  fi

  local f; f="$(profile_file "$profile")"
  [ -f "$f" ] || : > "$f"
  upsert "$f" "$var" "$SECRET_MARKER"
  printf 'stored %s/%s (secret) and wrote @secret reference to %s\n' "$profile" "$var" "$f"
}

cmd_set() {
  local profile="$1" var="$2" val="$3"
  valid_profile "$profile" || die "invalid profile name '$profile'"
  valid_name "$var" || die "invalid VAR name '$var'"
  mkdir -p "$PROFILE_DIR"
  local f; f="$(profile_file "$profile")"
  [ -f "$f" ] || : > "$f"
  upsert "$f" "$var" "$val"
  printf '%s=%s → %s\n' "$var" "$val" "$f"
}

cmd_list() {
  if [ $# -eq 0 ]; then
    if [ -d "$PROFILE_DIR" ]; then
      find "$PROFILE_DIR" -name '*.env' -maxdepth 1 -exec basename {} .env \; 2>/dev/null | sort
    else
      err "no profiles yet"
    fi
    return 0
  fi
  valid_profile "$1" || die "invalid profile name '$1'"
  local f; f="$(profile_file "$1")"
  [ -f "$f" ] || { err "no profile '$1'"; return 1; }
  while IFS= read -r line; do
    case "$line" in
      \#*|'') continue ;;
      *=*) printf '  %s -> %s\n' "${line%%=*}" "${line#*=}" ;;
    esac
  done < "$f"
}

# ── The holder ──────────────────────────────────────────────────────────
#
# `cred unlock` and `cred add` both need to (re)start it; nothing else does.
# It is detached on purpose: it has to outlive the command that started it, or
# the window would close the moment unlock returned.

# Stop a live holder and drop its socket. Safe when there is none: a holder that
# closed its own window on TTL leaves the pidfile behind.
#
# The identity check is load-bearing. A pidfile outlives the holder it names, so
# after a TTL expiry the PID in it may belong to an unrelated process that was
# handed the same number — and `cred lock` would kill that instead. Signal only a
# PID whose command line is still this holder.
stop_holder() {
  if [ -f "$HOLD_PID" ]; then
    local pid
    pid="$(cat "$HOLD_PID")"
    case "$(ps -o command= -p "$pid" 2>/dev/null)" in
      *cred-run*hold) kill "$pid" 2>/dev/null || true ;;
    esac
    rm -f "$HOLD_PID"
  fi
  rm -f "$HOLD_SOCK"
}

holder_running() { [ -S "$HOLD_SOCK" ] && [ -f "$HOLD_PID" ] && kill -0 "$(cat "$HOLD_PID")" 2>/dev/null; }

# Start the holder on a decrypted vault arriving on stdin, and wait for its
# socket. The holder binds only after reading and parsing the whole vault, so a
# socket that exists means ready — no readiness protocol needed.
start_holder() {
  stop_holder
  # `<&0` is load-bearing. An asynchronous list starts with stdin from /dev/null
  # when the shell has nowhere to put it (no job control), and that default wins
  # over a redirect placed on the call — so without re-redirecting here the
  # holder reads an empty vault, comes up healthy, and fails every secret lookup
  # with 'no secret for <profile>/<var>'.
  nohup "$RUN_BIN" hold <&0 >/dev/null 2>&1 &
  echo $! > "$HOLD_PID"
  local i
  for i in $(seq 1 100); do
    [ -S "$HOLD_SOCK" ] && return 0
    sleep 0.05
  done
  stop_holder
  die "the holder did not come up — wrong passphrase, or a broken cred-run build"
}

# ── The passphrase: the Keychain, then a terminal ───────────────────────
#
# `cred remember` stores the vault passphrase in a Keychain item created with
# `-T ""` — no trusted applications at all. Every read of it then raises a dialog
# that macOS draws itself, and that is the whole design: an agent cannot
# impersonate that dialog and cannot read the value out of it; only a human
# approving it gets the read through.
#
# So after `cred remember`, cred never asks anyone to TYPE the passphrase again —
# it asks for approval. That makes the phishing question decidable by
# construction: a dialog asking you to type the vault passphrase is not cred.

# Read the passphrase from the Keychain into PASS_VALUE, and say how it went in
# PASS_SOURCE. It reports rather than prints because a command substitution would
# run it in a subshell, where PASS_SOURCE would not survive to the caller — and
# with `set -u` that is not a silent bug but a crash.
keychain_pass() {
  PASS_VALUE=''
  if ! command -v security >/dev/null 2>&1; then
    PASS_SOURCE=absent # not macOS — the terminal path is the only one
    return 0
  fi
  local rc
  PASS_VALUE="$(security find-generic-password -w -s "$KC_SERVICE" -a "$KC_ACCOUNT" 2>/dev/null)"; rc=$?
  case "$rc" in
    0)  PASS_SOURCE=keychain ;;
    44) PASS_SOURCE=absent; PASS_VALUE='' ;;
    *)  PASS_SOURCE=denied; PASS_VALUE='' ;;
  esac
}

# The passphrase itself: the Keychain if it was remembered, otherwise a terminal.
passphrase() {
  keychain_pass
  case "$PASS_SOURCE" in
    keychain) printf '%s' "$PASS_VALUE"; return 0 ;;
    denied)   die "vault is LOCKED — Keychain access was denied; ask the human to approve the dialog" 2 ;;
  esac
  [ -t 0 ] || die "vault is LOCKED and cannot be opened here — no remembered passphrase and no terminal; ask the human to run 'cred unlock', or run 'cred remember' once to store it" 2
  read_secret 'vault passphrase (hidden): '
}

# One line per window opened, so an approval you did not expect to give is
# visible afterwards. A prompt cannot be made unforgeable; it can be made
# auditable, and that is the part that helps.
log_unlock() {
  [ -f "$UNLOCK_LOG" ] || { : > "$UNLOCK_LOG"; chmod 600 "$UNLOCK_LOG"; }
  printf '%s  %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$1" >> "$UNLOCK_LOG"
}

# Decrypt the vault and hand it to a holder. Shared by `unlock` and `run`, so
# there is exactly one way a window opens and one place the passphrase is used.
open_window() { # $1 = window length in seconds, default TTL
  local pw plain ttl="${1:-$TTL}"
  [ -f "$VAULT" ] || die "no vault yet — run 'cred init'"
  [ -x "$RUN_BIN" ] || die "cred-run binary not built — run: ./bin/build-project.sh credentials"
  pw="$(passphrase)"
  # In this shell's memory, never a file; and the text goes straight from the
  # decryption into the holder's stdin, which holds it in memory.
  plain="$(CRED_PW="$pw" VDEC -)" || die "wrong passphrase (or corrupt vault)"
  [ "${plain%%$'\n'*}" = "$HEADER" ] || die "wrong passphrase (or corrupt vault)"
  export CRED_PROFILE_DIR="$PROFILE_DIR"
  export CRED_HOLD_SOCK="$HOLD_SOCK"
  export CRED_TTL="$ttl"
  export CRED_SECRET_MARKER="$SECRET_MARKER"
  start_holder < <(printf '%s\n' "$plain")
}

# Store the vault passphrase in the Keychain so an agent can open the window by
# asking for approval. Once, by a human: the last time it is ever typed.
cmd_remember() {
  require_tty
  command -v security >/dev/null || die "'security' is not available — this verb is macOS-only"
  [ -f "$VAULT" ] || die "no vault yet — run 'cred init'"
  local pw hex plain
  pw="$(read_secret 'vault passphrase (hidden): ')"
  # Verify before storing: remembering a typo would make every later unlock fail.
  plain="$(CRED_PW="$pw" VDEC -)" || die "wrong passphrase (or corrupt vault)"
  [ "${plain%%$'\n'*}" = "$HEADER" ] || die "wrong passphrase (or corrupt vault)"
  # The value reaches `security` on stdin — never argv, which any same-user
  # process can read with ps. Hex, because interactive mode splits its input on
  # whitespace and a passphrase containing a space would arrive as several
  # arguments. -T "" is what makes every later read ask.
  hex="$(printf '%s' "$pw" | od -An -tx1 | tr -d ' \n')"
  printf 'add-generic-password -a %s -s %s -X %s -T "" -U\n' "$KC_ACCOUNT" "$KC_SERVICE" "$hex" |
    security -i >/dev/null || die "could not store the passphrase in the Keychain"
  log_unlock 'remember (passphrase stored in the Keychain)'
  printf 'remembered — agents now open the window by asking you to approve a read\n'
  printf 'approving means entering your LOGIN keychain password in that dialog (not the vault one)\n'
  printf 'cred will never ask you to type the vault passphrase again; a dialog that does is not cred.\n'
  printf 'never click "Always Allow": that would let any process read it silently, for good.\n'
}

# `cred unlock [--for <seconds>]`. The window defaults to $TTL because a longer
# one is a real trade — the decrypted vault stays in memory for all of it — so a
# longer window is asked for explicitly rather than assumed. It also buys the one
# thing that costs typing: approving a read needs your login password, so the
# fewer windows you open, the less you type.
cmd_unlock() {
  local ttl="$TTL"
  if [ $# -gt 0 ]; then
    { [ "$1" = "--for" ] && [ $# -eq 2 ]; } || die "usage: cred unlock [--for <seconds>]"
    [[ "$2" =~ ^[1-9][0-9]*$ ]] || die "--for takes a whole number of seconds"
    ttl="$2"
    log_unlock "unlock --for $ttl"
  else
    log_unlock 'unlock'
  fi
  open_window "$ttl"
  printf 'unlocked — relocks %ss after unlock\n' "$ttl"
}

cmd_lock() { stop_holder && printf 'locked\n'; }

cmd_run() {
  [ $# -ge 1 ] || die "usage: cred run <profile> -- <cmd> [args]"
  valid_profile "$1" || die "invalid profile name '$1'"
  if [ ! -x "$RUN_BIN" ]; then
    die "cred-run binary not built — run: ./bin/build-project.sh credentials"
  fi
  # A closed window is not a dead end: open one — which asks for approval when
  # the passphrase is remembered — and carry on. A refusal, or no passphrase and
  # no terminal, still fails as LOCKED rather than running a command with no
  # credentials in it.
  if [ ! -S "$HOLD_SOCK" ]; then
    log_unlock "run $1 (window was closed)"
    open_window
  fi
  # Hand cred-run the contract (see SECRET_MARKER above). Deliberately no
  # defaults: a missing export must fail loudly rather than let the binary fall
  # back to a stale constant of its own.
  export CRED_HOLD_SOCK="$HOLD_SOCK"
  exec "$RUN_BIN" run "$@"
}

# ── Dispatch ─────────────────────────────────────────────────────────────
[ $# -ge 1 ] || { usage; exit 1; }
verb="$1"; shift
case "$verb" in
  init)   cmd_init "$@" ;;
  add)    [ $# -eq 2 ] || die "usage: cred add <profile> <VAR>"; cmd_add "$@" ;;
  set)    [ $# -ge 3 ] || die "usage: cred set <profile> <VAR> <value>"; cmd_set "$@" ;;
  list)   cmd_list "$@" ;;
  unlock) cmd_unlock "$@" ;;
  remember) cmd_remember "$@" ;;
  lock)   cmd_lock ;;
  run)    cmd_run "$@" ;;
  -h|--help) usage ;;
  help)
    if [ "${1:-}" = "run" ]; then exec "$RUN_BIN" --help; fi
    usage
    ;;
  *)      err "unknown verb '$verb'"; usage; exit 1 ;;
esac
