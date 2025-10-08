package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type localProvider struct {
	basePath string
}

// NewLocalProvider creates a provider that persists backups on the filesystem.
func NewLocalProvider(basePath string) Provider {
	return &localProvider{basePath: basePath}
}

func (p *localProvider) EnsureDatabase(database string) error {
	return os.MkdirAll(p.databasePath(database), 0o755)
}

func (p *localProvider) Save(database, filename, localPath string) error {
	destPath := filepath.Join(p.databasePath(database), filename)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.Rename(localPath, destPath); err != nil {
		if err := copyFile(localPath, destPath); err != nil {
			return err
		}
		if err := os.Remove(localPath); err != nil {
			return fmt.Errorf("remove temporary file: %w", err)
		}
		return nil
	}
	return nil
}

func (p *localProvider) List(database string) ([]FileInfo, error) {
	entries, err := os.ReadDir(p.databasePath(database))
	if err != nil {
		return nil, err
	}
	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		fi, err := entry.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, FileInfo{Name: entry.Name(), Modified: fi.ModTime()})
	}
	return infos, nil
}

func (p *localProvider) Fetch(database, filename string) (string, func() error, error) {
	path := filepath.Join(p.databasePath(database), filename)
	if _, err := os.Stat(path); err != nil {
		return "", nil, err
	}
	return path, func() error { return nil }, nil
}

func (p *localProvider) Delete(database, filename string) error {
	return os.Remove(filepath.Join(p.databasePath(database), filename))
}

func (p *localProvider) databasePath(database string) string {
	return filepath.Join(p.basePath, database)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy file contents: %w", err)
	}

	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync destination file: %w", err)
	}

	return nil
}
