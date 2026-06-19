// Package hash computes streaming content hashes used to detect content
// divergence among candidates that tie on (mtime, size).
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

const bufSize = 1 << 20 // 1 MiB; content is streamed, never fully buffered.

// SHA256Stream returns the hex-encoded SHA-256 of the file at path, reading it
// in fixed-size chunks so memory stays constant regardless of file size.
func SHA256Stream(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, bufSize)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
