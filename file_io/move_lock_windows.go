package file_io

import "golang.org/x/sys/windows"

type moveLock struct {
	handle windows.Handle
	offset windows.Overlapped
}

func TryMoveLock(path string) (Unlocker, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// share-delete keeps the locked file movable with its parent directory
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	lock := &moveLock{handle: handle}
	if err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.offset); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return lock, nil
}

func (lock *moveLock) Unlock() error {
	err := windows.UnlockFileEx(lock.handle, 0, 1, 0, &lock.offset)
	closeErr := windows.CloseHandle(lock.handle)
	if err != nil {
		return err
	}
	return closeErr
}
