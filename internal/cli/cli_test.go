package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tempSource(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseRequiresOut(t *testing.T) {
	src := tempSource(t)
	if _, err := Parse([]string{"merge", src}); err == nil {
		t.Error("expected error when --out missing")
	}
}

func TestParseRequiresMergeSubcommand(t *testing.T) {
	if _, err := Parse([]string{"--out", "/tmp/x"}); err == nil {
		t.Error("expected error without merge subcommand")
	}
}

func TestParseDefaults(t *testing.T) {
	src := tempSource(t)
	out := filepath.Join(t.TempDir(), "out")
	c, err := Parse([]string{"merge", "--out", out, src})
	if err != nil {
		t.Fatal(err)
	}
	if c.TSClusterThreshold != 50 {
		t.Errorf("default threshold = %d, want 50", c.TSClusterThreshold)
	}
	if c.CopyTool != "go" {
		t.Errorf("default copy-tool = %q, want go", c.CopyTool)
	}
	if c.Workers != 1 {
		t.Errorf("default workers = %d, want 1", c.Workers)
	}
	wantManifest := filepath.Join(c.Out, "manifest.json")
	if c.ManifestPath != wantManifest {
		t.Errorf("default manifest = %q, want %q", c.ManifestPath, wantManifest)
	}
}

func TestParseRejectsBadCopyTool(t *testing.T) {
	src := tempSource(t)
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Parse([]string{"merge", "--out", out, "--copy-tool", "tar", src}); err == nil {
		t.Error("expected error for invalid --copy-tool")
	}
}

func TestParseRejectsNonexistentSource(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Parse([]string{"merge", "--out", out, "/no/such/dir/xyz"}); err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestParseRejectsOutInsideSource(t *testing.T) {
	src := tempSource(t)
	out := filepath.Join(src, "merged") // directly inside the source
	if _, err := Parse([]string{"merge", "--out", out, src}); err == nil {
		t.Error("expected error: --out inside source")
	}
}

// MAJOR-4: a symlinked --out that resolves inside a source must be rejected.
func TestParseRejectsSymlinkedOutInsideSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	src := tempSource(t)
	realInside := filepath.Join(src, "inside")
	if err := os.MkdirAll(realInside, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := t.TempDir()
	out := filepath.Join(linkDir, "out-link") // looks outside the source...
	if err := os.Symlink(realInside, out); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if _, err := Parse([]string{"merge", "--out", out, src}); err == nil {
		t.Error("expected error: symlinked --out resolves inside source")
	}
}

func TestParseExcludeRepeatable(t *testing.T) {
	src := tempSource(t)
	out := filepath.Join(t.TempDir(), "out")
	c, err := Parse([]string{"merge", "--out", out, "--exclude", "*.tmp", "--exclude", "$RECYCLE.BIN", src})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Excludes) != 2 || c.Excludes[0] != "*.tmp" || c.Excludes[1] != "$RECYCLE.BIN" {
		t.Errorf("Excludes = %v, want [*.tmp $RECYCLE.BIN]", c.Excludes)
	}
}

func TestParseRejectsManifestInsideSource(t *testing.T) {
	src := tempSource(t)
	out := filepath.Join(t.TempDir(), "out")
	mani := filepath.Join(src, "manifest.json")
	if _, err := Parse([]string{"merge", "--out", out, "--manifest", mani, src}); err == nil {
		t.Error("expected error: --manifest inside source")
	}
}
