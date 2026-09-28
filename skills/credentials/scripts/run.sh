#!/usr/bin/env bash
# Run a command with service credentials loaded — output goes to a temp file,
# NEVER to stdout (which the LLM sees). Credential values are scrubbed from
# the output file before the path is returned.
#
# Usage: bash skills/credentials/scripts/run.sh <service_name> <command>
#
# Returns (on stdout — this IS visible to LLM):
#   exit_code=<N>
#   output=<path to scrubbed temp file>
#   bytes=<size>
#
# The agent should then `read <path>` to inspect results.

set -euo pipefail

if [ $# -lt 2 ]; then
  cat >&2 <<'EOF'
Usage: run.sh <service_name> <command>

Loads credentials for service_name and runs command.
Output is written to a temp file (scrubbed of credential values)
and the file path is returned — output never hits LLM context directly.

Example:
  bash skills/credentials/scripts/run.sh atlassian \
    'curl -s -u "$ATLASSIAN_EMAIL:$ATLASSIAN_API_TOKEN" \
     "$ATLASSIAN_URL/rest/api/3/myself" | jq .'

Service name matches credential file: ~/.credentials/<service_name>.env
Override with ${SERVICE_NAME}_CREDENTIAL_FILE environment variable.
EOF
  exit 1
fi

SERVICE_NAME="$1"
shift
COMMAND="$*"

# Construct paths
SERVICE_NAME_UPPER=$(echo "$SERVICE_NAME" | tr '[:lower:]-' '[:upper:]_')
CREDENTIAL_FILE_VAR="${SERVICE_NAME_UPPER}_CREDENTIAL_FILE"
DEFAULT_FILE="$HOME/.credentials/${SERVICE_NAME}.env"
CREDENTIAL_FILE="${!CREDENTIAL_FILE_VAR:-$DEFAULT_FILE}"

# Check credential file exists
if [ ! -f "$CREDENTIAL_FILE" ]; then
  cat >&2 <<EOF
Error: Credentials not found for service: $SERVICE_NAME

Setup required (one-time):
  bash skills/credentials/scripts/create.sh \\
    ~/.credentials/${SERVICE_NAME}.env \\
    [VAR1 VAR2 ...]

Or set ${CREDENTIAL_FILE_VAR} to alternate location.
EOF
  exit 1
fi

# Load credentials and run command
# Source the credential file to load all variables
# shellcheck source=/dev/null
source "$CREDENTIAL_FILE"

# Collect credential values for scrubbing (values only, skip short/empty)
SCRUB_PATTERNS=()
while IFS='=' read -r key value; do
  # Skip comments and empty lines
  [[ "$key" =~ ^#.*$ ]] && continue
  [[ -z "$key" ]] && continue

  # Export the variable
  export "$key"

  # Only scrub values that look like secrets (≥8 chars, associated with
  # TOKEN/KEY/SECRET/PASSWORD in the var name, or any value ≥20 chars)
  if [ ${#value} -ge 8 ]; then
    if [[ "$key" =~ (TOKEN|KEY|SECRET|PASSWORD|PASS) ]] || [ ${#value} -ge 20 ]; then
      SCRUB_PATTERNS+=("$value")
    fi
  fi
done < "$CREDENTIAL_FILE"

# Create temp file for output
OUTPUT_FILE=$(mktemp "${TMPDIR:-/tmp}/cred-run-XXXXXX")

# Run the command, capturing stdout+stderr to the temp file
# Preserve exit code without exiting (set +e locally)
set +e
eval "$COMMAND" > "$OUTPUT_FILE" 2>&1
CMD_EXIT=$?
set -e

# Scrub credential values from output
for pattern in "${SCRUB_PATTERNS[@]}"; do
  if [ -s "$OUTPUT_FILE" ] && grep -qF "$pattern" "$OUTPUT_FILE" 2>/dev/null; then
    # Use perl for literal string replacement (no regex interpretation)
    _SCRUB_PAT="$pattern" perl -pi -e '
      BEGIN { $p = $ENV{"_SCRUB_PAT"}; $r = "[REDACTED]"; }
      s/\Q$p\E/$r/g;
    ' "$OUTPUT_FILE"
  fi
done 2>/dev/null  # suppress errors from scrub (non-critical)

# Generic pass: redact JSON values under secret-looking keys.
# Catches runtime-retrieved secrets unknown at scrub time (e.g. secrets fetched from Vault).
# Best-effort: only transforms recognizable "key": "value" patterns;
# on any error the file is left unchanged (scrub is non-critical).
# NOTE: this also redacts keys like "next_token" if they match the pattern.
# Commands needing pagination cursors under token-ish keys should chain
# internally (consume the cursor in the same run.sh command, not in the output).
if [ -s "$OUTPUT_FILE" ]; then
  perl -pi -e '
    # Match JSON string values: "secret_key": "some_value"
    s/
      ("                                    # opening quote of key
        [^"]*?                               # key prefix
        (?:secret|token|password|passwd|credential|private[_.]?key|api[_.]?key)
        [^"]*?                               # key suffix
      "                                      # closing quote of key
      \s*:\s*)                               # colon separator
      ("[^"]*")                              # string value to redact
    /$1"[REDACTED]"/gix;
    # Match JSON numeric/boolean/null values: "secret_key": 12345
    s/
      ("                                    # opening quote of key
        [^"]*?                               # key prefix
        (?:secret|token|password|passwd|credential|private[_.]?key|api[_.]?key)
        [^"]*?                               # key suffix
      "                                      # closing quote of key
      \s*:\s*)                               # colon separator
      ((?:true|false|null|-?\d[\d.eE+-]*))
    /$1"[REDACTED]"/gix;
  ' "$OUTPUT_FILE" 2>/dev/null || true  # non-critical: leave file unchanged on error
fi

# Report — this is all the LLM sees
BYTE_COUNT=$(wc -c < "$OUTPUT_FILE" | tr -d ' ')
echo "exit_code=${CMD_EXIT}"
echo "output=${OUTPUT_FILE}"
echo "bytes=${BYTE_COUNT}"
