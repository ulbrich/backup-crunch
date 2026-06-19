// Command backup-crunch merges several read-only backup trees into a single
// best-version output tree, recording every decision in a JSON manifest.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/logging"
	"github.com/janulbrich/backup-crunch/internal/manifest"
	"github.com/janulbrich/backup-crunch/internal/merge"
)

// version is the build version stamped into the manifest. Override at build
// time with: -ldflags "-X main.version=v1.2.3".
var version = "0.1.0"

func main() {
	c, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr)
		cli.Usage(os.Stderr)
		os.Exit(2)
	}

	// Cancel the run cleanly on Ctrl-C / SIGTERM so deferred temp-file cleanup
	// runs instead of leaving partial artifacts in --out.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Per-entry detail is gated behind --verbose (Debug level); genuine
	// warnings (e.g. hash failures) are logged at Warn and always shown.
	logger := logging.New(os.Stderr, c.Verbose)

	m, err := merge.Run(ctx, c, merge.WithLogger(logger), merge.WithVersion(version))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := manifest.PrintSummary(os.Stdout, m); err != nil {
		fmt.Fprintln(os.Stderr, "error: writing summary:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Manifest: %s\n", c.ManifestPath)
}
