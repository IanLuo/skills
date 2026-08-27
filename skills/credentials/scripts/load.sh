#!/usr/bin/env bash
# Generic credential loader - mechanism only
# The CONSUMER specifies file path and variables
#
# Usage: source skills/credentials/scripts/load.sh <credential_file> <var1> <var2> ...
#
# Example:
#   source skills/credentials/scripts/load.sh \
#     "${ATLASSIAN_CREDENTIAL_FILE:-$HOME/.credentials/atlassian.env}" \
#     ATLASSIAN_URL ATLASSIAN_EMAIL ATLASSIAN_API_TOKEN

if [ $# -lt 2 ]; then
  cat >&2 <<'EOF'
Error: load.sh requires credential file path and at least one variable name

Usage: source skills/credentials/scripts/load.sh <file_path> <var1> [var2 ...]

Example:
  source skills/credentials/scripts/load.sh \
    "${MY_CREDENTIAL_FILE:-$HOME/.credentials/myservice.env}" \
    VAR1 VAR2 VAR3

See: skills/credentials/references/setup-guide.md
EOF
  return 1 2>/dev/null || exit 1
fi

CREDENTIAL_FILE="$1"
shift
REQUIRED_VARS=("$@")

if [ ! -f "$CREDENTIAL_FILE" ]; then
  cat >&2 <<EOF
Error: Credential file not found: $CREDENTIAL_FILE

Expected format:
$(printf "  %s=...\n" "${REQUIRED_VARS[@]}")

See: skills/credentials/references/setup-guide.md
EOF
  return 1 2>/dev/null || exit 1
fi

# Source the credential file (does NOT send contents to LLM)
# shellcheck source=/dev/null
source "$CREDENTIAL_FILE"

# Validate required variables
missing_vars=()

for var in "${REQUIRED_VARS[@]}"; do
  if [ -z "${!var}" ]; then
    missing_vars+=("$var")
  fi
done

if [ ${#missing_vars[@]} -gt 0 ]; then
  cat >&2 <<EOF
Error: Missing required variables in $CREDENTIAL_FILE
Missing: ${missing_vars[*]}
Required: ${REQUIRED_VARS[*]}

See: skills/credentials/references/setup-guide.md
EOF
  return 1 2>/dev/null || exit 1
fi

# Export all required vars
export "${REQUIRED_VARS[@]}"
