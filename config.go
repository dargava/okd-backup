package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var validConfigKeys = map[string]bool{
	"backup_dir":    true,
	"control_plane": true,
	"ssh_key":       true,
	"ssh_user":      true,
}

var (
	defaultConfigPath = filepath.Join(homeDir(), ".config", "okd-backup", "config.yaml")
	localConfigPath   = "okd-backup.yaml"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}

// findConfigFile returns the path to the active config file, or "" if none.
func findConfigFile(configPath string) (string, error) {
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}
		return "", fmt.Errorf("config file not found: %s", configPath)
	}

	if env := os.Getenv("OKD_BACKUP_CONFIG"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}

	if _, err := os.Stat(localConfigPath); err == nil {
		return localConfigPath, nil
	}

	if _, err := os.Stat(defaultConfigPath); err == nil {
		return defaultConfigPath, nil
	}

	return "", nil
}

// loadConfig loads the config file and returns a map of key->value.
func loadConfig(configPath string) (map[string]string, error) {
	path, err := findConfigFile(configPath)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var result map[string]string
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	if result == nil {
		result = map[string]string{}
	}
	return result, nil
}

// saveConfig writes the config map to a YAML file. Returns the path written.
func saveConfig(values map[string]string, target string) (string, error) {
	if target == "" {
		target = defaultConfigPath
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}

	data, err := yaml.Marshal(values)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(target, data, 0644); err != nil {
		return "", err
	}

	return target, nil
}

const defaultBackupDir = "/mnt/nfs/okd-backups"

// resolveBackupDir returns the effective backup directory using this priority:
// 1. Explicit --backup-dir (non-default)
// 2. backup_dir from config file
// 3. Current working directory (if default path does not exist)
// 4. defaultBackupDir
func resolveBackupDir(backupDir, configPath string) string {
	if backupDir != defaultBackupDir {
		return backupDir
	}

	cfg, err := loadConfig(configPath)
	if err == nil {
		if v, ok := cfg["backup_dir"]; ok && v != "" {
			logVerbose(fmt.Sprintf("Using backup_dir from config: %q", v))
			return v
		}
	}

	if _, err := os.Stat(defaultBackupDir); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		logVerbose(fmt.Sprintf("Default backup dir %q not found, using current directory: %q", defaultBackupDir, cwd))
		return cwd
	}

	return defaultBackupDir
}
