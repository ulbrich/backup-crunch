// Package cli parses arguments into a validated Config for the "merge"
// subcommand and enforces the source-safety invariants.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxWorkers is the upper bound on --workers. The pool opens a file (and, with
// --hash, hashes) per concurrent worker, so an unbounded value would exhaust
// file descriptors on a large tree. 100 is well above the point of diminishing
// returns for I/O-bound copying while staying clear of typical ulimit -n.
const MaxWorkers = 100

// Config is a fully validated merge invocation.
type Config struct {
	Out                string
	Sources            []string // absolute paths
	DryRun             bool
	ManifestPath       string
	Hash               bool
	TSClusterThreshold int
	CopyTool           string
	Workers            int
	Verbose            bool
	Excludes           []string // path.Match globs; matched files/dirs are skipped
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

const usageText = `backup-crunch — merge scattered backup trees into one best-version tree

Usage:
  backup-crunch merge --out <dir> [flags] <src1> <src2> [<src3> ...]

Flags:
  --out <dir>                  output tree root (required; must not resolve inside any source)
  --dry-run                    scan + select + manifest only; write zero files to --out
  --manifest <file>            manifest JSON path (default <out>/manifest.json)
  --hash                       enable SHA-256 divergence detection on tied candidates
  --ts-cluster-threshold <n>   min identical-mtime files per source to flag a cluster (default 50)
  --copy-tool {go|cp|rsync}    copy backend (default go)
  --workers <n>                parallel copy/hash workers (default 1, max 100)
  --exclude <glob>             skip files/dirs matching this glob (repeatable;
                               matched against the relative path and base name)
  -v, --verbose                verbose logging (per-file skip/unreadable detail)

Sources are read-only. For each relative path (matched case- and Unicode-
insensitively) the newest non-empty copy wins; ties break to the largest file.
Positional source arguments must follow the flags.
`

// Usage writes the help text to w.
func Usage(w io.Writer) { fmt.Fprint(w, usageText) }

// Parse parses args (excluding the program name) into a validated Config.
func Parse(args []string) (*Config, error) {
	if len(args) < 1 || args[0] != "merge" {
		return nil, errors.New("expected subcommand \"merge\"")
	}

	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we render our own errors/usage
	var c Config
	fs.StringVar(&c.Out, "out", "", "")
	fs.BoolVar(&c.DryRun, "dry-run", false, "")
	fs.StringVar(&c.ManifestPath, "manifest", "", "")
	fs.BoolVar(&c.Hash, "hash", false, "")
	fs.IntVar(&c.TSClusterThreshold, "ts-cluster-threshold", 50, "")
	fs.StringVar(&c.CopyTool, "copy-tool", "go", "")
	fs.IntVar(&c.Workers, "workers", 1, "")
	fs.BoolVar(&c.Verbose, "v", false, "")
	fs.BoolVar(&c.Verbose, "verbose", false, "")
	var excludes stringList
	fs.Var(&excludes, "exclude", "")

	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	c.Excludes = excludes
	c.Sources = fs.Args()

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.Out == "" {
		return errors.New("--out is required")
	}
	if len(c.Sources) == 0 {
		return errors.New("at least one source directory is required")
	}
	switch c.CopyTool {
	case "go", "cp", "rsync":
	default:
		return fmt.Errorf("--copy-tool must be go|cp|rsync, got %q", c.CopyTool)
	}
	if c.Workers < 1 {
		return errors.New("--workers must be >= 1")
	}
	if c.Workers > MaxWorkers {
		return fmt.Errorf("--workers must be <= %d", MaxWorkers)
	}
	if c.TSClusterThreshold < 1 {
		return errors.New("--ts-cluster-threshold must be >= 1")
	}

	for i, s := range c.Sources {
		info, err := os.Stat(s)
		if err != nil {
			return fmt.Errorf("source %q: %w", s, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("source %q is not a directory", s)
		}
		abs, err := filepath.Abs(s)
		if err != nil {
			return err
		}
		c.Sources[i] = abs
	}

	outAbs, err := filepath.Abs(c.Out)
	if err != nil {
		return err
	}
	c.Out = outAbs

	if c.ManifestPath == "" {
		c.ManifestPath = filepath.Join(c.Out, "manifest.json")
	} else if abs, err := filepath.Abs(c.ManifestPath); err == nil {
		c.ManifestPath = abs
	}

	// MAJOR-4: resolve symlinks before the containment check so a symlinked
	// --out cannot alias into a source and risk mutating it.
	if err := pathNotInsideAnySource(c.Out, c.Sources, "--out"); err != nil {
		return err
	}
	if err := pathNotInsideAnySource(c.ManifestPath, c.Sources, "--manifest"); err != nil {
		return err
	}
	return nil
}

// pathNotInsideAnySource fails if p (with symlinks resolved) resolves to a
// location inside any source root (also symlink-resolved). label names the flag
// for the error message.
func pathNotInsideAnySource(p string, sources []string, label string) error {
	realP, err := resolveRealPath(p)
	if err != nil {
		return err
	}
	for _, s := range sources {
		realSrc, err := filepath.EvalSymlinks(s)
		if err != nil {
			realSrc = s
		}
		if isInside(realP, realSrc) {
			return fmt.Errorf("%s %q resolves inside source %q (would risk modifying a source)", label, p, s)
		}
	}
	return nil
}

// resolveRealPath resolves symlinks for p. If p does not exist yet, it resolves
// the nearest existing ancestor and re-appends the remaining (not-yet-created)
// components.
func resolveRealPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	remainder := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remainder), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil // reached root without resolving; fall back to abs
		}
		remainder = filepath.Join(filepath.Base(cur), remainder)
		cur = parent
	}
}

// isInside reports whether child is parent or lies within parent.
func isInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
