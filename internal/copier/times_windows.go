//go:build windows

package copier

import (
	"os"
	"syscall"
	"time"
)

// preserveTimestamps makes the recovered file's timestamps match the source: it
// sets the creation time to the source's creation time and the last-write (and
// access) time to mtime — the selected winner's modification time, which is the
// source's mtime.
//
// Go's os.Chtimes can only set access/modification times, so the creation time
// (a Windows/NTFS attribute) is applied here via SetFileTime. Without this, a
// recovered file's "Created" timestamp would be the moment the copy ran rather
// than when the original was created.
//
// The source's creation time comes from srcInfo, whose Sys() is a
// *syscall.Win32FileAttributeData on Windows; if it is somehow unavailable we
// fall back to mtime so the creation time is at least not "now".
func preserveTimestamps(path string, srcInfo os.FileInfo, mtime time.Time) error {
	mft := syscall.NsecToFiletime(mtime.UnixNano())
	creation := mft
	if attr, ok := srcInfo.Sys().(*syscall.Win32FileAttributeData); ok {
		creation = attr.CreationTime
	}

	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// FILE_FLAG_BACKUP_SEMANTICS lets this work uniformly for any path; we only
	// need FILE_WRITE_ATTRIBUTES to change the timestamps.
	h, err := syscall.CreateFile(pathp,
		syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)

	// Args: creation, last-access, last-write.
	return syscall.SetFileTime(h, &creation, &mft, &mft)
}
