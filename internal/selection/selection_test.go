package selection

import (
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/model"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func f(src int, rel string, size int64, mtime string) model.File {
	return model.File{
		SourceIndex: src,
		SourceRoot:  "/src",
		RelPath:     rel,
		FoldKey:     "k",
		Size:        size,
		ModTime:     ts(mtime),
		IsEmpty:     size == 0,
	}
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name        string
		candidates  []model.File
		withHash    bool
		wantStatus  model.Status
		wantWinner  string // RelPath of winner, "" for none
		wantRejects int
		wantWarn    []string
	}{
		{
			name:        "single non-empty wins",
			candidates:  []model.File{f(0, "a.txt", 10, "2020-01-01T00:00:00Z")},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "a.txt",
			wantRejects: 0,
		},
		{
			name: "newest mtime beats older larger file",
			candidates: []model.File{
				f(0, "a.txt", 100, "2020-01-01T00:00:00Z"),
				f(1, "a.txt", 10, "2021-06-15T00:00:00Z"),
			},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "a.txt",
			wantRejects: 1,
		},
		{
			name: "mtime tie breaks to larger size",
			candidates: []model.File{
				f(0, "a.txt", 10, "2021-01-01T00:00:00Z"),
				f(1, "a.txt", 999, "2021-01-01T00:00:00Z"),
			},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "a.txt",
			wantRejects: 1,
		},
		{
			name: "mtime+size tie breaks to lowest source index",
			candidates: []model.File{
				f(2, "a.txt", 10, "2021-01-01T00:00:00Z"),
				f(0, "a.txt", 10, "2021-01-01T00:00:00Z"),
				f(1, "a.txt", 10, "2021-01-01T00:00:00Z"),
			},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "a.txt",
			wantRejects: 2,
		},
		{
			name: "all empty -> unrecoverable",
			candidates: []model.File{
				f(0, "a.txt", 0, "2021-01-01T00:00:00Z"),
				f(1, "a.txt", 0, "2022-01-01T00:00:00Z"),
			},
			wantStatus:  model.StatusUnrecoverable,
			wantWinner:  "",
			wantRejects: 2,
		},
		{
			name: "mix empty + non-empty: empties always rejected, newest non-empty wins",
			candidates: []model.File{
				f(0, "a.txt", 0, "2023-01-01T00:00:00Z"), // empty but newest
				f(1, "a.txt", 50, "2020-01-01T00:00:00Z"),
				f(2, "a.txt", 50, "2021-01-01T00:00:00Z"), // newest non-empty
			},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "a.txt",
			wantRejects: 2,
		},
		{
			name:        "single empty -> unrecoverable",
			candidates:  []model.File{f(0, "a.txt", 0, "2021-01-01T00:00:00Z")},
			wantStatus:  model.StatusUnrecoverable,
			wantWinner:  "",
			wantRejects: 1,
		},
		{
			name: "case collision warns but stays recovered",
			candidates: []model.File{
				f(0, "Foo.txt", 10, "2020-01-01T00:00:00Z"),
				f(1, "foo.txt", 20, "2021-01-01T00:00:00Z"),
			},
			wantStatus:  model.StatusRecovered,
			wantWinner:  "foo.txt",
			wantRejects: 1,
			wantWarn:    []string{model.WarnCaseCollision},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := Select(model.CandidateGroup{FoldKey: "k", Candidates: tt.candidates}, tt.withHash)
			if rec.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", rec.Status, tt.wantStatus)
			}
			if tt.wantWinner == "" {
				if rec.Winner != nil {
					t.Errorf("winner = %+v, want nil", rec.Winner)
				}
			} else if rec.Winner == nil {
				t.Errorf("winner = nil, want %q", tt.wantWinner)
			} else if rec.Winner.RelPath != tt.wantWinner {
				t.Errorf("winner = %q, want %q", rec.Winner.RelPath, tt.wantWinner)
			}
			if len(rec.Rejected) != tt.wantRejects {
				t.Errorf("rejected = %d, want %d", len(rec.Rejected), tt.wantRejects)
			}
			// winner not double-counted: winner + rejected == candidates
			winnerCount := 0
			if rec.Winner != nil {
				winnerCount = 1
			}
			if winnerCount+len(rec.Rejected) != len(tt.candidates) {
				t.Errorf("winner(%d)+rejected(%d) != candidates(%d)", winnerCount, len(rec.Rejected), len(tt.candidates))
			}
			if rec.CandidatesCount != len(tt.candidates) {
				t.Errorf("CandidatesCount = %d, want %d", rec.CandidatesCount, len(tt.candidates))
			}
			for _, w := range tt.wantWarn {
				if !hasWarn(rec.Warnings, w) {
					t.Errorf("missing warning %q in %v", w, rec.Warnings)
				}
			}
		})
	}
}

func TestSelectMtimeTieLargestWins(t *testing.T) {
	rec := Select(model.CandidateGroup{FoldKey: "k", Candidates: []model.File{
		f(0, "a.txt", 10, "2021-01-01T00:00:00Z"),
		f(1, "a.txt", 999, "2021-01-01T00:00:00Z"),
	}}, false)
	if rec.Winner == nil || rec.Winner.Size != 999 {
		t.Fatalf("expected larger (999) file to win on mtime tie, got %+v", rec.Winner)
	}
}

func TestSelectContentDivergentFlags(t *testing.T) {
	a := f(0, "a.txt", 10, "2021-01-01T00:00:00Z")
	a.Hash = "aaaa"
	b := f(1, "a.txt", 10, "2021-01-01T00:00:00Z")
	b.Hash = "bbbb"
	rec := Select(model.CandidateGroup{FoldKey: "k", Candidates: []model.File{a, b}}, true)
	if rec.Status != model.StatusFlagged {
		t.Errorf("status = %q, want flagged", rec.Status)
	}
	if !hasWarn(rec.Warnings, model.WarnContentDivergent) {
		t.Errorf("missing content_divergent warning: %v", rec.Warnings)
	}
}

func unreadable(src int, rel string) model.File {
	return model.File{SourceIndex: src, SourceRoot: "/src", RelPath: rel, FoldKey: "k", IsEmpty: true, Unreadable: true}
}

func TestSelectUnreadableOnlyIsUnrecoverable(t *testing.T) {
	rec := Select(model.CandidateGroup{FoldKey: "k", Candidates: []model.File{
		unreadable(0, "a.txt"),
		unreadable(1, "a.txt"),
	}}, false)
	if rec.Status != model.StatusUnrecoverable {
		t.Errorf("status = %q, want unrecoverable", rec.Status)
	}
	if !hasWarn(rec.Warnings, model.WarnUnreadable) {
		t.Errorf("missing unreadable warning: %v", rec.Warnings)
	}
	if len(rec.Rejected) != 2 {
		t.Errorf("rejected = %d, want 2 (both unreadable phantoms)", len(rec.Rejected))
	}
}

func TestSelectReadableBeatsUnreadable(t *testing.T) {
	rec := Select(model.CandidateGroup{FoldKey: "k", Candidates: []model.File{
		unreadable(0, "a.txt"),
		f(1, "a.txt", 20, "2021-01-01T00:00:00Z"),
	}}, false)
	if rec.Status != model.StatusRecovered || rec.Winner == nil || rec.Winner.SourceIndex != 1 {
		t.Errorf("expected readable copy (source 1) to win, got %+v", rec.Winner)
	}
	if !hasWarn(rec.Warnings, model.WarnUnreadable) {
		t.Errorf("expected unreadable warning even when a readable copy wins: %v", rec.Warnings)
	}
}

func hasWarn(ws []string, w string) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}
