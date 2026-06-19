// Package copier performs streamed, atomic file copies that preserve the
// source's mode and modification time.
//
// The package is named "copier" rather than "copy" to avoid shadowing Go's
// builtin copy.
package copier

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/janulbrich/backup-crunch/internal/iobuf"
)

// CopyFile copies src to dst, preserving src's permission bits and modification
// time. It returns the number of bytes that were (or in dry-run, would be)
// copied.
//
// ctx cancels the copy: the pure-Go backend checks it between chunks (so a
// large file aborts promptly), and the cp/rsync backends run as child
// processes that are killed when ctx is done. A cancelled copy leaves no temp
// residue.
//
// tool selects the backend: "go" (default, pure-Go streamed copy), "cp", or
// "rsync". The cp/rsync backends are best-effort escape hatches; "go" is the
// primary, fully-tested path.
//
// In dryRun mode no filesystem writes occur; the source size is returned.
//
// Atomicity / MAJOR-3: regardless of backend, the data is written to a temp
// file in the SAME directory as the final destination (never $TMPDIR) and then
// renamed into place. os.Rename is only atomic within a single filesystem;
// backups and --out commonly live on external drives, so a temp file on a
// different filesystem would make rename fail with EXDEV or fall back to a
// non-atomic copy. On any error the temp file is removed, so no partial
// .bc-tmp-* artifacts are left in --out.
func CopyFile(ctx context.Context, src, dst string, mtime time.Time, dryRun bool, tool string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return 0, err
	}
	size := srcInfo.Size()
	if dryRun {
		return size, nil
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(dir, ".bc-tmp-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpName)
		}
	}()

	switch tool {
	case "", "go":
		if err := streamCopy(ctx, tmp, in); err != nil {
			tmp.Close()
			return 0, err
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return 0, err
		}
		if err := tmp.Close(); err != nil {
			return 0, err
		}
	case "cp", "rsync":
		// The external tool writes the temp file; release our handle first.
		tmp.Close()
		if err := runCopyTool(ctx, tool, src, tmpName); err != nil {
			return 0, err
		}
	default:
		tmp.Close()
		return 0, fmt.Errorf("unknown copy tool %q", tool)
	}

	// Preserve mode and mtime on the temp file before the atomic rename. These
	// target only the temp file inside --out, never the source.
	if err := os.Chmod(tmpName, srcInfo.Mode().Perm()); err != nil {
		return 0, err
	}
	if err := os.Chtimes(tmpName, mtime, mtime); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, fmt.Errorf("rename %s -> %s: %w", tmpName, dst, err)
	}
	success = true

	// Best-effort durability: fsync the destination directory so the rename
	// itself is persisted (matters on external drives / power loss). Failures
	// here are non-fatal — the data is already written and renamed.
	if dirf, derr := os.Open(dir); derr == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return size, nil
}

// streamCopy copies all of in into dst in fixed-size chunks, checking ctx
// between chunks so a large copy aborts promptly on cancellation.
func streamCopy(ctx context.Context, dst io.Writer, in io.Reader) error {
	buf := make([]byte, iobuf.Size)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		nr, rerr := in.Read(buf)
		if nr > 0 {
			if _, werr := dst.Write(buf[:nr]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// runCopyTool invokes cp or rsync to copy src onto the (already existing) temp
// file tmp. Arguments are passed directly to exec.CommandContext (no shell), so
// source paths cannot inject options or commands and the child is killed if ctx
// is cancelled. "--" ends option parsing.
func runCopyTool(ctx context.Context, tool, src, tmp string) error {
	var cmd *exec.Cmd
	switch tool {
	case "cp":
		cmd = exec.CommandContext(ctx, "cp", "-pf", "--", src, tmp) // -p preserves mode/times
	case "rsync":
		cmd = exec.CommandContext(ctx, "rsync", "-a", "--", src, tmp) // -a archive: perms+times
	default:
		return fmt.Errorf("unknown copy tool %q", tool)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s copy failed: %w: %s", tool, err, string(out))
	}
	return nil
}
