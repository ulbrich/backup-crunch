package manifest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janulbrich/backup-crunch/internal/model"
)

func sampleManifest() model.Manifest {
	w := model.File{SourceIndex: 1, SourceRoot: "/b", RelPath: "a.txt", Size: 10, ModTime: time.Unix(1, 0).UTC()}
	return model.Manifest{
		Tool:        "backup-crunch",
		Version:     "0.1.0",
		GeneratedAt: time.Unix(2, 0).UTC(),
		OutDir:      "/out",
		Sources:     []string{"/a", "/b"},
		Summary:     model.Summary{Recovered: 1, PathsSeen: 1},
		Records: []model.DecisionRecord{{
			RelPath: "a.txt", FoldKey: "a.txt", Status: model.StatusRecovered,
			Winner: &w, CandidatesCount: 2,
		}},
	}
}

func TestWriteRoundTrip(t *testing.T) {
	m := sampleManifest()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := Write(m, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got model.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if got.Tool != "backup-crunch" || len(got.Records) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Records[0].Winner == nil || got.Records[0].Winner.RelPath != "a.txt" {
		t.Errorf("winner not preserved through round-trip")
	}
}

func TestPrintSummary(t *testing.T) {
	var buf bytes.Buffer
	PrintSummary(&buf, sampleManifest())
	if buf.Len() == 0 {
		t.Error("expected non-empty summary output")
	}
}
