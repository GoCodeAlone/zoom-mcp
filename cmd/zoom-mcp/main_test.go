package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/GoCodeAlone/modular"
	mcpplugin "github.com/GoCodeAlone/workflow-plugin-mcp/mcp"
)

// Guards the notifications/message path against silent regressions if the
// modular GetModule API or the configured mcp.server module name drifts.
// If this test fails, the setup-hint notification will silently stop working.
func TestResolveMCPServer_ReturnsRegisteredServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := modular.NewStdApplication(nil, logger)

	mod := mcpplugin.NewServerModule(mcpServerModule, mcpplugin.ServerConfig{
		Implementation: mcpplugin.Implementation{Name: "test", Version: "0.0.0"},
	})
	if err := mod.Init(app); err != nil {
		t.Fatalf("ServerModule.Init: %v", err)
	}
	app.RegisterModule(mod)

	server := resolveMCPServer(app)
	if server == nil {
		t.Fatal("resolveMCPServer returned nil for a registered mcp.server module")
	}
}

func TestResolveMCPServer_ReturnsNilWhenMissing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := modular.NewStdApplication(nil, logger)

	if server := resolveMCPServer(app); server != nil {
		t.Fatalf("resolveMCPServer should return nil when module is not registered, got %v", server)
	}
}
