package merge

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/janulbrich/backup-crunch/internal/cli"
)

// TestMergeTestBackupFixture runs the tool against the committed test-backup
// fixture (sources A, B, C) and verifies the merged tree contains exactly
// one.txt, two.txt and nested/three.txt, each holding the content "Winner".
//
// In the fixture, A holds only empty placeholders, one.txt has its only
// non-empty copy in B, two.txt only in C, and nested/three.txt exists in both
// B ("Older") and C ("Winner") — C wins on both newer mtime and larger size, so
// the expected winner is stable even if a checkout resets timestamps.
func TestMergeTestBackupFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	var sources []string
	for _, name := range []string{"A", "B", "C"} {
		p := filepath.Join(repoRoot, "test-backup", name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture source missing: %s (%v)", p, err)
		}
		sources = append(sources, p)
	}

	out := filepath.Join(t.TempDir(), "out")
	// Exclude macOS clutter so the output set is exactly the three winners.
	args := append([]string{"merge", "--out", out, "--exclude", ".DS_Store"}, sources...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(c, nil); err != nil {
		t.Fatal(err)
	}

	// Each expected winner exists with the content "Winner".
	for _, rel := range []string{"one.txt", "two.txt", filepath.Join("nested", "three.txt")} {
		if got := mustRead(t, filepath.Join(out, rel)); got != "Winner" {
			t.Errorf("%s = %q, want \"Winner\"", filepath.ToSlash(rel), got)
		}
	}

	// And nothing other than those three files (plus the manifest) was written.
	var files []string
	err = filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(p) == "manifest.json" {
			return nil
		}
		rel, _ := filepath.Rel(out, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	want := []string{"nested/three.txt", "one.txt", "two.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("output files = %v, want %v", files, want)
	}
}
