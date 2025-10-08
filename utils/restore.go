package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"docker-postgres-backuper/internal/storage"
)

func Restore(database string, local *storage.Local, filename string, databaseList []string) {
	if local == nil {
		log.Println("local storage is disabled; restore is unavailable")
		return
	}

	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		dumpPath := filepath.Join(local.BasePath(), item, filename)
		dumpCommand := exec.Command(
			"pg_restore",
			"-c",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", getDatabaseEnv(item, "POSTGRES_HOST"),
			"-d", getDatabaseEnv(item, "POSTGRES_DB"),
			dumpPath,
		)
		dumpCommand.Env = append(os.Environ(), "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			log.Println("restore backup error:", fmt.Errorf("%w: %s", err, string(message)))
		}
	}
}
