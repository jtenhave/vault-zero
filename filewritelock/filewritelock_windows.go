package filewritelock

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func GetFileWriteLock(path string) (uintptr, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ|windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ, // Allow readers, deny writers and deleters
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)

	if err != nil {
		return 0, fmt.Errorf("filewritelock_windows.GetFileWriteLock: failed to get lock: %w", err)
	}

	handleInt := uintptr(handle)

	return handleInt, nil
}

func ReleaseFileWriteLock(handle uintptr) error {
	err := windows.CloseHandle(windows.Handle(handle))
	if err != nil {
		return fmt.Errorf("filewritelock_windows.ReleaseFileWriteLock: failed to release lock: %w", err)
	}

	return nil
}
