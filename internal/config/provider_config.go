package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func loadProviderConfig(configDir string) (map[string]any, error) {
	cfgPath := filepath.Join(configDir, "qoder.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
