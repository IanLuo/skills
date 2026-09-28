---
name: figma
description: Access Figma via REST API using curl. Use when the user asks to "get figma data", "figma file", "download figma images", "download figma icons", "figma design", "export from figma", "figma node", mentions figma.com or figma.design URLs, mentions a node-id, or needs to interact with Figma files or assets. Do NOT use for Figma plugin development or FigJam-only operations.
metadata:
  audience: personal
  domain: integration
---


# figma

Access Figma file data and render/download images via the REST API using curl and bash. Replaces the figma-developer-mcp server — loads on-demand instead of always-on, saving tokens when Figma isn't needed.

## Setup

**One-time credential setup:**

```bash
bash skills/credentials/scripts/create.sh figma \
  FIGMA_API_KEY
```

**Token:** Figma → Settings → Security → Personal access tokens → Generate new token. Set scopes to include `file_content:read` / `files:read`.

**That's it!** Use the commands below — credentials load automatically.

## How to Use

**All commands use this pattern:**

```bash
bash skills/credentials/scripts/run.sh figma 'YOUR_COMMAND_HERE'
# Returns: exit_code=0  output=/tmp/cred-run-XXXXXX  bytes=...
# Then: read the output file to see results
```

`run.sh` writes command output to a temp file (with credentials scrubbed) and returns the file path. **Use the `read` tool on that path to see results.**

**Variables available in commands:**
- `$FIGMA_API_KEY` — Your personal access token

**Auth header:** Every curl call must include `-H "X-Figma-Token: $FIGMA_API_KEY"`.

## Parsing a Figma URL

Figma URLs follow these patterns:

```
https://www.figma.com/file/<fileKey>/<fileName>?node-id=<nodeId>
https://www.figma.com/design/<fileKey>/<fileName>?node-id=<nodeId>
```

- **fileKey** — the alphanumeric segment after `/file/` or `/design/`
- **nodeId** — the `node-id` query parameter (format `1234-5678` in the URL, which maps to `1234:5678` in the API — replace the hyphen with a colon)

Example URL:
```
https://www.figma.com/design/ABC123def/My-Design?node-id=42-1337
```
→ fileKey = `ABC123def`, nodeId = `42:1337`

## 1. Get File / Node Data

### Get entire file

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/files/ABC123def" \
   | jq "{name, lastModified, editorType, version}"'
```

**Tip:** Use `depth=1` to get just pages, `depth=2` for pages + top-level objects. Full files can be very large — always limit depth when exploring.

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/files/ABC123def?depth=2" \
   | jq ".document.children[] | {id, name, type}"'
```

### Get specific nodes

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/files/ABC123def/nodes?ids=42:1337" \
   | jq ".nodes"'
```

Multiple nodes — comma-separated:

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/files/ABC123def/nodes?ids=42:1337,10:99&depth=1" \
   | jq ".nodes"'
```

**Query parameters:**

| Param | Type | Description |
|---|---|---|
| `ids` | String (required) | Comma-separated node IDs (format `1234:5678`) |
| `depth` | Number | How deep into the node tree to traverse |
| `version` | String | Specific file version ID |
| `geometry` | String | Set to `paths` to include vector data |
| `plugin_data` | String | Comma-separated plugin IDs or `shared` |

**Response shape:**
```json
{
  "name": "File Name",
  "lastModified": "2025-01-01T00:00:00Z",
  "nodes": {
    "42:1337": {
      "document": { /* Node tree */ },
      "components": {},
      "styles": {}
    }
  }
}
```

Nodes in the map may be `null` if the ID doesn't exist in the file.

## 2. Render & Download Images

This is a **two-step** process:

1. **Request render URLs** from Figma (server-side render)
2. **Download** the rendered images from the returned URLs

### Step 1 — Get render URLs

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/images/ABC123def?ids=42:1337&format=svg&scale=1" \
   | jq .'
```

**Query parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `ids` | String (required) | — | Comma-separated node IDs to render |
| `format` | String | `png` | Output format: `png`, `jpg`, `svg`, or `pdf` |
| `scale` | Number | 1 | Image scale factor (0.01–4) |
| `svg_outline_text` | Boolean | `true` | Render text as vector paths in SVGs |
| `svg_include_id` | Boolean | `false` | Add layer name as `id` on SVG elements |
| `svg_include_node_id` | Boolean | `false` | Add `data-node-id` attribute on SVG elements |
| `svg_simplify_stroke` | Boolean | `true` | Simplify strokes in SVGs |
| `contents_only` | Boolean | `true` | Exclude overlapping content |
| `use_absolute_bounds` | Boolean | `false` | Full node dimensions (useful for text) |
| `version` | String | — | Specific file version ID |

**Response shape:**
```json
{
  "err": null,
  "images": {
    "42:1337": "https://figma-alpha-api.s3.us-west-2.amazonaws.com/images/..."
  }
}
```

A `null` value for a node ID means rendering failed for that node. Image URLs **expire after 30 days**.

### Step 2 — Download the rendered image

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/images/ABC123def?ids=42:1337&format=svg&scale=2" \
   | jq -r ".images[\"42:1337\"]" \
   | xargs -I{} curl -s -o icon.svg "{}"'
```

### Worked example — export an icon as SVG from a Figma URL

Given: `https://www.figma.com/design/ABC123def/Icons?node-id=42-1337`

```bash
# 1. Parse: fileKey=ABC123def, nodeId=42:1337

# 2. (Optional) Inspect the node first
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/files/ABC123def/nodes?ids=42:1337&depth=1" \
   | jq ".nodes[\"42:1337\"].document | {name, type}"'

# 3. Render + download as SVG
bash skills/credentials/scripts/run.sh figma \
  'RENDER_URL=$(curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/images/ABC123def?ids=42:1337&format=svg&scale=2" \
   | jq -r ".images[\"42:1337\"]") && \
   curl -s -o icon.svg "$RENDER_URL" && \
   echo "Downloaded: icon.svg ($(wc -c < icon.svg) bytes)"'
```

### Download multiple nodes at once

```bash
bash skills/credentials/scripts/run.sh figma \
  'curl -s -H "X-Figma-Token: $FIGMA_API_KEY" \
   "https://api.figma.com/v1/images/ABC123def?ids=42:1337,10:99,5:200&format=png&scale=2" \
   | jq -r ".images | to_entries[] | select(.value != null) | \"\(.key) \(.value)\"" \
   | while read id url; do
       name=$(echo "$id" | tr ":" "-")
       curl -s -o "${name}.png" "$url"
       echo "Downloaded: ${name}.png"
     done'
```

## Working Rules

1. **Start with depth limits** — Figma files can be enormous. Always use `depth=1` or `depth=2` when exploring. Fetch specific nodes by ID when you know the target.

2. **Filter with jq** — extract only needed fields. Common patterns:
   - `.document.children[] | {id, name, type}` — list pages
   - `.nodes["ID"].document | {name, type, children: [.children[]? | {id, name, type}]}` — node overview
   - `.images | to_entries[] | {node: .key, url: .value}` — image URLs

3. **Node ID format** — the API uses colon format (`42:1337`). Figma URLs use hyphen format (`42-1337`). Always convert hyphens to colons before calling the API.

4. **Two-step image download** — the images endpoint returns render URLs, not image data. You must curl the render URL in a second request. Render URLs expire after 30 days.

5. **Handle errors** — check for `"err"` in response:
   ```bash
   response=$(curl -s -w "\n%{http_code}" -H "X-Figma-Token: $FIGMA_API_KEY" ...)
   http_code=$(echo "$response" | tail -n1)
   if [ "$http_code" -ne 200 ]; then echo "Error $http_code"; fi
   ```

6. **Stop speculative searches** — after 2-3 failed attempts, stop and ask user for: the exact Figma URL, node IDs, or file key.

7. **Rate limits** — Figma API has rate limits. If you get 429, back off and retry.

## Interaction Style

- **Verify setup** — if credentials are missing, provide the exact setup command.
- **Parse URLs for the user** — when given a Figma URL, extract fileKey and nodeId automatically and confirm them before proceeding.
- **Show results** — display node names, types, and IDs in readable format. Don't dump raw JSON.
- **Confirm downloads** — before rendering, confirm the format, scale, and destination path.

## Error Handling

Common errors:

- **403 Forbidden** — invalid token or insufficient scopes
- **404 Not Found** — file key or node ID doesn't exist, or token lacks access
- **400 Bad Request** — malformed node IDs or missing required params
- **429 Too Many Requests** — rate limited, back off and retry

## References

**Read [figma-api-reference.md](references/figma-api-reference.md) when:**
- You need component, style, or comment endpoints
- You need version history or file metadata beyond the basics
- You hit node-ID encoding issues or need SVG rendering options explained

API Documentation: https://developers.figma.com/docs/rest-api/
