package storage

import "time"

type Storage interface {
	Prepare(databases []string) error
	Store(database, filename, sourcePath string) error
	Cleanup(database string, now time.Time) error
	Name() string
}
