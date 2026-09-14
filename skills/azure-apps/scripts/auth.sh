#!/usr/bin/env bash
#
# auth.sh — Get a Microsoft Graph access token via Azure CLI.
#
# Relies on `az login` (SSO). No app registration or credentials needed.
# Outputs ONLY the access_token to stdout; all messages go to stderr.

set -euo pipefail

# ── Check az CLI ─────────────────────────────────────────────────────────
if ! command -v az &>/dev/null; then
  echo "Error: Azure CLI (az) not found." >&2
  echo "Install: https://learn.microsoft.com/en-us/cli/azure/install-azure-cli" >&2
  exit 1
fi

# ── Check logged in ──────────────────────────────────────────────────────
if ! az account show &>/dev/null 2>&1; then
  echo "Error: Not logged in. Run 'az login --allow-no-subscriptions' first (opens browser for SSO)." >&2
  exit 1
fi

# ── Get token ────────────────────────────────────────────────────────────
# az handles caching and refresh automatically
token=$(az account get-access-token \
  --resource https://graph.microsoft.com \
  --query accessToken -o tsv 2>/dev/null)

if [[ -z "$token" ]]; then
  echo "Error: Failed to get Graph token. Try 'az login' again." >&2
  exit 1
fi

printf '%s' "$token"
