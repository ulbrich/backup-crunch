package merge

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/logging"
	"github.com/janulbrich/backup-crunch/internal/model"
)

// With --hash, two candidates that tie on (mtime, size) but differ in content
// must be flagged content_divergent. Running with several worker counts also
// exercises the parallel hash pass under -race.
func TestRunHashFlagsContentDivergent(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run("workers="+strconv.Itoa(workers), func(t *testing.T) {
			base := t.TempDir()
			a := base + "/A"
			b := base + "/B"
			mt := time.Unix(1600000000, 0).UTC()
			// Same path, same mtime, same size (4 bytes), different content.
			write(t, a, "dup.txt", "AAAA", mt)
			write(t, b, "dup.txt", "BBBB", mt)

			out := t.TempDir() + "/out"
			args := []string{"merge", "--out", out, "--hash", "--workers", strconv.Itoa(workers), a, b}
			c, err := cli.Parse(args)
			if err != nil {
				t.Fatal(err)
			}
			m, err := Run(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			rec := recordByFold(m, "dup.txt")
			if rec == nil {
				t.Fatal("no record for dup.txt")
			}
			if !contains(rec.Warnings, model.WarnContentDivergent) {
				t.Errorf("expected content_divergent warning, got %v", rec.Warnings)
			}
			if rec.Status != model.StatusFlagged {
				t.Errorf("status = %q, want flagged", rec.Status)
			}
		})
	}
}

// A cancelled context must abort the hash pass with a context error rather than
// silently hashing nothing.
func TestHashTiedGroupsContextCancelled(t *testing.T) {
	dir := t.TempDir()
	mt := time.Unix(1600000000, 0).UTC()
	write(t, dir, "a.txt", "AAAA", mt)
	write(t, dir, "b.txt", "BBBB", mt)

	g := &model.CandidateGroup{FoldKey: "k", Candidates: []model.File{
		{SourceRoot: dir, RelPath: "a.txt", Size: 4, ModTime: mt},
		{SourceRoot: dir, RelPath: "b.txt", Size: 4, ModTime: mt},
	}}
	groups := map[string]*model.CandidateGroup{"k": g}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := hashTiedGroups(ctx, groups, 2, logging.Discard())
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
