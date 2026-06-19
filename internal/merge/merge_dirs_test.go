package merge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/model"
	"github.com/janulbrich/backup-crunch/internal/scan"
)

// mkEmptyDir creates an empty directory under root and stamps its mtime.
func mkEmptyDir(t *testing.T, root, rel string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// planEmptyDirs must drop directories that a file occupies in the merged tree
// (case-insensitively) and de-duplicate the rest by fold key, keeping the
// newest mtime.
func TestPlanEmptyDirs(t *testing.T) {
	older := time.Unix(1000, 0).UTC()
	newer := time.Unix(2000, 0).UTC()
	win := model.File{RelPath: "Docs/a.txt"}
	records := []model.DecisionRecord{
		{RelPath: "Docs/a.txt", Winner: &win},                  // occupies "docs"
		{RelPath: "gone.txt", Status: model.StatusUnrecoverable}, // no winner: occupies nothing
	}
	scanned := []scan.EmptyDir{
		{RelPath: "docs", ModTime: older},  // occupied by Docs/a.txt → dropped
		{RelPath: "empty", ModTime: older}, // kept, but...
		{RelPath: "empty", ModTime: newer}, // ...this newer duplicate wins
	}

	got := planEmptyDirs(records, scanned)
	want := []scan.EmptyDir{{RelPath: "empty", ModTime: newer}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planEmptyDirs = %v, want %v", got, want)
	}
}

// dirTimes must propagate the newest content timestamp up to every ancestor,
// combining file mtimes and preserved empty-directory mtimes.
func TestDirTimes(t *testing.T) {
	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(3000, 0).UTC() // newest file
	t3 := time.Unix(2000, 0).UTC() // empty dir's own time
	wc := model.File{RelPath: "a/b/c.txt", ModTime: t1}
	wd := model.File{RelPath: "a/d.txt", ModTime: t2}
	records := []model.DecisionRecord{
		{RelPath: "a/b/c.txt", Winner: &wc},
		{RelPath: "a/d.txt", Winner: &wd},
	}
	emptyDirs := []scan.EmptyDir{{RelPath: "a/empty", ModTime: t3}}

	got := dirTimes(records, emptyDirs)
	want := map[string]time.Time{
		"a":       t2, // max(t1, t2, t3)
		"a/b":     t1,
		"a/empty": t3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dirTimes = %v, want %v", got, want)
	}
}

// End-to-end: empty source directories are recreated in the output and every
// directory's mtime is restored to the newest content beneath it.
func TestRunRecreatesEmptyDirsAndRestoresMtimes(t *testing.T) {
	src := t.TempDir()
	fileMt := time.Unix(1609459200, 0).UTC() // 2021-01-01
	emptyMt := time.Unix(1643760000, 0).UTC() // 2022-02-02
	leafMt := time.Unix(1677801600, 0).UTC()  // 2023-03-03

	write(t, src, "keep/file.txt", "data", fileMt) // gives "keep" a file
	mkEmptyDir(t, src, "empty", emptyMt)           // a genuinely empty dir
	mkEmptyDir(t, src, "branch/leaf", leafMt)      // empty leaf; "branch" only holds it

	out := filepath.Join(t.TempDir(), "out")
	c, err := cli.Parse([]string{"merge", "--out", out, src})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	// Structure: empty dirs exist in the output.
	for _, rel := range []string{"empty", "branch", "branch/leaf"} {
		info, statErr := os.Stat(filepath.Join(out, filepath.FromSlash(rel)))
		if statErr != nil || !info.IsDir() {
			t.Errorf("output dir %q missing (err=%v)", rel, statErr)
		}
	}

	// The manifest records exactly the two empty leaves (sorted), not "branch"
	// (it holds a subdirectory) nor "keep" (it holds a file).
	wantEmpty := []string{"branch/leaf", "empty"}
	if !reflect.DeepEqual(m.EmptyDirs, wantEmpty) {
		t.Errorf("manifest EmptyDirs = %v, want %v", m.EmptyDirs, wantEmpty)
	}
	if m.Summary.EmptyDirs != 2 {
		t.Errorf("summary EmptyDirs = %d, want 2", m.Summary.EmptyDirs)
	}

	// Timestamps: each directory's mtime reflects its newest content.
	wantTimes := map[string]time.Time{
		"keep":        fileMt, // only the file
		"empty":       emptyMt, // its own preserved time
		"branch":      leafMt,  // inherits its empty leaf's time
		"branch/leaf": leafMt,  // its own preserved time
	}
	for rel, want := range wantTimes {
		info, statErr := os.Stat(filepath.Join(out, filepath.FromSlash(rel)))
		if statErr != nil {
			t.Errorf("stat %q: %v", rel, statErr)
			continue
		}
		if !info.ModTime().Equal(want) {
			t.Errorf("dir %q mtime = %v, want %v", rel, info.ModTime().UTC(), want)
		}
	}
}

// In dry-run nothing is written, but the empty dirs are still planned and
// reported so the user sees what a real run would create.
func TestRunDryRunReportsButDoesNotCreateEmptyDirs(t *testing.T) {
	src := t.TempDir()
	mkEmptyDir(t, src, "empty", time.Unix(1643760000, 0).UTC())
	write(t, src, "keep/file.txt", "data", time.Unix(1609459200, 0).UTC())

	out := filepath.Join(t.TempDir(), "out")
	c, err := cli.Parse([]string{"merge", "--dry-run", "--out", out, src})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	if _, statErr := os.Stat(filepath.Join(out, "empty")); !os.IsNotExist(statErr) {
		t.Errorf("dry-run must not create out/empty (err=%v)", statErr)
	}
	// But it is still reported (in the manifest written to disk and the result).
	if !reflect.DeepEqual(m.EmptyDirs, []string{"empty"}) {
		t.Errorf("dry-run manifest EmptyDirs = %v, want [empty]", m.EmptyDirs)
	}
	data, rerr := os.ReadFile(c.ManifestPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var got model.Manifest
	if jerr := json.Unmarshal(data, &got); jerr != nil {
		t.Fatal(jerr)
	}
	if !reflect.DeepEqual(got.EmptyDirs, []string{"empty"}) {
		t.Errorf("written manifest EmptyDirs = %v, want [empty]", got.EmptyDirs)
	}
}
