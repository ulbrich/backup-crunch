package cluster

import (
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/model"
)

func mk(src int, rel string, mt time.Time) model.File {
	return model.File{SourceIndex: src, SourceRoot: "/s", RelPath: rel, ModTime: mt}
}

func TestDetectThresholdBoundary(t *testing.T) {
	t0 := time.Unix(1600000000, 0).UTC()
	var files []model.File
	for i := 0; i < 3; i++ {
		files = append(files, mk(0, string(rune('a'+i))+".txt", t0))
	}
	// Below threshold: no cluster.
	if got := Detect(files, 4); len(got) != 0 {
		t.Errorf("threshold 4 with 3 files: got %d clusters, want 0", len(got))
	}
	// At threshold: cluster flagged.
	got := Detect(files, 3)
	if len(got) != 1 {
		t.Fatalf("threshold 3 with 3 files: got %d clusters, want 1", len(got))
	}
	if got[0].FileCount != 3 {
		t.Errorf("FileCount = %d, want 3", got[0].FileCount)
	}
}

func TestDetectPerSourceIsolation(t *testing.T) {
	t0 := time.Unix(1600000000, 0).UTC()
	files := []model.File{
		mk(0, "a.txt", t0), mk(0, "b.txt", t0), // source 0: 2 share t0
		mk(1, "c.txt", t0), // source 1: only 1 at t0
	}
	got := Detect(files, 2)
	if len(got) != 1 || got[0].SourceIndex != 0 {
		t.Fatalf("expected one cluster in source 0, got %+v", got)
	}
}

func TestDetectDistinctMtimesNoCluster(t *testing.T) {
	files := []model.File{
		mk(0, "a.txt", time.Unix(1, 0).UTC()),
		mk(0, "b.txt", time.Unix(2, 0).UTC()),
	}
	if got := Detect(files, 2); len(got) != 0 {
		t.Errorf("distinct mtimes: got %d clusters, want 0", len(got))
	}
}
