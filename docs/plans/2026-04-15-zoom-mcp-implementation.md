# Zoom MCP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ship a local single-user Zoom MCP server on `github.com/GoCodeAlone/workflow` + `github.com/modelcontextprotocol/go-sdk`, with the supporting upstream Workflow contributions and a reusable `workflow-plugin-mcp` plugin.

**Architecture:** YAML-driven Workflow service in `zoom-mcp`; five upstream PRs to Workflow (secrets.KeychainProvider, step.secret_set, http.client, client: ref, step.while); new plugin repo wrapping the mcp go-sdk. zoom-mcp itself is ~150 LOC Go + YAML + JSON Schemas.

**Tech Stack:** Go 1.26+, `github.com/GoCodeAlone/workflow`, `github.com/GoCodeAlone/modular`, `github.com/modelcontextprotocol/go-sdk`, `github.com/zalando/go-keyring`, `github.com/pkg/browser`, `github.com/santhosh-tekuri/jsonschema/v5`, Go text/template.

**Design doc:** `docs/plans/2026-04-15-zoom-mcp-design.md`

---

## Execution model

This plan spans three repositories. Each phase works in a specific local path:

| Phase | Repo | Local path | Branch style |
|---|---|---|---|
| 1 | `GoCodeAlone/workflow` | `~/Projects/workflow` | `feat/<name>` branch per PR; one PR per phase task group |
| 2 | `GoCodeAlone/workflow-plugin-mcp` (new) | `~/Projects/workflow-plugin-mcp` | Direct main + tags |
| 3 | `GoCodeAlone/zoom-mcp` | `~/Projects/zoom-mcp` | Direct main; feature branches for discrete work if desired |

Upstream PRs (Phase 1) can be developed in any order, but PR 3 (`http.client`) depends on PR 1 (`KeychainProvider`) and PR 2 (`step.secret_set`) at zoom-mcp integration time (not at PR merge time). PRs can merge independently.

Phase 2 depends on PR 3 landing (for the `secrets.Provider` + `http.client` machinery the plugin's integration tests use). Phase 3 depends on all of Phase 1 and Phase 2.

**Autonomy boundary:** Phase 0 requires human action (repo creation under GoCodeAlone org, PR opening on upstream). All later phases run autonomously under subagent-driven-development once prerequisites are confirmed.

---

## Phase 0 — Prerequisites (manual)

Before any automated execution can proceed, confirm:

1. **Write access to `github.com/GoCodeAlone/workflow`.** PRs require push access (or fork + cross-repo PR). Verify with `gh repo view GoCodeAlone/workflow --json viewerPermission`.
2. **Permission to create repos under `GoCodeAlone` org** (for `workflow-plugin-mcp`). Verify with `gh repo create GoCodeAlone/workflow-plugin-mcp --template GoCodeAlone/workflow-plugin-template --public --dry-run` or equivalent.
3. **`~/Projects/workflow` is clean** — no uncommitted work. Run `git -C ~/Projects/workflow status` and stash/commit anything in progress.
4. **Go 1.26+, gh CLI, jq installed.** Run `go version && gh --version && jq --version`.
5. **Zoom OAuth app (user-managed) exists** for E2E testing. Create at https://marketplace.zoom.us/user/build if needed. Register `http://127.0.0.1:8765/oauth/callback` as the redirect URI. Record client_id + client_secret.

If any prerequisite is missing, pause and resolve before launching subagent-driven-development.

---

## Phase 1 — Upstream Workflow contributions

All work on `~/Projects/workflow`. Each PR is a separate branch off `main`. Commits are conventional: `feat:`, `test:`, `docs:`, `refactor:`. Tests are added alongside code (TDD: test first, then implementation).

### Task 1.1: Prepare the workflow repo

**Files:**
- None (branch bookkeeping only)

**Step 1:** Fetch latest main and verify clean.

Run:
```
git -C ~/Projects/workflow fetch origin
git -C ~/Projects/workflow checkout main
git -C ~/Projects/workflow reset --hard origin/main
git -C ~/Projects/workflow status
```
Expected: clean working tree on `main`, up to date with `origin/main`.

**Step 2:** Commit.

No code changes — skip commit. Move to Task 1.2.

---

### Task 1.2 (PR 1): `secrets.KeychainProvider` — branch + test first

**Files:**
- Create: `~/Projects/workflow/secrets/keychain_provider_test.go`

**Step 1:** Create feature branch.

Run: `git -C ~/Projects/workflow checkout -b feat/secrets-keychain-provider`

**Step 2:** Add `github.com/zalando/go-keyring` dependency.

Run: `cd ~/Projects/workflow && go get github.com/zalando/go-keyring@latest`

**Step 3:** Write failing test in `secrets/keychain_provider_test.go`.

```go
package secrets_test

import (
	"context"
	"testing"

	"github.com/GoCodeAlone/workflow/secrets"
	"github.com/zalando/go-keyring"
)

func TestKeychainProvider_SetAndGet(t *testing.T) {
	keyring.MockInit()
	p := secrets.NewKeychainProvider("test-service")

	ctx := context.Background()
	if err := p.Set(ctx, "api_key", "secret-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := p.Get(ctx, "api_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret-123" {
		t.Errorf("got %q, want secret-123", got)
	}
}

func TestKeychainProvider_GetMissing(t *testing.T) {
	keyring.MockInit()
	p := secrets.NewKeychainProvider("test-service")
	_, err := p.Get(context.Background(), "absent")
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

func TestKeychainProvider_Delete(t *testing.T) {
	keyring.MockInit()
	p := secrets.NewKeychainProvider("test-service")
	ctx := context.Background()
	_ = p.Set(ctx, "x", "1")
	if err := p.Delete(ctx, "x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := p.Get(ctx, "x"); err == nil {
		t.Fatal("expected error after Delete")
	}
}

func TestKeychainProvider_List(t *testing.T) {
	keyring.MockInit()
	p := secrets.NewKeychainProvider("test-service")
	ctx := context.Background()
	_ = p.Set(ctx, "a", "1")
	_ = p.Set(ctx, "b", "2")
	keys, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("got %d keys, want 2", len(keys))
	}
}
```

**Step 4:** Run test to verify failure.

Run: `cd ~/Projects/workflow && go test ./secrets/ -run TestKeychainProvider -v`

Expected: FAIL with "undefined: secrets.NewKeychainProvider".

**Step 5:** Commit failing test.

```
git -C ~/Projects/workflow add secrets/keychain_provider_test.go go.mod go.sum
git -C ~/Projects/workflow commit -m "test: add failing tests for KeychainProvider"
```

---

### Task 1.3: `KeychainProvider` — implementation

**Files:**
- Create: `~/Projects/workflow/secrets/keychain_provider.go`

**Step 1:** Read `~/Projects/workflow/secrets/secrets.go` to confirm the `Provider` interface signature and error conventions.

**Step 2:** Implement the provider in `secrets/keychain_provider.go`.

```go
package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// KeychainProvider implements Provider using the OS credential store
// (macOS Keychain, Linux Secret Service, Windows Credential Manager).
//
// All keys are namespaced under a single "service" string so multiple
// workflow services on the same machine don't collide.
//
// On Linux, requires a running Secret Service implementation (libsecret,
// gnome-keyring, or KWallet). Headless servers without one should use
// FileProvider or VaultProvider instead.
type KeychainProvider struct {
	service string
	// trackedKeys is maintained in-process for List() support, because the
	// go-keyring API doesn't provide a native list-by-service operation.
	// On cold start, List() returns only keys set during this process.
	trackedKeys map[string]struct{}
}

// NewKeychainProvider returns a provider namespaced to the given service name.
func NewKeychainProvider(service string) *KeychainProvider {
	return &KeychainProvider{
		service:     service,
		trackedKeys: make(map[string]struct{}),
	}
}

func (p *KeychainProvider) Get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	v, err := keyring.Get(p.service, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("secrets.keychain: %w", ErrNotFound)
		}
		return "", fmt.Errorf("secrets.keychain get %q: %w", key, err)
	}
	return v, nil
}

func (p *KeychainProvider) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := keyring.Set(p.service, key, value); err != nil {
		return fmt.Errorf("secrets.keychain set %q: %w", key, err)
	}
	p.trackedKeys[key] = struct{}{}
	return nil
}

func (p *KeychainProvider) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := keyring.Delete(p.service, key); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil // idempotent
		}
		return fmt.Errorf("secrets.keychain delete %q: %w", key, err)
	}
	delete(p.trackedKeys, key)
	return nil
}

func (p *KeychainProvider) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(p.trackedKeys))
	for k := range p.trackedKeys {
		out = append(out, k)
	}
	return out, nil
}
```

Note: if `secrets/secrets.go` already defines `ErrNotFound`, reuse it; otherwise add it to `secrets.go` — consult the file first.

**Step 3:** Run tests.

Run: `cd ~/Projects/workflow && go test ./secrets/ -run TestKeychainProvider -v`

Expected: PASS for all four sub-tests.

**Step 4:** Run full workflow test suite to confirm no regressions.

Run: `cd ~/Projects/workflow && go test ./... -timeout 60s`

Expected: all pass.

**Step 5:** Commit.

```
git -C ~/Projects/workflow add secrets/keychain_provider.go
git -C ~/Projects/workflow commit -m "feat(secrets): add KeychainProvider backed by go-keyring"
```

---

### Task 1.4: `KeychainProvider` — register with the provider factory

**Files:**
- Modify: `~/Projects/workflow/secrets/providers.go` (or wherever `NewProvider(type, config)` dispatches)

**Step 1:** Grep to locate the provider factory registration.

Run: `grep -rn "case \"file\"" ~/Projects/workflow/secrets/ || grep -rn "provider:" ~/Projects/workflow/secrets/`

Identify the switch statement or factory map that maps `provider: keychain` → a constructor.

**Step 2:** Add the `keychain` case.

Example modification (adapt to the actual file structure):

```go
case "keychain":
    service, _ := cfg["service"].(string)
    if service == "" {
        return nil, fmt.Errorf("secrets.keychain: 'service' is required")
    }
    return NewKeychainProvider(service), nil
```

**Step 3:** Add a factory-level integration test.

**Step 4:** Run tests.

Run: `cd ~/Projects/workflow && go test ./secrets/... -v`

Expected: PASS.

**Step 5:** Update README/docs if the secrets provider list is documented.

Run: `grep -rn "provider:" ~/Projects/workflow/docs/ ~/Projects/workflow/README.md 2>/dev/null`

Add a `keychain` entry to any provider-list section with a one-line description and the Linux Secret Service caveat.

**Step 6:** Commit.

```
git -C ~/Projects/workflow add secrets/providers.go docs/ README.md
git -C ~/Projects/workflow commit -m "feat(secrets): register keychain provider in factory"
```

---

### Task 1.5: Open PR 1

**Step 1:** Push branch.

Run: `git -C ~/Projects/workflow push -u origin feat/secrets-keychain-provider`

**Step 2:** Open PR.

Run:
```
gh pr create --repo GoCodeAlone/workflow --title "feat(secrets): add KeychainProvider" --body "$(cat <<'EOF'
## Summary

Adds a `KeychainProvider` implementation of `secrets.Provider` backed by the OS credential store (macOS Keychain, Linux Secret Service, Windows Credential Manager) via `github.com/zalando/go-keyring`.

## Motivation

Single-user local services (e.g., MCP servers) benefit from storing OAuth tokens and app credentials in the OS credential store rather than files. This is the minimal upstream addition required by zoom-mcp; other workflow-based single-user services benefit the same way.

## Surface

```yaml
modules:
  - name: my-secrets
    type: secrets.provider
    config:
      provider: keychain
      service: my-app
```

## Notes

- On Linux requires a working Secret Service (libsecret/gnome-keyring/KWallet). Documented.
- `List()` returns keys set during the current process only, because `go-keyring` has no native list-by-service operation. Noted in code comment.

## Test plan

- [x] Unit tests (set/get/delete/list happy paths + missing-key error)
- [x] `go test ./...` passes
- [ ] Manual: set a value, restart process, verify persistence (reviewer to confirm)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

**Step 3:** Checkout back to main for the next PR.

Run: `git -C ~/Projects/workflow checkout main`

---

### Task 1.6 (PR 2): `step.secret_set`

**Files:**
- Read first: `~/Projects/workflow/module/pipeline_step_secret_fetch.go` (model for the new step)
- Create: `~/Projects/workflow/module/pipeline_step_secret_set.go`
- Create: `~/Projects/workflow/module/pipeline_step_secret_set_test.go`
- Modify: `~/Projects/workflow/plugins/pipelinesteps/plugin.go` (register new step)

**Step 1:** Branch.

Run: `git -C ~/Projects/workflow checkout -b feat/step-secret-set`

**Step 2:** Read `pipeline_step_secret_fetch.go` fully. This new step is nearly identical in shape — same config loading, same `secrets.Provider` resolution, different operation (Set not Get).

**Step 3:** Write failing test at `module/pipeline_step_secret_set_test.go`.

```go
package module_test

import (
	"context"
	"testing"

	"github.com/GoCodeAlone/workflow/module"
	"github.com/GoCodeAlone/workflow/secrets"
	// plus any test harness imports used by the existing secret_fetch test
)

func TestSecretSetStep_WritesMultipleKeys(t *testing.T) {
	provider := secrets.NewMemoryProvider() // or whatever in-memory test helper exists
	// register provider in the fake module registry...

	step := module.NewSecretSetStep()
	cfg := map[string]any{
		"module": "test-secrets",
		"secrets": map[string]any{
			"client_id":     "{{ .request.form.client_id }}",
			"client_secret": "{{ .request.form.client_secret }}",
		},
	}
	// set up template context with .request.form.client_id, .request.form.client_secret

	ctx := /* pipeline context with provider registered */
	err := step.Execute(ctx, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := provider.Get(context.Background(), "client_id")
	if got != "my-id" { t.Errorf("client_id = %q, want my-id", got) }
	got, _ = provider.Get(context.Background(), "client_secret")
	if got != "my-secret" { t.Errorf("client_secret = %q, want my-secret", got) }
}
```

Read the existing `pipeline_step_secret_fetch_test.go` to mirror its setup harness precisely; the pseudocode above is a shape guide, not literal code.

**Step 4:** Run test — expect FAIL.

Run: `cd ~/Projects/workflow && go test ./module/ -run TestSecretSet -v`

Expected: FAIL with undefined symbol or unregistered step type.

**Step 5:** Commit failing test.

```
git -C ~/Projects/workflow add module/pipeline_step_secret_set_test.go
git -C ~/Projects/workflow commit -m "test: add failing tests for step.secret_set"
```

---

### Task 1.7: `step.secret_set` — implementation

**Step 1:** Create `module/pipeline_step_secret_set.go` by copying `pipeline_step_secret_fetch.go` and swapping the operation. Retain the exact config-loading patterns, module-reference resolution, and error handling from the fetch step.

Key differences vs. fetch:
- Config is `secrets: map[string]<template-string>` instead of `keys: []string`.
- For each entry, resolve the template against the pipeline context, then call `provider.Set(ctx, key, resolvedValue)`.
- Return `map[string]any{"set_keys": []string{...}}` or similar from the step's output for observability.

**Step 2:** Register `step.secret_set` in `plugins/pipelinesteps/plugin.go` alongside the existing `step.secret_fetch` registration.

**Step 3:** Run tests.

Run: `cd ~/Projects/workflow && go test ./module/ -run TestSecretSet -v && go test ./plugins/pipelinesteps/... -v`

Expected: PASS.

**Step 4:** Run full suite.

Run: `cd ~/Projects/workflow && go test ./... -timeout 60s`

Expected: PASS.

**Step 5:** Commit.

```
git -C ~/Projects/workflow add module/pipeline_step_secret_set.go plugins/pipelinesteps/plugin.go
git -C ~/Projects/workflow commit -m "feat(module): add step.secret_set for writing to secrets.Provider"
```

---

### Task 1.8: Open PR 2

Same as Task 1.5 but for `step.secret_set`. Title: `feat(module): add step.secret_set step`. Body summarizes the mirror-of-secret_fetch structure, notes the `secrets:` map-of-templates shape, references the Zoom MCP use case.

```
git -C ~/Projects/workflow push -u origin feat/step-secret-set
gh pr create --repo GoCodeAlone/workflow --title "feat(module): add step.secret_set step" --body "..."
git -C ~/Projects/workflow checkout main
```

---

### Task 1.9 (PR 3): `http.client` module — design sketch

**Files:**
- Create: `~/Projects/workflow/module/http_client.go`
- Create: `~/Projects/workflow/module/http_client_test.go`
- Create: `~/Projects/workflow/module/http_client_oauth2.go` (auth transports)
- Create: `~/Projects/workflow/module/http_client_oauth2_test.go`
- Modify: the module registration/factory file (find via `grep -rn "http.server" ~/Projects/workflow/plugins/`)
- Modify: `~/Projects/workflow/module/pipeline_step_http_call.go` (minor — register the service consumer pattern, no config change yet; config change is PR 4)

**Step 1:** Branch: `git -C ~/Projects/workflow checkout -b feat/http-client-module`.

**Step 2:** Review `~/Projects/workflow/module/pipeline_step_http_call.go:1-250` for the existing oauth2 client_credentials code — we will NOT remove it in this PR (back-compat). Instead, we copy the lookup/refresh/cache logic into the new module as a shared helper, and the step continues to work for now via its inline path.

**Step 3:** Define the public interface. Create a top-level doc comment in `http_client.go`:

```go
// Package module provides the http.client module, a reusable *http.Client
// provider exposed via modular's service registry.
//
// http.client wraps an *http.Client with an auth transport chain controlled
// by the module's config. Other modules and pipeline steps reference it by
// name to get an authenticated, ready-to-use client.
//
// Config:
//
//   modules:
//     - name: zoom-client
//       type: http.client
//       config:
//         base_url: "https://api.zoom.us/v2"   # optional; used by step.http_call with `client:` ref
//         timeout: 30s                          # default 30s
//         auth:
//           type: oauth2_refresh_token          # one of: none, static_bearer,
//                                               # oauth2_client_credentials, oauth2_refresh_token
//           token_url: "https://zoom.us/oauth/token"
//           client_id_from_secret:     {provider: zoom-secrets, key: client_id}
//           client_secret_from_secret: {provider: zoom-secrets, key: client_secret}
//           token_secrets:     zoom-secrets
//           token_secrets_key: oauth_token
//
// Consumers get the client via the service registry:
//
//   var c module.HTTPClient
//   app.GetService("zoom-client", &c)
//   resp, err := c.Client().Get(...)
```

**Step 4:** Write the test skeleton covering happy paths.

Tests to include in `module/http_client_test.go`:

1. `TestHTTPClient_NoneAuth` — module with `auth.type: none` returns a plain `*http.Client` with configured timeout.
2. `TestHTTPClient_StaticBearer` — requests get `Authorization: Bearer <token>` header.
3. `TestHTTPClient_OAuth2ClientCredentials` — given a mock token endpoint, client fetches token on first request and caches for subsequent calls.
4. `TestHTTPClient_OAuth2RefreshToken_TokenAbsent` — when the secrets.Provider returns ErrNotFound, client surfaces `ErrNoToken` as an `oauth2.RetrieveError`; request fails cleanly with a 401-ish error, module does NOT panic or fail to start.
5. `TestHTTPClient_OAuth2RefreshToken_TokenPresent` — token exists in provider, client uses it without refresh.
6. `TestHTTPClient_OAuth2RefreshToken_Refresh` — access token expired, client exchanges refresh_token for new access+refresh, persists the rotated refresh token back to secrets.Provider.
7. `TestHTTPClient_OAuth2RefreshToken_401Retry` — cached access token is rejected (401), client refreshes and retries once.
8. `TestHTTPClient_OAuth2RefreshToken_LateTokenArrival` — request errors when store is empty → test then writes a valid token to the store → subsequent request succeeds (validates the "no restart needed" invariant).

**Step 5:** Run failing tests.

```
cd ~/Projects/workflow && go test ./module/ -run TestHTTPClient -v
```

Expected: FAIL with undefined symbols.

**Step 6:** Commit failing tests.

```
git -C ~/Projects/workflow add module/http_client_test.go
git -C ~/Projects/workflow commit -m "test: add failing tests for http.client module"
```

---

### Task 1.10: `http.client` module — implementation, auth=none + static_bearer

**Files:**
- Create: `~/Projects/workflow/module/http_client.go`

**Step 1:** Implement the module skeleton: config struct, modular.Module interface methods (Name, Init, Start, Stop, Provides), HTTPClient service interface.

```go
type HTTPClient interface {
	Client() *http.Client
	BaseURL() string
}

type httpClientModule struct {
	name    string
	cfg     httpClientConfig
	client  *http.Client
	// populated in Init after resolving auth config
}

type httpClientConfig struct {
	BaseURL string        `yaml:"base_url" json:"base_url"`
	Timeout time.Duration `yaml:"timeout"  json:"timeout"`
	Auth    authConfig    `yaml:"auth"     json:"auth"`
}

type authConfig struct {
	Type                    string `yaml:"type" json:"type"`
	// ... union of all auth-type fields (type-switched on Type)
	BearerToken             string `yaml:"bearer_token,omitempty"`
	TokenURL                string `yaml:"token_url,omitempty"`
	ClientID                string `yaml:"client_id,omitempty"`
	ClientSecret            string `yaml:"client_secret,omitempty"`
	Scopes                  []string `yaml:"scopes,omitempty"`
	ClientIDFromSecret      *secretRef `yaml:"client_id_from_secret,omitempty"`
	ClientSecretFromSecret  *secretRef `yaml:"client_secret_from_secret,omitempty"`
	TokenSecrets            string `yaml:"token_secrets,omitempty"`
	TokenSecretsKey         string `yaml:"token_secrets_key,omitempty"`
}

type secretRef struct {
	Provider string `yaml:"provider" json:"provider"`
	Key      string `yaml:"key"      json:"key"`
}
```

**Step 2:** Implement `Init()` for `auth.type = none` and `auth.type = static_bearer`. Run the 2 corresponding tests.

**Step 3:** Commit.

```
git -C ~/Projects/workflow add module/http_client.go
git -C ~/Projects/workflow commit -m "feat(module): http.client module — none + static_bearer auth"
```

---

### Task 1.11: `http.client` — oauth2_client_credentials

**Step 1:** Extract the existing client_credentials logic from `pipeline_step_http_call.go` into `http_client_oauth2.go` (shared helper package-private). Leave the step's inline copy intact for back-compat until a later removal.

**Step 2:** Implement `buildOAuth2ClientCredentialsTransport` using `golang.org/x/oauth2/clientcredentials`. Wire it in `Init()` for `auth.type = oauth2_client_credentials`.

**Step 3:** Run tests 3. Expected: PASS.

**Step 4:** Commit.

```
git -C ~/Projects/workflow add module/http_client.go module/http_client_oauth2.go
git -C ~/Projects/workflow commit -m "feat(module): http.client — oauth2_client_credentials auth"
```

---

### Task 1.12: `http.client` — oauth2_refresh_token

**Step 1:** Implement the refresh-token auth path. Key design:

```go
// secretsBackedTokenSource implements oauth2.TokenSource, reading from and
// writing to a secrets.Provider. Each call to Token() consults the provider;
// after a successful refresh (rotation), the new token is persisted back.
type secretsBackedTokenSource struct {
	ctx        context.Context
	conf       *oauth2.Config  // has Endpoint.TokenURL, ClientID, ClientSecret
	store      secrets.Provider
	storeKey   string
}

func (s *secretsBackedTokenSource) Token() (*oauth2.Token, error) {
	raw, err := s.store.Get(s.ctx, s.storeKey)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, &oauth2.RetrieveError{
				Response:    &http.Response{StatusCode: 401},
				Body:        []byte("no token in secrets store"),
				ErrorCode:   "no_token",
				ErrorDescription: "token not yet set; complete setup flow first",
			}
		}
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("http.client: decode stored token: %w", err)
	}
	if tok.Valid() {
		return &tok, nil
	}
	// Expired — use its refresh token.
	newTok, err := s.conf.TokenSource(s.ctx, &tok).Token()
	if err != nil {
		return nil, err
	}
	// Persist rotated token.
	b, _ := json.Marshal(newTok)
	if err := s.store.Set(s.ctx, s.storeKey, string(b)); err != nil {
		return nil, fmt.Errorf("http.client: persist refreshed token: %w", err)
	}
	return newTok, nil
}
```

Wrap this with `oauth2.ReuseTokenSource` for in-process caching (avoid hitting the store on every HTTP call). Use `&oauth2.Transport{Source: ..., Base: http.DefaultTransport}` as the http.Client's Transport.

**Step 2:** Implement the 401-retry transport wrapper: on 401 response, invalidate the in-memory cache and retry once. This layer sits above `oauth2.Transport` so it can force the next Token() call to re-read from the store (which may have been rotated externally). Model on the existing 401-retry code in `pipeline_step_http_call.go`.

**Step 3:** Also resolve `client_id_from_secret` / `client_secret_from_secret` at `Init()` time by reading the secrets provider. Populate `oauth2.Config` with the resolved values.

**Step 4:** Run the 5 relevant tests (4–8 from the list in Task 1.9).

Run: `cd ~/Projects/workflow && go test ./module/ -run TestHTTPClient_OAuth2Refresh -v`

Expected: PASS.

**Step 5:** Run full suite.

Run: `cd ~/Projects/workflow && go test ./... -timeout 120s`

Expected: PASS.

**Step 6:** Commit.

```
git -C ~/Projects/workflow add module/http_client.go module/http_client_oauth2.go module/http_client_oauth2_test.go
git -C ~/Projects/workflow commit -m "feat(module): http.client — oauth2_refresh_token with secrets.Provider backing"
```

---

### Task 1.13: `http.client` — register module + integration test

**Step 1:** Register `http.client` as a module type. Grep for where `http.server` is registered (probably `plugins/http/plugin.go`) and add the new module type alongside.

**Step 2:** Add an integration test that loads a full workflow YAML (minimal inline) with an `http.client` module + an http.server trigger + a pipeline that calls back to itself via the client.

**Step 3:** Run tests.

**Step 4:** Commit.

```
git -C ~/Projects/workflow commit -m "feat(plugins/http): register http.client module"
```

---

### Task 1.14: PR 3 — docs + open PR

**Step 1:** Add `docs/modules/http-client.md` (or whatever the docs convention is) describing all four auth types, the `base_url` behavior, and the `secrets.Provider` integration.

**Step 2:** Update README or module-index docs.

**Step 3:** Commit docs.

**Step 4:** Push and open PR.

```
git -C ~/Projects/workflow push -u origin feat/http-client-module
gh pr create --repo GoCodeAlone/workflow --title "feat(module): add http.client module" --body "$(cat <<'EOF'
## Summary

Adds `http.client` — a reusable `*http.Client` provider module. Exposed via modular's service registry so other modules and pipeline steps can reference it by name to get a ready-to-use client with built-in auth, timeout, and future middleware support.

## Auth types

- `none`
- `static_bearer`
- `oauth2_client_credentials` (equivalent to inline OAuth2 in `step.http_call`, usable from any consumer)
- `oauth2_refresh_token` — persisted tokens via `secrets.Provider`; handles rotation

## Motivation

Multiple services (zoom-mcp being the driver) need OAuth-authenticated HTTP with per-call auth isolation. Moving this into a dedicated module avoids `step.http_call` growing into a config god-object and enables future `client:` refs from other step types (PR 4).

## Integration with `secrets.Provider`

The `oauth2_refresh_token` path reads/writes its token via a named `secrets.Provider`. Rotating refresh tokens (Zoom, Slack, etc.) are persisted back automatically. Missing-token state is tolerated: the module starts and surfaces an auth error on request rather than panicking.

## Follow-ups (separate PRs)

- PR 4: `client:` ref field on `step.http_call` — this PR does not change `step.http_call`'s config surface; back-compat preserved.

## Test plan

- [x] Unit tests for all four auth types
- [x] Test: late token arrival (store empty at boot → populated mid-life → request succeeds)
- [x] Test: 401 retry with rotated refresh token persistence
- [x] `go test ./...` passes

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
git -C ~/Projects/workflow checkout main
```

---

### Task 1.15 (PR 4): `client:` ref on `step.http_call`

**Files:**
- Modify: `~/Projects/workflow/module/pipeline_step_http_call.go` — add `Client string` field to `HTTPCallStep` config; resolve via service registry in `Execute`; when set, use that client's `*http.Client` instead of `http.DefaultClient`.
- Modify: test file to cover the new path.

**Step 1:** Branch: `git -C ~/Projects/workflow checkout -b feat/http-call-client-ref`.

**Step 2:** Write failing test. Cover:
- Step with `client: zoom-client` references a registered `http.client` module; request uses that client's transport (verify via mock that the right auth header was set).
- Step with `client:` + inline `oauth2:` config returns a config error at parse time.
- Step with relative URL + `client:` pointing at a module with `base_url` resolves URL against base_url.

**Step 3:** Run — expect FAIL.

**Step 4:** Commit failing tests.

**Step 5:** Implement:
- Add `Client` field to the step's config struct.
- In `Execute`, if `Client != ""`, resolve the HTTPClient service by that name from the pipeline context's app instance, use `client.Client()` for the request, and resolve relative URLs against `client.BaseURL()`.
- Reject `Client != "" && OAuth2 != nil` as a config error during validation/parse.

**Step 6:** Run tests. Expected: PASS.

**Step 7:** Run existing `step.http_call` tests to confirm back-compat.

Run: `cd ~/Projects/workflow && go test ./module/ -run TestHTTPCallStep -v`

Expected: PASS (including existing oauth2/no-client paths).

**Step 8:** Commit + push + open PR.

```
git -C ~/Projects/workflow add module/pipeline_step_http_call.go module/pipeline_step_http_call_test.go
git -C ~/Projects/workflow commit -m "feat(module): add 'client' ref field to step.http_call"
git -C ~/Projects/workflow push -u origin feat/http-call-client-ref
gh pr create --repo GoCodeAlone/workflow --title "feat(module): add client: ref on step.http_call" --body "..."
git -C ~/Projects/workflow checkout main
```

---

### Task 1.16 (PR 5): `step.while`

**Files:**
- Create: `~/Projects/workflow/module/pipeline_step_while.go`
- Create: `~/Projects/workflow/module/pipeline_step_while_test.go`
- Modify: `~/Projects/workflow/plugins/pipelinesteps/plugin.go` (register)

**Step 1:** Branch: `git -C ~/Projects/workflow checkout -b feat/step-while`.

**Step 2:** Review `module/pipeline_step_foreach.go` for the existing loop-step pattern — the `while` step borrows its sub-step invocation, iteration-var context population, and accumulator shape.

**Step 3:** Write failing tests. Cover:
- Simple while: condition template referencing last step's output; 3 iterations then stops.
- max_iterations enforcement: erroring loop exceeds cap.
- Accumulator: `accumulate.key` is populated with flattened array of all iterations' `accumulate.from` values.
- Empty body (no-op condition): zero iterations if condition is false at start.

**Step 4:** Implement:

```go
type WhileStepConfig struct {
	Condition     string       `yaml:"condition"      json:"condition"`
	MaxIterations int          `yaml:"max_iterations" json:"max_iterations"`
	IterationVar  string       `yaml:"iteration_var"  json:"iteration_var"`
	Accumulate    *AccumCfg    `yaml:"accumulate,omitempty" json:"accumulate,omitempty"`
	Steps         []StepConfig `yaml:"steps"          json:"steps"`
}

type AccumCfg struct {
	Key  string `yaml:"key"  json:"key"`
	From string `yaml:"from" json:"from"`
}

func (s *WhileStep) Execute(ctx pipelineCtx) error {
	cap := s.cfg.MaxIterations
	if cap == 0 {
		cap = 1000
	}
	var accumulated []any
	for i := 0; i < cap+1; i++ {
		if i == cap {
			return fmt.Errorf("step.while: exceeded max_iterations (%d)", cap)
		}

		// Evaluate condition before loop body (first iteration: based on initial context).
		cond, err := s.evalCondition(ctx)
		if err != nil {
			return err
		}
		if !cond {
			break
		}

		// Populate iteration_var in context.
		if s.cfg.IterationVar != "" {
			ctx.Set(s.cfg.IterationVar, map[string]any{"index": i, "first": i == 0})
		}

		// Run sub-steps.
		for _, sub := range s.cfg.Steps {
			if err := sub.Execute(ctx); err != nil {
				return err
			}
		}

		// Accumulate.
		if s.cfg.Accumulate != nil {
			val, err := resolveTemplate(s.cfg.Accumulate.From, ctx)
			if err != nil {
				return err
			}
			if slice, ok := val.([]any); ok {
				accumulated = append(accumulated, slice...)
			} else if val != nil {
				accumulated = append(accumulated, val)
			}
		}
	}

	if s.cfg.Accumulate != nil {
		ctx.SetAccumulated(s.cfg.Accumulate.Key, accumulated)
	}
	return nil
}

func (s *WhileStep) evalCondition(ctx pipelineCtx) (bool, error) {
	val, err := resolveTemplate(s.cfg.Condition, ctx)
	if err != nil {
		return false, err
	}
	return truthy(val), nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "" && t != "false" && t != "0"
	case int, int64:
		return fmt.Sprintf("%d", t) != "0"
	case nil:
		return false
	default:
		return true
	}
}
```

Note: adapt to actual Workflow pipeline context API — this is shape guidance, not literal. Study `pipeline_step_foreach.go` for the real integration points.

**Step 5:** Register in `plugins/pipelinesteps/plugin.go`.

**Step 6:** Run tests, push, PR.

```
git -C ~/Projects/workflow add module/pipeline_step_while.go module/pipeline_step_while_test.go plugins/pipelinesteps/plugin.go
git -C ~/Projects/workflow commit -m "feat(module): add step.while for cursor/condition-driven loops"
git -C ~/Projects/workflow push -u origin feat/step-while
gh pr create --repo GoCodeAlone/workflow --title "feat(module): add step.while" --body "..."
git -C ~/Projects/workflow checkout main
```

---

## Phase 2 — `workflow-plugin-mcp`

All work in `~/Projects/workflow-plugin-mcp` (new repo created from template).

### Task 2.1: Create the plugin repo from template

**Step 1:** Create repo from template.

Run:
```
gh repo create GoCodeAlone/workflow-plugin-mcp --template GoCodeAlone/workflow-plugin-template --public --description "MCP (Model Context Protocol) plugin for GoCodeAlone/workflow"
gh repo clone GoCodeAlone/workflow-plugin-mcp ~/Projects/workflow-plugin-mcp
cd ~/Projects/workflow-plugin-mcp
```

**Step 2:** Rename template placeholders.

Run: `grep -rln 'workflow-plugin-TEMPLATE\|workflow_plugin_TEMPLATE\|TEMPLATE' . --exclude-dir=.git | xargs sed -i '' 's/workflow-plugin-TEMPLATE/workflow-plugin-mcp/g; s/TEMPLATE/mcp/g'` (adjust if sed args differ on macOS vs GNU).

Verify no placeholders remain: `grep -rn 'TEMPLATE' . --exclude-dir=.git` (expected: nothing, or only text in docs that's obviously a template instruction).

**Step 3:** Update `go.mod` module path to `github.com/GoCodeAlone/workflow-plugin-mcp`. Rename `cmd/workflow-plugin-TEMPLATE/` to `cmd/workflow-plugin-mcp/`. Run `go mod tidy`.

**Step 4:** Add dependencies.

```
cd ~/Projects/workflow-plugin-mcp
go get github.com/modelcontextprotocol/go-sdk@latest
go get github.com/GoCodeAlone/workflow@latest
go get github.com/santhosh-tekuri/jsonschema/v5@latest
```

**Step 5:** Commit.

```
git add .
git commit -m "chore: initialize from workflow-plugin-template for MCP plugin"
git push
```

---

### Task 2.2: `mcp.server` module — skeleton + test

**Files:**
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/server_module.go`
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/server_module_test.go`

**Step 1:** Write failing test.

```go
func TestServerModule_InitBindsInstance(t *testing.T) {
	app := modular.NewTestApp(t)
	mod := mcp.NewServerModule(mcp.ServerConfig{
		Implementation: mcp.Implementation{Name: "test", Version: "0.0.1"},
	})
	// register mod with app, run Init, assert that the service registry has an
	// *mcp.Server registered under mod.Name()
}

func TestServerModule_AddTool_ValidatesSchemaIsObject(t *testing.T) {
	// creating the server succeeds; registering a tool whose input_schema is
	// not an object should error at registration time
}
```

Adapt to the actual Workflow/modular test harness patterns.

**Step 2:** Implement the module. Core:

```go
type ServerModule struct {
	name   string
	cfg    ServerConfig
	server *mcp.Server
}

func (m *ServerModule) Name() string { return m.name }

func (m *ServerModule) Init(app modular.App) error {
	m.server = mcp.NewServer(&mcp.Implementation{
		Name:    m.cfg.Implementation.Name,
		Version: m.cfg.Implementation.Version,
	}, nil)
	app.RegisterService(m.name, m.server)
	return nil
}

// Start/Stop are no-ops; transports start the server via transport.Start.
```

**Step 3:** Run tests — PASS.

**Step 4:** Commit.

```
git add internal/mcp/server_module.go internal/mcp/server_module_test.go
git commit -m "feat: add mcp.server module"
```

---

### Task 2.3: `mcp.stdio_transport` + `mcp.http_transport`

**Files:**
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/stdio_transport.go` + test
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/http_transport.go` + test

**Step 1:** Write failing tests. Each transport module takes a `server: <module-name>` config, resolves the server via service registry at Init time, and in Start spawns the transport.

For `http_transport`, the test uses `httptest.NewServer(mcp.NewStreamableHTTPHandler(...))` and asserts the handler is reachable. For `stdio_transport`, test with `mcp.InMemoryTransport` as a stand-in (or test the module's Start/Stop flow against a pipe).

**Step 2:** Implement.

```go
// stdio_transport.go
func (m *StdioTransportModule) Start(ctx context.Context) error {
	go func() {
		if err := m.server.Run(ctx, &mcp.StdioTransport{}); err != nil {
			// log via modular logger
		}
	}()
	return nil
}

// http_transport.go
func (m *HTTPTransportModule) Start(ctx context.Context) error {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return m.server
	}, nil)
	m.httpSrv = &http.Server{Addr: m.cfg.Address, Handler: handler}
	go func() {
		if err := m.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// log
		}
	}()
	return nil
}

func (m *HTTPTransportModule) Stop(ctx context.Context) error {
	return m.httpSrv.Shutdown(ctx)
}
```

**Step 3:** Tests PASS.

**Step 4:** Commit each transport separately.

```
git commit -m "feat: add mcp.stdio_transport module"
git commit -m "feat: add mcp.http_transport module"
```

---

### Task 2.4: `mcp.tool` trigger

**Files:**
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/tool_trigger.go` + test

**Step 1:** Write failing test using `InMemoryTransport`.

```go
func TestToolTrigger_RegistersAndDispatches(t *testing.T) {
	// 1. Create an mcp.server module + InMemoryTransport.
	// 2. Create a tool_trigger for pipeline "greet" with input schema
	//    {"type":"object","properties":{"name":{"type":"string"}}}.
	// 3. Register pipeline "greet" with a fake handler that reads .input.name
	//    and sets output {"greeting": "hi " + name}.
	// 4. Start everything; from the client end, send a tools/call request.
	// 5. Assert the output.
}

func TestToolTrigger_ValidatesInputAgainstSchema(t *testing.T) {
	// Invalid input (missing required field) returns IsError: true without
	// invoking the pipeline.
}

func TestToolTrigger_ResolvesSchemaRef(t *testing.T) {
	// input_schema: {$ref: ./schemas/greet.json} is read from disk.
}
```

**Step 2:** Implement the trigger.

Core logic:
1. At trigger Init: load/parse `input_schema` (inline `map[string]any` or `$ref: <path>` → read file → parse JSON).
2. Register with the server: `server.AddTool(&mcp.Tool{Name: ..., Description: ..., InputSchema: inputSchemaMap, OutputSchema: outputSchemaMap}, handler)`.
3. `handler` (the ToolHandler passed to AddTool):
   - Parse `req.Params.Arguments` (json.RawMessage) into `map[string]any`.
   - Validate against the compiled input schema (`jsonschema/v5`).
   - If invalid, return `*CallToolResult{Content: [TextContent{Text: "invalid input: ..."}], IsError: true}, nil`.
   - Construct a pipeline event with payload `{input: argsMap, meta: {tool_name, request_id}}`.
   - Dispatch to the named pipeline via the Workflow engine's handler-dispatch API (inspect Workflow's HTTP trigger for the exact dispatch mechanism — the MCP trigger mirrors that pattern).
   - On pipeline return: JSON-marshal the output into a `TextContent` block; set `StructuredContent` if the SDK version supports it; set `IsError: true` if the pipeline marked an error.

**Step 3:** Run tests — PASS.

**Step 4:** Commit.

```
git commit -m "feat: add mcp.tool trigger for pipeline registration"
```

---

### Task 2.5: Plugin registration + end-to-end test

**Files:**
- Modify: `~/Projects/workflow-plugin-mcp/plugin.go` — the template's main plugin type. Register `ServerModule`, `StdioTransportModule`, `HTTPTransportModule` via `ModuleFactories()`. Register `ToolTrigger` via `TriggerFactories()`.
- Modify: `~/Projects/workflow-plugin-mcp/internal/mcp/server.go` — ensure `ServerModule` declares a modular dependency on the tool-registration phase so it starts after triggers have registered.
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/e2e_test.go`
- Create: `~/Projects/workflow-plugin-mcp/internal/mcp/lifecycle_test.go`

**Step 1:** Implement `EnginePlugin` interface per the template.

**Step 2:** Declare lifecycle ordering on `ServerModule`. The design requires `mcp.server` to start only after every `mcp.tool` trigger has registered its tool with the server, so the initial `tools/list` response is complete.

Use modular's dependency declaration — whichever of these the current `github.com/GoCodeAlone/modular` exposes (check imports first):

- If modular supports `DependsOn() []string`, declare it on `ServerModule` referencing the trigger service name(s).
- If modular uses a start-order interface (e.g. `StartsAfter`), use that.
- Otherwise, have `ToolTrigger.Register()` push into a shared registry module that `ServerModule` consumes, and wire the registry as a dependency.

Concretely:

```go
// internal/mcp/server.go
func (m *ServerModule) DependsOn() []string {
    // The tool registry is populated by mcp.tool triggers at their Register()
    // step; depending on it guarantees ServerModule.Start runs after all
    // triggers have registered their tools.
    return []string{"mcp.tool-registry"}
}
```

If no explicit registry module exists, create one (`ToolRegistryModule`) whose `Register()` is a no-op and whose presence in the module graph makes the dependency edge valid; triggers obtain it via the DI container and call `Registry.Add(name, handler, schema)`; `ServerModule.Start` iterates `Registry.All()` to populate the mcp.Server before starting transports.

**Step 3:** Write lifecycle test (`lifecycle_test.go`): build an engine with the plugin, 2 pipelines each with `mcp.tool` triggers, and assert that by the time `ServerModule.Start` is called, both tools are visible via `mcp.Server.ListTools()` (or whatever the go-sdk exposes).

**Step 4:** Write E2E test: full YAML with `mcp.server` + `mcp.stdio_transport` (using InMemoryTransport instead) + a pipeline with `mcp.tool` trigger. Start engine, send a `tools/call` from an in-memory MCP client, assert output.

**Step 5:** Run tests.

**Step 6:** Commit.

```
git commit -m "feat: register modules and trigger in EnginePlugin; enforce server-after-triggers lifecycle; add E2E test"
```

---

### Task 2.6: README + tag v0.1.0

**Step 1:** Write README with: overview, install/import, YAML shape for all three modules + trigger, schema handling (inline vs $ref), testing with `InMemoryTransport`.

**Step 2:** Commit README.

**Step 3:** Tag.

```
git tag v0.1.0
git push origin main --tags
```

---

## Phase 3 — `zoom-mcp` service

All work in `~/Projects/zoom-mcp` (this repo). Fresh branch `feat/initial-implementation` off `main`.

### Task 3.1: Go module setup

**Files:**
- Modify: `~/Projects/zoom-mcp/go.mod` — create
- Create: `~/Projects/zoom-mcp/cmd/zoom-mcp/main.go` — stub

**Step 1:** Branch: `git -C ~/Projects/zoom-mcp checkout -b feat/initial-implementation`.

**Step 2:** Init module.

Run:
```
cd ~/Projects/zoom-mcp
go mod init github.com/GoCodeAlone/zoom-mcp
go get github.com/GoCodeAlone/workflow@latest
go get github.com/GoCodeAlone/workflow-plugin-mcp@latest
go get github.com/pkg/browser@latest
```

**Step 3:** Create `cmd/zoom-mcp/main.go` stub that just prints "zoom-mcp v0.0.1" and exits (will be filled in in Task 3.5).

**Step 4:** `go build ./...` succeeds.

**Step 5:** Commit.

```
git add go.mod go.sum cmd/zoom-mcp/main.go
git commit -m "chore: initialize Go module"
```

---

### Task 3.2: JSON schemas for all 15 tools

**Files:**
- Create: `~/Projects/zoom-mcp/config/schemas/get_me.input.json` + `.output.json`
- Create: `~/Projects/zoom-mcp/config/schemas/list_meetings.input.json` + `.output.json`
- ... 15 pairs total (see design doc Section 5 tool catalog)

**Step 1:** Write each schema pair. Input schemas mirror the Zoom endpoint's query/path parameters; output schemas wrap the Zoom response in the unified shape `{ "ok": true, "data": <zoom-body> }` or `{ "ok": false, "error": {...} }`.

Example (`list_meetings.input.json`):

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "type": {
      "type": "string",
      "enum": ["scheduled", "live", "upcoming", "upcoming_meetings", "previous_meetings"],
      "default": "upcoming",
      "description": "Which set of meetings to return. Defaults to 'upcoming'."
    }
  }
}
```

Example (`list_meetings.output.json`):

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "oneOf": [
    {
      "type": "object",
      "properties": {
        "ok": { "const": true },
        "data": {
          "type": "object",
          "properties": {
            "meetings": { "type": "array" },
            "count":    { "type": "integer" }
          },
          "required": ["meetings", "count"]
        }
      },
      "required": ["ok", "data"]
    },
    {
      "type": "object",
      "properties": {
        "ok": { "const": false },
        "error": {
          "type": "object",
          "properties": {
            "code":        { "type": "string" },
            "message":     { "type": "string" },
            "retry_after": { "type": "integer" }
          },
          "required": ["code", "message"]
        }
      },
      "required": ["ok", "error"]
    }
  ]
}
```

**Step 2:** Validate each schema with `jq . config/schemas/<file>.json` (syntax check).

**Step 3:** Commit schemas in one commit.

```
git add config/schemas/
git commit -m "feat: JSON schemas for all 15 v1 tools"
```

---

### Task 3.3: Workflow YAML — modules and setup pipelines

**Files:**
- Create: `~/Projects/zoom-mcp/config/zoom-mcp.yaml`

**Step 1:** Write the top of the YAML — modules, workflows section, and the three setup pipelines (`setup-form`, `setup-save`, `oauth-callback`). Copy from design doc Section "Unified Workflow YAML" verbatim as a starting point.

**Step 2:** Commit.

```
git add config/zoom-mcp.yaml
git commit -m "feat(config): add modules + setup flow pipelines"
```

---

### Task 3.4: Workflow YAML — one tool pipeline at a time

15 sub-tasks, one per tool, plus one shared error-mapping sub-task (3.4.0) that every pipeline inherits. Same pattern each time: add a `pipelines.<name>` entry with `trigger: { type: mcp.tool, config: {...} }` + `steps: [...]`. Commit after each one so progress is visible.

**Sub-task 3.4.0: Shared error-mapping helper and 429 handling**

All 15 tool pipelines must produce the unified error shape on non-2xx responses. Define it once and reuse.

**Files:**
- Modify: `~/Projects/zoom-mcp/config/zoom-mcp.yaml` — add a reusable `error_mapping` anchor or helper pipeline.

**Step 1:** Add the following near the top of the YAML (under `workflows:` or as a template block if Workflow supports YAML anchors):

```yaml
# Reusable error mapper: turns any non-2xx Zoom response into the
# unified `{ ok: false, error: { code, message, retry_after } }` shape.
# Callers invoke it via step.transform with `.response` in scope.
error_shapes:
  default_map: |
    {{- if eq .response.status 401 -}}
    {"ok": false, "error": {"code": "not_authenticated", "message": "Zoom OAuth token is missing or invalid; run setup flow."}}
    {{- else if eq .response.status 403 -}}
    {"ok": false, "error": {"code": "scope_missing", "message": "{{ .response.body.message }}"}}
    {{- else if eq .response.status 404 -}}
    {"ok": false, "error": {"code": "not_found", "message": "{{ .response.body.message }}"}}
    {{- else if eq .response.status 429 -}}
    {"ok": false, "error": {"code": "rate_limited", "message": "Zoom rate limit exceeded", "retry_after": {{ or (index .response.headers "Retry-After") 60 }}}}
    {{- else -}}
    {"ok": false, "error": {"code": "zoom_error", "message": "{{ .response.body.message | default (printf "HTTP %d" .response.status) }}"}}
    {{- end -}}
```

**Step 2:** In each tool pipeline (sub-tasks 3.4.1–3.4.15) the final `step.transform` or `step.raw_response` selects between success and the shared error mapper based on `.response.status >= 400`. Pattern:

```yaml
- type: step.transform
  name: shape_output
  config:
    template: |
      {{- if lt .response.status 400 -}}
      {"ok": true, "data": {{ toJson .response.body }}}
      {{- else -}}
      {{ template "error_shapes.default_map" . }}
      {{- end -}}
```

**Step 3:** Write a unit test that feeds a synthetic `.response` with status=429 and `Retry-After: 30` into the template and asserts the output equals `{"ok": false, "error": {"code": "rate_limited", "message": "Zoom rate limit exceeded", "retry_after": 30}}`.

**Step 4:** Run the test — PASS.

**Step 5:** Commit.

```
git add config/zoom-mcp.yaml
git commit -m "feat(config): shared error mapper with 429 retry_after extraction"
```

**Sub-task 3.4.1: `get_me`** — single `step.http_call` to `/users/me`, `step.transform` wraps output.

**Sub-task 3.4.2: `list_meetings`** — `step.while` with paginated `step.http_call` (see design doc).

**Sub-task 3.4.3: `get_meeting`** — single `step.http_call` to `/meetings/{{ .input.meeting_id }}`.

**Sub-task 3.4.4: `get_past_meeting`** — single call to `/past_meetings/{{ .input.meeting_id }}`.

**Sub-task 3.4.5: `list_past_meeting_instances`** — single call (not paginated per Zoom docs).

**Sub-task 3.4.6: `list_past_meeting_participants`** — paginated.

**Sub-task 3.4.7: `list_recordings`** — paginated; input has `from`, `to` date range params; default to last 30 days.

**Sub-task 3.4.8: `get_meeting_recordings`** — single call, returns `recording_files[]`.

**Sub-task 3.4.9: `get_transcript`** — composite. Two step.http_call attempts (direct `/meetings/{id}/transcript` then fallback to recordings-files + download via `zoom-download-client`). Model on the design doc's notes.

**Sub-task 3.4.10: `get_meeting_summary`** — single call; handle 404 → `ai_companion_unavailable` error response.

**Sub-task 3.4.11: `list_meeting_summaries`** — paginated.

**Sub-task 3.4.12: `list_chat_channels`** — paginated.

**Sub-task 3.4.13: `get_chat_channel`** — single call.

**Sub-task 3.4.14: `list_chat_messages`** — paginated; input has `to_channel` XOR `to_contact`.

**Sub-task 3.4.15: `get_chat_message`** — single call.

For each sub-task: Step 1 add the YAML → Step 2 run `go build ./... && zoom-mcp-config-lint config/zoom-mcp.yaml` (if such a lint exists; otherwise `go run ./cmd/zoom-mcp --config-validate config/zoom-mcp.yaml`) → Step 3 commit with message `feat(config): add <tool_name> pipeline`.

---

### Task 3.5: `cmd/zoom-mcp/main.go` — bootstrap + CLI

**Files:**
- Modify: `~/Projects/zoom-mcp/cmd/zoom-mcp/main.go`

**Step 1:** Implement the main function:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/GoCodeAlone/modular"
	"github.com/GoCodeAlone/workflow"
	// plus all the plugin imports for init() side effects
	_ "github.com/GoCodeAlone/workflow/secrets"
	mcpplugin "github.com/GoCodeAlone/workflow-plugin-mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pkg/browser"
)

func main() {
	var (
		configPath = flag.String("config", "config/zoom-mcp.yaml", "path to workflow config")
		noBrowser  = flag.Bool("no-browser", false, "don't auto-open setup URL")
	)
	flag.Parse()

	args := flag.Args()

	// Subcommand dispatch.
	if len(args) > 0 {
		switch args[0] {
		case "config":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: zoom-mcp config {show|reset}")
				os.Exit(2)
			}
			switch args[1] {
			case "show":
				configShow()
				return
			case "reset":
				configReset()
				return
			default:
				fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", args[1])
				os.Exit(2)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
			os.Exit(2)
		}
	}

	// Default: run the engine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine, err := workflow.NewFromConfig(*configPath, modular.WithPlugins(mcpplugin.Plugin()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "zoom-mcp: %v\n", err)
		os.Exit(1)
	}

	// Before starting, check keychain state and open browser if needed.
	ready := checkKeychainReady(engine)
	setupURL := "http://127.0.0.1:8765/setup"
	if !ready && !*noBrowser {
		browser.OpenURL(setupURL)
		fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; opened "+setupURL)
	} else if !ready {
		fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; visit "+setupURL)
	}

	// Schedule an MCP notifications/message once transports have started, so
	// Claude Desktop (and any other client) can surface the setup URL in UI.
	// Best-effort — if the server isn't up yet or the notification fails, the
	// stderr message above + browser-open are already user-visible.
	if !ready {
		go func() {
			mcpServer := resolveMCPServer(engine) // DI lookup; returns nil until started
			for i := 0; i < 20 && mcpServer == nil; i++ {
				time.Sleep(100 * time.Millisecond)
				mcpServer = resolveMCPServer(engine)
			}
			if mcpServer == nil {
				return
			}
			_ = mcpServer.Notify(ctx, "notifications/message", map[string]any{
				"level":  "info",
				"logger": "zoom-mcp",
				"data":   "Zoom credentials not configured. Complete setup at " + setupURL,
			})
		}()
	}

	if err := engine.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "zoom-mcp: %v\n", err)
		os.Exit(1)
	}
}

// resolveMCPServer pulls the *mcp.Server instance out of the DI container.
// Returns nil if the mcp.server module hasn't finished starting yet.
//
// The Workflow engine exposes a service registry via modular's container.
// The mcp.server module registers itself under the name "mcp.server" in
// workflow-plugin-mcp's ServerModule.Register(). We look it up here and
// type-assert to *mcp.Server.
func resolveMCPServer(engine *workflow.Engine) *mcp.Server {
	svc, ok := engine.Container().Lookup("mcp.server")
	if !ok {
		return nil
	}
	server, ok := svc.(*mcp.Server)
	if !ok {
		return nil
	}
	return server
}

func configShow() {
	// read secrets provider via keyring (service=zoom-mcp), print redacted.
}

func configReset() {
	// delete client_id, client_secret, oauth_token from keyring.
}

func checkKeychainReady(engine *workflow.Engine) bool {
	// resolve zoom-secrets provider from engine services; Get all 3 required keys.
}
```

**Step 2:** Verify `resolveMCPServer` uses the correct modular service-lookup API. At time of writing the planner has not confirmed the exact method name; the sketch above uses `engine.Container().Lookup("mcp.server")`. When implementing:

- Read `github.com/GoCodeAlone/modular` to find the actual service-lookup API (likely one of `GetService`, `Resolve`, `Lookup`, or `Services().Get`).
- Read `workflow-plugin-mcp/internal/mcp/server.go` (Task 2.2) to confirm the exact service name ServerModule registers under (`"mcp.server"` is the target; verify).
- Replace the two `engine.Container().Lookup(...)` call sites with the correct API.

**Step 3:** Run `go build ./cmd/zoom-mcp` — compiles.

**Step 4:** Run `./zoom-mcp config show` — exits cleanly (prints "no credentials configured" or similar when keychain is empty).

**Step 5:** Write a unit test `cmd/zoom-mcp/main_test.go` that builds a fake engine with a pre-registered `*mcp.Server` under the expected service name and asserts `resolveMCPServer(engine) != nil`. This guards against silent notification-path regressions if the modular API or service name changes.

**Step 6:** Run `go test ./cmd/zoom-mcp/...` — PASS.

**Step 7:** Commit.

```
git add cmd/zoom-mcp/main.go cmd/zoom-mcp/main_test.go
git commit -m "feat: cmd/zoom-mcp bootstrap + config CLI + setup auto-open + notifications/message"
```

---

### Task 3.6: Integration test against mock Zoom

**Files:**
- Create: `~/Projects/zoom-mcp/cmd/zoom-mcp/e2e_test.go`

**Step 1:** Write integration test that exercises every required scenario from the design doc's Testing section:

1. **Setup.** Spin up a `httptest.Server` impersonating Zoom (OAuth token endpoint + a subset of API endpoints, configurable per-test). Spin up the zoom-mcp engine pointed at a test config where `zoom-client.auth.token_url` and `base_url` point to the httptest server. Use an `InMemoryTransport` MCP client.

2. **Happy path.** Call `list_meetings`, `get_meeting`, `get_me`. Assert `ok: true` and the expected data shape.

3. **401 refresh.** Token in store has expired `expires_at`. Zoom mock responds 401 on first call with the expired token, 200 after the client exchanges the refresh token. Assert tool call succeeds and the new access token is persisted to the keychain provider.

4. **Pagination (3 pages).** `list_meetings` mock responds with `next_page_token` on pages 1 and 2, empty on page 3. Assert the tool returns all accumulated meetings in one response and that `step.while` stopped correctly.

5. **Missing transcript → 404 fallback → `transcript_unavailable`.** `get_transcript` mock responds 404 on the direct `/meetings/{id}/transcript` endpoint. The fallback path (recordings files + download via `zoom-download-client`) also finds no VTT file. Assert the tool returns `{ ok: false, error: { code: "transcript_unavailable", ... } }`.

6. **Missing AI Companion summary → 404 → `ai_companion_unavailable`.** `get_meeting_summary` mock responds 404. Assert the tool returns `{ ok: false, error: { code: "ai_companion_unavailable", ... } }`.

7. **Rate limit → 429 → `rate_limited` with retry_after.** Any tool; mock responds 429 with `Retry-After: 30`. Assert the tool returns `{ ok: false, error: { code: "rate_limited", retry_after: 30 } }`.

Use table-driven tests where natural (one table per scenario shape).

**Step 2:** Run test — PASS. Expected: all 7 sub-scenarios pass.

**Step 3:** Commit.

```
git add cmd/zoom-mcp/e2e_test.go
git commit -m "test: E2E against mock Zoom covering happy path, 401 refresh, pagination, 404 transcript, 404 AI Companion, 429 rate limit"
```

---

### Task 3.7: README + distribution

**Files:**
- Modify: `~/Projects/zoom-mcp/README.md`

**Step 1:** Write comprehensive README:
- Introduction: what zoom-mcp is, who it's for.
- Installation: `go install github.com/GoCodeAlone/zoom-mcp/cmd/zoom-mcp@latest` or binary download link (once releases exist).
- Zoom OAuth app setup: step-by-step with screenshots or sections.
  - Create a User-managed OAuth app at `https://marketplace.zoom.us/user/build`.
  - Register redirect URI: `http://127.0.0.1:8765/oauth/callback`.
  - Record client_id + client_secret.
- First run: `zoom-mcp` → browser opens → fill form → authorize → done.
- MCP client setup: snippet for Claude Desktop `claude_desktop_config.json`:

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

- Available tools list (from design doc tool catalog).
- Troubleshooting: common errors, `zoom-mcp config show`, `zoom-mcp config reset`.
- Linux keychain note.

**Step 2:** Commit.

```
git add README.md
git commit -m "docs: comprehensive README with setup instructions"
```

---

### Task 3.8: Manual acceptance tests (human-in-the-loop)

**Not a subagent task — flag for human execution.**

Checklist:
- [ ] `zoom-mcp config reset` then `zoom-mcp` → browser opens → complete real Zoom OAuth → token stored → `zoom-mcp config show` shows valid token.
- [ ] Claude Desktop: add config → restart → tools available → run `list_meetings`.
- [ ] `get_transcript` against a meeting with a real transcript → returns VTT text.
- [ ] `get_meeting_summary` against a meeting with AI Companion enabled → returns structured summary.
- [ ] `list_chat_channels` → returns your channels.
- [ ] Wait >1 hour so access token expires, then make a tool call → should transparently refresh.

---

### Task 3.9: Merge, tag, release

**Step 1:** Merge `feat/initial-implementation` into `main` (via PR or direct, per repo conventions).

**Step 2:** Tag v0.1.0.

```
git tag v0.1.0
git push origin main --tags
```

**Step 3:** Create GitHub release with binary artifacts (if goreleaser is configured via the template).

---

## Summary of commits

| Phase | Commits | Files touched |
|---|---|---|
| 1 (PR 1–5) | ~20 commits | `~/Projects/workflow/{secrets,module,plugins,docs}/...` |
| 2 (plugin) | ~10 commits | `~/Projects/workflow-plugin-mcp/**` |
| 3 (service) | ~25 commits | `~/Projects/zoom-mcp/**` |

Total: ~55 commits. Each is TDD (test first) and atomic.

## Success criteria

- [ ] All 5 upstream PRs open; CI passing on each.
- [ ] `workflow-plugin-mcp` v0.1.0 tagged and published.
- [ ] `zoom-mcp` v0.1.0 tagged; binaries available.
- [ ] `zoom-mcp config reset && zoom-mcp` results in a working Claude Desktop integration end-to-end against real Zoom within 5 minutes.
- [ ] All 15 tools demonstrably work against real Zoom data.
