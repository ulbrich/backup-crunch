package copier

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyFilePreservesBytesAndMtime(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	// Larger than the 1 MiB streaming buffer to exercise multi-chunk copy.
	payload := bytes.Repeat([]byte("backup-crunch"), 100000) // ~1.4 MiB
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1500000000, 0).UTC()

	dst := filepath.Join(dir, "out", "nested", "dst.bin")
	n, err := CopyFile(src, dst, mtime, false, "go")
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(payload)) {
		t.Errorf("bytes copied = %d, want %d", n, len(payload))
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("destination bytes differ from source")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Errorf("dst mtime = %v, want %v", info.ModTime(), mtime)
	}
}

func TestCopyFileDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	dst := filepath.Join(out, "dst.txt")
	n, err := CopyFile(src, dst, time.Now(), true, "go")
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("dry-run byte count = %d, want 5", n)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dry-run created dst (err=%v); should not exist", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("dry-run created out dir; should not exist")
	}
}

// MAJOR-3: the temp file lives in the destination's directory (not $TMPDIR) and
// no .bc-tmp-* residue is left after a successful copy.
func TestCopyFileNoTempResidue(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "out")
	dst := filepath.Join(destDir, "dst.txt")
	if _, err := CopyFile(src, dst, time.Unix(1, 0), false, "go"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".bc-tmp-") {
			t.Errorf("temp residue left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("dest dir has %d entries, want 1 (just dst.txt)", len(entries))
	}
}

// A copy that fails (unreadable source) must leave no temp file or partial dest.
func TestCopyFileErrorLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "out")
	dst := filepath.Join(destDir, "dst.txt")
	_, err := CopyFile(filepath.Join(dir, "does-not-exist"), dst, time.Unix(1, 0), false, "go")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("partial dst left behind after error")
	}
}

// Recovered files must keep the source's permission bits (not the 0o600 a temp
// file defaults to).
func TestCopyFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "dst.sh")
	if _, err := CopyFile(src, dst, time.Unix(1, 0), false, "go"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("dst mode = %o, want 0755 (source mode preserved)", got)
	}
}

// The cp backend copies bytes and preserves mtime, going through the same
// same-directory temp + rename path.
func TestCopyFileCpBackend(t *testing.T) {
	if _, err := exec.LookPath("cp"); err != nil {
		t.Skip("cp not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("via cp backend"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "dst.txt")
	mtime := time.Unix(1500000000, 0).UTC()
	if _, err := CopyFile(src, dst, mtime, false, "cp"); err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, dst); got != "via cp backend" {
		t.Errorf("cp backend content = %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Errorf("cp backend mtime = %v, want %v", info.ModTime(), mtime)
	}
	// no temp residue
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".bc-tmp-") {
			t.Errorf("cp backend left temp residue: %s", e.Name())
		}
	}
}

func mustReadFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
