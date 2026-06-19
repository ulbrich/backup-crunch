// Package merge orchestrates a full run: scan sources, group by fold key,
// detect timestamp clusters, optionally hash tied candidates, select winners,
// copy them into the output tree, and assemble the manifest.
package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/cluster"
	"github.com/janulbrich/backup-crunch/internal/copier"
	"github.com/janulbrich/backup-crunch/internal/hash"
	"github.com/janulbrich/backup-crunch/internal/manifest"
	"github.com/janulbrich/backup-crunch/internal/model"
	"github.com/janulbrich/backup-crunch/internal/scan"
	"github.com/janulbrich/backup-crunch/internal/selection"
)

// Version is the tool version stamped into the manifest.
const Version = "0.1.0"

// Now is overridable in tests; production uses time.Now.
var Now = func() time.Time { return time.Now().UTC() }

// Run executes the merge described by c and returns the assembled manifest.
// The manifest is always written to disk (even in dry-run). logf may be nil.
func Run(c *cli.Config, logf func(string, ...any)) (model.Manifest, error) {
	// 1. Scan all sources (read-only).
	var all []model.File
	stats := scan.Stats{}
	for i, s := range c.Sources {
		files, err := scan.ScanSource(i, s, c.Excludes, &stats, c.Verbose, logf)
		if err != nil {
			return model.Manifest{}, fmt.Errorf("scan source %q: %w", s, err)
		}
		all = append(all, files...)
	}

	// 2. Group by fold key.
	groups := make(map[string]*model.CandidateGroup)
	for _, f := range all {
		g := groups[f.FoldKey]
		if g == nil {
			g = &model.CandidateGroup{FoldKey: f.FoldKey}
			groups[f.FoldKey] = g
		}
		g.Candidates = append(g.Candidates, f)
	}

	// 3. Detect suspicious timestamp clusters (independent of selection).
	clusters := cluster.Detect(all, c.TSClusterThreshold)

	// 4. Optional hashing of tied top candidates for divergence detection.
	if c.Hash {
		hashTiedGroups(groups, logf)
	}

	// 5. Select winners (pure).
	records := make([]model.DecisionRecord, 0, len(groups))
	for _, g := range groups {
		records = append(records, selection.Select(*g, c.Hash))
	}

	// 6. Deterministic record order.
	sort.Slice(records, func(i, j int) bool { return records[i].FoldKey < records[j].FoldKey })

	// 7. Copy winners (dry-run aware) and tally the summary.
	summary := model.Summary{
		SourcesScanned:    len(c.Sources),
		FilesScanned:      stats.FilesScanned,
		PathsSeen:         len(groups),
		SkippedNonRegular: stats.SkippedNonRegular,
		Excluded:          stats.Excluded,
		Unreadable:        stats.Unreadable,
		UnreadableDirs:    stats.UnreadableDirs,
		DryRun:            c.DryRun,
	}
	var jobs []*model.DecisionRecord
	for i := range records {
		r := &records[i]
		switch r.Status {
		case model.StatusRecovered, model.StatusFlagged:
			summary.Recovered++
			if r.Status == model.StatusFlagged {
				summary.Flagged++
			}
			if r.Winner != nil {
				jobs = append(jobs, r)
			}
		case model.StatusUnrecoverable:
			summary.UnrecoverableEmpty++
		}
	}

	bytesCopied, err := copyWinners(jobs, c)
	if err != nil {
		return model.Manifest{}, err
	}
	// BytesCopied reflects bytes actually written; a dry-run copies none.
	if !c.DryRun {
		summary.BytesCopied = bytesCopied
	}
	summary.SuspiciousClusters = len(clusters)

	m := model.Manifest{
		Tool:           "backup-crunch",
		Version:        Version,
		GeneratedAt:    Now(),
		OutDir:         c.Out,
		Sources:        c.Sources,
		Summary:        summary,
		Records:        records,
		Clusters:       clusters,
		UnreadableDirs: stats.UnreadableDirList,
	}

	// Manifest is always written, even in dry-run.
	if err := os.MkdirAll(filepath.Dir(c.ManifestPath), 0o755); err != nil {
		return m, fmt.Errorf("create manifest dir: %w", err)
	}
	if err := manifest.Write(m, c.ManifestPath); err != nil {
		return m, fmt.Errorf("write manifest: %w", err)
	}
	return m, nil
}

// copyWinners copies every job's winner into the output tree. With c.Workers
// > 1 the copies run on a bounded worker pool; otherwise they run sequentially.
// Output is deterministic regardless of worker count because each job writes a
// distinct destination and the manifest records were already ordered. Returns
// the total bytes copied (would-be bytes in dry-run) and the first error, if any.
func copyWinners(jobs []*model.DecisionRecord, c *cli.Config) (int64, error) {
	if c.Workers <= 1 || len(jobs) <= 1 {
		var total int64
		for _, r := range jobs {
			n, err := copyOne(r, c)
			if err != nil {
				return total, err
			}
			total += n
		}
		return total, nil
	}

	var (
		mu       sync.Mutex
		total    int64
		firstErr error
		wg       sync.WaitGroup
	)
	ch := make(chan *model.DecisionRecord)
	for w := 0; w < c.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range ch {
				n, err := copyOne(r, c)
				mu.Lock()
				total += n
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	for _, r := range jobs {
		ch <- r
	}
	close(ch)
	wg.Wait()
	return total, firstErr
}

// copyOne copies a single winner. os.MkdirAll and os.CreateTemp inside CopyFile
// are safe under concurrent calls (idempotent dir creation, unique temp names).
func copyOne(r *model.DecisionRecord, c *cli.Config) (int64, error) {
	src := filepath.Join(r.Winner.SourceRoot, filepath.FromSlash(r.Winner.RelPath))
	dst := filepath.Join(c.Out, filepath.FromSlash(r.RelPath))
	n, err := copier.CopyFile(src, dst, r.Winner.ModTime, c.DryRun, c.CopyTool)
	if err != nil {
		return 0, fmt.Errorf("copy %q: %w", r.RelPath, err)
	}
	return n, nil
}

// hashTiedGroups hashes only the candidates that tie the best (mtime,size)
// within a group — the only ones whose divergence can affect the decision —
// keeping hashing cost bounded. Hash errors are logged and skipped.
func hashTiedGroups(groups map[string]*model.CandidateGroup, logf func(string, ...any)) {
	for _, g := range groups {
		var nonEmpty []*model.File
		for i := range g.Candidates {
			if !g.Candidates[i].IsEmpty {
				nonEmpty = append(nonEmpty, &g.Candidates[i])
			}
		}
		if len(nonEmpty) < 2 {
			continue
		}
		best := nonEmpty[0]
		for _, f := range nonEmpty[1:] {
			if f.ModTime.After(best.ModTime) || (f.ModTime.Equal(best.ModTime) && f.Size > best.Size) {
				best = f
			}
		}
		var tied []*model.File
		for _, f := range nonEmpty {
			if f.ModTime.Equal(best.ModTime) && f.Size == best.Size {
				tied = append(tied, f)
			}
		}
		if len(tied) < 2 {
			continue
		}
		for _, f := range tied {
			h, err := hash.SHA256Stream(filepath.Join(f.SourceRoot, filepath.FromSlash(f.RelPath)))
			if err != nil {
				if logf != nil {
					logf("hash %q: %v", f.RelPath, err)
				}
				continue
			}
			f.Hash = h
		}
	}
}
