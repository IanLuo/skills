---
name: credentials
description: Secure credential loading mechanism for integration skills. Provides sourcing without LLM exposure. Use when building skills that need API tokens, keys, or secrets - provides only the loading mechanism, consumers specify file paths and variable names.
metadata:
  audience: personal
  domain: security
---

# credentials

Generic credential management infrastructure. Consumers only need service name + command.

## The Mechanism

**Consumer API:**
```bash
bash skills/credentials/scripts/run.sh <service_name> '<command>'
```

**What it does:**
1. Loads credentials from `~/.credentials/<service_name>.env`
2. Exports all variables to environment
3. Runs your command — output goes to a **temp file**, not stdout
4. Scrubs credential values from the output file
5. Returns only: `exit_code`, `output` (file path), `bytes`

**To read results:** use the `read` tool on the returned file path.

**Consumer knows:**
- Service name
- Command to run
- Which variables are available

**Consumer doesn't know:**
- Store location
- File structure
- How loading works
- Raw credential values (scrubbed from output)

## For Users

**1. Setup credentials (one-time):**

```bash
bash skills/credentials/scripts/create.sh myservice \
  VAR1 VAR2
```

**2. Use them:**

```bash
bash skills/credentials/scripts/run.sh myservice 'echo $VAR1 $VAR2'
# Returns: exit_code=0  output=/tmp/cred-run-XXXXXX  bytes=...
# Then: read the output file to see results
```

That's it!

## For Skill Authors

**Document in your SKILL.md:**

```markdown
## Setup

```bash
bash skills/credentials/scripts/create.sh your-service \
  YOUR_VAR1 YOUR_VAR2
```

## How to Use

```bash
bash skills/credentials/scripts/run.sh your-service 'YOUR_COMMAND'
```

Variables available: `$YOUR_VAR1`, `$YOUR_VAR2`
```

**No scripts needed in your skill!** Credentials skill handles everything.

## Security Principles

1. **Interactive input only** — use `create.sh`/`update.sh` scripts to prompt for credentials; never send to LLM
2. **Source, never read** — `load.sh` sources files without content entering LLM context
3. **Store structure** — `~/.credentials/` directory (700), `.env` files (600)
4. **External storage** — files live outside repo
5. **Fail fast** — validation errors before execution
6. **Two-layer output scrubbing** — `run.sh` scrubs the output file in two passes:
   - **(a) Known values** — credential values loaded from `~/.credentials/<svc>.env` are replaced with `[REDACTED]`
   - **(b) Generic JSON keys** — JSON string/number values under keys matching `secret|token|password|passwd|credential|private_key|api_key` (case-insensitive) are replaced with `[REDACTED]`. This catches secrets *retrieved at runtime* (e.g. a `client_secret` fetched from Vault) that are unknown at load time.

   > **Pagination note:** keys like `next_token` or `continuation_token` will also be redacted. Commands that need pagination cursors should chain internally — consume the cursor in the same `run.sh` command and only emit the final aggregated result.

## Anti-Patterns

❌ Reading files: `cat ~/.credentials/service.env`  
❌ Inline secrets: `export TOKEN=abc123`  
❌ Settings.json: agents can read it  
❌ Piping run.sh to stdout: credentials may appear in command output  
❌ Using `jq '.data.data'` on Vault responses: retrieved secrets land in the output file  
❌ Reading output files containing runtime-fetched secrets (even after known-value scrub)  

✅ Sourcing: `source skills/credentials/scripts/load.sh`  
✅ File-based output: `run.sh` writes to temp file, returns path only  
✅ Chain retrieve→use inside one `run.sh` command; only emit non-secret final responses  
✅ Use `jq '... | keys'` or metadata filters to inspect without exposing values

## References

- [setup-guide.md](references/setup-guide.md) — multi-account, troubleshooting
- **scripts/create.sh** — interactive credential creator
- **scripts/update.sh** — update existing credentials  
- **scripts/run.sh** — run commands with credentials loaded
- **scripts/load.sh** — low-level loader (rarely needed directly)

## Skills Using This Pattern

- [atlassian](../atlassian) — Jira & Confluence
- [github](../github) — GitHub API & private repos
