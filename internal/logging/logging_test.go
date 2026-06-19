package logging

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// In non-verbose mode, Debug detail is suppressed but Warn is always shown.
func TestNewNonVerboseFiltersDebug(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	l.Debug("per-entry detail", "path", "a.txt")
	l.Warn("hash failed", "path", "b.txt")

	out := buf.String()
	if strings.Contains(out, "per-entry detail") {
		t.Errorf("debug output should be suppressed in non-verbose mode:\n%s", out)
	}
	if !strings.Contains(out, "hash failed") {
		t.Errorf("warn output should always be shown:\n%s", out)
	}
	// CLI prefix and structured attrs are rendered.
	if !strings.Contains(out, "backup-crunch: hash failed") || !strings.Contains(out, "path=b.txt") {
		t.Errorf("unexpected warn formatting:\n%s", out)
	}
}

// In verbose mode, Debug detail is included.
func TestNewVerboseIncludesDebug(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, true)
	l.Debug("per-entry detail", "path", "a.txt")
	if !strings.Contains(buf.String(), "per-entry detail") {
		t.Errorf("debug output should appear in verbose mode:\n%s", buf.String())
	}
}

// Concurrent logging through a shared logger (and a WithAttrs-derived child
// that shares the same lock) must be race-free and must not interleave or drop
// lines. Run under -race to exercise the slog.Handler concurrency contract.
func TestConcurrentLoggingIsRaceFree(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, true) // verbose so Info passes the level filter
	child := base.With("worker", "child")

	const goroutines = 16
	const perGoroutine = 64

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		l := base
		if g%2 == 0 {
			l = child // half use the derived handler, sharing the same mutex
		}
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				l.Info("copy", "n", i)
			}
		}()
	}
	wg.Wait()

	lines := strings.Count(buf.String(), "\n")
	if want := goroutines * perGoroutine; lines != want {
		t.Errorf("logged lines = %d, want %d (lines lost or interleaved)", lines, want)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "backup-crunch: copy") {
			t.Errorf("corrupted line (write interleaving?): %q", line)
			break
		}
	}
}

// Discard drops everything, including warnings.
func TestDiscard(t *testing.T) {
	l := Discard()
	// Must not panic and must produce no observable output; we only assert it is
	// safe to call at every level.
	l.Debug("x")
	l.Info("y")
	l.Warn("z")
	l.Error("e")
}
