# Extension protocol v3

Protocol v3 is a bounded JSON-over-HTTP contract between Lisan and an
out-of-process extension. Extensions send semantic content only: no ANSI
escapes, terminal coordinates, styles, executable UI code, or host commands.

The client accepts only `http` and `https` endpoints without URL credentials,
caps JSON responses at 1 MiB, strips terminal controls, validates identifiers
and limits, and rejects unknown fields or protocol versions.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v3/health` | readiness |
| `GET` | `/v3/manifest` | identity and advertised surfaces |
| `GET` | `/v3/views/{view}` | structured view data |
| `POST` | `/v3/jobs` | start an advertised action |
| `GET` | `/v3/jobs/{job}` | poll progress and results |
| `DELETE` | `/v3/jobs/{job}` | request cancellation |
| `GET` | `/v3/jobs/{job}/artifacts/{artifact}` | download verified artifact |
| `POST` | `/v3/sessions` | open an advertised session |
| `POST` | `/v3/sessions/{session}/input` | send bounded session input |
| `POST` | `/v3/sessions/{session}/resize` | report viewport dimensions |
| `DELETE` | `/v3/sessions/{session}` | close the session |

## Manifest and views

```json
{
  "protocol_version": 3,
  "id": "example",
  "name": "Example",
  "version": "3.0.0",
  "description": "One precise capability",
  "views": [
    {"id": "overview", "title": "Overview", "default": true}
  ],
  "actions": [
    {
      "id": "survey",
      "name": "Run survey",
      "inputs": [
        {"id": "subject", "label": "Subject", "kind": "text", "required": true},
        {"id": "samples", "label": "Samples", "kind": "number", "default": "5", "min": 1, "max": 20},
        {"id": "deep", "label": "Deep scan", "kind": "boolean", "default": "false"},
        {"id": "format", "label": "Format", "kind": "select", "options": [{"value": "md", "label": "Markdown"}]}
      ]
    }
  ],
  "sessions": [
    {"id": "console", "name": "Restricted Console"}
  ]
}
```

`GET /v3/views/overview` returns blocks in display order:

```json
{
  "id": "overview",
  "title": "Overview",
  "updated": "2026-08-08T12:00:00Z",
  "blocks": [
    {"id": "health", "kind": "status", "title": "Health", "tone": "success", "text": "Ready"},
    {"id": "runtime", "kind": "key-value", "title": "Runtime", "fields": [{"label": "Region", "value": "Arrakeen"}]},
    {"id": "items", "kind": "list", "title": "Findings", "items": [{"label": "Trace", "detail": "stable"}]},
    {"id": "samples", "kind": "table", "title": "Samples", "columns": [{"id": "id", "title": "ID"}], "rows": [["01"]]},
    {"id": "scan", "kind": "progress", "title": "Scan", "progress": 40, "detail": "in progress"},
    {"id": "notes", "kind": "text", "title": "Notes", "text": "Bounded plain text"}
  ]
}
```

Supported tones are `neutral`, `info`, `success`, `warning`, and `danger`.
Tone is a rendering hint, never an instruction or permission.

## Jobs and artifacts

Start a job with values keyed by the advertised input IDs:

```json
{"action_id":"survey","inputs":{"subject":"maker traces","samples":"5","deep":"false","format":"md"}}
```

The service returns `202 Accepted` with a job:

```json
{
  "id": "job-000001",
  "action_id": "survey",
  "status": "running",
  "progress": 40,
  "status_text": "sample 2 of 5",
  "logs": ["sample 01 stable"]
}
```

Statuses are `queued`, `running`, `succeeded`, `failed`, and `cancelled`.
Terminal jobs may include `result`, `error`, `exit_code`, and artifacts:

```json
{
  "id": "survey-report",
  "name": "survey.md",
  "media_type": "text/markdown",
  "size": 1204,
  "sha256": "64-lowercase-hex-characters"
}
```

Lisan downloads at most 32 MiB and requires the exact declared size and SHA-256
before exporting an artifact. Artifact names are reduced to a base filename.

## Sessions

Open:

```json
{"session_id":"console","rows":24,"columns":100}
```

Response:

```json
{
  "id": "session-000001",
  "session_id": "console",
  "status": "open",
  "output": "Ready. Type help.\n",
  "prompt": "example> "
}
```

Input uses `{"input":"help\n"}` and resize uses
`{"rows":30,"columns":120}`. Each response returns the latest bounded session
state. Status is `open`, `closed`, or `failed`. This transport does not grant a
shell: the extension decides which input is valid and where it executes.

## Limits

- 32 views and 64 blocks per view
- 100 actions and 32 inputs per action
- 16 session types
- 24 table columns and 1,000 rows
- 4,000 job log lines and 32 artifacts
- progress from 0 through 100
- 1 MiB JSON and 32 MiB artifact bodies

Identifiers use letters, numbers, `.`, `_`, and `-`, beginning with an
alphanumeric character. IDs are unique within their surface. A manifest must
contain at least one view and no more than one default view.

Protocol v3 is the only supported extension protocol. There is no v2 adapter,
declarative host, or fallback endpoint.
