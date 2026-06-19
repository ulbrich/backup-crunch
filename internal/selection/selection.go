// Package selection implements the pure, deterministic winner-selection rule:
// among non-empty candidates for a fold key, pick newest mtime, tie → largest
// size, final tie → lowest source index. Zero-length files never win.
package selection

import (
	"sort"

	"github.com/janulbrich/backup-crunch/internal/model"
)

// Select applies the selection rule to a candidate group and returns the
// auditable DecisionRecord. withHash indicates whether content hashes were
// computed (enabling content-divergence flagging on ties).
//
// Select is pure: it does not touch the filesystem and depends only on its
// input, so the same group always yields the same record.
func Select(group model.CandidateGroup, withHash bool) model.DecisionRecord {
	rec := model.DecisionRecord{
		FoldKey:         group.FoldKey,
		CandidatesCount: len(group.Candidates),
	}

	var nonEmpty, empty []model.File
	for _, f := range group.Candidates {
		if f.IsEmpty {
			empty = append(empty, f)
		} else {
			nonEmpty = append(nonEmpty, f)
		}
	}

	// case_collision: ≥2 distinct original casings/forms share this fold key.
	if distinctRelPaths(group.Candidates) >= 2 {
		rec.Warnings = append(rec.Warnings, model.WarnCaseCollision)
	}

	// unreadable_source: at least one copy existed by name but could not be read.
	if anyUnreadable(group.Candidates) {
		rec.Warnings = append(rec.Warnings, model.WarnUnreadable)
	}

	if len(nonEmpty) == 0 {
		rec.Status = model.StatusUnrecoverable
		rec.RelPath = canonicalRelPath(group.Candidates)
		rec.Rejected = append(rec.Rejected, empty...)
		return rec
	}

	// Total order: newest mtime, then largest size, then lowest source index.
	// The final source-index tiebreak makes this a strict total order, so a
	// plain (non-stable) sort is already deterministic.
	sort.Slice(nonEmpty, func(i, j int) bool {
		a, b := nonEmpty[i], nonEmpty[j]
		if !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.After(b.ModTime)
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.SourceIndex < b.SourceIndex
	})

	winner := nonEmpty[0]
	rec.Winner = &winner
	rec.RelPath = winner.RelPath
	rec.Status = model.StatusRecovered

	rec.Rejected = append(rec.Rejected, nonEmpty[1:]...)
	rec.Rejected = append(rec.Rejected, empty...)

	// content_divergent: a candidate ties the winner on (mtime,size) but differs
	// in content. Only this flips status to flagged.
	if withHash && winner.Hash != "" {
		for _, f := range nonEmpty[1:] {
			if f.ModTime.Equal(winner.ModTime) && f.Size == winner.Size &&
				f.Hash != "" && f.Hash != winner.Hash {
				rec.Warnings = append(rec.Warnings, model.WarnContentDivergent)
				rec.Status = model.StatusFlagged
				break
			}
		}
	}

	return rec
}

// distinctRelPaths counts distinct original relative paths among candidates.
// Since all candidates share one fold key, any difference is a casing or
// Unicode-form difference.
func distinctRelPaths(files []model.File) int {
	set := make(map[string]struct{}, len(files))
	for _, f := range files {
		set[f.RelPath] = struct{}{}
	}
	return len(set)
}

// anyUnreadable reports whether any candidate was named but unreadable.
func anyUnreadable(files []model.File) bool {
	for _, f := range files {
		if f.Unreadable {
			return true
		}
	}
	return false
}

// canonicalRelPath picks a deterministic representative path for an
// unrecoverable group (lowest RelPath, then lowest source index).
func canonicalRelPath(files []model.File) string {
	best := files[0]
	for _, f := range files[1:] {
		if f.RelPath < best.RelPath ||
			(f.RelPath == best.RelPath && f.SourceIndex < best.SourceIndex) {
			best = f
		}
	}
	return best.RelPath
}
