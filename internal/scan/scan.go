// Package scan walks source backup roots and emits file candidates. Sources are
// read strictly read-only: nothing here ever opens a source for writing.
package scan

import (
	"context"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/janulbrich/backup-crunch/internal/logging"
	"github.com/janulbrich/backup-crunch/internal/model"
)

// Stats accumulates per-run scan counters.
type Stats struct {
	FilesScanned      int
	SkippedNonRegular int
	Excluded          int      // entries matching an exclude pattern
	Unreadable        int      // regular files named but not stat'able/openable
	UnreadableDirs    int      // directories that could not be entered
	UnreadableDirList []string // the subtree roots we could not enter
}

// FoldKey normalizes a relative path into a case- and Unicode-normalized
// grouping key. NFC normalization runs BEFORE case-folding so that a file
// stored in NFC form on Windows/OneDrive groups with the same name decomposed
// to NFD on macOS (HFS+/APFS). Without this, identical files silently fail to
// merge — a correctness bug in the recovery core.
func FoldKey(rel string) string {
	return strings.ToLower(norm.NFC.String(filepath.ToSlash(rel)))
}

// isExcluded reports whether a slash relative path matches any exclude pattern,
// testing both the full path and its base name (so "$RECYCLE.BIN" matches a
// directory anywhere and "*.tmp" matches files anywhere). Patterns use
// path.Match glob syntax and are case-sensitive.
func isExcluded(relSlash string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	base := path.Base(relSlash)
	for _, p := range patterns {
		if ok, _ := path.Match(p, relSlash); ok {
			return true
		}
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}

// ScanSource walks one source root, appending a File for every regular file.
//
// Symlinks and other non-regular entries are skipped and counted. Entries
// matching an exclude pattern are skipped (directories prune their whole
// subtree). A regular file that can be named but not stat'd/opened (e.g. a
// dataless cloud placeholder) is recorded as an Unreadable candidate so a path
// whose only copies are unreadable still surfaces in the manifest rather than
// vanishing. A directory that cannot be entered is counted and its path
// recorded, but its contents cannot be enumerated.
//
// Per-entry diagnostics are emitted at Debug level (shown only when the logger
// is verbose); counts are always kept. logger may be nil. The walk is aborted
// if ctx is cancelled.
func ScanSource(ctx context.Context, index int, root string, excludes []string, stats *Stats, logger *slog.Logger) ([]model.File, error) {
	if logger == nil {
		logger = logging.Discard()
	}
	var files []model.File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// The entry could not be accessed.
			if d != nil && !d.IsDir() {
				files = append(files, unreadableFile(index, root, p))
				stats.Unreadable++
				logger.Debug("scan: unreadable file", "path", p, "err", walkErr)
				return nil
			}
			// A directory (or unknown) we cannot enter — its contents are lost.
			stats.UnreadableDirs++
			if rel, rerr := filepath.Rel(root, p); rerr == nil {
				stats.UnreadableDirList = append(stats.UnreadableDirList, filepath.ToSlash(rel))
			}
			logger.Debug("scan: unreadable dir", "path", p, "err", walkErr)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			// Cannot compute a relative path — record a phantom candidate (using
			// the absolute path as a fallback) so the entry still surfaces in the
			// manifest rather than vanishing silently.
			files = append(files, unreadableFile(index, root, p))
			stats.Unreadable++
			logger.Debug("scan: unreadable file", "path", p, "err", rerr)
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() {
			if relSlash != "." && isExcluded(relSlash, excludes) {
				stats.Excluded++
				logger.Debug("scan: excluded dir", "path", relSlash)
				return fs.SkipDir
			}
			return nil
		}

		if isExcluded(relSlash, excludes) {
			stats.Excluded++
			logger.Debug("scan: excluded", "path", relSlash)
			return nil
		}

		// WalkDir does not follow symlinks, so a symlink appears here as a
		// non-regular entry and is skipped — this also prevents escaping the
		// source root via a symlink.
		if !d.Type().IsRegular() {
			stats.SkippedNonRegular++
			logger.Debug("scan: skip non-regular", "path", relSlash)
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			// Named but not stat'able — record as an unreadable candidate.
			files = append(files, unreadableFile(index, root, p))
			stats.Unreadable++
			logger.Debug("scan: unreadable file", "path", p, "err", ierr)
			return nil
		}

		size := info.Size()
		files = append(files, model.File{
			SourceIndex: index,
			SourceRoot:  root,
			RelPath:     relSlash,
			FoldKey:     FoldKey(rel),
			Size:        size,
			ModTime:     info.ModTime().UTC(),
			IsEmpty:     size == 0,
		})
		stats.FilesScanned++
		return nil
	})
	return files, err
}

// unreadableFile builds a phantom candidate for a file that exists by name but
// could not be read. It is empty (never a winner) and flagged Unreadable so the
// manifest records it.
func unreadableFile(index int, root, p string) model.File {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		rel = p
	}
	relSlash := filepath.ToSlash(rel)
	return model.File{
		SourceIndex: index,
		SourceRoot:  root,
		RelPath:     relSlash,
		FoldKey:     FoldKey(rel),
		IsEmpty:     true,
		Unreadable:  true,
	}
}
