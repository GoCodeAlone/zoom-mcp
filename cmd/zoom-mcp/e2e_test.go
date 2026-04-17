package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/modular"
	"github.com/GoCodeAlone/workflow"
	"github.com/GoCodeAlone/workflow/config"
	allplugins "github.com/GoCodeAlone/workflow/plugins/all"
	_ "github.com/GoCodeAlone/workflow/setup"
	mcpplugin "github.com/GoCodeAlone/workflow-plugin-mcp/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

// testZoomYAML is a minimal YAML mirroring zoom-mcp.yaml's pattern (secrets →
// http.client with oauth2_refresh_token → tool pipelines) but stripped of
// stdio/http transports, the setup flow, and router modules so it can run
// inside a Go test without binding os.Stdin or TCP ports. The {{ZoomURL}} and
// {{TokenURL}} placeholders are substituted at test time.
//
// It carries two tool pipelines whose shape exactly matches the production
// YAML, exercising the step.http_call (error_on_status: false) → step.jq →
// step.pipeline_output pattern for both the happy and the 429 branches.
const testZoomYAML = `
modules:
  - name: zoom-secrets
    type: secrets.keychain
    config:
      service: zoom-mcp-test

  - name: zoom-client
    type: http.client
    config:
      base_url: "{{ZoomURL}}"
      timeout: 5s
      auth:
        type: oauth2_refresh_token
        token_url: "{{TokenURL}}"
        client_id_from_secret: { provider: zoom-secrets, key: client_id }
        client_secret_from_secret: { provider: zoom-secrets, key: client_secret }
        token_secrets: zoom-secrets
        token_secrets_key: oauth_token

  - name: mcp-tool-registry
    type: mcp.tool_registry

  - name: mcp-server
    type: mcp.server
    config:
      implementation:
        name: zoom-mcp-test
        version: "0.0.0"
      registry: mcp-tool-registry

pipelines:
  get_me:
    trigger:
      type: mcp.tool
      config:
        server: mcp-server
        registry: mcp-tool-registry
        name: get_me
        description: "Get the authenticated user's Zoom profile."
        input_schema:
          type: object
          properties: {}
    steps:
      - name: call
        type: step.http_call
        config:
          client: zoom-client
          url: "/users/me"
          method: GET
          error_on_status: false
      - name: shape
        type: step.jq
        config:
          input_from: steps.call
          expression: |
            if .status_code < 400 then {ok: true, data: .body}
            elif .status_code == 401 then {ok: false, error: {code: "not_authenticated", message: "Zoom OAuth token is missing or invalid; run setup flow."}}
            elif .status_code == 403 then {ok: false, error: {code: "scope_missing", message: (.body.message // "required OAuth scope is missing")}}
            elif .status_code == 404 then {ok: false, error: {code: "not_found", message: (.body.message // "resource not found")}}
            elif .status_code == 429 then {ok: false, error: {code: "rate_limited", message: "Zoom rate limit exceeded", retry_after: ((.headers["Retry-After"] // "60") | tonumber)}}
            else {ok: false, error: {code: "zoom_error", message: (.body.message // ("HTTP " + (.status_code | tostring)))}}
            end
      - type: step.pipeline_output
        config:
          source: steps.shape.result
`

type zoomMock struct {
	// mu protects the handler swap between sub-tests.
	handler http.Handler
}

func (m *zoomMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.handler.ServeHTTP(w, r)
}

// setupTestEnv wires a mock Zoom server, a fake keychain (via go-keyring's
// MockInit), and a freshly built engine pointing at the mock. It returns an
// MCP client session ready for CallTool plus a setHandler hook for per-test
// endpoint customisation.
func setupTestEnv(t *testing.T) (ctx context.Context, session *mcpsdk.ClientSession, setHandler func(http.Handler)) {
	t.Helper()

	keyring.MockInit()

	// Seed the keychain with a live access token so no refresh is triggered.
	storedToken, _ := json.Marshal(oauth2.Token{
		AccessToken:  "live-access",
		RefreshToken: "live-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	})
	for k, v := range map[string]string{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret",
		"oauth_token":   string(storedToken),
	} {
		if err := keyring.Set("zoom-mcp-test", k, v); err != nil {
			t.Fatalf("seed keyring %s: %v", k, err)
		}
	}

	mock := &zoomMock{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unconfigured", http.StatusTeapot)
	})}
	zoomSrv := httptest.NewServer(mock)
	t.Cleanup(zoomSrv.Close)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should not be hit when a live token is seeded; respond sanely anyway.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"token_type":"Bearer"}`)
	}))
	t.Cleanup(tokenSrv.Close)

	yamlStr := strings.NewReplacer(
		"{{ZoomURL}}", zoomSrv.URL,
		"{{TokenURL}}", tokenSrv.URL+"/token",
	).Replace(testZoomYAML)

	cfg, err := config.LoadFromString(yamlStr)
	if err != nil {
		t.Fatalf("LoadFromString: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := modular.NewStdApplication(nil, logger)
	engine := workflow.NewStdEngine(app, logger)
	if err := allplugins.LoadAll(engine); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if err := engine.LoadPlugin(mcpplugin.New()); err != nil {
		t.Fatalf("LoadPlugin mcp: %v", err)
	}
	if err := engine.BuildFromConfig(cfg); err != nil {
		t.Fatalf("BuildFromConfig: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := engine.Start(runCtx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = engine.Stop(stopCtx)
	})

	serverMod, ok := engine.App().GetModule("mcp-server").(*mcpplugin.ServerModule)
	if !ok {
		t.Fatal("mcp-server module missing or wrong type")
	}
	mcpServer := serverMod.Server()
	if mcpServer == nil {
		t.Fatal("mcp server is nil after Start")
	}

	clientTr, serverTr := mcpsdk.NewInMemoryTransports()
	go func() { _ = mcpServer.Run(runCtx, serverTr) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e-client", Version: "0"}, nil)
	sess, err := client.Connect(runCtx, clientTr, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	setHandler = func(h http.Handler) { mock.handler = h }
	return runCtx, sess, setHandler
}

// structuredResult unmarshals the MCP CallToolResult StructuredContent into
// the discriminated ok/error shape every Zoom tool pipeline produces.
type structuredResult struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		RetryAfter *int   `json:"retry_after,omitempty"`
	} `json:"error,omitempty"`
}

func callGetMe(t *testing.T, ctx context.Context, session *mcpsdk.ClientSession) *structuredResult {
	t.Helper()
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_me"})
	if err != nil {
		t.Fatalf("CallTool get_me: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_me IsError=true: %v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-marshal structured content: %v", err)
	}
	var out structuredResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v (raw: %s)", err, string(raw))
	}
	return &out
}

// TestE2E_GetMe_HappyPath asserts the full stack — config load, plugin
// registration, http.client auth, jq mapping, pipeline_output, MCP structured
// content — wires up cleanly for a 2xx response.
func TestE2E_GetMe_HappyPath(t *testing.T) {
	ctx, session, setHandler := setupTestEnv(t)

	var gotAuth string
	setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"me-id-123","email":"me@example.com","first_name":"Test","last_name":"User","type":1}`)
	}))

	result := callGetMe(t, ctx, session)
	if !result.OK {
		t.Fatalf("expected ok=true, got error: %+v", result.Error)
	}
	if gotAuth != "Bearer live-access" {
		t.Errorf("expected Authorization: Bearer live-access, got %q", gotAuth)
	}
	var data struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.ID != "me-id-123" || data.Email != "me@example.com" {
		t.Errorf("unexpected data payload: %+v", data)
	}
}

// TestE2E_GetMe_RateLimited asserts the jq error-mapping branch that turns a
// 429 with Retry-After into {ok:false, error:{code:"rate_limited", retry_after:N}}.
func TestE2E_GetMe_RateLimited(t *testing.T) {
	ctx, session, setHandler := setupTestEnv(t)

	setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":429,"message":"Too many requests"}`)
	}))

	result := callGetMe(t, ctx, session)
	if result.OK {
		t.Fatalf("expected ok=false, got ok=true with data: %s", string(result.Data))
	}
	if result.Error == nil {
		t.Fatal("error field is nil on !ok result")
	}
	if result.Error.Code != "rate_limited" {
		t.Errorf("error.code = %q, want %q", result.Error.Code, "rate_limited")
	}
	if result.Error.RetryAfter == nil || *result.Error.RetryAfter != 30 {
		retry := "<nil>"
		if result.Error.RetryAfter != nil {
			retry = fmt.Sprintf("%d", *result.Error.RetryAfter)
		}
		t.Errorf("error.retry_after = %s, want 30", retry)
	}
}
