package filemetadata

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func getFilesystemType(path string) (string, error) {
	driveLetter, err := driveLetterFromPath(path)
	if err != nil {
		return "", fmt.Errorf("filemetadataprovider_windows.getFilesystemType: failed to get drive letter from path: %w", err)
	}

	volumePath := windows.StringToUTF16Ptr(driveLetter)

	var fsName [windows.MAX_PATH + 1]uint16

	err = windows.GetVolumeInformation(
		volumePath,
		nil,
		0,
		nil,
		nil,
		nil,
		&fsName[0],
		uint32(len(fsName)),
	)
	if err != nil {
		return "", fmt.Errorf("filemetadataprovider_windows.getFilesystemType: failed to get volume information: %w", err)
	}

	return windows.UTF16ToString(fsName[:]), nil
}

func driveLetterFromPath(path string) (string, error) {
	if len(path) < 2 || path[1] != ':' {
		return "", fmt.Errorf("filemetadataprovider_windows.driveLetterFromPath: path is not a valid drive letter path")
	}
	return path[:2], nil
}
