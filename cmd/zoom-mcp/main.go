// Command zoom-mcp is the MCP server for the Zoom API. It is a YAML-driven
// Workflow service: the embedded config/zoom-mcp.yaml wires the first-run
// setup web flow, the secrets keychain, and the MCP tool pipelines that
// expose Zoom endpoints as MCP tools.
//
// On first run (no Zoom credentials in the OS keychain), zoom-mcp opens the
// setup URL in the user's browser so they can register an OAuth app. On
// subsequent runs it reads credentials from the keychain and serves MCP
// over stdio (and, in parallel, over streamable HTTP on 127.0.0.1:7823).
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GoCodeAlone/modular"
	"github.com/GoCodeAlone/workflow"
	"github.com/GoCodeAlone/workflow/config"
	allplugins "github.com/GoCodeAlone/workflow/plugins/all"
	"github.com/GoCodeAlone/workflow/secrets"
	mcpplugin "github.com/GoCodeAlone/workflow-plugin-mcp/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pkg/browser"
)

//go:embed zoom-mcp.yaml
var embeddedConfig string

const (
	keychainService = "zoom-mcp"
	setupURL        = "http://127.0.0.1:8765/setup"
	mcpServerModule = "mcp-server"
)

// requiredSecrets are the keychain entries checked for first-run detection.
// client_id and client_secret are written by the setup form; oauth_token is
// written after the user completes the Zoom authorization callback.
var requiredSecrets = []string{"client_id", "client_secret", "oauth_token"}

func main() {
	noBrowser := flag.Bool("no-browser", false, "don't auto-open the setup URL on first run")
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 {
		if err := runSubcommand(args); err != nil {
			fmt.Fprintf(os.Stderr, "zoom-mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := runEngine(*noBrowser); err != nil {
		fmt.Fprintf(os.Stderr, "zoom-mcp: %v\n", err)
		os.Exit(1)
	}
}

func runSubcommand(args []string) error {
	switch args[0] {
	case "config":
		if len(args) < 2 {
			return errors.New("usage: zoom-mcp config {show|reset}")
		}
		switch args[1] {
		case "show":
			return configShow()
		case "reset":
			return configReset()
		default:
			return fmt.Errorf("unknown config subcommand: %s", args[1])
		}
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runEngine(noBrowser bool) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Logger must not write to stdout — stdout is the MCP stdio transport.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ready := keychainReady(ctx)
	if !ready {
		// The http.client module with oauth2_refresh_token auth eagerly resolves
		// client_id / client_secret from the keychain at Start. On first run
		// those values don't exist yet, so we seed placeholders to keep Start
		// from failing. The setup-save pipeline overwrites them with real
		// values once the user submits the form; the placeholders are never
		// used for a real token refresh because watchForSetupCompletion exits
		// the process as soon as oauth_token appears, and the next invocation
		// resolves the real values eagerly.
		if err := seedPlaceholderSecrets(ctx); err != nil {
			return fmt.Errorf("seed placeholder secrets: %w", err)
		}
	}

	cfg, err := config.LoadFromString(embeddedConfig)
	if err != nil {
		return fmt.Errorf("load embedded config: %w", err)
	}

	app := modular.NewStdApplication(nil, logger)
	engine := workflow.NewStdEngine(app, logger)

	if err := allplugins.LoadAll(engine); err != nil {
		return fmt.Errorf("load default plugins: %w", err)
	}
	if err := engine.LoadPlugin(mcpplugin.New()); err != nil {
		return fmt.Errorf("load workflow-plugin-mcp: %w", err)
	}

	if err := engine.BuildFromConfig(cfg); err != nil {
		return fmt.Errorf("build engine: %w", err)
	}

	if err := engine.Start(ctx); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	// Browser auto-open and setup-hint MCP notification happen AFTER Start
	// so the http.server module is actually listening on 127.0.0.1:8765 when
	// the user's browser hits it.
	if !ready {
		if !noBrowser {
			_ = browser.OpenURL(setupURL)
			fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; opened "+setupURL)
		} else {
			fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; visit "+setupURL)
		}
		go notifySetup(ctx, engine, setupURL)
		go watchForSetupCompletion(ctx, engine, cancel)
	}

	<-ctx.Done()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := engine.Stop(stopCtx); err != nil {
		return fmt.Errorf("stop engine: %w", err)
	}
	return nil
}

// seedPlaceholderSecrets writes placeholder values for any missing client_id /
// client_secret secrets. Only these two are seeded because they are resolved
// eagerly at http.client Start; oauth_token is resolved lazily on the first
// HTTP request and does not need a placeholder.
func seedPlaceholderSecrets(ctx context.Context) error {
	provider, err := secrets.NewKeychainProvider(keychainService)
	if err != nil {
		return fmt.Errorf("open keychain: %w", err)
	}
	for _, key := range []string{"client_id", "client_secret"} {
		if _, err := provider.Get(ctx, key); err == nil {
			continue
		}
		if err := provider.Set(ctx, key, "pending-setup"); err != nil {
			return fmt.Errorf("seed %s: %w", key, err)
		}
	}
	return nil
}

// watchForSetupCompletion polls the keychain for oauth_token. When the user
// finishes the setup flow, the oauth-callback pipeline writes oauth_token
// (and real client_id / client_secret over the placeholders); this goroutine
// notices, sends a best-effort MCP log asking the client to restart, then
// cancels the main context so the engine shuts down. The user's next
// invocation picks up the real credentials at Start.
func watchForSetupCompletion(ctx context.Context, engine *workflow.StdEngine, cancel context.CancelFunc) {
	const pollInterval = 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
		if !keychainReady(ctx) {
			continue
		}
		if server := resolveMCPServer(engine.App()); server != nil {
			msg := &mcpsdk.LoggingMessageParams{
				Level:  "info",
				Logger: "zoom-mcp",
				Data:   "Zoom setup complete. Restart your MCP client to load the Zoom tools.",
			}
			for ss := range server.Sessions() {
				_ = ss.Log(ctx, msg)
				break
			}
		}
		fmt.Fprintln(os.Stderr, "zoom-mcp: setup complete; exiting so the next run can load credentials")
		cancel()
		return
	}
}

// resolveMCPServer fetches the underlying *mcpsdk.Server from the DI container.
// Returns nil if the configured mcp.server module isn't registered under
// mcpServerModule or hasn't finished Init yet. The caller should retry if nil.
func resolveMCPServer(app modular.Application) *mcpsdk.Server {
	mod := app.GetModule(mcpServerModule)
	if mod == nil {
		return nil
	}
	sm, ok := mod.(*mcpplugin.ServerModule)
	if !ok {
		return nil
	}
	return sm.Server()
}

// notifySetup waits for an MCP ServerSession to appear, then sends a
// notifications/message log pointing the client at the setup URL. Best-effort.
func notifySetup(ctx context.Context, engine *workflow.StdEngine, url string) {
	server := resolveMCPServer(engine.App())
	for i := 0; i < 50 && server == nil; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		server = resolveMCPServer(engine.App())
	}
	if server == nil {
		return
	}

	msg := &mcpsdk.LoggingMessageParams{
		Level:  "info",
		Logger: "zoom-mcp",
		Data:   "Zoom credentials not configured. Complete setup at " + url,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		for ss := range server.Sessions() {
			_ = ss.Log(ctx, msg)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// keychainReady returns true when all required Zoom secrets are present in
// the OS keychain under the zoom-mcp service namespace. Any missing or
// errored lookup is treated as "not ready" so the setup flow can run.
func keychainReady(ctx context.Context) bool {
	provider, err := secrets.NewKeychainProvider(keychainService)
	if err != nil {
		return false
	}
	for _, key := range requiredSecrets {
		if _, err := provider.Get(ctx, key); err != nil {
			return false
		}
	}
	return true
}

func configShow() error {
	provider, err := secrets.NewKeychainProvider(keychainService)
	if err != nil {
		return fmt.Errorf("open keychain: %w", err)
	}
	ctx := context.Background()
	for _, key := range requiredSecrets {
		val, err := provider.Get(ctx, key)
		if err != nil {
			fmt.Printf("%-14s  (not set)\n", key)
			continue
		}
		fmt.Printf("%-14s  %s\n", key, redact(val))
	}
	return nil
}

func configReset() error {
	provider, err := secrets.NewKeychainProvider(keychainService)
	if err != nil {
		return fmt.Errorf("open keychain: %w", err)
	}
	ctx := context.Background()
	for _, key := range requiredSecrets {
		if err := provider.Delete(ctx, key); err != nil {
			fmt.Fprintf(os.Stderr, "warning: delete %s: %v\n", key, err)
		}
	}
	fmt.Println("zoom-mcp: credentials cleared from keychain")
	return nil
}

func redact(s string) string {
	if len(s) <= 8 {
		return "********"
	}
	return s[:4] + "…" + s[len(s)-4:]
}
