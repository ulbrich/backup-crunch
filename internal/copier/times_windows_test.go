//go:build windows

package copier

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// On Windows the recovered file must carry the SOURCE's creation time, not the
// time the copy ran. This is the whole point of preserveTimestamps on Windows.
func TestCopyFilePreservesCreationTimeWindows(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("recovered"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Backdate the source's creation time well into the past so it cannot be
	// confused with "now". We set creation here and read it back off the dst.
	srcCreation := time.Date(2009, 3, 1, 12, 0, 0, 0, time.UTC)
	mtime := time.Date(2018, 7, 14, 9, 30, 0, 0, time.UTC)
	if err := setCreationTime(src, srcCreation); err != nil {
		t.Fatalf("seeding source creation time: %v", err)
	}

	dst := filepath.Join(dir, "out", "dst.txt")
	if _, err := CopyFile(context.Background(), src, dst, mtime, false, "go"); err != nil {
		t.Fatal(err)
	}

	gotCreation, gotWrite, err := fileTimes(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !gotCreation.Equal(srcCreation) {
		t.Errorf("dst creation time = %v, want source's %v", gotCreation, srcCreation)
	}
	if !gotWrite.Equal(mtime) {
		t.Errorf("dst last-write time = %v, want %v", gotWrite, mtime)
	}
}

// setCreationTime sets only the creation time on path (test helper).
func setCreationTime(path string, t time.Time) error {
	ft := syscall.NsecToFiletime(t.UnixNano())
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(pathp,
		syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	return syscall.SetFileTime(h, &ft, nil, nil)
}

// fileTimes returns the creation and last-write times of path.
func fileTimes(path string) (creation, write time.Time, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	attr := info.Sys().(*syscall.Win32FileAttributeData)
	creation = time.Unix(0, attr.CreationTime.Nanoseconds())
	write = time.Unix(0, attr.LastWriteTime.Nanoseconds())
	return creation, write, nil
}
