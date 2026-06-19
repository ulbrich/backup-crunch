//go:build !windows

package copier

import (
	"os"
	"time"
)

// preserveTimestamps sets the modification time (mirrored to access time) on
// path. Unix filesystems expose no settable creation/birth time — it is a
// Windows/NTFS concept — so on these platforms only the modification time is
// preserved, matching what os.Chtimes can do.
func preserveTimestamps(path string, _ os.FileInfo, mtime time.Time) error {
	return os.Chtimes(path, mtime, mtime)
}
