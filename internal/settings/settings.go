package settings

import (
	"encoding/json"
	"path/filepath"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/safefile"
)

type Settings struct {
	Theme string `json:"theme"`
}

func Load() Settings {
	path, err := settingsPath()
	if err != nil {
		return Settings{}
	}
	data, err := safefile.Read(path, 64<<10)
	if err != nil {
		return Settings{}
	}
	var result Settings
	if json.Unmarshal(data, &result) != nil {
		return Settings{}
	}
	return result
}

func Save(value Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return safefile.Write(path, data, 0o700, 0o600)
}

func settingsPath() (string, error) {
	configPath, err := appconfig.ConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "settings.json"), nil
}
