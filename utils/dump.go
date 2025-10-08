package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"docker-postgres-backuper/internal/storage"
)

func Dump(database, backupType string, storages []storage.Storage, shared storage.Storage, copyToShared bool, databaseList []string) {
	if len(storages) == 0 {
		log.Println("no storage destinations configured, skipping dump")
		return
	}

	filename := fmt.Sprintf("file_%s_%s.dump", backupType, time.Now().Format(time.RFC3339))
	targets := []string{database}
	if database == "--all" {
		targets = databaseList
	}

	for _, item := range targets {
		if item == "" {
			continue
		}

		tempDir, err := os.MkdirTemp("", "pgdump-")
		if err != nil {
			log.Println("create temp directory error:", err)
			continue
		}

		dumpPath := filepath.Join(tempDir, filename)
		if err := runDump(item, dumpPath); err != nil {
			log.Println("create backup error:", err)
			_ = os.RemoveAll(tempDir)
			continue
		}

		for _, destination := range storages {
			if err := destination.Store(item, filename, dumpPath); err != nil {
				log.Printf("store backup in %s error: %v\n", destination.Name(), err)
			}
		}

		if copyToShared && shared != nil {
			if err := shared.Store(item, "file.dump", dumpPath); err != nil {
				log.Printf("copying to shared storage error: %v\n", err)
			}
		}

		now := time.Now()
		for _, destination := range storages {
			if err := destination.Cleanup(item, now); err != nil {
				log.Printf("cleanup in %s error: %v\n", destination.Name(), err)
			}
		}

		if err := os.RemoveAll(tempDir); err != nil {
			log.Println("cleanup temp directory error:", err)
		}
	}
}

func runDump(database, dumpPath string) error {
	dumpCommand := exec.Command(
		"pg_dump",
		"-c",
		"-Fc",
		"-U", getDatabaseEnv(database, "POSTGRES_USER"),
		"-h", getDatabaseEnv(database, "POSTGRES_HOST"),
		"-d", getDatabaseEnv(database, "POSTGRES_DB"),
		"-f", dumpPath,
	)
	dumpCommand.Env = append(os.Environ(), "PGPASSWORD="+getDatabaseEnv(database, "POSTGRES_PASSWORD"))

	if message, err := dumpCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(message))
	}
	return nil
}

func GetBackupType() string {
	now := time.Now()
	day := now.Day()
	weekday := now.Weekday()

	if day == 1 {
		return "monthly"
	}
	if weekday == time.Saturday {
		return "weekly"
	}
	return "daily"
}
