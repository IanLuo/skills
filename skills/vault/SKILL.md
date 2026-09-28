---
name: vault
description: Access HashiCorp Vault (CIAM team) via REST API using curl. Use when the user asks to "get secret", "read secret", "ciam vault", "vault credentials", "auth info from vault", "list secrets", "write secret", mentions vault.ciam.stg.aligntech.com or vault.ciam.aligntech.com URLs, or needs application credentials stored in Vault. Do NOT use for Vault server administration, unsealing, or Terraform-managed Vault config.
metadata:
  audience: personal
  domain: integration
---


# vault

Read and manage secrets in the CIAM team's HashiCorp Vault instances via the REST API using curl and bash.

## ⛔ SECURITY — RETRIEVED SECRETS MUST NEVER ENTER LLM CONTEXT

**Hard rule:** Never `read` an output file that may contain secret values. `run.sh` scrubs *known* credential values, but secrets *retrieved at runtime* (e.g. a `client_secret` fetched FROM Vault) are unknown at scrub time and will appear in plaintext.

**Two safe patterns:**

1. **INSPECT** — prove a secret exists without seeing its value:
   - `jq '.data.data | keys'` — list field names only
   - `jq '.data.metadata'` — version/timestamps, no values
   - `jq '.data.data | to_entries | map({key, len: (.value | tostring | length)})'` — field names + value lengths

2. **USE** — retrieve and consume in ONE chained command:
   - Login → fetch secret → use it in a downstream API call → print only the *non-secret* final response
   - The secret lives in a shell variable inside `run.sh`, never written to the output file
   - See the worked example below

**Never do:** `jq '.data.data'`, `jq '.auth.client_token'`, or any jq filter that emits secret values to the output file.

## Instances

| Environment | UI | API Base |
|---|---|---|
| Dev & Stage | https://vault.ciam.stg.aligntech.com/ui/vault/dashboard | `https://vault.ciam.stg.aligntech.com/v1` |
| Prod | https://vault.ciam.aligntech.com/ui/vault/dashboard | `https://vault.ciam.aligntech.com/v1` |

**URL mapping:** UI path `/ui/vault/secrets/<mount>/show/<path>` → API `GET /v1/<mount>/data/<path>`

## Setup

### Option A — Userpass auth (recommended for API/automation)

```bash
bash skills/credentials/scripts/create.sh vault \
  VAULT_ADDR VAULT_USERNAME VAULT_PASSWORD
```

- `VAULT_ADDR` — Instance URL, e.g. `https://vault.ciam.stg.aligntech.com`
- `VAULT_USERNAME` — Your Vault userpass username
- `VAULT_PASSWORD` — Your Vault userpass password

### Option B — Direct token (short-lived, from OIDC browser login)

```bash
bash skills/credentials/scripts/create.sh vault \
  VAULT_ADDR VAULT_TOKEN
```

- `VAULT_TOKEN` — Copy from the UI after OIDC login (top-right menu → "Copy token")

> **OIDC login (humans):** Navigate to the Vault UI → select "OIDC" method → "Sign in with OIDC Provider" → authenticate with your Align email. This is browser-based and cannot be automated via curl.

**That's it!** Use the commands below — credentials load automatically.

## How to Use

**All commands use this pattern:**

```bash
bash skills/credentials/scripts/run.sh vault 'YOUR_COMMAND_HERE'
# Returns: exit_code=0  output=/tmp/cred-run-XXXXXX  bytes=...
# Then: read the output file to see results
```

`run.sh` writes command output to a temp file (with credentials scrubbed) and returns the file path. **Use the `read` tool on that path to see results.**

**Variables available in commands:**
- `$VAULT_ADDR` — Instance base URL (no trailing slash)
- `$VAULT_USERNAME` / `$VAULT_PASSWORD` — Userpass credentials (Option A)
- `$VAULT_TOKEN` — Direct token (Option B)

## Authentication — Login & Get Token

Userpass auth is a **two-step** process: login to get a client token, then use that token for all subsequent calls. **The token itself is a secret — never emit it to the output file.**

### Login (userpass) — verify success only

```bash
bash skills/credentials/scripts/run.sh vault \
  'curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" \
   | jq "{policies: .auth.policies, lease_duration: .auth.lease_duration, renewable: .auth.renewable}"'
```

### Login + Inspect Secret Keys (safe)

Chain login and inspection — only field names reach the output file:

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token") && \
   curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/data/ciam/stg/qas-us/Application_Credentials/my_app" \
   | jq "{keys: (.data.data | keys), metadata: .data.metadata}"'
```

### Login + USE Secret → Call Target API (the real workflow)

Retrieve credentials from Vault and use them to call a downstream API — only the **target API's non-secret response** reaches the output file:

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token") && \
   SECRET_JSON=$(curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/data/ciam/stg/qas-us/Application_Credentials/my_app" \
   | jq -r ".data.data") && \
   CID=$(echo "$SECRET_JSON" | jq -r ".client_id") && \
   CSEC=$(echo "$SECRET_JSON" | jq -r ".client_secret") && \
   curl -s -X POST "https://api.example.com/oauth/token" \
   -d "grant_type=client_credentials&client_id=$CID&client_secret=$CSEC" \
   | jq "{status: .status, token_type: .token_type, expires_in: .expires_in}"'
```

> **Pattern:** login → fetch secret into shell var → extract fields into vars → call target API → jq-filter only non-secret fields from the final response.

### Token Lookup (verify token is valid)

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/auth/token/lookup-self" \
   | jq "{display_name: .data.display_name, policies: .data.policies, ttl: .data.ttl, expire_time: .data.expire_time}"'
```

### Token Renew — safe output only

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -X POST -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/auth/token/renew-self" \
   | jq "{ttl: .auth.lease_duration, expire_time: .auth.expire_time, policies: .auth.policies, renewable: .auth.renewable}"'
```

## Reading Secrets (KV v2)

### Inspect Secret Keys (no values)

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/data/ciam/stg/qas-us/Application_Credentials/ciam~ciam_api_user" \
   | jq "{keys: (.data.data | keys), metadata: .data.metadata}"'
```

### Inspect a Specific Version (metadata + keys only)

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/data/ciam/stg/qas-us/Application_Credentials/my_app?version=2" \
   | jq "{keys: (.data.data | keys), metadata: .data.metadata}"'
```

## Listing Secrets

### List Entries Under a Path

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -X LIST -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/metadata/ciam/stg/qas-us/Application_Credentials" \
   | jq ".data.keys"'
```

Folders are suffixed with `/`. Navigate deeper by appending the folder name to the path.

### Browse from the Top of a Mount

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -X LIST -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/metadata/" \
   | jq ".data.keys"'
```

## Writing Secrets (if policy allows)

### Create or Update a Secret

> **Note:** The payload below contains secret values — they're passed as shell literals inside `run.sh` and never reach the output file. Only the write-confirmation metadata is emitted.

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -X POST -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/data/ciam/stg/qas-us/Application_Credentials/my_app" \
   -d "{\"data\": {\"client_id\": \"abc\", \"client_secret\": \"xyz\"}}" \
   | jq "{created_time: .data.created_time, version: .data.version, destroyed: .data.destroyed}"'
```

### Read Secret Metadata (version history)

```bash
bash skills/credentials/scripts/run.sh vault \
  'TOKEN=${VAULT_TOKEN:-$(curl -s -X POST "$VAULT_ADDR/v1/auth/userpass/login/$VAULT_USERNAME" \
   -d "{\"password\": \"$VAULT_PASSWORD\"}" | jq -r ".auth.client_token")} && \
   curl -s -H "X-Vault-Token: $TOKEN" \
   "$VAULT_ADDR/v1/customer/metadata/ciam/stg/qas-us/Application_Credentials/my_app" \
   | jq "{current_version: .data.current_version, versions: .data.versions}"'
```

## Secret Path Convention

Paths follow this structure (from the CIAM team wiki):

```
<mount>/<group_name>/<phase>/<env>-<region>/ApplicationCredentials/<app_name>
```

| Segment | Examples |
|---|---|
| mount | `customer` |
| group_name | `ciam` |
| phase | `stg`, `prd` |
| env-region | `qas-us`, `prd-us`, `prd-eu` |
| app_name | `ciam~ciam_api_user`, `myapp~client` |

Example full path: `customer/ciam/stg/qas-us/Application_Credentials/ciam~ciam_api_user`

> **Note:** Application teams control who can access their app credentials in the vault. The platform team no longer provides access to application credentials. Ask the app team owner to grant your policy access.

## Getting secret values (human-friendly, LLM-safe)

The `vault-get` helper retrieves secrets with **four output modes** — three safe for agent use, one restricted to the user's own terminal.

### Decision table

| I want to… | Mode | Agent-safe? | What stdout shows |
|---|---|---|---|
| Verify a secret exists / check rotation | `--masked` (default) | ✅ | name, first4…last4, length, sha256 fingerprint |
| Paste a value into a browser/app | `--clipboard FIELD` | ✅ | "copied FIELD (N chars) to clipboard" |
| Populate a config file | `--write FILE` | ✅ | "wrote N keys to FILE" |
| Eyeball the raw value in YOUR terminal | `--reveal` | ⛔ user-only | full `key=value` lines |

**TTY gate (why `--reveal` is user-only):** The first thing `--reveal` does is `[ ! -t 1 ]` — if stdout is not an interactive terminal it prints an error and exits 2. Agent-run commands always pipe stdout (to capture output), so agents *mechanically cannot* reveal secrets. There is no `--force` override by design.

### Usage

```
vault-get <secret-path> [options]

  --field NAME           Operate on one field (default: all)
  --masked               (DEFAULT) Safe summary per field
  --clipboard FIELD      Copy raw value to clipboard (macOS pbcopy)
  --write FILE           Write to file (--format env|json, default: env)
  --reveal               Full values (⛔ TTY-gated, user terminal only)
```

### Worked examples

#### Direct in your terminal (user-run)

```bash
# Masked — verify what's there
bash ~/.claude/skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --masked'

# Copy a field to clipboard
bash ~/.claude/skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --clipboard client_secret'

# Write to .env file
bash ~/.claude/skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --write .env.development.local --format env'

# Reveal in your terminal (only works interactively)
~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --reveal
```

#### Agent-run (via run.sh)

```bash
# Agent uses --masked to verify
bash skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --masked'

# Agent copies to clipboard for the user
bash skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --clipboard client_secret'

# Agent writes config file
bash skills/credentials/scripts/run.sh vault \
  '~/.claude/skills/vault/scripts/vault-get customer/ciam/stg/qas-us/Application_Credentials/my_app --write .env.development.local --format env'
```

### Agent rule

When the user asks to "get/show me a secret": use `--clipboard` (to put it on their clipboard), `--write` (to put it in a file), or `--masked` (to verify). Tell the user where the value went. **Never attempt `--reveal`** — it will fail in agent context, and even if it didn't, the value would enter LLM context.

## Working Rules

1. **Always login first** — every API call needs `X-Vault-Token`. Use the `TOKEN=$(...) &&` pattern to chain login + operation in one `run.sh` call. If using Option B (direct token), `$VAULT_TOKEN` is available directly.

2. **KV v2 paths** — Read uses `/data/` in the path; List uses `/metadata/` in the path. This is the most common mistake. The UI hides this distinction.

3. **Start with list** — when exploring, list from the mount root and navigate down. Don't guess deep paths.

4. **Filter with jq** — extract only **non-secret** fields:
   - `.data.data | keys` — secret field names only (NEVER `.data.data` raw)
   - `.data.metadata` — version info, timestamps
   - `.data.keys` — child entries (list)
   - `.auth.policies`, `.auth.lease_duration` — login confirmation (NEVER `.auth.client_token`)

5. **Token lifecycle** — tokens from userpass have a TTL (check `lease_duration` in login response). Renew with `/auth/token/renew-self` before expiry. OIDC tokens from the UI are typically short-lived.

6. **Stop speculative paths** — after 2-3 404s, stop and ask the user for: the exact mount name, secret path, or app name. Check the UI first.

7. **Confirm writes** — before writing/updating secrets, show the user the exact path and payload. Never write without confirmation.

## Interaction Style

- **Verify setup** — if credentials are missing, provide the exact `create.sh` command for the user's auth method.
- **Show results** — display secret key names (NEVER values), path, and version info. Don't dump raw JSON. If the user needs to *use* a secret value, chain it inside one `run.sh` command.
- **Provide UI links** — construct clickable URLs: `$VAULT_ADDR/ui/vault/secrets/<mount>/show/<path>`
- **Confirm writes** — before creating/updating, show path and payload structure.

## Error Handling

Common errors:

- **400 Bad Request** — malformed JSON payload or missing required fields
- **403 Forbidden** — invalid/expired token, or policy doesn't grant access to the path
- **404 Not Found** — path doesn't exist (check mount name and `/data/` vs `/metadata/`)
- **429 Too Many Requests** — rate limited, back off
- **500 Internal Server Error** — server issue (check `/v1/sys/health`)

```bash
# Check health
bash skills/credentials/scripts/run.sh vault \
  'curl -s "$VAULT_ADDR/v1/sys/health" \
   | jq "{initialized, sealed, version, cluster_name}"'
```

## ⚠️ Verify on First Use

The auth methods and mount paths documented here are based on probing unauthenticated endpoints and the team wiki. On first authenticated use, verify:

1. **Auth method** — if userpass login fails with 403, you may need OIDC (browser) or approle credentials instead. Check with the CIAM platform team.
2. **Mount names** — `customer/` is the known mount from wiki examples. Run a list on `/v1/sys/mounts` with your token to discover all available mounts.
3. **Path structure** — the `<stack>/<group>/<phase>/<env>-<region>/Application_Credentials/<app>` convention is from wiki; actual paths may vary.

## References

- **[vault-api-reference.md](references/vault-api-reference.md)** — full KV v2 and auth endpoint reference
- **Wiki:** [Vault Access](https://cloud.wiki.aligntech.com/wiki/spaces/IntegrEng/pages/99102991/Vault+Access) (IntegrEng space) — login steps, path conventions
- **Vault API Docs:** https://developer.hashicorp.com/vault/api-docs
