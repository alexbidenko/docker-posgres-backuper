package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Local struct {
	basePath string
}

func NewLocal(basePath string) *Local {
	return &Local{basePath: basePath}
}

func (l *Local) Name() string {
	return fmt.Sprintf("local:%s", l.basePath)
}

func (l *Local) BasePath() string {
	return l.basePath
}

func (l *Local) Prepare(databases []string) error {
	for _, database := range databases {
		dir := filepath.Join(l.basePath, database)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

func (l *Local) Store(database, filename, sourcePath string) error {
	destinationDir := filepath.Join(l.basePath, database)
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return fmt.Errorf("ensure destination directory %s: %w", destinationDir, err)
	}

	destinationPath := filepath.Join(destinationDir, filename)
	if err := copyFile(sourcePath, destinationPath); err != nil {
		return fmt.Errorf("copy backup to %s: %w", destinationPath, err)
	}
	return nil
}

func (l *Local) Cleanup(database string, now time.Time) error {
	directory := filepath.Join(l.basePath, database)
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read directory %s: %w", directory, err)
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read file info for %s: %w", entry.Name(), err)
		}

		backupType, ok := parseBackupType(entry.Name())
		if !ok {
			continue
		}

		if shouldRemove(backupType, info.ModTime(), now) {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
				return fmt.Errorf("remove outdated backup %s: %w", entry.Name(), err)
			}
		}
	}

	return nil
}

func copyFile(source, destination string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() {
		_ = dst.Close()
	}()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	return dst.Close()
}
