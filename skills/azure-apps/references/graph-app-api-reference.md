# Microsoft Graph App API Reference

Advanced operations for Entra ID app registrations, service principals, permissions, and consent.

## Table of Contents

- [API Permissions (requiredResourceAccess)](#api-permissions-requiredresourceaccess)
- [Consent Grants](#consent-grants)
- [Certificates (addKey/removeKey)](#certificates)
- [Owners](#owners)
- [Service Principals](#service-principals)
- [Common Permission GUIDs](#common-permission-guids)
- [Pagination Patterns](#pagination-patterns)

## API Permissions (requiredResourceAccess)

App registrations declare required API permissions via the `requiredResourceAccess` property. Each entry specifies a resource (API) and its required permission scopes or roles.

### Permission Types

- **Scope** (delegated) — `type: "Scope"` — permissions granted on behalf of a signed-in user
- **Role** (application) — `type: "Role"` — permissions granted directly to the app (no user context)

### View Current Permissions

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID \
  | jq '.requiredResourceAccess'
```

### Set API Permissions

Replace the entire `requiredResourceAccess` array (this is a full overwrite, not a merge):

```bash
bash skills/azure-apps/scripts/graph-request.sh PATCH /applications/OBJECT_ID '{
  "requiredResourceAccess": [
    {
      "resourceAppId": "00000003-0000-0000-c000-000000000000",
      "resourceAccess": [
        {"id": "e1fe6dd8-ba31-4d61-89e7-88639da4683d", "type": "Scope"},
        {"id": "570282fd-fa5c-430d-a7fd-fc8dc98a9dca", "type": "Scope"}
      ]
    }
  ]
}'
```

The `resourceAppId` `00000003-0000-0000-c000-000000000000` is Microsoft Graph.

### Add Permissions Without Losing Existing

1. GET the current `requiredResourceAccess`
2. Append new entries to the array
3. PATCH with the full updated array

## Consent Grants

After declaring permissions, they must be consented to (either by user or admin).

### Delegated Permission Grants (oauth2PermissionGrants)

List existing delegated grants:

```bash
bash skills/azure-apps/scripts/graph-request.sh GET \
  '/oauth2PermissionGrants?$filter=clientId%20eq%20%27SERVICE_PRINCIPAL_ID%27' \
  | jq '.value[]'
```

Grant admin consent for delegated permissions:

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /oauth2PermissionGrants '{
  "clientId": "SERVICE_PRINCIPAL_ID",
  "consentType": "AllPrincipals",
  "resourceId": "RESOURCE_SERVICE_PRINCIPAL_ID",
  "scope": "User.Read Mail.Read"
}'
```

- `clientId` — service principal ID of YOUR app (not the app registration object ID)
- `resourceId` — service principal ID of the resource API (e.g., Microsoft Graph's service principal)
- `scope` — space-separated list of delegated permission values

### Application Permission Grants (appRoleAssignments)

Grant application-level permissions (admin consent required):

```bash
bash skills/azure-apps/scripts/graph-request.sh POST \
  /servicePrincipals/SERVICE_PRINCIPAL_ID/appRoleAssignments '{
  "principalId": "SERVICE_PRINCIPAL_ID",
  "resourceId": "RESOURCE_SERVICE_PRINCIPAL_ID",
  "appRoleId": "PERMISSION_GUID"
}'
```

List current app role assignments:

```bash
bash skills/azure-apps/scripts/graph-request.sh GET \
  /servicePrincipals/SERVICE_PRINCIPAL_ID/appRoleAssignments \
  | jq '.value[] | {id, appRoleId, resourceDisplayName}'
```

### Find Microsoft Graph's Service Principal ID

```bash
bash skills/azure-apps/scripts/graph-request.sh GET \
  '/servicePrincipals?$filter=appId%20eq%20%2700000003-0000-0000-c000-000000000000%27' \
  | jq '.value[0].id'
```

## Certificates

### Add Certificate (addKey)

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications/OBJECT_ID/addKey '{
  "keyCredential": {
    "type": "AsymmetricX509Cert",
    "usage": "Verify",
    "key": "BASE64_ENCODED_CERT"
  },
  "passwordCredential": null,
  "proof": "JWT_PROOF_OF_POSSESSION"
}'
```

**Note:** `addKey` requires a proof-of-possession JWT signed by an existing valid certificate on the app. For the first certificate, use the Azure portal or upload via `keyCredentials` property directly:

```bash
bash skills/azure-apps/scripts/graph-request.sh PATCH /applications/OBJECT_ID '{
  "keyCredentials": [
    {
      "type": "AsymmetricX509Cert",
      "usage": "Verify",
      "key": "BASE64_ENCODED_CERT",
      "displayName": "my-cert"
    }
  ]
}'
```

### Remove Certificate (removeKey)

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications/OBJECT_ID/removeKey '{
  "keyId": "KEY_ID_GUID",
  "proof": "JWT_PROOF_OF_POSSESSION"
}'
```

### List Certificates

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID \
  | jq '.keyCredentials[] | {keyId, displayName, type, endDateTime}'
```

## Owners

### List Owners

```bash
bash skills/azure-apps/scripts/graph-request.sh GET /applications/OBJECT_ID/owners \
  | jq '.value[] | {id, displayName, userPrincipalName}'
```

### Add Owner

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /applications/OBJECT_ID/owners/\$ref '{
  "@odata.id": "https://graph.microsoft.com/v1.0/directoryObjects/USER_OBJECT_ID"
}'
```

### Remove Owner

```bash
bash skills/azure-apps/scripts/graph-request.sh DELETE \
  /applications/OBJECT_ID/owners/USER_OBJECT_ID/\$ref
```

## Service Principals

A service principal is the local representation of an app registration in a tenant. Every app that authenticates needs a service principal.

### Relationship

- **App registration** (`/applications`) — the global app definition (template)
- **Service principal** (`/servicePrincipals`) — the local instance in a specific tenant

An app registration has exactly one service principal per tenant where it's used.

### Create Service Principal

```bash
bash skills/azure-apps/scripts/graph-request.sh POST /servicePrincipals '{
  "appId": "APP_CLIENT_ID"
}' | jq '{id, appId, displayName}'
```

### Get Service Principal by App ID

```bash
bash skills/azure-apps/scripts/graph-request.sh GET \
  '/servicePrincipals?$filter=appId%20eq%20%27APP_CLIENT_ID%27' \
  | jq '.value[0]'
```

### List Service Principals

```bash
bash skills/azure-apps/scripts/graph-request.sh GET '/servicePrincipals?$top=10' \
  | jq '.value[] | {id, appId, displayName, servicePrincipalType}'
```

### Update Service Principal

```bash
bash skills/azure-apps/scripts/graph-request.sh PATCH /servicePrincipals/SP_ID '{
  "tags": ["WindowsAzureActiveDirectoryIntegratedApp"],
  "notes": "Updated via Graph API"
}'
```

### Delete Service Principal

```bash
bash skills/azure-apps/scripts/graph-request.sh DELETE /servicePrincipals/SP_ID
```

## Common Permission GUIDs

### Microsoft Graph (resourceAppId: 00000003-0000-0000-c000-000000000000)

#### Delegated Permissions (Scope)

| Permission | GUID | Description |
|---|---|---|
| User.Read | `e1fe6dd8-ba31-4d61-89e7-88639da4683d` | Sign in and read user profile |
| User.ReadWrite | `b4e74841-8e56-480b-be8b-910348b18b4c` | Read and write user profile |
| User.Read.All | `a154be20-db9c-4678-8ab7-66f6cc099a59` | Read all users' profiles |
| Mail.Read | `570282fd-fa5c-430d-a7fd-fc8dc98a9dca` | Read user mail |
| Mail.Send | `e383f46e-2787-4529-855e-0e479a3ffac0` | Send mail as user |
| Files.Read | `10465720-29dd-4523-a11a-6a75c743c9d9` | Read user files |
| Calendars.Read | `465a38f9-76ea-45b9-9f34-9e8b0d4b0b42` | Read user calendars |
| Directory.Read.All | `06da0dbc-49e2-44d2-8312-53f166ab848a` | Read directory data |
| offline_access | `7427e0e9-2fba-42fe-b0c0-848c9e6a8182` | Maintain access to data |

#### Application Permissions (Role)

| Permission | GUID | Description |
|---|---|---|
| Application.ReadWrite.All | `1bfefb4e-e0b5-418b-a88f-73c46d2cc8e9` | Read and write all apps |
| Application.Read.All | `9a5d68dd-52b0-4cc2-bd40-abcf44ac3a30` | Read all apps |
| User.Read.All | `df021288-bdef-4463-88db-98f22de89214` | Read all users' profiles |
| Mail.Read | `810c84a8-4a9e-49e6-bf7d-12d183f40d01` | Read all users' mail |
| Mail.Send | `b633e1c5-b582-4048-a93e-9f11b44c7e96` | Send mail as any user |
| Directory.Read.All | `7ab1d382-f21e-4acd-a863-ba3e13f7da61` | Read directory data |
| Group.Read.All | `5b567255-7703-4780-807c-7be8301ae99b` | Read all groups |
| GroupMember.Read.All | `98830695-27a2-44f7-8c18-0c3ebc9698f6` | Read all group memberships |

## Pagination Patterns

Microsoft Graph uses `@odata.nextLink` for pagination.

### Manual Pagination

```bash
# First page
bash skills/azure-apps/scripts/graph-request.sh GET '/applications?$top=10' | jq .

# If response contains @odata.nextLink, fetch it (strip the base URL)
# The nextLink is a full URL, so extract the path:
bash skills/azure-apps/scripts/graph-request.sh GET '/applications?$top=10&$skiptoken=TOKEN_VALUE'
```

### Get All Results (loop pattern)

```bash
url='/applications?$top=100'
while [ -n "$url" ]; do
  response=$(bash skills/azure-apps/scripts/graph-request.sh GET "$url")
  echo "$response" | jq '.value[]'
  url=$(echo "$response" | jq -r '.["@odata.nextLink"] // empty' | sed 's|https://graph.microsoft.com/v1.0||')
done
```

### Count Results

```bash
bash skills/azure-apps/scripts/graph-request.sh GET '/applications/$count' \
  -H "ConsistencyLevel: eventual"
```

**Note:** `$count` requires the `ConsistencyLevel: eventual` header. For most operations, use `$top` with pagination instead.

### Useful Query Parameters

| Parameter | Example | Description |
|---|---|---|
| `$top` | `$top=10` | Limit results per page |
| `$skip` | `$skip=20` | Skip N results |
| `$select` | `$select=id,displayName` | Return only specified fields |
| `$filter` | `$filter=displayName eq 'MyApp'` | Filter results |
| `$orderby` | `$orderby=displayName` | Sort results |
| `$search` | `$search="displayName:MyApp"` | Search (requires ConsistencyLevel header) |
