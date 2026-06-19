package merge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/janulbrich/backup-crunch/internal/model"
	"github.com/janulbrich/backup-crunch/internal/scan"
)

// eachAncestor calls fn for dir and each of its parent directories, stopping
// before the tree root ("."). Paths are slash-separated.
func eachAncestor(dir string, fn func(string)) {
	for d := dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		fn(d)
	}
}

// planEmptyDirs decides which scanned empty directories to recreate in the
// output. A directory that, in the merged tree, actually receives a file (it is
// an ancestor of a recovered path) is no longer empty and is dropped. The rest
// are de-duplicated across sources by fold key — keeping the newest mtime — and
// returned sorted by relative path for deterministic output.
func planEmptyDirs(records []model.DecisionRecord, scanned []scan.EmptyDir) []scan.EmptyDir {
	occupied := map[string]bool{}
	for i := range records {
		if records[i].Winner == nil {
			continue // no file is written for this path, so it occupies nothing
		}
		eachAncestor(path.Dir(records[i].RelPath), func(d string) {
			occupied[scan.FoldKey(d)] = true
		})
	}

	best := map[string]scan.EmptyDir{}
	for _, e := range scanned {
		fk := scan.FoldKey(e.RelPath)
		if occupied[fk] {
			continue // a file lives here in the merged tree — not empty
		}
		cur, ok := best[fk]
		if !ok || e.ModTime.After(cur.ModTime) ||
			(e.ModTime.Equal(cur.ModTime) && e.RelPath < cur.RelPath) {
			best[fk] = e
		}
	}

	out := make([]scan.EmptyDir, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}

// emptyDirRelPaths extracts the (already sorted) relative paths for the manifest.
func emptyDirRelPaths(dirs []scan.EmptyDir) []string {
	if len(dirs) == 0 {
		return nil
	}
	out := make([]string, len(dirs))
	for i, e := range dirs {
		out[i] = e.RelPath
	}
	return out
}

// createEmptyDirs recreates each planned empty directory under outRoot. It
// aborts if ctx is cancelled.
func createEmptyDirs(ctx context.Context, outRoot string, dirs []scan.EmptyDir) error {
	for _, e := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(outRoot, filepath.FromSlash(e.RelPath))
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("create empty dir %q: %w", e.RelPath, err)
		}
	}
	return nil
}

// dirTimes computes, for every output directory, the modification time to
// restore: the newest timestamp among the files (and preserved empty
// subdirectories) anywhere beneath it. Each contributing timestamp is propagated
// up to all ancestor directories so a parent reflects its newest content. The
// tree root (".") is never included.
func dirTimes(records []model.DecisionRecord, emptyDirs []scan.EmptyDir) map[string]time.Time {
	times := map[string]time.Time{}
	bump := func(start string, t time.Time) {
		if t.IsZero() {
			return
		}
		eachAncestor(start, func(d string) {
			if cur, ok := times[d]; !ok || t.After(cur) {
				times[d] = t
			}
		})
	}
	for i := range records {
		r := &records[i]
		if r.Winner == nil {
			continue
		}
		// A file contributes its mtime to its parent directory and upward.
		bump(path.Dir(r.RelPath), r.Winner.ModTime)
	}
	for _, e := range emptyDirs {
		// An empty directory contributes its own source mtime to itself and upward.
		bump(e.RelPath, e.ModTime)
	}
	return times
}

// restoreDirTimes sets each directory's modification time to the value computed
// by dirTimes. It must run after all files are copied and all directories are
// created, since those operations bump directory mtimes to "now". Failures are
// best-effort: a directory whose time cannot be set (e.g. it does not exist
// because its only file's copy failed) is logged and skipped, never fatal.
func restoreDirTimes(outRoot string, times map[string]time.Time, logger *slog.Logger) {
	dirs := make([]string, 0, len(times))
	for d := range times {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		t := times[d]
		p := filepath.Join(outRoot, filepath.FromSlash(d))
		if err := os.Chtimes(p, t, t); err != nil {
			logger.Warn("restore dir mtime failed", "path", d, "err", err)
		}
	}
}
