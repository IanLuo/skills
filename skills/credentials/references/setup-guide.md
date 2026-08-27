# Credential Setup Guide

Generic setup instructions for the credentials loading mechanism.

## First-Time Setup

### 1. Create Credentials Directory

```bash
mkdir -p ~/.credentials
chmod 700 ~/.credentials
```

Restrictive permissions (700 = owner-only) prevent other users from reading secrets.

### 2. Create Service Credential File

```bash
# Generic example
cat > ~/.credentials/myservice.env <<'EOF'
VAR1=value1
VAR2=value2
VAR3=value3
EOF

chmod 600 ~/.credentials/myservice.env
```

**File naming:** Your choice (credentials skill doesn't dictate)  
**Permissions:** `chmod 600` (owner read/write only)

### 3. Load Credentials

```bash
# Manual load
source skills/credentials/scripts/load.sh \
  ~/.credentials/myservice.env \
  VAR1 VAR2 VAR3

# With override variable
source skills/credentials/scripts/load.sh \
  "${MY_CREDENTIAL_FILE:-$HOME/.credentials/myservice.env}" \
  VAR1 VAR2 VAR3
```

**Or auto-load in shell profile** (`~/.zshrc` or `~/.bashrc`):

```bash
# Example - adjust to your service
if [ -f ~/path/to/skills/credentials/scripts/load.sh ]; then
  source ~/path/to/skills/credentials/scripts/load.sh \
    ~/.credentials/myservice.env \
    VAR1 VAR2
fi
```

## Managing Multiple Accounts

Keep separate files for different accounts/environments:

```
~/.credentials/
├── service-personal.env
├── service-work.env
├── service-dev.env
└── service-prod.env
```

Load specific one:

```bash
# Personal
MY_FILE=~/.credentials/service-personal.env
source skills/credentials/scripts/load.sh "$MY_FILE" VAR1 VAR2

# Work
MY_FILE=~/.credentials/service-work.env
source skills/credentials/scripts/load.sh "$MY_FILE" VAR1 VAR2
```

Or use override variables (if the consuming skill defines them):

```bash
MY_CREDENTIAL_FILE=~/.credentials/service-work.env \
  source skills/credentials/scripts/load.sh \
    "${MY_CREDENTIAL_FILE:-$HOME/.credentials/service.env}" \
    VAR1 VAR2
```

## Troubleshooting

### "Credential file not found"

**Cause:** File doesn't exist at specified path

**Fix:**
1. Check path: `ls -la ~/.credentials/`
2. Create file (see step 2 above)
3. Verify path matches what you're passing to load.sh

### "Missing required variables"

**Cause:** File exists but doesn't define all required vars

**Fix:**
1. Check what vars are required (listed in error message)
2. Add missing vars to credential file
3. Ensure no typos (variable names are case-sensitive)

### "Permission denied"

**Cause:** File permissions too restrictive or wrong ownership

**Fix:**
```bash
chmod 600 ~/.credentials/myservice.env
# If owned by different user:
sudo chown $USER ~/.credentials/myservice.env
```

### Variables not available in subshells

**Cause:** Variables loaded but not exported

**Fix:** The load.sh script exports automatically. If manually sourcing:
```bash
export VAR1 VAR2 VAR3
```

## Security Best Practices

### 1. Never commit credential files

Add to `.gitignore`:
```
.credentials/
*.env
```

### 2. Rotate credentials regularly

- Personal: quarterly
- Production: monthly
- After team member departure: immediately

### 3. Use minimum permissions

Request only scopes/permissions actually needed for the task.

### 4. Keep credentials out of shell history

Don't set secrets inline:
```bash
# BAD - exposes in history
export API_TOKEN=sk-xxxxx

# GOOD - source from file
source skills/credentials/scripts/load.sh ~/.credentials/service.env API_TOKEN
```

Clear history after accidental exposure:
```bash
history -d <line-number>
# Or full clear
history -c
```

### 5. Encrypt credentials directory (optional)

For extra security on shared machines:
- macOS: FileVault encrypts home directory automatically
- Linux: LUKS full-disk encryption or eCryptfs per-directory

## Custom Credential Location

Override default path:

```bash
# Project-local (not recommended - easy to commit)
MY_FILE=./.credentials/local.env

# Shared team on mounted volume
MY_FILE=/mnt/shared/credentials/service.env

# 1Password CLI integration
MY_FILE=<(op inject -i ~/.credentials/service.env.tpl)

source skills/credentials/scripts/load.sh "$MY_FILE" VAR1 VAR2
```

## Verification

Test your setup:

```bash
# Load credentials
source skills/credentials/scripts/load.sh \
  ~/.credentials/myservice.env \
  VAR1 VAR2

# Verify variables set (without printing values)
[ -n "$VAR1" ] && echo "✓ VAR1 set"
[ -n "$VAR2" ] && echo "✓ VAR2 set"
```

Service-specific verification (e.g., test API call) belongs in that service's documentation.
