package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

type cachedConfig struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// CacheGood saves validated text with its original path for relative dirs.
func CacheGood(stateDir, path string, data []byte) error {
	if stateDir == "" {
		return nil
	}
	if _, err := Parse(path, data); err != nil {
		return err
	}
	content, err := json.Marshal(cachedConfig{Path: path, Text: string(data)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, paths.DirMode); err != nil {
		return err
	}
	dest := filepath.Join(stateDir, "last-good-config.json")
	if err := os.WriteFile(dest+".tmp", content, 0o600); err != nil {
		return err
	}
	return os.Rename(dest+".tmp", dest)
}

// LoadRecoverable always returns usable settings. Its error describes the
// requested file, which remains editable while the last good settings run.
func LoadRecoverable(path, stateDir string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var cfg *Config
		cfg, err = Parse(path, data)
		if err == nil {
			if err := CacheGood(stateDir, path, data); err != nil {
				log.Printf("cache config: %v", err)
			}
			return cfg, nil
		}
	}
	original := err
	var cached cachedConfig
	if data, err := os.ReadFile(filepath.Join(stateDir, "last-good-config.json")); err == nil && json.Unmarshal(data, &cached) == nil && filepath.Clean(cached.Path) == filepath.Clean(path) {
		if cfg, err := Parse(path, []byte(cached.Text)); err == nil {
			return cfg, original
		}
	}
	cfg, _ := Parse(path, nil)
	if port, err := ReadPort(path); err == nil {
		cfg.Port = port
	}
	return cfg, original
}

// ReadPort reads just the dashboard port even when server validation fails.
func ReadPort(path string) (int, error) {
	var raw struct {
		Port int `toml:"port"`
	}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return 0, err
	}
	if raw.Port == 0 {
		return app.DefaultPort, nil
	}
	if raw.Port < 1 || raw.Port > 65535 {
		return 0, fmt.Errorf("dashboard port %d out of range", raw.Port)
	}
	return raw.Port, nil
}
