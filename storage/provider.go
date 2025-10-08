package storage

import "time"

// FileInfo represents a backup artifact in storage.
type FileInfo struct {
	Name     string
	Modified time.Time
}

// Provider describes the capabilities required by the controller to
// persist and retrieve backups.
type Provider interface {
	EnsureDatabase(database string) error
	Save(database, filename, localPath string) error
	List(database string) ([]FileInfo, error)
	Fetch(database, filename string) (localPath string, cleanup func() error, err error)
	Delete(database, filename string) error
}
