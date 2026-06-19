// Package hash computes streaming content hashes used to detect content
// divergence among candidates that tie on (mtime, size).
package hash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"github.com/janulbrich/backup-crunch/internal/iobuf"
)

// SHA256Stream returns the hex-encoded SHA-256 of the file at path, reading it
// in fixed-size chunks so memory stays constant regardless of file size. ctx is
// checked between chunks so hashing a large file aborts promptly on
// cancellation.
func SHA256Stream(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, iobuf.Size)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n]) // (hash.Hash).Write never returns an error
		}
		if rerr == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if rerr != nil {
			return "", rerr
		}
	}
}
