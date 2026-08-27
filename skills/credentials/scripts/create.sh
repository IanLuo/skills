#!/usr/bin/env bash
# Interactive credential file creator
# Never sends credentials to LLM - prompts securely and writes directly
#
# Usage: bash skills/credentials/scripts/create.sh <service_name> <var1> <var2> ...
#
# Example:
#   bash skills/credentials/scripts/create.sh atlassian \
#     ATLASSIAN_URL ATLASSIAN_EMAIL ATLASSIAN_API_TOKEN

set -euo pipefail

if [ $# -lt 2 ]; then
  cat >&2 <<'EOF'
Usage: create.sh <service_name> <var1> [var2 ...]

Creates a credential file at ~/.credentials/<service_name>.env
Values are never sent to LLM - written directly to file.

Example:
  bash skills/credentials/scripts/create.sh myservice \
    API_KEY API_SECRET

EOF
  exit 1
fi

SERVICE_NAME="$1"
shift
REQUIRED_VARS=("$@")

# Construct credential file path
CREDENTIAL_DIR="$HOME/.credentials"
CREDENTIAL_FILE="$CREDENTIAL_DIR/${SERVICE_NAME}.env"

# Check if file already exists
if [ -f "$CREDENTIAL_FILE" ]; then
  echo "Error: File already exists: $CREDENTIAL_FILE" >&2
  echo "Use scripts/update.sh to modify existing credentials" >&2
  exit 1
fi

# Create parent directory if needed
if [ ! -d "$CREDENTIAL_DIR" ]; then
  echo "Creating directory: $CREDENTIAL_DIR"
  mkdir -p "$CREDENTIAL_DIR"
  chmod 700 "$CREDENTIAL_DIR"
fi

# Prompt for each variable
echo "Creating credential file: $CREDENTIAL_FILE"
echo ""

# Bash 3 compatible: use parallel arrays instead of associative array
var_names=()
var_values=()

for var in "${REQUIRED_VARS[@]}"; do
  # Check if this looks like a secret (TOKEN, KEY, SECRET, PASSWORD)
  if [[ "$var" =~ (TOKEN|KEY|SECRET|PASSWORD|PASS) ]]; then
    # Use read -s for secrets (no echo)
    read -s -p "${var}: " value
    echo  # newline after hidden input
  else
    # Regular input for non-secrets (URLs, emails, etc.)
    read -p "${var}: " value
  fi

  if [ -z "$value" ]; then
    echo "Error: $var cannot be empty" >&2
    exit 1
  fi

  var_names+=("$var")
  var_values+=("$value")
done

echo ""

# Write to file
{
  echo "# Credential file created $(date)"
  echo "# Managed by skills/credentials"
  echo ""
  for i in "${!var_names[@]}"; do
    echo "${var_names[$i]}=${var_values[$i]}"
  done
} > "$CREDENTIAL_FILE"

# Set restrictive permissions
chmod 600 "$CREDENTIAL_FILE"

echo "✓ Created: $CREDENTIAL_FILE"
echo "✓ Permissions: 600 (owner read/write only)"
echo ""
echo "To load these credentials:"
echo "  source skills/credentials/scripts/load.sh \\"
echo "    $CREDENTIAL_FILE \\"
echo "    ${REQUIRED_VARS[*]}"
