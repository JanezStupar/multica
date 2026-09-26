//go:build windows

package execenv

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusiveNonBlocking takes an exclusive lock on f without waiting.
// ok is false when another process already holds it.
//
// Windows releases the lock when the handle closes, including on abnormal
// process termination, giving the same crash-safe liveness answer as flock on
// unix — see the unix build of this file for why that property matters.
// openLockFile opens (creating if needed) the lock file with
// FILE_SHARE_DELETE, which os.OpenFile does not request.
//
// Without it Windows refuses to delete a file that anyone still has open, so a
// held lock would pin the whole env root: the GC could not reclaim the
// directory, and neither could a test's temp-dir teardown. Unix already allows
// unlinking an open file, and this is what makes Windows behave the same. The
// lock coordinates executions; it is not meant to pin the directory.
func openLockFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// openTargetFileForLock opens the target itself with delete sharing so the
// descriptor can be locked against helpers using the same target inode. When
// create is true, created reports whether this call created the file rather
// than opening one that already existed.
func openTargetFileForLock(path string, create bool) (f *os.File, created bool, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	disposition := uint32(windows.OPEN_EXISTING)
	if create {
		disposition = windows.CREATE_NEW
	}
	h, err := windows.CreateFile(p, access, share, nil, disposition, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err == nil {
		return os.NewFile(uintptr(h), path), create, nil
	}
	if !create || (!errors.Is(err, windows.ERROR_FILE_EXISTS) && !errors.Is(err, windows.ERROR_ALREADY_EXISTS)) {
		return nil, false, err
	}
	h, err = windows.CreateFile(p, access, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, false, err
	}
	return os.NewFile(uintptr(h), path), false, nil
}

func lockFileExclusiveNonBlocking(f *os.File) (ok bool, err error) {
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

// lockFileExclusive blocks until f's target inode is exclusively locked.
// Callers should hold it only across one short file update.
func lockFileExclusive(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, overlapped,
	)
}

// unlockFile drops the lock.
func unlockFile(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped)
}
