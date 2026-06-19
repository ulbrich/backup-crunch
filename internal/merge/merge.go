// Package merge orchestrates a full run: scan sources, group by fold key,
// detect timestamp clusters, optionally hash tied candidates, select winners,
// copy them into the output tree, and assemble the manifest.
package merge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/janulbrich/backup-crunch/internal/cli"
	"github.com/janulbrich/backup-crunch/internal/cluster"
	"github.com/janulbrich/backup-crunch/internal/copier"
	"github.com/janulbrich/backup-crunch/internal/hash"
	"github.com/janulbrich/backup-crunch/internal/logging"
	"github.com/janulbrich/backup-crunch/internal/manifest"
	"github.com/janulbrich/backup-crunch/internal/model"
	"github.com/janulbrich/backup-crunch/internal/scan"
	"github.com/janulbrich/backup-crunch/internal/selection"
)

// defaultVersion is stamped into the manifest unless overridden with
// WithVersion. The binary normally injects its build version there.
const defaultVersion = "0.1.0"

// options holds the injectable dependencies of a run. They replace the former
// package-level mutable globals (logger, clock, version) so runs are isolated
// and safe to exercise concurrently in tests.
type options struct {
	logger  *slog.Logger
	clock   func() time.Time
	version string
}

// Option configures a Run.
type Option func(*options)

// WithLogger sets the logger for progress/diagnostic output. A nil logger is
// ignored (logging is discarded by default).
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithClock overrides the time source stamped into the manifest's GeneratedAt.
// A nil clock is ignored.
func WithClock(f func() time.Time) Option {
	return func(o *options) {
		if f != nil {
			o.clock = f
		}
	}
}

// WithVersion overrides the tool version recorded in the manifest.
func WithVersion(v string) Option {
	return func(o *options) {
		if v != "" {
			o.version = v
		}
	}
}

// Run executes the merge described by c and returns the assembled manifest.
// The manifest is always written to disk (even in dry-run). ctx cancels the
// scan and the copy phase.
func Run(ctx context.Context, c *cli.Config, opts ...Option) (model.Manifest, error) {
	o := options{
		logger:  logging.Discard(),
		clock:   func() time.Time { return time.Now().UTC() },
		version: defaultVersion,
	}
	for _, opt := range opts {
		opt(&o)
	}

	// 1. Scan all sources (read-only).
	all, stats, err := scanAll(ctx, c, o.logger)
	if err != nil {
		return model.Manifest{}, err
	}

	// 2. Group by fold key.
	groups := groupByFoldKey(all)

	// 3. Detect suspicious timestamp clusters (independent of selection).
	clusters := cluster.Detect(all, c.TSClusterThreshold)

	// 4. Optional hashing of tied top candidates for divergence detection.
	if c.Hash {
		hashTiedGroups(groups, o.logger)
	}

	// 5. Select winners (pure), in deterministic fold-key order.
	records := selectAll(groups, c.Hash)

	// 6. Tally the summary and collect the copy jobs.
	summary, jobs := summarize(c, stats, len(groups), records)

	// 7. Copy winners (dry-run aware).
	bytesCopied, err := copyWinners(ctx, jobs, c)
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
		Version:        o.version,
		GeneratedAt:    o.clock(),
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

// scanAll walks every source (read-only) and returns the combined files plus
// the aggregate scan stats.
func scanAll(ctx context.Context, c *cli.Config, logger *slog.Logger) ([]model.File, scan.Stats, error) {
	var all []model.File
	stats := scan.Stats{}
	for i, s := range c.Sources {
		files, err := scan.ScanSource(ctx, i, s, c.Excludes, &stats, logger)
		if err != nil {
			return nil, stats, fmt.Errorf("scan source %q: %w", s, err)
		}
		all = append(all, files...)
	}
	return all, stats, nil
}

// groupByFoldKey buckets files by their case/Unicode-folded relative path.
func groupByFoldKey(all []model.File) map[string]*model.CandidateGroup {
	groups := make(map[string]*model.CandidateGroup)
	for _, f := range all {
		g := groups[f.FoldKey]
		if g == nil {
			g = &model.CandidateGroup{FoldKey: f.FoldKey}
			groups[f.FoldKey] = g
		}
		g.Candidates = append(g.Candidates, f)
	}
	return groups
}

// selectAll applies the pure selection rule to every group and returns the
// records sorted by fold key for deterministic output.
func selectAll(groups map[string]*model.CandidateGroup, withHash bool) []model.DecisionRecord {
	records := make([]model.DecisionRecord, 0, len(groups))
	for _, g := range groups {
		records = append(records, selection.Select(*g, withHash))
	}
	sort.Slice(records, func(i, j int) bool { return records[i].FoldKey < records[j].FoldKey })
	return records
}

// summarize tallies run-level totals and returns the copy jobs (recovered
// records that have a winner).
func summarize(c *cli.Config, stats scan.Stats, pathsSeen int, records []model.DecisionRecord) (model.Summary, []*model.DecisionRecord) {
	summary := model.Summary{
		SourcesScanned:    len(c.Sources),
		FilesScanned:      stats.FilesScanned,
		PathsSeen:         pathsSeen,
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
	return summary, jobs
}

// copyWinners copies every job's winner into the output tree. With c.Workers
// > 1 the copies run on a bounded errgroup; otherwise they run sequentially.
// Output is deterministic regardless of worker count because each job writes a
// distinct destination and the manifest records were already ordered. On the
// first copy error the shared context is cancelled, so in-flight and pending
// copies abort instead of needlessly continuing. Returns the total bytes
// copied (would-be bytes in dry-run) and the first error, if any.
func copyWinners(ctx context.Context, jobs []*model.DecisionRecord, c *cli.Config) (int64, error) {
	if len(jobs) == 0 {
		return 0, nil
	}

	var total atomic.Int64
	g, ctx := errgroup.WithContext(ctx)
	if c.Workers > 1 {
		g.SetLimit(c.Workers)
	} else {
		g.SetLimit(1)
	}

	for _, r := range jobs {
		g.Go(func() error {
			n, err := copyOne(ctx, r, c)
			total.Add(n)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return total.Load(), err
	}
	return total.Load(), nil
}

// copyOne copies a single winner. os.MkdirAll and os.CreateTemp inside CopyFile
// are safe under concurrent calls (idempotent dir creation, unique temp names).
func copyOne(ctx context.Context, r *model.DecisionRecord, c *cli.Config) (int64, error) {
	if r.Winner == nil {
		return 0, fmt.Errorf("copy %q: record has no winner", r.RelPath)
	}
	src := filepath.Join(r.Winner.SourceRoot, filepath.FromSlash(r.Winner.RelPath))
	dst := filepath.Join(c.Out, filepath.FromSlash(r.RelPath))
	n, err := copier.CopyFile(ctx, src, dst, r.Winner.ModTime, c.DryRun, c.CopyTool)
	if err != nil {
		return 0, fmt.Errorf("copy %q: %w", r.RelPath, err)
	}
	return n, nil
}

// hashTiedGroups hashes only the candidates that tie the best (mtime,size)
// within a group — the only ones whose divergence can affect the decision —
// keeping hashing cost bounded. Hash errors are logged and skipped.
func hashTiedGroups(groups map[string]*model.CandidateGroup, logger *slog.Logger) {
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
				logger.Warn("hash failed", "path", f.RelPath, "err", err)
				continue
			}
			f.Hash = h
		}
	}
}
