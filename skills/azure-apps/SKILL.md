---
name: azure-apps
description: Access Microsoft Entra ID (Azure AD) app registrations via Microsoft Graph REST API. List, create, update, delete app registrations, rotate secrets/certificates, manage API permissions and consent grants. Authenticates via Azure CLI (az login / SSO) — no app registration or secrets needed. Use when the user asks to "list my Azure apps", "create app registration", "rotate app secret", "add API permission", "grant admin consent", mentions an Azure app by name or client ID, or needs to manage Entra ID applications. Do NOT use for Azure resource management (VMs, storage, subscriptions), Azure DevOps, or Atlassian products.
metadata:
  audience: personal
  domain: integration
---

# azure-apps

Manage Microsoft Entra ID (Azure AD) app registrations via Microsoft Graph REST API using bash and curl. Authenticates via Azure CLI (`az login`) — uses your existing SSO session.

## Setup

**Prerequisites:** Azure CLI installed and logged in.

```bash
# Install (if needed): https://learn.microsoft.com/en-us/cli/azure/install-azure-cli
az login --allow-no-subscriptions   # opens browser → SSO sign in
az account show                     # verify you're logged in
```

Your account needs an Entra ID role that can manage app registrations (e.g., **Application Administrator**, **Cloud Application Administrator**, or **Global Administrator**). If you can only manage apps you own, that works too — you'll just see fewer results.

**That's it!** No config files, no secrets, no app registration needed. The scripts use `az account get-access-token` to get a Graph API token from your SSO session.

## How to Use

**All commands use this pattern:**

```bash
bash skills/azure-apps/scripts/graph-request.sh METHOD /path [json-body] | jq .
```

**Examples:**

```bash
# List all app registrations
bash skills/azure-apps/scripts/graph-request.sh GET /applications | jq '.value[] | {id, appId, displayName}'

# Get a specific app by object ID
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID | jq '{id, appId, displayName}'

# Search by display name
bash skills/azure-apps/scripts/graph-request.sh GET '/applications?$filter=displayName%20eq%20%27MyApp%27' | jq '.value[]'
```

## Common Operations

### List App Registrations

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications \
  | jq '.value[] | {id, appId, displayName, createdDateTime}'
```

Use `$top` and `$select` to limit results:

```bash
bash skills/azure-apps/scripts/graph-request.sh GET '/applications?$top=10&$select=id,appId,displayName' \
  | jq '.value[]'
```

### Get App by Object ID

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID | jq .
```

### Create App Registration

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications \
  '{"displayName":"my-new-app"}' | jq '{id, appId, displayName}'
```

With redirect URIs:

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications '{
  "displayName": "my-web-app",
  "web": {
    "redirectUris": ["https://localhost:3000/auth/callback"]
  },
  "signInAudience": "AzureADMyOrg"
}' | jq '{id, appId, displayName}'
```

### Update App Registration

```bash
bash skills/azure-apps/scripts/graph-request.sh PATCH /applications/OBJECT_ID \
  '{"displayName":"updated-name"}' | jq .
```

### Delete App Registration

```bash
bash skills/azure-apps/scripts/graph-request.sh DELETE /applications/OBJECT_ID
```

### Rotate Client Secret

**Add a new secret:**

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications/OBJECT_ID/addPassword '{
  "passwordCredential": {
    "displayName": "my-secret",
    "endDateTime": "2026-12-31T00:00:00Z"
  }
}' | jq '{keyId, displayName, secretText, endDateTime}'
```

**Important:** The `secretText` is returned only once — capture it immediately.

**Remove an old secret:**

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications/OBJECT_ID/removePassword '{
  "keyId": "KEY_ID_GUID"
}'
```

**Rotation pattern:** Add new secret, update consuming services, then remove old secret.

### List Current Secrets

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID \
  | jq '.passwordCredentials[] | {keyId, displayName, endDateTime}'
```

## Working Rules

1. **Start small, filter aggressively** — use `$top=10` when exploring, increase if needed. Use `$select` to request only needed fields. Use `$filter` for targeted queries.

2. **Check az login first** — verify `az account show` works before making requests. If not logged in, tell the user to run `az login`.

3. **Use jq for field extraction** — pipe output to `jq` and extract only needed fields:
   - `.value[] | {id, appId, displayName}` — list results
   - `.passwordCredentials[] | {keyId, displayName, endDateTime}` — secrets
   - Not: `jq .` for large responses (returns everything)

4. **Handle errors** — the graph-request.sh script handles HTTP errors, but check output for error messages. Common issues: 401 (re-auth needed), 403 (insufficient permissions), 404 (wrong ID).

5. **Stop speculative searches** — after 2-3 failed search attempts, stop and ask the user for: exact app name, object ID vs app (client) ID, or tenant details. Don't burn tokens on broad guesses.

6. **Object ID vs App (Client) ID** — Graph API paths use the object ID (`id` field), NOT the application/client ID (`appId` field). If the user provides a client ID, search by `$filter=appId eq 'CLIENT_ID'` first to get the object ID.

7. **Confirm destructive operations** — before deleting apps or removing secrets, show what will be affected and confirm with the user.

8. **Secret handling** — `secretText` from addPassword is shown only once. Always capture and display it clearly. Warn the user to store it securely.

## References

**Read [references/graph-app-api-reference.md](references/graph-app-api-reference.md) when you need:**
- API permissions (requiredResourceAccess), permission GUIDs
- Consent grants (delegated and application)
- Certificate management (addKey/removeKey)
- Owners (list/add/remove)
- Service principals (CRUD, relationship to app registrations)
- Common Microsoft Graph permission GUIDs
- Pagination patterns for large result sets

## Interaction Style

- **Verify setup** — if not logged in, tell the user to run `az login`.
- **Show results** — display key fields (displayName, appId, id) in readable format, not raw JSON dumps.
- **Provide links** — construct portal URLs: `https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/~/Overview/appId/APP_ID`
- **Confirm writes** — before creating, updating, or deleting, show the payload and confirm.
- **Surface secrets carefully** — when rotating secrets, clearly label the secretText and warn it won't be shown again.

## Error Handling

Common errors:

- **401 Unauthorized** — token expired or invalid. The script retries automatically. If persistent, run `az login` again.
- **403 Forbidden** — insufficient permissions. The authenticating app needs `Application.ReadWrite.All` (or narrower scopes for read-only). May need admin consent.
- **404 Not Found** — wrong object ID, or app doesn't exist. Check if user provided appId instead of id.
- **400 Bad Request** — malformed JSON or invalid property. Check the request body.
- **429 Too Many Requests** — rate limited. Wait and retry.
