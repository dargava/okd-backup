package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupMetadata is the on-disk metadata.json structure.
type BackupMetadata struct {
	BackupID       string   `json:"backup_id"`
	CreatedAt      string   `json:"created_at"`
	ClusterVersion string   `json:"cluster_version"`
	Contents       []string `json:"contents"`
}

// BackupContext represents an open (new or existing) backup.
type BackupContext struct {
	Path     string
	Metadata BackupMetadata
}

// HasContent returns true if the named component was backed up.
func (c *BackupContext) HasContent(name string) bool {
	for _, v := range c.Metadata.Contents {
		if v == name {
			return true
		}
	}
	return false
}

// AddContent marks a component as backed up and persists metadata.
func (c *BackupContext) AddContent(name string) {
	if !c.HasContent(name) {
		c.Metadata.Contents = append(c.Metadata.Contents, name)
	}
	_ = c.writeMetadata()
}

func (c *BackupContext) writeMetadata() error {
	data, err := json.MarshalIndent(c.Metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.Path, "metadata.json"), data, 0644)
}

// DiskSize returns the total bytes used by this backup.
func (c *BackupContext) DiskSize() int64 {
	var total int64
	filepath.Walk(c.Path, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// Summary returns a human-readable summary string.
func (c *BackupContext) Summary() string {
	contents := strings.Join(c.Metadata.Contents, ", ")
	if contents == "" {
		contents = "(empty)"
	}
	return fmt.Sprintf(
		"%sBackup: %s%s\n  Created:  %s\n  Cluster:  %s\n  Contents: %s",
		colorBold, c.Metadata.BackupID, colorReset,
		c.Metadata.CreatedAt,
		c.Metadata.ClusterVersion,
		contents,
	)
}

// BackupStorage manages the backup root directory.
type BackupStorage struct {
	Root string
}

// NewBackupStorage creates a BackupStorage for root, resolving relative paths.
func NewBackupStorage(root string) (*BackupStorage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("cannot create backup directory %s: %w", abs, err)
	}
	return &BackupStorage{Root: abs}, nil
}

// NewBackup creates a new backup directory with a timestamped ID.
func (s *BackupStorage) NewBackup(clusterVersion string) (*BackupContext, error) {
	id := time.Now().Format("2006-01-02_1504")
	path := filepath.Join(s.Root, id)
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	ctx := &BackupContext{
		Path: path,
		Metadata: BackupMetadata{
			BackupID:       id,
			CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			ClusterVersion: clusterVersion,
			Contents:       []string{},
		},
	}
	if err := ctx.writeMetadata(); err != nil {
		return nil, err
	}
	return ctx, nil
}

// OpenBackup opens an existing backup by ID.
func (s *BackupStorage) OpenBackup(id string) (*BackupContext, error) {
	path := filepath.Join(s.Root, id)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("backup not found: %s (looked in %s)", id, s.Root)
	}

	metaFile := filepath.Join(path, "metadata.json")
	data, err := os.ReadFile(metaFile)
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	var meta BackupMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	return &BackupContext{Path: path, Metadata: meta}, nil
}

// ListBackups returns all backups sorted newest first.
func (s *BackupStorage) ListBackups() ([]BackupMetadata, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}

	var result []BackupMetadata
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaFile := filepath.Join(s.Root, e.Name(), "metadata.json")
		data, err := os.ReadFile(metaFile)
		if err != nil {
			continue
		}
		var meta BackupMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		result = append(result, meta)
	}

	// Sort newest first (backup IDs are timestamps, so lexicographic reverse works)
	sort.Slice(result, func(i, j int) bool {
		return result[i].BackupID > result[j].BackupID
	})

	return result, nil
}

// fmtSize formats bytes into a human-readable string.
func fmtSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case size >= TB:
		return fmt.Sprintf("%.1f TB", float64(size)/TB)
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
