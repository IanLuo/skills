#!/usr/bin/env bash
# Template: Auto-load credentials if not already set
# Copy to your integration skill at scripts/ensure-credentials.sh
#
# Replace:
#   YOUR_SERVICE with your service name
#   YOUR_VAR1, YOUR_VAR2, YOUR_VAR3 with your actual variables

# Check if credentials already loaded
if [ -n "$YOUR_VAR1" ] && [ -n "$YOUR_VAR2" ] && [ -n "$YOUR_VAR3" ]; then
  # Already loaded, nothing to do
  return 0 2>/dev/null || exit 0
fi

# Not loaded - load them now
CREDENTIAL_FILE="${YOUR_SERVICE_CREDENTIAL_FILE:-$HOME/.credentials/your-service.env}"

if [ ! -f "$CREDENTIAL_FILE" ]; then
  cat >&2 <<EOF
Error: Your-service credentials not found

Setup required (one-time):
  bash skills/credentials/scripts/create.sh \\
    ~/.credentials/your-service.env \\
    YOUR_VAR1 YOUR_VAR2 YOUR_VAR3

Or set YOUR_SERVICE_CREDENTIAL_FILE to alternate location.
EOF
  return 1 2>/dev/null || exit 1
fi

# Load credentials using the credentials skill
# Path resolution: scripts/ → skill/ → skills/ → credentials/
source "$(dirname "$(dirname "$(dirname "$BASH_SOURCE")")")/credentials/scripts/load.sh" \
  "$CREDENTIAL_FILE" \
  YOUR_VAR1 YOUR_VAR2 YOUR_VAR3
