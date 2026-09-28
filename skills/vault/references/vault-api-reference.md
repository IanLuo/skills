# Vault API Reference

Supplementary reference for the vault skill. Read this when you need endpoint details beyond what SKILL.md covers.

## Instance Details (from probing)

- **Version:** 2.0.0 (build 2026-04-13)
- **Cluster:** vault-stage-primary
- **Storage:** Raft
- **Seal:** AWS KMS (recovery seal: Shamir)
- **HA:** connection healthy, standby=true

## Auth Methods (confirmed active)

| Method | Mount Path | Notes |
|---|---|---|
| OIDC | `auth/oidc/` | Azure AD (tenant `9ac44c96-980a-481b-ae23-d8f56b82c605`). Browser-based only. |
| Userpass | `auth/userpass/` | Returns 403 (exists, needs credentials). For API/automation use. |
| AppRole | `auth/approle/` | Returns 500 on empty login (exists). For service-to-service auth. |
| LDAP | `auth/ldap/` | Returns 403 (exists). May be tied to corporate directory. |
| Token | `auth/token/` | Always present. Token lookup/renew/revoke. |

## OIDC Auth (Browser Only)

The OIDC flow is interactive — it opens a browser to Azure AD:

1. `POST /v1/auth/oidc/oidc/auth_url` with `{"redirect_uri": "http://localhost:8250/oidc/callback", "role": ""}` → returns `data.auth_url`
2. User opens `auth_url` in browser, authenticates with Align email
3. Azure AD redirects to callback with `state` + `code`
4. `GET /v1/auth/oidc/oidc/callback?state=...&nonce=...&code=...` → returns `auth.client_token`

**For CLI users:** `vault login -method=oidc` automates steps 1-4 (opens browser, listens on localhost:8250).

**For curl users:** Use the UI to login via OIDC, then copy the token from the top-right menu.

## Userpass Auth

### Login

```
POST /v1/auth/userpass/login/:username
Body: {"password": "..."}
Response: {
  "auth": {
    "client_token": "hvs.CAESIJye...",
    "accessor": "...",
    "policies": ["default", "..."],
    "lease_duration": 2764800,
    "renewable": true,
    "entity_id": "...",
    "token_type": "service"
  }
}
```

No `X-Vault-Token` header needed for login.

### List Users (admin)

```
LIST /v1/auth/userpass/users
Header: X-Vault-Token: ...
Response: {"data": {"keys": ["user1", "user2"]}}
```

## AppRole Auth

### Login

```
POST /v1/auth/approle/login
Body: {"role_id": "...", "secret_id": "..."}
Response: {"auth": {"client_token": "...", ...}}
```

## Token Operations

### Lookup Self

```
GET /v1/auth/token/lookup-self
Header: X-Vault-Token: ...
Response: {
  "data": {
    "display_name": "...",
    "policies": [...],
    "ttl": 3600,
    "expire_time": "..."
  }
}
```

### Renew Self

```
POST /v1/auth/token/renew-self
Header: X-Vault-Token: ...
Body: {"increment": "1h"}  (optional)
Response: {"auth": {"client_token": "...", "lease_duration": 3600}}
```

### Revoke Self

```
POST /v1/auth/token/revoke-self
Header: X-Vault-Token: ...
```

## KV v2 Secret Engine

All paths below are relative to the mount point (e.g., `customer/`).

### Read Secret

```
GET /v1/<mount>/data/<path>
GET /v1/<mount>/data/<path>?version=N
Header: X-Vault-Token: ...
Response: {
  "data": {
    "data": {"key1": "val1", "key2": "val2"},
    "metadata": {
      "created_time": "...",
      "version": 3,
      "destroyed": false,
      "deletion_time": ""
    }
  }
}
```

### Write/Update Secret

```
POST /v1/<mount>/data/<path>
Header: X-Vault-Token: ...
Body: {
  "data": {"key1": "newval1"},
  "options": {"cas": 3}          // optional, check-and-set
}
Response: {
  "data": {"created_time": "...", "version": 4, "destroyed": false}
}
```

### List Secrets

```
LIST /v1/<mount>/metadata/<path>
  (or GET /v1/<mount>/metadata/<path>?list=true)
Header: X-Vault-Token: ...
Response: {"data": {"keys": ["secret1", "subfolder/"]}}
```

Folders end with `/`.

### Delete Secret (soft — latest version)

```
DELETE /v1/<mount>/data/<path>
Header: X-Vault-Token: ...
```

### Delete Specific Versions (soft)

```
POST /v1/<mount>/delete/<path>
Header: X-Vault-Token: ...
Body: {"versions": [1, 2]}
```

### Undelete Versions

```
POST /v1/<mount>/undelete/<path>
Header: X-Vault-Token: ...
Body: {"versions": [1, 2]}
```

### Destroy Versions (permanent)

```
PUT /v1/<mount>/destroy/<path>
Header: X-Vault-Token: ...
Body: {"versions": [1, 2]}
```

### Read Metadata

```
GET /v1/<mount>/metadata/<path>
Header: X-Vault-Token: ...
Response: {
  "data": {
    "current_version": 3,
    "max_versions": 0,
    "cas_required": false,
    "versions": {
      "1": {"created_time": "...", "deletion_time": "", "destroyed": false},
      "2": {...}
    }
  }
}
```

### Delete All Metadata + Versions (permanent)

```
DELETE /v1/<mount>/metadata/<path>
Header: X-Vault-Token: ...
```

## System Endpoints (often need admin token)

### Health (unauthenticated)

```
GET /v1/sys/health
Response: {"initialized": true, "sealed": false, "version": "2.0.0", ...}
```

### Seal Status (unauthenticated)

```
GET /v1/sys/seal-status
Response: {"type": "awskms", "initialized": true, "sealed": false, ...}
```

### List Auth Methods (admin)

```
GET /v1/sys/auth
Header: X-Vault-Token: ...
```

### List Secret Mounts (admin)

```
GET /v1/sys/mounts
Header: X-Vault-Token: ...
```

## CIAM Path Convention

From IntegrEng wiki "Vault Access" page:

```
<mount>/<group_name>/<phase>/<env>-<region>/Application_Credentials/<app_name>
```

Known example: `customer/ciam/stg/qas-us/Application_Credentials/ciam~ciam_api_user`

Application teams own access to their credentials. Contact the app team to request a policy granting read access to their paths.
