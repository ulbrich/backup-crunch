// Package manifest assembles, writes, and summarizes the JSON decision manifest
// — the auditable product of a merge run.
package manifest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/janulbrich/backup-crunch/internal/model"
)

// Write marshals m as indented JSON to path.
func Write(m model.Manifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// PrintSummary writes a concise human-readable summary of the run to w and
// returns the first write error encountered (if any).
func PrintSummary(w io.Writer, m model.Manifest) error {
	s := m.Summary
	mode := "COPY"
	if s.DryRun {
		mode = "DRY-RUN (no files written)"
	}
	// ew accumulates the first write error; once set, subsequent Fprintf calls
	// are effectively no-ops as far as the caller is concerned.
	var ew error
	p := func(format string, args ...any) {
		if ew != nil {
			return
		}
		_, ew = fmt.Fprintf(w, format, args...)
	}

	p("backup-crunch %s — %s\n", m.Version, mode)
	p("  sources scanned : %d\n", s.SourcesScanned)
	p("  files scanned   : %d\n", s.FilesScanned)
	p("  distinct paths  : %d\n", s.PathsSeen)
	p("  recovered       : %d (of which flagged: %d)\n", s.Recovered, s.Flagged)
	p("  unrecoverable   : %d (only zero-length copies existed)\n", s.UnrecoverableEmpty)
	p("  skipped (non-regular): %d\n", s.SkippedNonRegular)
	if s.Excluded > 0 {
		p("  excluded        : %d (matched --exclude)\n", s.Excluded)
	}
	if s.Unreadable > 0 || s.UnreadableDirs > 0 {
		p("  unreadable      : %d files, %d dirs (named but not readable — see manifest)\n", s.Unreadable, s.UnreadableDirs)
	}
	p("  bytes copied    : %d\n", s.BytesCopied)
	if s.EmptyDirs > 0 {
		verb := "recreated"
		if s.DryRun {
			verb = "would recreate"
		}
		p("  empty dirs      : %d (%s)\n", s.EmptyDirs, verb)
	}

	if len(m.Clusters) > 0 {
		p("\nSuspicious timestamp clusters (%d) — mtimes may have been clobbered:\n", len(m.Clusters))
		for _, c := range m.Clusters {
			p("  source[%d] %s: %d files share mtime %s\n",
				c.SourceIndex, c.SourceRoot, c.FileCount, c.ModTime.Format("2006-01-02T15:04:05Z"))
		}
	}

	var unrec []string
	for _, r := range m.Records {
		if r.Status == model.StatusUnrecoverable {
			unrec = append(unrec, r.RelPath)
		}
	}
	if len(unrec) > 0 {
		p("\nUnrecoverable paths (%d):\n", len(unrec))
		for _, path := range unrec {
			p("  %s\n", path)
		}
	}
	p("\nOutput tree: %s\n", m.OutDir)
	return ew
}
