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

// PrintSummary writes a concise human-readable summary of the run to w.
func PrintSummary(w io.Writer, m model.Manifest) {
	s := m.Summary
	mode := "COPY"
	if s.DryRun {
		mode = "DRY-RUN (no files written)"
	}
	fmt.Fprintf(w, "backup-crunch %s — %s\n", m.Version, mode)
	fmt.Fprintf(w, "  sources scanned : %d\n", s.SourcesScanned)
	fmt.Fprintf(w, "  files scanned   : %d\n", s.FilesScanned)
	fmt.Fprintf(w, "  distinct paths  : %d\n", s.PathsSeen)
	fmt.Fprintf(w, "  recovered       : %d (of which flagged: %d)\n", s.Recovered, s.Flagged)
	fmt.Fprintf(w, "  unrecoverable   : %d (only zero-length copies existed)\n", s.UnrecoverableEmpty)
	fmt.Fprintf(w, "  skipped (non-regular): %d\n", s.SkippedNonRegular)
	if s.Excluded > 0 {
		fmt.Fprintf(w, "  excluded        : %d (matched --exclude)\n", s.Excluded)
	}
	if s.Unreadable > 0 || s.UnreadableDirs > 0 {
		fmt.Fprintf(w, "  unreadable      : %d files, %d dirs (named but not readable — see manifest)\n", s.Unreadable, s.UnreadableDirs)
	}
	fmt.Fprintf(w, "  bytes copied    : %d\n", s.BytesCopied)

	if len(m.Clusters) > 0 {
		fmt.Fprintf(w, "\nSuspicious timestamp clusters (%d) — mtimes may have been clobbered:\n", len(m.Clusters))
		for _, c := range m.Clusters {
			fmt.Fprintf(w, "  source[%d] %s: %d files share mtime %s\n",
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
		fmt.Fprintf(w, "\nUnrecoverable paths (%d):\n", len(unrec))
		for _, p := range unrec {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}
	fmt.Fprintf(w, "\nOutput tree: %s\n", m.OutDir)
}
