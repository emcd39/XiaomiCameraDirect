// Command camera-probe collects a sanitized, read-only gateway preflight.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/diagnostics"
)

func main() {
	var gateway string
	var includeInventory bool
	var cameraID string
	var dryRun bool
	flag.StringVar(&gateway, "gateway", "", "local gateway URL using loopback or a private IP")
	flag.BoolVar(&includeInventory, "inventory", false, "read camera inventory; an installed provider may refresh Xiaomi cloud inventory")
	flag.StringVar(&cameraID, "camera-id", "", "filter inventory to this opaque camera ID (requires -inventory)")
	flag.BoolVar(&dryRun, "dry-run", false, "validate options and print planned GETs without making network requests")
	flag.Parse()

	report, err := diagnostics.NewRunner().Collect(context.Background(), diagnostics.Options{
		GatewayURL:       gateway,
		Token:            os.Getenv("XLG_API_TOKEN"),
		IncludeInventory: includeInventory,
		CameraID:         cameraID,
		DryRun:           dryRun,
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
