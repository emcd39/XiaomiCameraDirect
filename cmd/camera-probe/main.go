// Command camera-probe collects a sanitized, read-only gateway preflight.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/diagnostics"
)

func main() {
	var gateway string
	var includeInventory bool
	var cameraID string
	var sessionDiagnostics bool
	var sessionOperation string
	var sessionGeneration string
	var dryRun bool
	flag.StringVar(&gateway, "gateway", "", "local gateway URL using loopback or a private IP")
	flag.BoolVar(&includeInventory, "inventory", false, "read camera inventory; an installed provider may refresh Xiaomi cloud inventory")
	flag.StringVar(&cameraID, "camera-id", "", "filter inventory to this opaque camera ID (requires -inventory)")
	flag.BoolVar(&sessionDiagnostics, "session", false, "read status or transport metadata from this camera's existing session (requires -inventory and -camera-id; gateway route must be enabled)")
	flag.StringVar(&sessionOperation, "session-operation", "session_status", "named read-only operation: session_status or transport_metadata")
	flag.StringVar(&sessionGeneration, "session-generation", "", "generation returned by session_status (required for transport_metadata)")
	flag.BoolVar(&dryRun, "dry-run", false, "validate options and print planned GETs without making network requests")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var operation backend.CameraDiagnosticOperation
	if sessionDiagnostics {
		operation = backend.CameraDiagnosticOperation(sessionOperation)
	}
	report, err := diagnostics.NewRunner().Collect(ctx, diagnostics.Options{
		GatewayURL:                gateway,
		Token:                     os.Getenv("XLG_API_TOKEN"),
		IncludeInventory:          includeInventory,
		CameraID:                  cameraID,
		IncludeSessionDiagnostics: sessionDiagnostics,
		SessionOperation:          operation,
		SessionGeneration:         sessionGeneration,
		DryRun:                    dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "camera-probe:", err)
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "camera-probe: could not write report")
		os.Exit(2)
	}
}
