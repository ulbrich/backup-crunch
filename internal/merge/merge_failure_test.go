package merge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/model"
)

// A copy failure partway through a run must NOT discard the manifest: files may
// already be on disk, so the audit trail (decisions + partial progress + the
// failure itself) has to be written. This is the core of the "the manifest is
// the product" guarantee under failure.
func TestRunWritesManifestOnCopyFailure(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "A")
	mt := time.Unix(1600000000, 0).UTC()
	write(t, a, "a.txt", "AAAA", mt)   // sorts first by fold key → copies fine
	write(t, a, "z/b.txt", "BBBB", mt) // needs out/z, which we block below

	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a regular file where the winner needs a directory, so creating
	// out/z fails deterministically — but only after a.txt has been copied.
	if err := os.WriteFile(filepath.Join(out, "z"), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default --workers 1 → jobs run sequentially in fold-key order.
	c, err := cli.Parse([]string{"merge", "--out", out, a})
	if err != nil {
		t.Fatal(err)
	}

	m, err := Run(context.Background(), c)
	if err == nil {
		t.Fatal("expected a copy error")
	}

	// The manifest must still be on disk and valid JSON.
	data, rerr := os.ReadFile(c.ManifestPath)
	if rerr != nil {
		t.Fatalf("manifest not written on copy failure: %v", rerr)
	}
	var got model.Manifest
	if jerr := json.Unmarshal(data, &got); jerr != nil {
		t.Fatalf("manifest is not valid JSON: %v", jerr)
	}

	// Audit trail preserved: decisions recorded, failure recorded, partial bytes.
	if got.RunError == "" {
		t.Error("manifest RunError should record the copy failure")
	}
	if len(got.Records) < 2 {
		t.Errorf("records = %d, want >= 2 (a.txt and z/b.txt)", len(got.Records))
	}
	if got.Summary.BytesCopied == 0 {
		t.Error("partial BytesCopied should be recorded (a.txt copied before the failure)")
	}

	// Returned manifest is populated and matches what was written (issue #2:
	// consistent error contract on the copy path).
	if m.RunError != got.RunError || m.OutDir != got.OutDir {
		t.Error("returned manifest should be populated and match the written one on copy error")
	}

	// The file that could be copied is on disk; the blocked one is not.
	if !fileExists(filepath.Join(out, "a.txt")) {
		t.Error("a.txt should have been copied before the failure")
	}
	if fileExists(filepath.Join(out, "z", "b.txt")) {
		t.Error("z/b.txt should not exist (its copy failed)")
	}
}
