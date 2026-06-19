package hash

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSHA256Stream(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SHA256Stream(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Well-known SHA-256 of "abc".
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("SHA256Stream = %q, want %q", got, want)
	}
}

// A cancelled context must abort hashing (checked between chunks).
func TestSHA256StreamContextCancelled(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	// Larger than the streaming buffer so multiple chunks would be read.
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SHA256Stream(ctx, p); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
