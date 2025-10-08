package utils

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"docker-postgres-backuper/storage"
)

func Restore(provider storage.Provider, database, filename string, databaseList []string) {
	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		localPath, cleanup, err := provider.Fetch(item, filename)
		if err != nil {
			fmt.Println("fetch backup error:", err)
			continue
		}

		dumpCommand := exec.Command(
			"pg_restore",
			"-c",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", getDatabaseEnv(item, "POSTGRES_HOST"),
			"-d", getDatabaseEnv(item, "POSTGRES_DB"),
			localPath,
		)
		dumpCommand.Env = append(dumpCommand.Env, "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			fmt.Println("restore backup error:", err, string(message))
		}

		if cleanup != nil {
			if err := cleanup(); err != nil {
				fmt.Println("cleanup temporary file error:", err)
			}
		}
	}
}

func RestoreFromShared(database, sharedPath, filename string, databaseList []string) {
	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		dumpCommand := exec.Command(
			"pg_restore",
			"-c",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", getDatabaseEnv(item, "POSTGRES_HOST"),
			"-d", getDatabaseEnv(item, "POSTGRES_DB"),
			filepath.Join(sharedPath, item, filename),
		)
		dumpCommand.Env = append(dumpCommand.Env, "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			fmt.Println("restore shared backup error:", err, string(message))
		}
	}
}
