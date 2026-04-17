# zoom-mcp

A Zoom [Model Context Protocol](https://modelcontextprotocol.io/) server. Connects Claude Desktop (or any MCP client) to your Zoom account so you can ask for meetings, participants, recordings, transcripts, AI-Companion summaries, and chat history in natural language.

Built on the [Workflow](https://github.com/GoCodeAlone/workflow) engine: the entire service — setup UI, OAuth flow, 15 Zoom tools — lives in a single YAML (`cmd/zoom-mcp/zoom-mcp.yaml`) that is embedded into the binary at build time. The Go binary is thin: it loads the embedded config, wires the MCP stdio transport, and handles first-run bootstrap.

## Install

```
go install github.com/GoCodeAlone/zoom-mcp/cmd/zoom-mcp@latest
```

The binary is self-contained. Credentials live in your OS keychain under the service name `zoom-mcp` — macOS Keychain, GNOME Keyring (Linux), or Windows Credential Manager.

## First-run setup

1. **Create a Zoom OAuth app.** Open [https://marketplace.zoom.us/user/build](https://marketplace.zoom.us/user/build) → **Build App** → **General app** → **User-managed app**.
2. **Register the redirect URI:** `http://127.0.0.1:8765/oauth/callback`.
3. **Add scopes** (in the *Scopes* tab, the app needs all of these for the 15 tools to work):
   - `user:read:user`
   - `meeting:read:list_meetings`, `meeting:read:meeting`, `meeting:read:past_meeting`, `meeting:read:list_past_instances`, `meeting:read:list_past_participants`
   - `meeting:read:summary`, `meeting:read:list_summaries`
   - `cloud_recording:read:list_user_recordings`, `cloud_recording:read:list_recording_files`, `cloud_recording:read:meeting_transcript`
   - `chat:read:list_user_channels`, `chat:read:user_channel`, `chat:read:list_user_messages`, `chat:read:user_message`
4. **Copy the client ID and client secret** from the *Basic Information* tab.
5. **Run zoom-mcp.** On first launch it detects no credentials in the keychain, opens [http://127.0.0.1:8765/setup](http://127.0.0.1:8765/setup) in your browser, and walks you through pasting the client ID / secret and authorizing the OAuth app. When the callback completes the access token + refresh token are written to the keychain and you can close the tab.

```
zoom-mcp
```

## Wire it into Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or the equivalent on your platform:

```json
{
  "mcpServers": {
    "zoom": {
      "command": "zoom-mcp",
      "args": []
    }
  }
}
```

Restart Claude Desktop. The 15 Zoom tools will appear under the MCP icon.

## Tools

| Tool | What it does |
|---|---|
| `get_me` | Current user profile |
| `list_meetings` | Upcoming / scheduled meetings (paginated) |
| `get_meeting` | Single meeting details |
| `get_past_meeting` | Past meeting details |
| `list_past_meeting_instances` | Individual instances of a recurring meeting |
| `list_past_meeting_participants` | Participants who joined a past meeting |
| `list_recordings` | Cloud recordings for the user (paginated) |
| `get_meeting_recordings` | Recording files for a single meeting |
| `get_transcript` | VTT transcript for a meeting |
| `get_meeting_summary` | AI Companion meeting summary |
| `list_meeting_summaries` | All AI Companion summaries for the user (paginated) |
| `list_chat_channels` | User's Zoom chat channels (paginated) |
| `get_chat_channel` | Single channel details |
| `list_chat_messages` | Messages in a channel or DM (paginated) |
| `get_chat_message` | Single message |

Every tool returns a discriminated union:

```json
{ "ok": true,  "data":  { ... } }
{ "ok": false, "error": { "code": "rate_limited", "message": "...", "retry_after": 30 } }
```

Error codes: `not_authenticated` (401), `scope_missing` (403), `not_found` (404), `rate_limited` (429, includes `retry_after`), `transcript_unavailable` (from `get_transcript` when no VTT exists), `ai_companion_unavailable` (from `get_meeting_summary` when AI Companion wasn't enabled), `zoom_error` (everything else).

## Other commands

```
zoom-mcp config show     # list what's in the keychain (values redacted)
zoom-mcp config reset    # delete client_id, client_secret, oauth_token from the keychain
zoom-mcp --no-browser    # don't auto-open the setup URL; print it instead
```

## Troubleshooting

- **"credentials not configured" on every run.** The keychain entry is missing or your user doesn't have access. Run `zoom-mcp config show` to confirm; re-run `zoom-mcp` to redo setup.
- **Linux: setup flow saves but next run asks again.** You need a running Secret Service implementation (`libsecret` + GNOME Keyring or KWallet). Headless servers aren't supported by this build.
- **A tool returns `scope_missing`.** Your OAuth app is missing one of the scopes listed above. Add it in the Zoom Marketplace UI, then run `zoom-mcp config reset` and redo setup so the new token picks up the scope.
- **`get_transcript` returns `transcript_unavailable` for a meeting you know has a transcript.** Zoom only exposes VTT transcripts for cloud-recorded meetings; meetings recorded locally don't have one.
- **`get_meeting_summary` returns `ai_companion_unavailable`.** AI Companion was either off for the meeting or the feature isn't on your Zoom plan.

## Architecture

- `cmd/zoom-mcp/zoom-mcp.yaml` — the service (embedded into the binary via `//go:embed`). Modules (secrets, HTTP client with oauth2-refresh auth, MCP server, stdio + HTTP transports, tiny web server for the setup flow) + pipelines (setup form / OAuth callback + 15 tool pipelines).
- `cmd/zoom-mcp/main.go` — thin bootstrap. Loads the embedded YAML, loads Workflow's default plugins plus [workflow-plugin-mcp](https://github.com/GoCodeAlone/workflow-plugin-mcp), starts the engine, detects first-run and opens the browser.
- Tool pipelines use a uniform `step.http_call (error_on_status: false) → step.jq → step.pipeline_output` pattern; paginated tools wrap the call in `step.while`. The jq stage maps Zoom's HTTP status codes onto the discriminated `{ok, data/error}` shape.

## License

MIT. See [LICENSE](./LICENSE).
