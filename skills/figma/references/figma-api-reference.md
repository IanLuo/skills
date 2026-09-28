# Figma REST API — Extended Reference

Read this when you need endpoints beyond the basics in SKILL.md, or when you hit formatting/encoding issues.

## Base URL

```
https://api.figma.com/v1
```

For Figma for Government: `https://api.figma-gov.com/v1`

## Authentication

Header: `X-Figma-Token: <personal_access_token>`

Generate at: Figma → Settings → Security → Personal access tokens.

Scopes: `file_content:read` / `files:read` for read operations.

## Node ID Encoding

- API format: `1234:5678` (colon separator)
- URL format: `1234-5678` (hyphen separator, in `?node-id=` param)
- When passing node IDs in query strings, the colon usually works as-is. If it doesn't, URL-encode as `1234%3A5678`.
- Multiple IDs: comma-separated `42:1337,10:99`

## File Endpoints

### GET /v1/files/:key

Returns entire file as JSON.

| Param | Type | Description |
|---|---|---|
| `version` | String | Specific version ID |
| `ids` | String | Comma-separated node IDs (returns subset) |
| `depth` | Number | Traversal depth (1=pages, 2=pages+top-level) |
| `geometry` | String | `paths` to include vector data |
| `plugin_data` | String | Plugin IDs or `shared` |
| `branch_data` | Boolean | Include branch metadata (default: false) |

### GET /v1/files/:key/nodes

Returns specific nodes by ID.

| Param | Type | Description |
|---|---|---|
| `ids` | String **(required)** | Comma-separated node IDs |
| `depth` | Number | Node tree traversal depth |
| `version` | String | Specific version ID |
| `geometry` | String | `paths` for vector data |
| `plugin_data` | String | Plugin IDs or `shared` |

Response: `{ name, lastModified, nodes: { "<id>": { document, components, styles } } }`

### GET /v1/images/:key

Renders nodes as images. Returns URLs, not image data.

| Param | Type | Default | Description |
|---|---|---|---|
| `ids` | String **(required)** | — | Node IDs to render |
| `format` | String | `png` | `png`, `jpg`, `svg`, `pdf` |
| `scale` | Number | 1 | 0.01–4 |
| `svg_outline_text` | Boolean | true | Text as vector paths |
| `svg_include_id` | Boolean | false | Layer name as SVG `id` |
| `svg_include_node_id` | Boolean | false | `data-node-id` attribute |
| `svg_simplify_stroke` | Boolean | true | Simplify strokes |
| `contents_only` | Boolean | true | Exclude overlaps |
| `use_absolute_bounds` | Boolean | false | Full dimensions |
| `version` | String | — | File version ID |

Response: `{ err, images: { "<nodeId>": "<url>" | null }, status }`

Image URLs expire after **30 days**. Max export: **32 megapixels** (larger scaled down).

## Additional Endpoints (not covered in SKILL.md)

### GET /v1/files/:key/comments

List comments on a file.

### POST /v1/files/:key/comments

Add a comment. Body: `{ message, client_meta: { node_id, node_offset: { x, y } } }`

### GET /v1/files/:key/versions

List version history. Useful for `version` param in other endpoints.

### GET /v1/files/:key/components

List components published from the file.

### GET /v1/files/:key/styles

List styles published from the file.

### GET /v1/teams/:team_id/components

List components across a team library.

### GET /v1/teams/:team_id/styles

List styles across a team library.

## Common Node Types

| Type | Description |
|---|---|
| `DOCUMENT` | Root of the file |
| `CANVAS` | A page |
| `FRAME` | Frame/group container |
| `GROUP` | Group container |
| `COMPONENT` | Reusable component definition |
| `INSTANCE` | Instance of a component |
| `VECTOR` | Vector shape |
| `TEXT` | Text layer |
| `RECTANGLE`, `ELLIPSE`, `LINE` | Shape primitives |
| `BOOLEAN_OPERATION` | Boolean operation result |

## Rate Limits

Figma enforces rate limits per-token. On 429 responses, respect `Retry-After` header. No published per-minute quota — use reasonable delays between batch requests.

## API Docs

- REST API overview: https://developers.figma.com/docs/rest-api/
- File endpoints: https://developers.figma.com/docs/rest-api/file-endpoints/
- Authentication: https://developers.figma.com/docs/rest-api/authentication/
- Node types: https://developers.figma.com/docs/rest-api/file-node-types/
- OpenAPI spec: https://github.com/figma/rest-api-spec
