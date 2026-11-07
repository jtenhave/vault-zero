package vaultzero

import (
	"encoding/json"
	"fmt"
	"os"
)

const settingsDirName = "VaultZero"

type settingsFileData struct {
	ID              string `json:"id"`
	BucketName      string `json:"bucket_name"`
	AccessKey       string `json:"access_key"`
	SecretAccessKey string `json:"secret_access_key"`
}

func getSettingsDirPath() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %s", err)
	}

	return fmt.Sprintf("%s\\%s", userConfigDir, settingsDirName), nil
}

func getSettingsFilePath(archiveID string) (string, error) {
	settingsDirPath, err := getSettingsDirPath()
	if err != nil {
		return "", fmt.Errorf("settingsfile.getSettingsFilePath: failed to get settings dir path: %w", err)
	}

	return fmt.Sprintf("%s\\%s.json", settingsDirPath, archiveID), nil
}

func getSettingsFileData(archiveID string) (*settingsFileData, error) {
	settingsFilePath, err := getSettingsFilePath(archiveID)
	if err != nil {
		return nil, fmt.Errorf("settingsfile.getSettingsFileData: failed to get settings file path: %w", err)
	}

	settingsFileRaw, err := os.ReadFile(settingsFilePath)
	if err != nil {
		return nil, fmt.Errorf("settingsfile.getSettingsFileData: failed to read settings file: %w", err)
	}

	var settingsFileData settingsFileData
	err = json.Unmarshal(settingsFileRaw, &settingsFileData)
	if err != nil {
		return nil, fmt.Errorf("settingsfile.getSettingsFileData: failed to unmarshal settings file data: %w", err)
	}

	if settingsFileData.AccessKey == "" || settingsFileData.SecretAccessKey == "" || settingsFileData.BucketName == "" {
		return nil, fmt.Errorf("settingsfile.getSettingsFileData: settings file data is missing required fields")
	}

	return &settingsFileData, nil
}

func setSettingsFileData(archiveID string, data settingsFileData) (func() error, error) {
	settingsFilePath, err := getSettingsFilePath(archiveID)

	settingsFileDataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("settingsfile.setSettingsFileData: error marshaling settings file data to JSON: %w", err)
	}
	err = writeNewLocalFile(settingsFilePath, settingsFileDataBytes)
	if err != nil {
		return nil, fmt.Errorf("settingsfile.setSettingsFileData: failed to write settings file: %w", err)
	}

	return func() error {
		err := os.Remove(settingsFilePath)
		if err != nil {
			return fmt.Errorf("settingsfile.setSettingsFileData: failed to delete settings file: %w", err)
		}

		return nil
	}, nil

}