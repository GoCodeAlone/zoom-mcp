# Zoom MCP — Design

**Date:** 2026-04-15
**Status:** Approved, ready for implementation planning
**Repo:** `github.com/GoCodeAlone/zoom-mcp`

## Goal

A local, single-user MCP server that exposes Zoom meetings, recordings, transcripts, AI Companion summaries, and Team Chat messages to MCP clients (Claude Desktop and similar). Built on `github.com/GoCodeAlone/workflow` (a YAML-driven workflow engine) and the official `github.com/modelcontextprotocol/go-sdk`.

The explicit aim is to keep the zoom-mcp repo minimal: one `main.go` plus YAML and JSON Schema files. All reusable machinery lives either in Workflow upstream or in a sibling `workflow-plugin-mcp` plugin.

## Architecture

```
┌──────────────┐  stdio or streamable HTTP   ┌──────────────────────────┐
│  MCP client  │ ───────────────────────────▶│  workflow-plugin-mcp     │
│ (Claude etc) │                             │  mcp.server module       │
└──────────────┘                             │  mcp.{stdio,http}_       │
                                             │    transport modules     │
                                             │  mcp.tool trigger        │
                                             └────────────┬─────────────┘
                                                          │ pipeline dispatch
                                                          ▼
┌─────────────────────────┐   authenticated    ┌──────────────────────┐
│  secrets.provider       │◀──────creds───────▶│  http.client module  │
│    (keychain-backed)    │   token storage    │  (oauth2_refresh_    │
│                         │                    │   token flow)        │
└─────────────────────────┘                    └──────────────────────┘
       ▲                                                  │
       │ Set from setup form                              │
       │                                                  │
┌──────┴──────────────────┐                               │
│  http.server + pipelines│                               │
│  /setup, /setup (POST), │                               │
│  /oauth/callback        │                               │
└─────────────────────────┘                               ▼
                                                 ┌────────────────┐
                                                 │  Zoom REST API │
                                                 └────────────────┘
```

Three artifacts, each in its own repo:

1. **`github.com/GoCodeAlone/workflow`** — gains five upstream contributions (see below).
2. **`github.com/GoCodeAlone/workflow-plugin-mcp`** — new plugin, created from `workflow-plugin-template`. Wraps the mcp go-sdk as Workflow modules and a trigger.
3. **`github.com/GoCodeAlone/zoom-mcp`** — this repo. Almost entirely YAML + JSON Schema.

## Upstream contributions to `github.com/GoCodeAlone/workflow`

Five PRs, landable incrementally. zoom-mcp can start consuming PR 1 while 2–5 land in parallel.

### PR 1 — `secrets.KeychainProvider`

New implementation of the existing `secrets.Provider` interface (`secrets/secrets.go:24-35`) backed by the OS credential store via `github.com/zalando/go-keyring`. Implements `Get`, `Set`, `Delete`, `List`. ~100 lines including tests.

YAML usage:
```yaml
modules:
  - name: zoom-secrets
    type: secrets.provider
    config:
      provider: keychain
      service: zoom-mcp
```

Requires a working Secret Service on Linux (libsecret/gnome-keyring/KWallet); works out of the box on macOS. Documented in the PR.

### PR 2 — `step.secret_set`

Copy `pipeline_step_secret_fetch.go` → `pipeline_step_secret_set.go`, swap the `secrets.Provider.Get` call for `Set`. Takes a `module` reference (secrets provider name) and a `secrets:` map of key → template-resolved value. ~50 lines.

YAML usage:
```yaml
- type: step.secret_set
  config:
    module: zoom-secrets
    secrets:
      client_id:     '{{ .request.form.client_id }}'
      client_secret: '{{ .request.form.client_secret }}'
```

### PR 3 — `http.client` module

A reusable module that exposes an `*http.Client` via modular's service registry. Other modules and steps reference it by name. Owns the auth transport lifecycle.

Config:
```yaml
modules:
  - name: zoom-client
    type: http.client
    config:
      base_url: "https://api.zoom.us/v2"
      timeout: 30s
      auth:
        type: oauth2_refresh_token
        token_url: "https://zoom.us/oauth/token"
        client_id_from_secret:     { provider: zoom-secrets, key: client_id }
        client_secret_from_secret: { provider: zoom-secrets, key: client_secret }
        token_secrets:     zoom-secrets
        token_secrets_key: oauth_token
```

Auth type variants:
- `none` — no wrapping.
- `static_bearer` — static token (inline or env-var).
- `oauth2_client_credentials` — factored out of the existing `step.http_call` inline code for reuse; existing inline support stays in `step.http_call` for back-compat.
- `oauth2_refresh_token` — loads a persisted token from the referenced secrets.Provider, refreshes when expired or on 401, persists the rotated token back.

**Key requirement for Zoom compatibility:** the `oauth2_refresh_token` path must tolerate `ErrNoToken` at any time, including at boot. If no token is yet stored, the client doesn't panic — it surfaces an auth error on requests. Once a token is written (by the setup flow), the very next request picks it up. No process restart needed.

### PR 4 — `client:` config on `step.http_call`

New `client:` field referencing an `http.client` module:

```yaml
- type: step.http_call
  config:
    client: zoom-client                 # NEW
    url: /users/me/meetings              # relative; resolved against client.base_url
    method: GET
    query: { page_size: "300" }
```

When `client:` is present: request routes through that module's `*http.Client`; relative URLs resolve against its `base_url`; inline `oauth2:` config is rejected as conflicting.

When `client:` is absent: unchanged behavior. ~100 lines.

### PR 5 — `step.while`

Iterative step for cursor-based pagination:

```yaml
- name: fetch-all-pages
  type: step.while
  config:
    condition: '{{ ne (default "" (last_result "fetch-page").body.next_page_token) "" }}'
    max_iterations: 100
    iteration_var: iter
    accumulate:
      key: meetings
      from: '{{ (last_result "fetch-page").body.meetings }}'
    steps:
      - name: fetch-page
        type: step.http_call
        config: { client: zoom-client, url: /users/me/meetings, query: {...} }
```

Semantics:
- Condition is a Go template evaluated after each body run. Non-empty string / non-zero int / `true` bool → continue.
- `max_iterations` defaults to 1000; errors on exceed.
- `accumulate` is optional; with it, the step's output contains an array under the configured key containing the concatenated `from:` values from every iteration.

~300 lines.

## `workflow-plugin-mcp`

New repo: `github.com/GoCodeAlone/workflow-plugin-mcp`, created from `workflow-plugin-template`. Pure MCP-sdk bridge; knows nothing about Zoom.

### Modules

- **`mcp.server`** — owns the mcp go-sdk `*Server` instance. Config: `implementation: { name, version }`, plus optional server-level metadata.
- **`mcp.stdio_transport`** — attaches a stdio transport to a named `mcp.server`. Config: `server: <module-name>`.
- **`mcp.http_transport`** — attaches a streamable HTTP transport (via `mcp.NewStreamableHTTPHandler`) to a named server, bound to a local address. Config: `server`, `address`.

Splitting server from transports lets a deployment run stdio-only (Claude Desktop use case), HTTP-only, or both. Tool definitions are transport-agnostic.

### Trigger

- **`mcp.tool`** — a pipeline trigger that registers a tool with a named server and dispatches each tool call into the enclosing pipeline.

Pipeline wiring follows Workflow's existing trigger pattern (same as `http`):

```yaml
pipelines:
  list-meetings:
    trigger:
      type: mcp.tool
      config:
        server: mcp-server
        name: list_meetings
        description: "List the authenticated user's Zoom meetings."
        input_schema:  { $ref: ./schemas/list_meetings.input.json }
        output_schema: { $ref: ./schemas/list_meetings.output.json }
    steps: [...]
```

### Schema handling

Plugin supports both inline objects (parsed to `map[string]any`) and `{$ref: ./path.json}` file references resolved relative to the workflow config's directory. SDK constraint: top-level schema must be `"type": "object"`.

zoom-mcp uses the `$ref` form throughout.

### Input validation

Plugin runs inbound `Arguments` (as `json.RawMessage`) through a JSON Schema validator (`github.com/santhosh-tekuri/jsonschema/v5`) before dispatching to the pipeline. The mcp go-sdk does not auto-validate when schemas are passed as raw values.

### Tool call flow

1. MCP client calls `tools/call` over chosen transport.
2. `mcp.server`'s registered `ToolHandler` is invoked with `*CallToolRequest` (Arguments: `json.RawMessage`).
3. `mcp.tool` trigger unmarshals + validates against `input_schema`, constructs a pipeline event with the payload under `.input`, dispatches to the trigger's owning pipeline.
4. Pipeline runs (typically `step.while` + `step.http_call` + `step.transform`).
5. Trigger converts the pipeline's final output into `*CallToolResult` — JSON-marshaled into a `TextContent` block (with `StructuredContent` when the spec supports it). Errors with `IsError: true`.

### Lifecycle

`mcp.server` starts after all `mcp.tool` triggers have registered, leveraging modular's dependency graph. Graceful shutdown: transports stop accepting first, server drains in-flight calls, then modular tears down the rest.

## `zoom-mcp`

### Repo layout

```
zoom-mcp/
├── cmd/zoom-mcp/main.go              # bootstrap + CLI dispatch + browser open
├── config/
│   ├── zoom-mcp.yaml                 # full Workflow config
│   └── schemas/
│       ├── get_me.input.json            / .output.json
│       ├── list_meetings.input.json     / .output.json
│       ├── get_meeting.input.json       / .output.json
│       ├── ... (one pair per tool)
├── docs/plans/2026-04-15-zoom-mcp-design.md
├── go.mod / go.sum
├── LICENSE
└── README.md
```

All custom Go totals ~150 lines in `cmd/zoom-mcp/main.go`:
- Register plugin imports (workflow core, workflow-plugin-mcp, keyring provider).
- Load YAML.
- Minimal CLI: `zoom-mcp` (default: run engine), `zoom-mcp --no-browser`, `zoom-mcp config show`, `zoom-mcp config reset`.
- Detect "no creds in keychain" state at boot → open browser to `http://127.0.0.1:8765/setup` via `github.com/pkg/browser` (best-effort; fall back to printing the URL on stderr).
- Send an MCP `notifications/message` surfacing the setup URL (belt-and-suspenders so Claude Desktop can show it in its UI).

### Unified Workflow YAML

Both setup and service endpoints live in one YAML. Single `http.server` module on `127.0.0.1:8765` handles the setup flow; same server optionally handles the MCP streamable HTTP transport on the same or a different port.

```yaml
modules:
  - { name: zoom-secrets, type: secrets.provider,
      config: { provider: keychain, service: zoom-mcp } }

  - name: zoom-client
    type: http.client
    config:
      base_url: "https://api.zoom.us/v2"
      timeout: 30s
      auth:
        type: oauth2_refresh_token
        token_url: "https://zoom.us/oauth/token"
        client_id_from_secret:     { provider: zoom-secrets, key: client_id }
        client_secret_from_secret: { provider: zoom-secrets, key: client_secret }
        token_secrets:     zoom-secrets
        token_secrets_key: oauth_token

  - name: zoom-download-client
    type: http.client
    config:
      timeout: 60s
      auth: { type: none }   # Zoom recording downloads use ?access_token= in URL, not a bearer header

  - { name: mcp-server, type: mcp.server,
      config: { implementation: { name: zoom-mcp, version: "0.1.0" } } }
  - { name: mcp-stdio,  type: mcp.stdio_transport, config: { server: mcp-server } }
  - { name: mcp-http,   type: mcp.http_transport,
      config: { server: mcp-server, address: "127.0.0.1:7823" } }

  - { name: http-server, type: http.server,
      config: { address: "127.0.0.1:8765" } }
  - { name: http-router, type: http.router, dependsOn: [http-server] }

workflows:
  http: { router: http-router, server: http-server, routes: [] }

pipelines:
  # --- Setup flow ---

  setup-form:
    trigger: { type: http, config: { method: GET, path: /setup } }
    steps:
      - type: step.raw_response
        config:
          content_type: "text/html; charset=utf-8"
          body: |
            <!doctype html><html><body style="font-family:system-ui; max-width:480px; margin:2em auto;">
              <h1>Zoom MCP — Setup</h1>
              <p>Register <code>http://127.0.0.1:8765/oauth/callback</code> as a redirect URI
                 in your <a href="https://marketplace.zoom.us/user/build" target="_blank">Zoom OAuth app</a>.</p>
              <form method="POST" action="/setup">
                <label>Client ID<br><input name="client_id" required style="width:100%"/></label><br><br>
                <label>Client Secret<br><input name="client_secret" type="password" required style="width:100%"/></label><br><br>
                <button type="submit">Authorize with Zoom →</button>
              </form>
            </body></html>

  setup-save:
    trigger: { type: http, config: { method: POST, path: /setup } }
    steps:
      - type: step.secret_set
        config:
          module: zoom-secrets
          secrets:
            client_id:     '{{ .request.form.client_id }}'
            client_secret: '{{ .request.form.client_secret }}'
      - type: step.redirect
        config:
          url: >
            https://zoom.us/oauth/authorize?response_type=code
            &client_id={{ .request.form.client_id }}
            &redirect_uri=http://127.0.0.1:8765/oauth/callback
            &state={{ uuid }}
            &scope=user:read:user+meeting:read:list_meetings+meeting:read:meeting+meeting:read:past_meeting+meeting:read:list_past_instances+meeting:read:list_past_participants+meeting:read:summary+meeting:read:list_summaries+cloud_recording:read:list_user_recordings+cloud_recording:read:list_recording_files+cloud_recording:read:meeting_transcript+chat:read:list_user_channels+chat:read:user_channel+chat:read:list_user_messages+chat:read:user_message

  oauth-callback:
    trigger: { type: http, config: { method: GET, path: /oauth/callback } }
    steps:
      - name: exchange-code
        type: step.secret_fetch
        config:
          module: zoom-secrets
          keys: [client_id, client_secret]
      - name: token-exchange
        type: step.http_call
        config:
          url: "https://zoom.us/oauth/token"
          method: POST
          headers:
            Content-Type: "application/x-www-form-urlencoded"
            Authorization: 'Basic {{ b64 (printf "%s:%s" (secret "client_id") (secret "client_secret")) }}'
          body_form:
            grant_type: authorization_code
            code: '{{ .request.query.code }}'
            redirect_uri: "http://127.0.0.1:8765/oauth/callback"
      - type: step.secret_set
        config:
          module: zoom-secrets
          secrets:
            oauth_token: '{{ (last_result "token-exchange").body | toJson }}'
      - type: step.raw_response
        config:
          content_type: "text/html; charset=utf-8"
          body: |
            <!doctype html><html><body style="font-family:system-ui; text-align:center; margin-top:4em;">
              <h1>✓ Authorized</h1><p>You can close this tab.</p>
            </body></html>

  # --- MCP tools ---

  list-meetings:
    trigger:
      type: mcp.tool
      config:
        server: mcp-server
        name: list_meetings
        description: "List the authenticated user's Zoom meetings."
        input_schema:  { $ref: ./schemas/list_meetings.input.json }
        output_schema: { $ref: ./schemas/list_meetings.output.json }
    steps:
      - type: step.while
        config:
          condition: '{{ ne (default "" (last_result "fetch-page").body.next_page_token) "" }}'
          max_iterations: 50
          accumulate: { key: meetings, from: '{{ (last_result "fetch-page").body.meetings }}' }
          steps:
            - name: fetch-page
              type: step.http_call
              config:
                client: zoom-client
                url: /users/me/meetings
                query:
                  type: '{{ default "upcoming" .input.type }}'
                  page_size: "300"
                  next_page_token: '{{ default "" (last_result "fetch-page").body.next_page_token }}'
      - type: step.transform
        config:
          template: '{ "ok": true, "data": { "meetings": {{ .accumulated.meetings | toJson }}, "count": {{ len .accumulated.meetings }} } }'

  # ... plus 12 more tool pipelines following the same shape ...
```

### Tool catalog (v1)

All user-level scopes; granular scope names verified against Zoom docs.

| Tool | Method & path | Scope(s) |
|---|---|---|
| `get_me` | `GET /users/me` | `user:read:user` |
| `list_meetings` | `GET /users/me/meetings?type=…` | `meeting:read:list_meetings` |
| `get_meeting` | `GET /meetings/{meetingId}` | `meeting:read:meeting` |
| `get_past_meeting` | `GET /past_meetings/{meetingId}` | `meeting:read:past_meeting` |
| `list_past_meeting_instances` | `GET /past_meetings/{meetingId}/instances` | `meeting:read:list_past_instances` |
| `list_past_meeting_participants` | `GET /past_meetings/{meetingId}/participants` | `meeting:read:list_past_participants` |
| `list_recordings` | `GET /users/me/recordings?from=&to=` | `cloud_recording:read:list_user_recordings` |
| `get_meeting_recordings` | `GET /meetings/{meetingId}/recordings` | `cloud_recording:read:list_recording_files` |
| `get_transcript` | `GET /meetings/{meetingId}/transcript` (fallback: recordings-files) | `cloud_recording:read:meeting_transcript` + `cloud_recording:read:list_recording_files` |
| `get_meeting_summary` | `GET /meetings/{meetingId}/meeting_summary` | `meeting:read:summary` |
| `list_meeting_summaries` | `GET /meetings/meeting_summaries` | `meeting:read:list_summaries` |
| `list_chat_channels` | `GET /chat/users/me/channels` | `chat:read:list_user_channels` |
| `get_chat_channel` | `GET /chat/users/me/channels/{channelId}` | `chat:read:user_channel` |
| `list_chat_messages` | `GET /chat/users/me/messages?to_channel=X` or `?to_contact=X` | `chat:read:list_user_messages` |
| `get_chat_message` | `GET /chat/users/me/messages/{messageId}` | `chat:read:user_message` |

#### `get_transcript` specifics

Zoom exposes two access patterns:
1. `GET /meetings/{meetingId}/transcript` — direct transcript endpoint.
2. `GET /meetings/{meetingId}/recordings` → filter `recording_files[].file_type == "TRANSCRIPT"` → download from `download_url?access_token=<download_token>` (bearer-in-URL, not header).

Handler tries (1) first; on 404 falls back to (2). The second path uses a second `http.client` module (`zoom-download-client`) configured with `auth: { type: none }` because Zoom's download URL signing is positional (query param), not header-based.

Returns raw VTT as `TextContent`. LLMs consume VTT fine and timestamps are preserved. Future enhancement: optional `as: "turns"` input to parse into speaker blocks.

### CLI surface

```
zoom-mcp                       # default: load YAML, start engine
zoom-mcp --no-browser          # don't auto-open setup URL; print it to stderr instead
zoom-mcp config show           # redacted dump of keychain state (scopes, token expiry)
zoom-mcp config reset          # wipe all zoom-mcp keychain entries
```

`setup` and `auth` are not separate commands. The default launch handles both first-run and steady state:
- Creds + token present → MCP server is fully operational; setup endpoints stay mounted but won't be visited.
- Creds or token missing → open browser to `http://127.0.0.1:8765/setup`; tool calls return a structured `"not_authenticated"` error until setup completes; immediately after setup completes, tool calls work (no restart, no reconnect).

### Launch decision tree

```
zoom-mcp starts
  │
  ├─ load Workflow YAML (always succeeds)
  ├─ start mcp.server + transports + http.server + all pipelines
  │
  ├─ check keychain via secrets.provider
  │    ├─ creds + token present → steady state
  │    └─ missing → notify (stderr + MCP notifications/message),
  │                open browser to http://127.0.0.1:8765/setup;
  │                tool calls return "not_authenticated" until setup completes
  │
  └─ serve indefinitely
```

## Testing

- **Upstream PRs** ship with Workflow-style table-driven unit tests in `testdata/` fixtures. PR 3 (`http.client`) uses `httptest.Server` for Zoom OAuth + API stubs; PR 5 (`step.while`) uses a fake counter step that toggles the condition.
- **`workflow-plugin-mcp`** tests use the mcp go-sdk's `InMemoryTransport` to exercise tool registration + call dispatch end-to-end without stdio or HTTP.
- **`zoom-mcp`** has a thin test suite that boots the engine against a `httptest.Server` mocking Zoom, running a few tool calls end-to-end: happy path, 401 refresh, pagination across 3 pages, missing-transcript 404, missing-AI-Companion 404.
- **Manual acceptance** after each PR lands, against a real Zoom sandbox: `zoom-mcp` (first-run setup flow), `list_meetings`, `get_transcript`, `get_meeting_summary`.

## Observability

- Workflow's built-in OpenTelemetry tracing emits spans per step. Active only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
- Structured logs via Workflow's stdlib logger. Tool calls at INFO (name + duration); OAuth refreshes at DEBUG; errors at ERROR with Zoom response body truncated to 2KB.
- `zoom-mcp config show` prints token expiry, last-refresh time, stored scopes.

## Error shape

Every tool pipeline ends by shaping output into one of:

```json
{ "ok": true,  "data": {...} }
{ "ok": false, "error": { "code": "...", "message": "...", "retry_after": 60 } }
```

Error codes: `not_authenticated`, `rate_limited`, `ai_companion_unavailable`, `recording_not_found`, `transcript_not_available`, `zoom_api_error`. Unexpected errors fall through with `IsError: true` and the raw Zoom body for debugging.

On Zoom 429: handler returns `ok: false, error.code = "rate_limited", retry_after = <header>`. No automatic retry in v1; caller decides. When `http.client` gains a generic 429-aware retry transport upstream later, the error path stops seeing 429s.

## Explicitly out of scope for v1

- Meeting/recording/summary create+update+delete (read-only for v1).
- Webinars, phone, rooms.
- Chat sending, reactions, files, threads, scheduled messages.
- Anything beyond `me` (single-user tool — no account admin scopes).
- Account-level analytics or dashboards.

Adding any of these post-v1 is a YAML-only change: new pipeline + JSON Schemas + additional scope in the `/oauth/authorize` URL.

## Risks and follow-ups

- **`step.while` template expressivity.** Go text/template is limited for complex conditions. If we hit a case that needs comparison operators or deeper expressions, a follow-up PR can add an `expr-lang/expr` alternative. Not blocking v1.
- **Linux headless keychain availability.** `go-keyring` requires a Secret Service implementation. Documented in README. If headless support becomes a requirement, `secrets.Provider` has a `FileProvider` option that could be wrapped with at-rest encryption.
- **Zoom rate-limit retry.** No generic 429 retry in v1. Follow-up upstream PR can add a retry transport to `http.client` that other services benefit from too.
- **MCP spec evolution.** The `StructuredContent` field and `notifications/message` behavior in Claude Desktop may change. Plugin is behind a stable SDK surface, so spec churn doesn't reach pipeline YAML.

## Summary of artifacts

| Artifact | Repo | Scope |
|---|---|---|
| `secrets.KeychainProvider` | `GoCodeAlone/workflow` | ~100 LOC |
| `step.secret_set` | `GoCodeAlone/workflow` | ~50 LOC |
| `http.client` module | `GoCodeAlone/workflow` | ~600–800 LOC |
| `client:` ref on `step.http_call` | `GoCodeAlone/workflow` | ~100 LOC |
| `step.while` step | `GoCodeAlone/workflow` | ~300 LOC |
| `workflow-plugin-mcp` | `GoCodeAlone/workflow-plugin-mcp` (new) | ~800–1200 LOC |
| `zoom-mcp` | `GoCodeAlone/zoom-mcp` (this repo) | ~150 LOC Go + YAML + schemas |
