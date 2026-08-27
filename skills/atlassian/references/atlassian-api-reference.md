# Atlassian API Reference

Advanced operations and field schemas for Jira and Confluence REST APIs.

## Jira Advanced Operations

### Get Projects

```bash
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/rest/api/3/project" \
  | jq '.[] | {key, name, id}'
```

### Get Issue Types for Project

```bash
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/rest/api/3/project/PROJ/statuses" \
  | jq '.'
```

### Get Custom Fields

```bash
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/rest/api/3/field" \
  | jq '.[] | select(.custom==true) | {id, name, schema}'
```

### Link Issues

```bash
# Link two issues (e.g., "blocks" relationship)
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issueLink" \
  -d '{
    "type": {"name": "Blocks"},
    "inwardIssue": {"key": "PROJ-123"},
    "outwardIssue": {"key": "PROJ-456"}
  }'
```

### Bulk Issue Operations

```bash
# Bulk update multiple issues
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issue/bulk" \
  -d '{
    "issueUpdates": [
      {
        "key": "PROJ-123",
        "update": {
          "labels": [{"add": "urgent"}]
        }
      },
      {
        "key": "PROJ-124",
        "update": {
          "labels": [{"add": "urgent"}]
        }
      }
    ]
  }'
```

### Attach Files

```bash
# Upload attachment to issue
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -X POST \
  -H "X-Atlassian-Token: no-check" \
  -F "file=@/path/to/file.pdf" \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/attachments"
```

### Watch/Unwatch Issue

```bash
# Add watcher
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/watchers" \
  -d '"user@example.com"'

# Remove watcher
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -X DELETE \
  "${ATLASSIAN_URL}/rest/api/3/issue/PROJ-123/watchers?username=user@example.com"
```

## Confluence Advanced Operations

### Get Child Pages

```bash
# Get child pages of a page
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/child/page" \
  | jq '.results[] | {id, title}'
```

### Get Page History

```bash
# Get version history
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/version" \
  | jq '.results[] | {number, when, by: .by.displayName}'
```

### Add Labels to Page

```bash
# Add label
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/label" \
  -d '[{"name": "important"}]'
```

### Get Attachments

```bash
# List page attachments
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/child/attachment" \
  | jq '.results[] | {title, download: ._links.download}'
```

### Upload Attachment to Page

```bash
# Upload file
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -X POST \
  -H "X-Atlassian-Token: no-check" \
  -F "file=@/path/to/file.pdf" \
  -F "comment=Uploaded via API" \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/child/attachment"
```

### Copy Page

```bash
# Create copy with new title
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X POST \
  "${ATLASSIAN_URL}/wiki/rest/api/content/12345/copy" \
  -d '{
    "copyAttachments": true,
    "copyPermissions": false,
    "copyProperties": true,
    "copyLabels": true,
    "copyCustomContents": false,
    "destination": {
      "type": "space",
      "value": "TARGETSPACE"
    },
    "pageTitle": "Copy of Original Page"
  }'
```

## Field Schemas

### Jira Issue Fields

Common fields in issue creation/update:

```json
{
  "fields": {
    "project": {"key": "PROJ"},
    "summary": "Title",
    "description": {
      "type": "doc",
      "version": 1,
      "content": [/* ADF nodes */]
    },
    "issuetype": {"name": "Task"},
    "priority": {"name": "High"},
    "assignee": {"id": "accountId"},
    "labels": ["label1", "label2"],
    "components": [{"name": "Component"}],
    "fixVersions": [{"name": "1.0"}],
    "duedate": "2026-12-31",
    "customfield_10001": "custom value"
  }
}
```

### Atlassian Document Format (ADF)

Jira Cloud uses ADF for descriptions and comments:

```json
{
  "type": "doc",
  "version": 1,
  "content": [
    {
      "type": "paragraph",
      "content": [
        {"type": "text", "text": "Normal text "},
        {"type": "text", "text": "bold text", "marks": [{"type": "strong"}]},
        {"type": "text", "text": " more text"}
      ]
    },
    {
      "type": "bulletList",
      "content": [
        {
          "type": "listItem",
          "content": [
            {
              "type": "paragraph",
              "content": [{"type": "text", "text": "List item"}]
            }
          ]
        }
      ]
    }
  ]
}
```

Common ADF node types:
- `paragraph` — text paragraph
- `heading` (attrs: `{level: 1-6}`) — heading
- `bulletList` / `orderedList` — lists
- `listItem` — list item
- `codeBlock` — code block
- `panel` (attrs: `{panelType: "info|note|warning|error"}`) — colored panel

Text marks:
- `strong` — bold
- `em` — italic
- `code` — inline code
- `link` (attrs: `{href: "url"}`) — hyperlink

### Confluence Storage Format

Basic XHTML with Confluence macros:

```html
<p>Paragraph text</p>
<h1>Heading 1</h1>
<h2>Heading 2</h2>

<ul>
  <li>List item</li>
</ul>

<ac:structured-macro ac:name="info">
  <ac:rich-text-body>
    <p>Info panel content</p>
  </ac:rich-text-body>
</ac:structured-macro>

<ac:link>
  <ri:page ri:content-title="Other Page"/>
  <ac:plain-text-link-body><![CDATA[Link text]]></ac:plain-text-link-body>
</ac:link>

<ac:image>
  <ri:attachment ri:filename="image.png"/>
</ac:image>

<table>
  <tr>
    <th>Header</th>
  </tr>
  <tr>
    <td>Cell</td>
  </tr>
</table>
```

## Rate Limits

Atlassian Cloud rate limits:
- **General API calls:** ~1000 requests per hour per user
- **Search queries:** Lower limits apply
- **Bulk operations:** Use cautiously

If rate limited, response returns `429 Too Many Requests` with `Retry-After` header.

## Pagination

### Jira Pagination

```bash
# Page through results
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/rest/api/3/search" \
  -G --data-urlencode "jql=project=PROJ" \
  --data-urlencode "startAt=0" \
  --data-urlencode "maxResults=50"
```

Response includes `total`, `startAt`, `maxResults` for pagination logic.

### Confluence Pagination

```bash
# Page through results
curl -s -u "${ATLASSIAN_EMAIL}:${ATLASSIAN_API_TOKEN}" \
  "${ATLASSIAN_URL}/wiki/rest/api/content" \
  -G --data-urlencode "start=0" \
  --data-urlencode "limit=25"
```

Response includes `_links.next` for next page URL.

## Performance Tips

1. **Use field filtering** — request only needed fields:
   ```bash
   # Jira
   --data-urlencode "fields=key,summary,status"
   
   # Confluence
   --data-urlencode "expand=body.storage,version"
   ```

2. **Batch operations** — use bulk APIs when updating multiple items

3. **Cache project/field metadata** — project keys, custom field IDs rarely change

4. **Use JQL/CQL efficiently** — index-friendly queries (project, status, created) perform better than text searches

## API Version Notes

- **Jira Cloud REST API v3** (`/rest/api/3/`) — current version, use this
- **Jira Cloud REST API v2** (`/rest/api/2/`) — deprecated but still works
- **Confluence Cloud REST API** (`/wiki/rest/api/`) — unversioned, stable

Always use v3 for Jira unless a specific v2-only endpoint is needed.
