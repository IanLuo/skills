#!/usr/bin/env bash
#
# cred.sh — store and inject service credentials without ever printing a value.
#
# Usage:
#   cred init                          create the encrypted vault + profile dir
#   cred add <profile> <VAR>           store a secret (prompts, no echo)
#   cred set <profile> <VAR> <value>   store a NON-secret var (plaintext)
#   cred list [profile]                list profiles / vars (names only)
#   cred unlock                        decrypt the vault into a 5-min cache
#   cred lock                          wipe the cache now
#   cred run <profile> -- <cmd> [args] run cmd with secrets injected + output scrubbed
#
# Secrets live in an encrypted vault file (~/.config/cred/vault) at rest, behind a
# passphrase the human sets. `cred unlock` decrypts it into a transient plaintext
# cache (0600, 300s TTL); `cred run` reads only that cache. Non-secret vars live in
# a plaintext profile. There is deliberately no `get` verb: a secret value is only
# ever placed into a child process environment, never printed.
#
# Env overrides (used by tests): CRED_DIR, CRED_RUN_BIN.

set -euo pipefail

# ── Paths ────────────────────────────────────────────────────────────────
CRED_DIR="${CRED_DIR:-$HOME/.config/cred}"
export CRED_DIR
PROFILE_DIR="$CRED_DIR/profiles"
VAULT="$CRED_DIR/vault"
UNLOCKED="$CRED_DIR/unlocked"
RUN_BIN="${CRED_RUN_BIN:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/cred-run}"
OPENSSL="${OPENSSL:-openssl}"
HEADER='# cred-vault-v1'
TTL=300

err() { printf 'cred: %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

require_tty() { [ -t 0 ] || die "this verb needs an interactive terminal (run it yourself)"; }
valid_profile() { [[ "$1" =~ ^[A-Za-z0-9_-]+$ ]]; }

# Temp files to remove on exit. EXIT (not RETURN) so it also fires on set -e
# abort and die — a RETURN trap would orphan plaintext vault temp files.
_TMP_CLEANUP=""
trap 'rm -f $_TMP_CLEANUP' EXIT

usage() {
  cat <<'USAGE'
cred — store and inject service credentials without ever printing a value.

Usage:
  cred init                          create the encrypted vault + profile dir
  cred add <profile> <VAR>           store a secret (prompts, no echo)
  cred set <profile> <VAR> <value>   store a NON-secret var (plaintext)
  cred list [profile]                list profiles / vars (names only)
  cred unlock                        decrypt the vault into a 5-min cache
  cred lock                          wipe the cache now
  cred run <profile> -- <cmd> [args] run cmd with secrets injected + output scrubbed

Setup (once):
  cred init                          # sets the vault passphrase (human)
  cred add github GH_TOKEN           # prompts for the token, stores it hidden
  cred set github GH_API https://api.github.com
  cred set github allow "gh git"

Use:
  cred unlock                        # human opens a 5-min window (needs TTY)
  cred run github -- gh api user     # token injected, output scrubbed
  cred list github                   # names only, never values

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
upsert() {
  local f="$1" key="$2" val="$3"
  if grep -qE "^[[:space:]]*${key}[[:space:]]*=" "$f"; then
    sed -i '' -E "s|^([[:space:]]*${key}[[:space:]]*=).*|\1${val}|" "$f"
  else
    printf '%s=%s\n' "$key" "$val" >> "$f"
  fi
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
  _TMP_CLEANUP="$_TMP_CLEANUP $tmp $tmp.new"
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

  # If the vault is unlocked, refresh the cache so `cred run` sees the new secret.
  if [ -f "$UNLOCKED" ]; then
    cp "$tmp" "$UNLOCKED"
    chmod 600 "$UNLOCKED"
  fi

  local f; f="$(profile_file "$profile")"
  [ -f "$f" ] || : > "$f"
  upsert "$f" "$var" "@secret"
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

cmd_unlock() {
  require_tty
  [ -f "$VAULT" ] || die "no vault yet — run 'cred init'"
  local pw tmp
  pw="$(read_secret 'vault passphrase (hidden): ')"
  tmp="$(mktemp "${TMPDIR:-/tmp}/cred.XXXXXX")"
  _TMP_CLEANUP="$_TMP_CLEANUP $tmp"
  CRED_PW="$pw" VDEC "$tmp" || die "wrong passphrase (or corrupt vault)"
  [ "$(head -n1 "$tmp")" = "$HEADER" ] || die "wrong passphrase (or corrupt vault)"
  cp "$tmp" "$UNLOCKED"
  chmod 600 "$UNLOCKED"
  printf 'unlocked — relocks %ss after unlock\n' "$TTL"
}

cmd_lock() { rm -f "$UNLOCKED" && printf 'locked\n'; }

cmd_run() {
  [ $# -ge 1 ] || die "usage: cred run <profile> -- <cmd> [args]"
  valid_profile "$1" || die "invalid profile name '$1'"
  if [ ! -x "$RUN_BIN" ]; then
    die "cred-run binary not built — run: nix develop -c bash src/credentials/build.sh"
  fi
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
  lock)   cmd_lock ;;
  run)    cmd_run "$@" ;;
  -h|--help) usage ;;
  help)
    if [ "${1:-}" = "run" ]; then exec "$RUN_BIN" --help; fi
    usage
    ;;
  *)      err "unknown verb '$verb'"; usage; exit 1 ;;
esac
