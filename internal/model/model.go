// Package model defines the core data types shared across backup-crunch:
// scanned files, candidate groups, per-path decisions, suspicious-timestamp
// clusters, and the JSON manifest that is the product of a merge run.
package model

import "time"

// Status is the outcome for one logical (case-folded) relative path.
type Status string

const (
	// StatusRecovered: a non-empty winner was chosen and will be copied.
	StatusRecovered Status = "recovered"
	// StatusUnrecoverable: every candidate for the path was zero-length.
	StatusUnrecoverable Status = "unrecoverable_empty_only"
	// StatusFlagged: recovered, but with an actionable warning (content divergence).
	StatusFlagged Status = "flagged"
)

// Warning codes attached to a DecisionRecord.
const (
	// WarnCaseCollision: ≥2 candidates had different original casing / Unicode
	// form for the same fold key. Informational only — status stays recovered.
	WarnCaseCollision = "case_collision"
	// WarnContentDivergent: the winner tied another candidate on (mtime,size)
	// but their content hashes differ. Flips status to flagged.
	WarnContentDivergent = "content_divergent"
	// WarnUnreadable: at least one candidate for this path could be named but
	// not read (e.g. a dehydrated OneDrive placeholder); it contributed no data.
	WarnUnreadable = "unreadable_source"
)

// File is one regular file found under a source root. Content is never held in
// memory — only this metadata is.
type File struct {
	SourceIndex int       `json:"source_index"` // index into Config.Sources
	SourceRoot  string    `json:"source_root"`
	RelPath     string    `json:"rel_path"` // original casing, separators normalized to "/"
	FoldKey     string    `json:"-"`        // ToLower(NFC(ToSlash(rel))) — grouping key
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mtime"` // stored in UTC for stable comparison
	Hash        string    `json:"hash,omitempty"`
	IsEmpty     bool      `json:"-"`                    // Size == 0
	Unreadable  bool      `json:"unreadable,omitempty"` // named but could not be stat'd/opened
}

// CandidateGroup holds all candidates competing for one fold key.
type CandidateGroup struct {
	FoldKey    string
	Candidates []File
}

// DecisionRecord is the auditable decision for one output path.
type DecisionRecord struct {
	RelPath         string   `json:"rel_path"` // canonical casing = winner's casing
	FoldKey         string   `json:"fold_key"`
	Status          Status   `json:"status"`
	Winner          *File    `json:"winner,omitempty"` // nil when unrecoverable
	CandidatesCount int      `json:"candidates_considered"`
	Rejected        []File   `json:"rejected"` // every non-winning candidate (incl. empties)
	Warnings        []string `json:"warnings,omitempty"`
}

// Cluster is a per-source set of files sharing one identical mtime — a likely
// sign that the backup process clobbered timestamps to the copy date.
type Cluster struct {
	SourceIndex int       `json:"source_index"`
	SourceRoot  string    `json:"source_root"`
	ModTime     time.Time `json:"mtime"`
	FileCount   int       `json:"file_count"`
	SamplePaths []string  `json:"sample_paths"`
}

// Summary holds run-level totals.
type Summary struct {
	SourcesScanned     int   `json:"sources_scanned"`
	FilesScanned       int   `json:"files_scanned"`
	PathsSeen          int   `json:"paths_seen"` // distinct fold keys
	Recovered          int   `json:"recovered"`  // includes flagged
	UnrecoverableEmpty int   `json:"unrecoverable_empty_only"`
	Flagged            int   `json:"flagged"`
	SuspiciousClusters int   `json:"suspicious_timestamp_clusters"`
	SkippedNonRegular  int   `json:"skipped_non_regular"`
	Excluded           int   `json:"excluded"`        // matched an --exclude pattern
	Unreadable         int   `json:"unreadable"`      // files named but not stat'able/openable
	UnreadableDirs     int   `json:"unreadable_dirs"` // subtrees that could not be entered
	BytesCopied        int64 `json:"bytes_copied"`    // 0 in dry-run
	DryRun             bool  `json:"dry_run"`
}

// Manifest is the complete, machine-readable record of a merge run.
type Manifest struct {
	Tool        string           `json:"tool"`
	Version     string           `json:"version"`
	GeneratedAt time.Time        `json:"generated_at"`
	OutDir      string           `json:"out_dir"`
	Sources     []string         `json:"sources"`
	Summary     Summary          `json:"summary"`
	Records     []DecisionRecord `json:"records"`
	Clusters    []Cluster        `json:"clusters"`
	// UnreadableDirs lists subtree roots that could not be entered (e.g. dataless
	// cloud placeholders). Their contents could not be enumerated or audited.
	UnreadableDirs []string `json:"unreadable_dirs,omitempty"`
}
