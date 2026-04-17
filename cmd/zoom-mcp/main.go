// Command zoom-mcp is the MCP server for the Zoom API. It is a YAML-driven
// Workflow service: a single config file (config/zoom-mcp.yaml by default)
// wires the first-run setup web flow, the secrets keychain, and the MCP
// tool pipelines that expose Zoom endpoints as MCP tools.
//
// On first run (no Zoom credentials in the OS keychain), zoom-mcp opens the
// setup URL in the user's browser so they can register an OAuth app. On
// subsequent runs it reads credentials from the keychain and serves MCP
// over stdio (and, in parallel, over streamable HTTP on 127.0.0.1:7823).
package main

import (
	"context"
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
	var (
		configPath = flag.String("config", "config/zoom-mcp.yaml", "path to workflow config")
		noBrowser  = flag.Bool("no-browser", false, "don't auto-open the setup URL on first run")
	)
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 {
		if err := runSubcommand(args); err != nil {
			fmt.Fprintf(os.Stderr, "zoom-mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := runEngine(*configPath, *noBrowser); err != nil {
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

func runEngine(configPath string, noBrowser bool) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Logger must not write to stdout — stdout is the MCP stdio transport.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
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

	ready := keychainReady(ctx)
	if !ready {
		if !noBrowser {
			_ = browser.OpenURL(setupURL)
			fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; opened "+setupURL)
		} else {
			fmt.Fprintln(os.Stderr, "zoom-mcp: credentials not configured; visit "+setupURL)
		}
	}

	if err := engine.Start(ctx); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	// Best-effort: surface the setup URL to any connected MCP client via
	// notifications/message once a session exists. Runs in the background;
	// exits when the context is cancelled or a notification is delivered.
	if !ready {
		go notifySetup(ctx, engine, setupURL)
	}

	<-ctx.Done()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := engine.Stop(stopCtx); err != nil {
		return fmt.Errorf("stop engine: %w", err)
	}
	return nil
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
