package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// TestFoldKeyNFCvsNFD is the direct regression test for MAJOR-1: a name stored
// in NFC (Windows/OneDrive) must produce the same fold key as the same name in
// NFD (macOS), so identical files group together.
func TestFoldKeyNFCvsNFD(t *testing.T) {
	nfc := norm.NFC.String("café.txt")
	nfd := norm.NFD.String("café.txt")
	if nfc == nfd {
		t.Skip("NFC and NFD encodings are identical on this input; nothing to test")
	}
	if FoldKey(nfc) != FoldKey(nfd) {
		t.Errorf("FoldKey(NFC)=%q != FoldKey(NFD)=%q", FoldKey(nfc), FoldKey(nfd))
	}
}

func TestFoldKeyCaseAndSlash(t *testing.T) {
	if FoldKey(filepath.Join("Docs", "A.TXT")) != "docs/a.txt" {
		t.Errorf("got %q, want docs/a.txt", FoldKey(filepath.Join("Docs", "A.TXT")))
	}
}

func TestScanSourceCounts(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "hello")
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), "world")
	mustWrite(t, filepath.Join(root, "empty.txt"), "") // zero-length

	stats := Stats{}
	files, err := ScanSource(context.Background(), 0, root, nil, &stats, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 3 {
		t.Errorf("FilesScanned = %d, want 3", stats.FilesScanned)
	}
	var empties int
	for _, f := range files {
		if f.IsEmpty {
			empties++
		}
		if f.SourceRoot != root {
			t.Errorf("SourceRoot = %q, want %q", f.SourceRoot, root)
		}
	}
	if empties != 1 {
		t.Errorf("empty files = %d, want 1", empties)
	}
}

func TestScanSourceSkipsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.txt"), "data")
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	stats := Stats{}
	files, err := ScanSource(context.Background(), 0, root, nil, &stats, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (symlink excluded)", stats.FilesScanned)
	}
	if stats.SkippedNonRegular != 1 {
		t.Errorf("SkippedNonRegular = %d, want 1", stats.SkippedNonRegular)
	}
	if len(files) != 1 || files[0].RelPath != "real.txt" {
		t.Errorf("files = %+v, want only real.txt", files)
	}
}

func TestScanSourceExclude(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "keep.txt"), "k")
	mustWrite(t, filepath.Join(root, "notes.tmp"), "t")
	mustWrite(t, filepath.Join(root, "$RECYCLE.BIN", "S-1-5", "$IABCDEF"), "junk")
	mustWrite(t, filepath.Join(root, "$RECYCLE.BIN", "S-1-5", "$RABCDEF"), "deleted-data")

	stats := Stats{}
	files, err := ScanSource(context.Background(), 0, root, []string{"*.tmp", "$RECYCLE.BIN"}, &stats, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].RelPath != "keep.txt" {
		t.Errorf("expected only keep.txt, got %+v", files)
	}
	// notes.tmp (1 file) + $RECYCLE.BIN (1 dir, subtree pruned) = 2 exclusions.
	if stats.Excluded != 2 {
		t.Errorf("Excluded = %d, want 2", stats.Excluded)
	}
}

func TestScanSourceUnreadableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permission bits don't restrict access")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ok.txt"), "fine")
	locked := filepath.Join(root, "locked")
	mustWrite(t, filepath.Join(locked, "secret.txt"), "x")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) }) // so TempDir cleanup works

	stats := Stats{}
	files, err := ScanSource(context.Background(), 0, root, nil, &stats, nil)
	if err != nil {
		t.Fatal(err)
	}
	// "ok.txt" readable; "locked" subtree could not be entered.
	if stats.UnreadableDirs < 1 {
		t.Skipf("dir remained readable on this system (UnreadableDirs=%d)", stats.UnreadableDirs)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.RelPath)
	}
	if !contains(names, "ok.txt") {
		t.Errorf("expected ok.txt among %v", names)
	}
	if len(stats.UnreadableDirList) == 0 {
		t.Errorf("UnreadableDirList should record the locked subtree")
	}
}

func TestScanSourceContextCancelled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "hello")
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), "world")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stats := Stats{}
	_, err := ScanSource(ctx, 0, root, nil, &stats, nil)
	if err == nil {
		t.Fatal("expected scan to abort on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestScanSourceEmptyDirs verifies that directories holding no entries are
// reported (so the merge can recreate them), while directories that contain a
// file, a subdirectory, or that are excluded are not.
func TestScanSourceEmptyDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "files", "a.txt"), "x") // "files" is not empty
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "branch", "leaf"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An excluded empty directory must not be reported.
	if err := os.MkdirAll(filepath.Join(root, "skip"), 0o755); err != nil {
		t.Fatal(err)
	}

	stats := Stats{}
	if _, err := ScanSource(context.Background(), 0, root, []string{"skip"}, &stats, nil); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, e := range stats.EmptyDirs {
		got = append(got, e.RelPath)
	}
	for _, want := range []string{"empty", "branch/leaf"} {
		if !contains(got, want) {
			t.Errorf("EmptyDirs %v missing %q", got, want)
		}
	}
	for _, notWant := range []string{".", "files", "branch", "skip"} {
		if contains(got, notWant) {
			t.Errorf("EmptyDirs %v should not contain %q", got, notWant)
		}
	}
	// The reported empty dirs must carry a non-zero source mtime to restore.
	for _, e := range stats.EmptyDirs {
		if e.ModTime.IsZero() {
			t.Errorf("empty dir %q has zero mtime", e.RelPath)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
