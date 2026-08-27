---
name: atlassian
description: Access Atlassian Jira and Confluence (wiki) via REST API using curl. Use when the user asks to "search Jira", "create issue", "get ticket", "update Jira", "search wiki", "get Confluence page", "create wiki page", mentions ticket numbers like "PROJ-123", or needs to interact with Atlassian products. Shares authentication across both services.
metadata:
  audience: personal
  domain: integration
---


# atlassian

Access Jira and Confluence via their REST APIs using curl and bash. Both services share the same Atlassian API token for authentication.

## Setup

**One-time credential setup:**

```bash
bash skills/credentials/scripts/create.sh atlassian \
  ATLASSIAN_URL ATLASSIAN_EMAIL ATLASSIAN_API_TOKEN
```

**Token:** https://id.atlassian.com/manage-profile/security/api-tokens

**That's it!** Use the commands below - credentials load automatically.

## How to Use

**All commands use this pattern:**

```bash
bash skills/credentials/scripts/run.sh atlassian 'YOUR_COMMAND_HERE'
```

**Variables available in commands:**
- `$ATLASSIAN_URL` - Your domain
- `$ATLASSIAN_EMAIL` - Your email
- `$ATLASSIAN_API_TOKEN` - Your token

**Example - Get my profile:**

```bash
bash skills/credentials/scripts/run.sh atlassian \
  'curl -s -u "$ATLASSIAN_EMAIL:$ATLASSIAN_API_TOKEN" \
   "$ATLASSIAN_URL/rest/api/3/myself" | jq .'
```

That's everything you need to know!

## Common Jira Operations

All examples use: `bash skills/credentials/scripts/run.sh atlassian 'COMMAND'`

### Search Issues (JQL)

```bash
bash skills/credentials/scripts/run.sh atlassian \
  'curl -s -u "$ATLASSIAN_EMAIL:$ATLASSIAN_API_TOKEN" \
   -H "Content-Type: application/json" \
   "$ATLASSIAN_URL/rest/api/3/search/jql" \
   -G --data-urlencode "jql=project = PROJ AND status = Open" \
   --data-urlencode "fields=key,summary,status" \
   --data-urlencode "maxResults=10" \
   | jq ".issues[] | {key, summary, status: .fields.status.name}"'
```

**Start with small limits** (5-10) when exploring, increase if needed. Filter fields to reduce context size.

Common JQL patterns:
- `project = PROJ` — all issues in project
- `assignee = currentUser()` — assigned to authenticated user
- `status = "In Progress"` — by status
- `created >= -7d` — last 7 days
- `text ~ "OAuth token rotation"` — multi-word phrases (more specific than single words)

### Get Issue Details

```bash
# Get specific issue by key (e.g., PROJ-123)
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123" \
  | jq '.'
```

### Create Issue

```bash
# Create new issue
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issue" \
  -d '{
    "fields": {
      "project": {"key": "PROJ"},
      "summary": "Issue title",
      "description": {
        "type": "doc",
        "version": 1,
        "content": [{
          "type": "paragraph",
          "content": [{"type": "text", "text": "Issue description"}]
        }]
      },
      "issuetype": {"name": "Task"}
    }
  }' | jq '.'
```

### Update Issue

```bash
# Update issue fields
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X PUT \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123" \
  -d '{
    "fields": {
      "summary": "Updated title"
    }
  }'
```

### Add Comment

```bash
# Add comment to issue
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/comment" \
  -d '{
    "body": {
      "type": "doc",
      "version": 1,
      "content": [{
        "type": "paragraph",
        "content": [{"type": "text", "text": "Comment text"}]
      }]
    }
  }' | jq '.'
```

### Transition Issue (Change Status)

```bash
# Get available transitions for an issue
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/transitions" \
  | jq '.transitions[] | {id, name}'

# Apply transition (e.g., move to "In Progress")
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/transitions" \
  -d '{"transition": {"id": "21"}}'
```

## Common Confluence (Wiki) Operations

Confluence uses `/rest/api/` endpoint (no `/3/` version).

### Search Pages (CQL)

```bash
# Search with CQL query
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${ATLASSIAN_URL}/wiki/rest/api/search" \
  -G --data-urlencode "cql=space=OAUTH AND text~\"OAuth token rotation\"" \
  --data-urlencode "limit=5" \
  | jq '.results[] | {id, title, space: .space.name}'
```

**Start specific:** Use space name + multi-word phrases, not broad single-word searches. Start with `limit=5`, increase if needed.

Common CQL patterns:
- `type=page` — pages only
- `space=SPACE` — in specific space (always include when known)
- `title~"Migration Guide"` — title contains (use quotes for multi-word)
- `text~"OAuth token rotation"` — content contains (specific phrases beat single words)
- `created >= "2026-01-01"` — date range

### Get Page by ID

```bash
# Get page content
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345?expand=body.storage,version" \
  | jq '.'
```

### Get Page by Title

```bash
# Find page by title in space
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${ATLASSIAN_URL}/wiki/rest/api/content" \
  -G --data-urlencode "title=Page Title" \
  --data-urlencode "spaceKey=SPACE" \
  --data-urlencode "expand=body.storage,version" \
  | jq '.'
```

### Create Page

```bash
# Create new page in space
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/wiki/rest/api/content" \
  -d '{
    "type": "page",
    "title": "New Page Title",
    "space": {"key": "SPACE"},
    "body": {
      "storage": {
        "value": "<p>Page content in Confluence storage format</p>",
        "representation": "storage"
      }
    }
  }' | jq '.'
```

### Update Page

```bash
# Update existing page (requires version increment)
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X PUT \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345" \
  -d '{
    "type": "page",
    "title": "Updated Title",
    "version": {"number": 2},
    "body": {
      "storage": {
        "value": "<p>Updated content</p>",
        "representation": "storage"
      }
    }
  }' | jq '.'
```

**Note:** Must increment version number. Get current version from GET request first.

### List Spaces

```bash
# List all spaces
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/wiki/rest/api/space" \
  | jq '.results[] | {key, name}'
```

## Working Rules

1. **Start small, filter aggressively** — use small limits (5-10) when exploring, increase if needed. Filter with jq at the source to extract only needed fields. Multi-word CQL/JQL phrases beat single words. Include space/project constraints when known.

2. **Check environment variables first** — verify `ATLASSIAN_URL`, `ATLASSIAN_EMAIL`, `ATLASSIAN_API_TOKEN` are set before making requests. If missing, tell user to set them.

3. **Use jq for field extraction** — pipe curl output to `jq` and extract only needed fields. Common patterns:
   - `.issues[] | {key, summary, status: .fields.status.name}` — Jira search
   - `.results[] | {id, title, space: .space.name}` — Confluence search
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

5. **Stop speculative searches** — after 3-4 failed search attempts, stop and ask user for: space name, project key, more specific keywords, or exact page/issue identifier. Don't burn tokens on broad guesses.

6. **Confluence storage format** — Confluence uses XHTML-based storage format for content. Basic patterns:
   - Paragraph: `<p>text</p>`
   - Heading: `<h1>Title</h1>`
   - Link: `<ac:link><ri:page ri:content-title="Page Name"/></ac:link>`

7. **Version increments** — updating Confluence pages requires the next version number. Always GET the page first to read current version.

8. **Issue keys** — when user mentions "PROJ-123" format, treat as Jira issue key.

9. **Pagination** — both APIs support pagination. Use `maxResults`/`limit` and `startAt`/`start` parameters for large result sets.

## Interaction Style

- **Verify setup** — if env vars missing, provide exact setup instructions with the user's domain.
- **Show results** — display key fields (issue key, title, status, URL) in readable format, not raw JSON dumps.
- **Provide links** — construct and show clickable URLs to issues/pages: `${ATLASSIAN_URL}/browse/PROJ-123` or `${ATLASSIAN_URL}/wiki/spaces/SPACE/pages/12345`
- **Confirm writes** — before creating/updating, show what will be changed and the payload.

## Error Handling

Common errors:

- **401 Unauthorized** — invalid credentials, verify email and token
- **403 Forbidden** — insufficient permissions for the operation
- **404 Not Found** — issue key, page ID, or space doesn't exist
- **400 Bad Request** — malformed JSON or required fields missing

Check response status and show meaningful error to user.

## References

**Read [atlassian-api-reference.md](references/atlassian-api-reference.md) when:**
- You need attachments, bulk operations, issue linking, or other advanced operations
- You hit format errors in descriptions/comments (ADF/storage format schemas)
- You need pagination (large result sets) or hit rate limits

API Documentation:
- Jira Cloud REST API: `${ATLASSIAN_URL}/rest/api/3/` (v3)
- Confluence Cloud REST API: `${ATLASSIAN_URL}/wiki/rest/api/`
