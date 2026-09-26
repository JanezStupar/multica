//go:build !windows

package execenv

import (
	"os"

	"golang.org/x/sys/unix"
)

// openTargetFileForLock opens the target itself so an advisory lock is tied to
// the same inode as other cooperative writers (including helpers using
// fcntl.flock). When create is true, created reports whether this call created
// the file rather than opening one that already existed.
func openTargetFileForLock(path string, create bool) (f *os.File, created bool, err error) {
	if create {
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}
	}
	f, err = os.OpenFile(path, os.O_RDWR, 0)
	return f, false, err
}

// lockFileExclusiveNonBlocking takes an exclusive advisory lock on f without
// waiting. ok is false when another process already holds it.
//
// The lock is released by the kernel when the file is closed OR when the
// holding process dies, which is the whole reason this is a lock and not
// another marker file: it answers "is the previous execution still alive?"
// without a heartbeat, a PID table, or a stale-state cleanup path.
func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
}

func lockFileExclusiveNonBlocking(f *os.File) (ok bool, err error) {
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return false, err
}

// lockFileExclusive blocks until f's target inode is exclusively locked.
// Callers should hold it only across one short file update.
func lockFileExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

// unlockFile drops the advisory lock. Closing the file would do it too; this
// makes the release explicit at the call site.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
