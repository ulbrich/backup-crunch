package merge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
)

// A cancelled context must abort the run during the scan phase: Run returns a
// context error and writes nothing under --out.
func TestRunContextCancelled(t *testing.T) {
	srcs, _ := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	args := append([]string{"merge", "--out", out, "--ts-cluster-threshold", "4"}, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the run starts

	_, err = Run(ctx, c)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	// Nothing should have been copied (manifest write is skipped on this error).
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("cancelled run created %d entries under --out, want 0", len(entries))
	}
}

// WithClock injects a deterministic timestamp into the manifest, proving the
// clock is a dependency rather than a mutable package global.
func TestRunWithClock(t *testing.T) {
	srcs, _ := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	args := append([]string{"merge", "--out", out, "--ts-cluster-threshold", "4"}, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}

	fixed := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	m, err := Run(context.Background(), c, WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatal(err)
	}
	if !m.GeneratedAt.Equal(fixed) {
		t.Errorf("GeneratedAt = %v, want injected %v", m.GeneratedAt, fixed)
	}
}

// WithVersion overrides the version stamped into the manifest.
func TestRunWithVersion(t *testing.T) {
	srcs, _ := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	args := append([]string{"merge", "--out", out, "--ts-cluster-threshold", "4"}, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}

	m, err := Run(context.Background(), c, WithVersion("v9.9.9-test"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v9.9.9-test" {
		t.Errorf("Version = %q, want v9.9.9-test", m.Version)
	}
}

// A copy failure must propagate out of the (errgroup-based) copy phase rather
// than being swallowed. Placing a regular file where a winner needs a parent
// directory makes the destination MkdirAll fail deterministically.
func TestRunSurfacesCopyError(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "A")
	write(t, a, "sub/x.txt", "data", time.Unix(1600000000, 0).UTC())

	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	// "sub" is a file, so creating the directory out/sub for the winner fails.
	if err := os.WriteFile(filepath.Join(out, "sub"), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"merge", "--out", out, a}
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), c); err == nil {
		t.Fatal("expected copy error to propagate, got nil")
	}
}
