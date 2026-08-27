#!/usr/bin/env bash
# Template: Load credentials from external file
# Copy this to your integration skill at scripts/load-credentials.sh

# Replace YOUR_SERVICE with your service name (lowercase-hyphen-case)
# Replace YOUR_VAR1, YOUR_VAR2 with actual required variables

SERVICE_NAME="your-service"  # e.g., "atlassian", "github", "slack"

# Default credential file location
# Users can override with: export YOUR_SERVICE_CREDENTIAL_FILE=/custom/path
CREDENTIAL_FILE="${YOUR_SERVICE_CREDENTIAL_FILE:-$HOME/.credentials/${SERVICE_NAME}.env}"

if [ ! -f "$CREDENTIAL_FILE" ]; then
  cat >&2 <<EOF
Error: ${SERVICE_NAME} credential file not found at: $CREDENTIAL_FILE

Expected format:
  YOUR_VAR1=value1
  YOUR_VAR2=value2
  YOUR_VAR3=value3

Setup instructions: skills/credentials/references/setup-guide.md
Service-specific docs: skills/${SERVICE_NAME}/references/credential-setup.md

Set YOUR_SERVICE_CREDENTIAL_FILE env var to use a custom path.
EOF
  return 1 2>/dev/null || exit 1
fi

# Source the credential file (does NOT send contents to LLM)
# shellcheck source=/dev/null
source "$CREDENTIAL_FILE"

# Validate required variables are set
# Replace with your actual required vars
required_vars=("YOUR_VAR1" "YOUR_VAR2" "YOUR_VAR3")
missing_vars=()

for var in "${required_vars[@]}"; do
  if [ -z "${!var}" ]; then
    missing_vars+=("$var")
  fi
done

if [ ${#missing_vars[@]} -gt 0 ]; then
  cat >&2 <<EOF
Error: Missing required variables in $CREDENTIAL_FILE
Missing: ${missing_vars[*]}
Required: ${required_vars[*]}

See skills/${SERVICE_NAME}/references/credential-setup.md for setup.
EOF
  return 1 2>/dev/null || exit 1
fi

# Export so they're available to subshells
export YOUR_VAR1 YOUR_VAR2 YOUR_VAR3

# Optional: validate format (e.g., URL schemes, token prefixes)
# if [[ ! "$YOUR_VAR1" =~ ^https:// ]]; then
#   echo "Error: YOUR_VAR1 must be a valid HTTPS URL" >&2
#   return 1 2>/dev/null || exit 1
# fi
