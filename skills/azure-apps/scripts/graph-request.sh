#!/usr/bin/env bash
#
# graph-request.sh — Make authenticated Microsoft Graph API requests.
#
# Usage:
#   graph-request.sh METHOD /path [json-body]
#
# Examples:
#   graph-request.sh GET /applications
#   graph-request.sh POST /applications '{"displayName":"my-app"}'
#   graph-request.sh PATCH /applications/ID '{"displayName":"new-name"}'
#   graph-request.sh DELETE /applications/ID

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
GRAPH_BASE="https://graph.microsoft.com/v1.0"

# ── Validate arguments ──────────────────────────────────────────────────
if [[ $# -lt 2 ]]; then
  echo "Usage: graph-request.sh METHOD /path [json-body]" >&2
  echo "" >&2
  echo "Examples:" >&2
  echo "  graph-request.sh GET /applications" >&2
  echo "  graph-request.sh POST /applications '{\"displayName\":\"my-app\"}'" >&2
  exit 1
fi

METHOD="$1"
PATH_ARG="$2"
BODY="${3:-}"

# Ensure path starts with /
if [[ "$PATH_ARG" != /* ]]; then
  PATH_ARG="/${PATH_ARG}"
fi

URL="${GRAPH_BASE}${PATH_ARG}"

# ── Get access token ────────────────────────────────────────────────────
get_token() {
  "$SCRIPT_DIR/auth.sh"
}

access_token=$(get_token)

if [[ -z "$access_token" ]]; then
  echo "Error: Failed to obtain access token." >&2
  exit 1
fi

# ── Build and run curl ──────────────────────────────────────────────────
do_request() {
  local token="$1"
  local curl_args=(
    -s
    -w "\n%{http_code}"
    -H "Authorization: Bearer ${token}"
    -H "Content-Type: application/json"
    -X "$METHOD"
  )

  if [[ -n "$BODY" ]]; then
    curl_args+=(-d "$BODY")
  fi

  curl_args+=("$URL")
  curl "${curl_args[@]}"
}

response=$(do_request "$access_token")

# Split response body and HTTP status code
http_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | sed '$d')

# ── Handle HTTP errors ──────────────────────────────────────────────────
case "$http_code" in
  2[0-9][0-9])
    # Success — output body (may be empty for 204 No Content)
    if [[ -n "$body" ]]; then
      echo "$body"
    fi
    ;;
  401)
    echo "Error 401: Token may be expired. Retrying with fresh token..." >&2

    # az login refresh and retry once
    access_token=$(get_token)
    if [[ -z "$access_token" ]]; then
      echo "Error: Re-authentication failed. Run 'az login'." >&2
      exit 1
    fi

    response=$(do_request "$access_token")
    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | sed '$d')

    if [[ "$http_code" =~ ^2 ]]; then
      if [[ -n "$body" ]]; then
        echo "$body"
      fi
    else
      echo "Error $http_code after retry:" >&2
      echo "$body" >&2
      exit 1
    fi
    ;;
  403)
    echo "Error 403: Forbidden — insufficient permissions." >&2
    echo "Your account may need an Entra ID role (e.g., Application Administrator)." >&2
    echo "$body" >&2
    exit 1
    ;;
  404)
    echo "Error 404: Not found." >&2
    echo "Check that you're using the object ID (id), not the application/client ID (appId)." >&2
    echo "$body" >&2
    exit 1
    ;;
  429)
    echo "Error 429: Rate limited — too many requests." >&2
    echo "$body" >&2
    exit 1
    ;;
  *)
    echo "Error $http_code:" >&2
    echo "$body" >&2
    exit 1
    ;;
esac
