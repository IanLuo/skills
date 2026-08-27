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
3. Runs your command with credentials available

**Consumer knows:**
- Service name
- Command to run
- Which variables are available

**Consumer doesn't know:**
- Store location
- File structure
- How loading works

## For Users

**1. Setup credentials (one-time):**

```bash
bash skills/credentials/scripts/create.sh myservice \
  VAR1 VAR2
```

**2. Use them:**

```bash
bash skills/credentials/scripts/run.sh myservice 'echo $VAR1 $VAR2'
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

## Anti-Patterns

❌ Reading files: `cat ~/.credentials/service.env`  
❌ Inline secrets: `export TOKEN=abc123`  
❌ Settings.json: agents can read it  

✅ Sourcing: `source skills/credentials/scripts/load.sh`

## References

- [setup-guide.md](references/setup-guide.md) — multi-account, troubleshooting
- **scripts/create.sh** — interactive credential creator
- **scripts/update.sh** — update existing credentials  
- **scripts/run.sh** — run commands with credentials loaded
- **scripts/load.sh** — low-level loader (rarely needed directly)

## Skills Using This Pattern

- [atlassian](../atlassian) — example implementation
- (Add more as created)
