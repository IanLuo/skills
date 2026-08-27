#!/usr/bin/env bash
# Interactive credential file updater
# Never sends credentials to LLM - prompts securely and writes directly
#
# Usage: bash skills/credentials/scripts/update.sh <service_name> <var1> [var2 ...]
#
# Example:
#   bash skills/credentials/scripts/update.sh atlassian \
#     ATLASSIAN_API_TOKEN

set -euo pipefail

if [ $# -lt 2 ]; then
  cat >&2 <<'EOF'
Usage: update.sh <service_name> <var1> [var2 ...]

Updates specific variables in ~/.credentials/<service_name>.env
Values are never sent to LLM - written directly to file.

Example:
  bash skills/credentials/scripts/update.sh myservice \
    API_TOKEN

EOF
  exit 1
fi

SERVICE_NAME="$1"
shift
UPDATE_VARS=("$@")

# Construct credential file path
CREDENTIAL_FILE="$HOME/.credentials/${SERVICE_NAME}.env"

# Check if file exists
if [ ! -f "$CREDENTIAL_FILE" ]; then
  echo "Error: File not found: $CREDENTIAL_FILE" >&2
  echo "Use scripts/create.sh to create a new credential file" >&2
  exit 1
fi

echo "Updating credential file: $CREDENTIAL_FILE"
echo ""

# Read current content
current_content=$(<"$CREDENTIAL_FILE")

# Prompt for new values - Bash 3 compatible: use parallel arrays
var_names=()
var_values=()

for var in "${UPDATE_VARS[@]}"; do
  # Show current value if exists (masked for secrets)
  current_value=$(echo "$current_content" | grep "^${var}=" | cut -d= -f2- || echo "")

  if [ -n "$current_value" ]; then
    if [[ "$var" =~ (TOKEN|KEY|SECRET|PASSWORD|PASS) ]]; then
      echo "Current ${var}: ******* (hidden)"
    else
      echo "Current ${var}: $current_value"
    fi
  fi

  # Prompt for new value
  if [[ "$var" =~ (TOKEN|KEY|SECRET|PASSWORD|PASS) ]]; then
    read -s -p "New ${var} (or press Enter to keep current): " value
    echo
  else
    read -p "New ${var} (or press Enter to keep current): " value
  fi

  # Keep current if empty input
  if [ -z "$value" ] && [ -n "$current_value" ]; then
    var_names+=("$var")
    var_values+=("$current_value")
  elif [ -n "$value" ]; then
    var_names+=("$var")
    var_values+=("$value")
  fi
done

echo ""

# Helper function to get value by name
get_value_for_var() {
  local target_var="$1"
  for i in "${!var_names[@]}"; do
    if [ "${var_names[$i]}" = "$target_var" ]; then
      echo "${var_values[$i]}"
      return
    fi
  done
}

# Update file - preserve comments and other vars
temp_file="${CREDENTIAL_FILE}.tmp"

while IFS= read -r line; do
  # Check if this line defines a variable we're updating
  updated=false
  for var in "${UPDATE_VARS[@]}"; do
    if [[ "$line" =~ ^${var}= ]]; then
      echo "${var}=$(get_value_for_var "$var")"
      updated=true
      break
    fi
  done

  # Keep line if not updated
  if [ "$updated" = false ]; then
    echo "$line"
  fi
done < "$CREDENTIAL_FILE" > "$temp_file"

# Replace original
mv "$temp_file" "$CREDENTIAL_FILE"
chmod 600 "$CREDENTIAL_FILE"

echo "✓ Updated: $CREDENTIAL_FILE"
echo "✓ Variables updated: ${UPDATE_VARS[*]}"
