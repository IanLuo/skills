---
name: github
description: Access GitHub via REST API and git operations using a PAT. Use when the user asks to "clone private repo", "access GitHub API", "install private gems", "install private npm packages", "list repos", "create issue", "get PR", "search code", mentions GitHub URLs, or needs any GitHub-authenticated operation. Do NOT use for public-only git operations that need no token.
metadata:
  audience: personal
  domain: integration
---


# github

Access GitHub REST API, clone private repos, and authenticate package managers — all via a single Personal Access Token (PAT).

## Setup

**One-time credential setup:**

```bash
bash skills/credentials/scripts/create.sh github \
  GITHUB_TOKEN
```

**Token:** https://github.com/settings/tokens (classic) or https://github.com/settings/personal-access-tokens (fine-grained)

**That's it!** Use the commands below - credentials load automatically.

## How to Use

**All commands use this pattern:**

```bash
bash skills/credentials/scripts/run.sh github 'YOUR_COMMAND_HERE'
# Returns: exit_code=0  output=/tmp/cred-run-XXXXXX  bytes=...
# Then: read the output file to see results
```

`run.sh` writes command output to a temp file (with credentials scrubbed) and returns the file path. **Use the `read` tool on that path to see results.**

**Variables available in commands:**
- `$GITHUB_TOKEN` - Your PAT

**Example - Get authenticated user:**

```bash
bash skills/credentials/scripts/run.sh github \
  'curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
   https://api.github.com/user | jq .'
# Read the output file path from the result
```

That's everything you need to know!

## Git & Package Manager Operations

All examples use: `bash skills/credentials/scripts/run.sh github 'COMMAND'`

### Clone Private Repo

```bash
bash skills/credentials/scripts/run.sh github \
  'git clone https://x-access-token:$GITHUB_TOKEN@github.com/org/repo.git'
```

### Bundle Install with Private Gems

```bash
bash skills/credentials/scripts/run.sh github \
  'BUNDLE_GITHUB__COM=x-access-token:$GITHUB_TOKEN bundle install'
```

### npm/yarn with Private GitHub Packages

```bash
bash skills/credentials/scripts/run.sh github \
  'npm config set //npm.pkg.github.com/:_authToken $GITHUB_TOKEN && npm install'
```

## Common API Operations

All examples use: `bash skills/credentials/scripts/run.sh github 'COMMAND'`

Base URL: `https://api.github.com`

### List Repos

```bash
# List repos for authenticated user
bash skills/credentials/scripts/run.sh github \
  'curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
   "https://api.github.com/user/repos?per_page=10&sort=updated" \
   | jq ".[] | {name: .full_name, private: .private, updated: .updated_at}"'
```

### List Org Repos

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/orgs/ORG/repos?per_page=10&sort=updated" \
  | jq '.[] | {name: .full_name, private: .private}'
```

### Create Issue

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  -H "Content-Type: application/json" \
  -X POST \
  "https://api.github.com/repos/OWNER/REPO/issues" \
  -d '{
    "title": "Issue title",
    "body": "Issue description",
    "labels": ["bug"]
  }' | jq '{number, title, html_url}'
```

### Get Pull Request

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/repos/OWNER/REPO/pulls/123" \
  | jq '{number, title, state, user: .user.login, mergeable}'
```

### List PR Files

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/repos/OWNER/REPO/pulls/123/files" \
  | jq '.[] | {filename, status, additions, deletions}'
```

### Search Code

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/search/code" \
  -G --data-urlencode "q=oauth+org:myorg+language:ruby" \
  --data-urlencode "per_page=5" \
  | jq '.items[] | {name, path, repo: .repository.full_name, html_url}'
```

### Search Issues/PRs

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/search/issues" \
  -G --data-urlencode "q=is:pr is:open repo:OWNER/REPO label:bug" \
  --data-urlencode "per_page=10" \
  | jq '.items[] | {number, title, state, user: .user.login}'
```

### Get Repo Details

```bash
curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/repos/OWNER/REPO" \
  | jq '{full_name, private, default_branch, language, open_issues_count}'
```

## Working Rules

1. **Start small, filter aggressively** — use `per_page=5` or `per_page=10` when exploring; increase if needed. Filter with jq at the source to extract only needed fields.

2. **Check environment variables first** — verify `GITHUB_TOKEN` is set before making requests. If missing, tell user to run the setup command.

3. **Use jq for field extraction** — pipe curl output to `jq` and extract only needed fields. Common patterns:
   - `.[] | {name: .full_name, private: .private}` — repo list
   - `.items[] | {number, title, state}` — search results
   - `{number, title, html_url}` — single issue/PR
   - Not: `jq .` (returns everything)

4. **Handle errors** — check curl exit code and parse error responses:
   ```bash
   response=$(curl -s -w "\n%{http_code}" ...)
   http_code=$(echo "$response" | tail -n1)
   body=$(echo "$response" | sed '$d')
   if [ "$http_code" -ne 200 ]; then
     echo "Error: $body"
   fi
   ```

5. **Auth header pattern** — always use `Authorization: Bearer $GITHUB_TOKEN`. The username for git URLs is always `x-access-token`.

6. **Stop speculative searches** — after 3-4 failed search attempts, stop and ask user for: org name, repo name, more specific keywords, or exact issue/PR number.

7. **Pagination** — GitHub uses Link headers and `per_page`/`page` params. Default is 30, max is 100.

8. **Rate limits** — authenticated requests get 5,000/hour. Check `X-RateLimit-Remaining` header if hitting limits.

## Interaction Style

- **Verify setup** — if token missing, provide exact setup instructions.
- **Show results** — display key fields (repo name, issue number, title, URL) in readable format, not raw JSON dumps.
- **Provide links** — construct clickable URLs: `https://github.com/OWNER/REPO/issues/123` or `https://github.com/OWNER/REPO/pull/456`
- **Confirm writes** — before creating/updating, show what will be changed and the payload.

## Error Handling

Common errors:

- **401 Unauthorized** — invalid or expired token, verify PAT
- **403 Forbidden** — insufficient token scopes or rate limited
- **404 Not Found** — repo/issue doesn't exist or token lacks access
- **422 Unprocessable Entity** — validation failed, check required fields

Check response status and show meaningful error to user.

## References

API Documentation: https://docs.github.com/en/rest
