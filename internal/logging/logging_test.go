package logging

import (
	"bytes"
	"strings"
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
