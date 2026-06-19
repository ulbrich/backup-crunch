package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/model"
	"github.com/janulbrich/backup-crunch/internal/scan"
)

func write(t *testing.T, root, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

type snap struct {
	size int64
	mod  int64
	sum  string
}

func snapshot(t *testing.T, root string) map[string]snap {
	t.Helper()
	out := map[string]snap{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		out[p] = snap{size: info.Size(), mod: info.ModTime().UnixNano(), sum: hex.EncodeToString(h[:])}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// buildFixture creates three source roots per §7 and returns them plus the
// shared cluster mtime/threshold.
func buildFixture(t *testing.T) (srcs []string, clusterThreshold int) {
	t.Helper()
	base := t.TempDir()
	a := filepath.Join(base, "A")
	b := filepath.Join(base, "B")
	c := filepath.Join(base, "C")

	older := time.Unix(1577836800, 0).UTC() // 2020-01-01
	newer := time.Unix(1623715200, 0).UTC() // 2021-06-15
	tie := time.Unix(1609459200, 0).UTC()   // 2021-01-01

	// shared.txt: non-empty in A (older) and B (newer) → B wins.
	write(t, a, "shared.txt", "old-content", older)
	write(t, b, "shared.txt", "new", newer)

	// tie.txt: mtime tie, B larger → B wins.
	write(t, a, "tie.txt", "aa", tie)
	write(t, b, "tie.txt", "bbbbbbbb", tie)

	// case variant: A has Docs/A.txt (older), B has docs/a.txt (newer) → one
	// output entry, canonical casing = winner (B), case_collision warning.
	write(t, a, "Docs/A.txt", "old", older)
	write(t, b, "docs/a.txt", "newwin", newer)

	// ghost.txt: zero-length everywhere → unrecoverable, absent from output.
	write(t, a, "ghost.txt", "", older)
	write(t, b, "ghost.txt", "", newer)
	write(t, c, "ghost.txt", "", tie)

	// unique-to-C file.
	write(t, c, "only-c.txt", "cdata", newer)

	// cluster in A: 4 files sharing one identical mtime (threshold 4).
	clusterMtime := time.Unix(1600000000, 0).UTC()
	for _, n := range []string{"c1.txt", "c2.txt", "c3.txt", "c4.txt"} {
		write(t, a, "clob/"+n, "x", clusterMtime)
	}

	// symlink in C (should be skipped) — best effort.
	if runtime.GOOS != "windows" {
		_ = os.Symlink(filepath.Join(c, "only-c.txt"), filepath.Join(c, "link.txt"))
	}

	return []string{a, b, c}, 4
}

func recordByFold(m model.Manifest, fold string) *model.DecisionRecord {
	for i := range m.Records {
		if m.Records[i].FoldKey == fold {
			return &m.Records[i]
		}
	}
	return nil
}

func TestMergeEndToEnd(t *testing.T) {
	srcs, threshold := buildFixture(t)

	// snapshot sources for the read-only invariant (criterion #4).
	var before []map[string]snap
	for _, s := range srcs {
		before = append(before, snapshot(t, s))
	}

	out := filepath.Join(t.TempDir(), "out")
	args := []string{"merge", "--out", out, "--ts-cluster-threshold", "4"}
	args = append(args, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	_ = threshold

	m, err := Run(c, nil)
	if err != nil {
		t.Fatal(err)
	}

	// criterion #4: sources unchanged byte-for-byte.
	for i, s := range srcs {
		if got := snapshot(t, s); !reflect.DeepEqual(got, before[i]) {
			t.Errorf("source %q was modified by the run", s)
		}
	}

	// shared.txt → winner from B ("new"), newer mtime.
	if got := mustRead(t, filepath.Join(out, "shared.txt")); got != "new" {
		t.Errorf("shared.txt = %q, want \"new\" (newer wins)", got)
	}
	// tie.txt → larger (B) wins on mtime tie.
	if got := mustRead(t, filepath.Join(out, "tie.txt")); got != "bbbbbbbb" {
		t.Errorf("tie.txt = %q, want \"bbbbbbbb\" (largest on tie)", got)
	}

	// criterion #8: case-only collision → single canonical entry, winner casing.
	rec := recordByFold(m, "docs/a.txt")
	if rec == nil {
		t.Fatal("no record for docs/a.txt fold key")
	}
	if rec.RelPath != "docs/a.txt" {
		t.Errorf("canonical casing = %q, want docs/a.txt (winner B)", rec.RelPath)
	}
	if !contains(rec.Warnings, model.WarnCaseCollision) {
		t.Errorf("expected case_collision warning, got %v", rec.Warnings)
	}
	// exactly one output file maps to that fold key (criterion #1/#8). Counting
	// by fold key is robust on case-insensitive filesystems where Docs/A.txt and
	// docs/a.txt would otherwise resolve to the same inode.
	if n := countOutputByFold(t, out, "docs/a.txt"); n != 1 {
		t.Errorf("output files for fold docs/a.txt = %d, want 1 (single canonical entry)", n)
	}

	// ghost.txt unrecoverable and absent from output.
	if fileExists(filepath.Join(out, "ghost.txt")) {
		t.Error("ghost.txt (empty-only) should not be in output")
	}
	if g := recordByFold(m, "ghost.txt"); g == nil || g.Status != model.StatusUnrecoverable {
		t.Errorf("ghost.txt should be unrecoverable, got %+v", g)
	}

	// criterion #9: mtime preserved on a winner.
	info, err := os.Stat(filepath.Join(out, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(time.Unix(1623715200, 0).UTC()) {
		t.Errorf("shared.txt mtime = %v, not preserved", info.ModTime())
	}

	// cluster flagged.
	if m.Summary.SuspiciousClusters < 1 {
		t.Errorf("expected ≥1 suspicious cluster, got %d", m.Summary.SuspiciousClusters)
	}

	// manifest content sanity (criteria #6/#7).
	if m.Summary.Recovered < 4 {
		t.Errorf("recovered = %d, want ≥4", m.Summary.Recovered)
	}
	if m.Summary.UnrecoverableEmpty != 1 {
		t.Errorf("unrecoverable = %d, want 1", m.Summary.UnrecoverableEmpty)
	}
	sharedRec := recordByFold(m, "shared.txt")
	if sharedRec == nil || sharedRec.Winner == nil {
		t.Fatal("missing shared.txt winner record")
	}
	if len(sharedRec.Rejected) != 1 {
		t.Errorf("shared.txt rejected = %d, want 1", len(sharedRec.Rejected))
	}
	if sharedRec.CandidatesCount != 2 {
		t.Errorf("shared.txt candidates = %d, want 2", sharedRec.CandidatesCount)
	}
}

func TestMergeDryRunWritesNoFiles(t *testing.T) {
	srcs, _ := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	mani := filepath.Join(t.TempDir(), "manifest.json") // manifest outside out
	args := []string{"merge", "--out", out, "--dry-run", "--manifest", mani, "--ts-cluster-threshold", "4"}
	args = append(args, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Run(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Summary.BytesCopied != 0 {
		t.Errorf("dry-run BytesCopied = %d, want 0", m.Summary.BytesCopied)
	}
	// criterion #5: no files created under --out.
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(out)
		if len(entries) != 0 {
			t.Errorf("dry-run created %d entries under --out, want 0", len(entries))
		}
	}
	// manifest still written.
	if _, err := os.Stat(mani); err != nil {
		t.Errorf("dry-run did not write manifest: %v", err)
	}
}

func TestMergeDeterministic(t *testing.T) {
	srcs, _ := buildFixture(t)
	run := func() []model.DecisionRecord {
		out := filepath.Join(t.TempDir(), "out")
		args := append([]string{"merge", "--out", out, "--ts-cluster-threshold", "4"}, srcs...)
		c, err := cli.Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Run(c, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m.Records
	}
	if !reflect.DeepEqual(run(), run()) {
		t.Error("records differ between identical runs — non-deterministic")
	}
}

// Dry-run with the DEFAULT manifest path (inside --out): --out may be created
// to hold the manifest, but it must contain nothing except manifest.json.
func TestMergeDryRunDefaultManifestOnlyManifest(t *testing.T) {
	srcs, _ := buildFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	args := append([]string{"merge", "--out", out, "--dry-run", "--ts-cluster-threshold", "4"}, srcs...)
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(c, nil); err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(p) != "manifest.json" {
			t.Errorf("dry-run wrote unexpected file under --out: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The worker pool must produce the same result as sequential copying (and be
// race-clean under -race).
func TestMergeWorkersMatchSequential(t *testing.T) {
	srcs, _ := buildFixture(t)
	run := func(workers string) (string, map[string]string) {
		out := filepath.Join(t.TempDir(), "out")
		args := append([]string{"merge", "--out", out, "--workers", workers, "--ts-cluster-threshold", "4"}, srcs...)
		c, err := cli.Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Run(c, nil); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		_ = filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Base(p) == "manifest.json" {
				return nil
			}
			rel, _ := filepath.Rel(out, p)
			files[filepath.ToSlash(rel)] = mustRead(t, p)
			return nil
		})
		return out, files
	}
	_, seq := run("1")
	_, par := run("4")
	if !reflect.DeepEqual(seq, par) {
		t.Errorf("parallel output differs from sequential:\n seq=%v\n par=%v", seq, par)
	}
}

func TestMergeExclude(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "A")
	mtime := time.Unix(1600000000, 0).UTC()
	write(t, a, "keep/report.txt", "keep me", mtime)
	write(t, a, "scratch/notes.tmp", "junk", mtime)
	write(t, a, "$RECYCLE.BIN/S-1-5/$IABCDEF", "stub", mtime)
	write(t, a, "$RECYCLE.BIN/S-1-5/$RABCDEF", "deleted bytes", mtime)

	out := filepath.Join(t.TempDir(), "out")
	args := []string{"merge", "--out", out, "--exclude", "*.tmp", "--exclude", "$RECYCLE.BIN", a}
	c, err := cli.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Run(c, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !fileExists(filepath.Join(out, "keep", "report.txt")) {
		t.Error("keep/report.txt should have been recovered")
	}
	if fileExists(filepath.Join(out, "scratch", "notes.tmp")) {
		t.Error("notes.tmp should have been excluded")
	}
	if _, err := os.Stat(filepath.Join(out, "$RECYCLE.BIN")); !os.IsNotExist(err) {
		t.Error("$RECYCLE.BIN should have been excluded entirely")
	}
	// notes.tmp (1 file) + $RECYCLE.BIN (1 dir pruned) = 2.
	if m.Summary.Excluded != 2 {
		t.Errorf("Summary.Excluded = %d, want 2", m.Summary.Excluded)
	}
	if m.Summary.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (only keep/report.txt)", m.Summary.FilesScanned)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// countOutputByFold walks the output tree and counts regular files whose
// relative path folds to wantFold.
func countOutputByFold(t *testing.T, out, wantFold string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		if scan.FoldKey(rel) == wantFold {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
