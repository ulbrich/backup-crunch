// Command backup-crunch merges several read-only backup trees into a single
// best-version output tree, recording every decision in a JSON manifest.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/manifest"
	"github.com/janulbrich/backup-crunch/internal/merge"
)

func main() {
	c, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr)
		cli.Usage(os.Stderr)
		os.Exit(2)
	}

	log.SetFlags(0)
	log.SetPrefix("backup-crunch: ")
	// Genuine warnings (e.g. hash failures) always log; high-volume per-entry
	// scan detail is gated behind --verbose inside the scanner.
	logf := log.Printf

	m, err := merge.Run(c, logf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	manifest.PrintSummary(os.Stdout, m)
	fmt.Fprintf(os.Stdout, "Manifest: %s\n", c.ManifestPath)
}
