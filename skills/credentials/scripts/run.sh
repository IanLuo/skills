#!/usr/bin/env bash
# Run a command with service credentials loaded
# Consumer only needs to know service name + command to run
#
# Usage: bash skills/credentials/scripts/run.sh <service_name> <command>
#
# Example:
#   bash skills/credentials/scripts/run.sh atlassian \
#     'curl -s "$ATLASSIAN_URL/rest/api/3/myself" | jq .'

set -euo pipefail

if [ $# -lt 2 ]; then
  cat >&2 <<'EOF'
Usage: run.sh <service_name> <command>

Loads credentials for service_name and runs command with them available.

Example:
  bash skills/credentials/scripts/run.sh atlassian \
    'curl -s -u "$ATLASSIAN_EMAIL:$ATLASSIAN_API_TOKEN" \
     "$ATLASSIAN_URL/rest/api/3/myself"'

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

# Export all variables from the credential file
while IFS='=' read -r key value; do
  # Skip comments and empty lines
  [[ "$key" =~ ^#.*$ ]] && continue
  [[ -z "$key" ]] && continue

  # Export the variable
  export "$key"
done < "$CREDENTIAL_FILE"

# Run the command with credentials available
eval "$COMMAND"
