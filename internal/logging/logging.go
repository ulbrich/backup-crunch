// Package logging provides the slog.Logger configuration used across
// backup-crunch. CLI output stays terse ("backup-crunch: <message> key=val");
// per-entry detail is emitted at Debug level and shown only in verbose mode,
// while genuine warnings are emitted at Warn level and always shown.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New returns a logger that writes CLI-style lines to w. When verbose is true
// the threshold drops to Debug so per-entry scan/skip detail is included;
// otherwise only Info and above (notably Warn) are shown.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(&cliHandler{w: w, level: level})
}

// Discard returns a logger that drops everything. It lets callers treat the
// logger as always non-nil instead of guarding every call site.
func Discard() *slog.Logger {
	return slog.New(&cliHandler{w: io.Discard, level: slog.LevelError + 1})
}

// cliHandler renders records as a single "backup-crunch: <msg> k=v" line,
// preserving the tool's original terse output instead of slog's default
// time/level-prefixed format.
type cliHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
}

func (h *cliHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *cliHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString("backup-crunch: ")
	sb.WriteString(r.Message)
	for _, a := range h.attrs {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value)
		return true
	})
	sb.WriteByte('\n')
	_, err := io.WriteString(h.w, sb.String())
	return err
}

func (h *cliHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &nh
}

func (h *cliHandler) WithGroup(string) slog.Handler { return h }
