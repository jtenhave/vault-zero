package synclock

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func GetSyncLock(archiveID string) (uintptr, error) {
	name := windows.StringToUTF16Ptr(fmt.Sprintf(`Global\vaultzero-%s`, archiveID))
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(handle)
		return 0, fmt.Errorf("synclock_windows.GetSyncLock: already running for archive ID: %s", archiveID)
	}

	if err != nil {
		return 0, fmt.Errorf("synclock_windows.GetSyncLock: failed to create mutex: %w", err)
	}
	return uintptr(handle), nil
}

func ReleaseSyncLock(handle uintptr) error {
	return windows.CloseHandle(windows.Handle(handle))
}
