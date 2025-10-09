package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"docker-postgres-backuper/storage"
)

func Dump(provider storage.Provider, database, backupType string, databaseList []string) {
	filename := "file_" + backupType + "_" + time.Now().Format(time.RFC3339) + ".dump"

	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		tempFile, err := os.CreateTemp("", "pgdump-*.dump")
		if err != nil {
			fmt.Println("create temporary file error:", err)
			continue
		}
		tempFilePath := tempFile.Name()
		tempFile.Close()

		dumpCommand := exec.Command(
			"pg_dump",
			"-c",
			"-Fc",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", getDatabaseEnv(item, "POSTGRES_HOST"),
			"-f", tempFilePath,
		)
		dumpCommand.Env = append(dumpCommand.Env, "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		dumpCommand.Env = append(dumpCommand.Env, "PGDATABASE="+getDatabaseEnv(item, "POSTGRES_DB"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			fmt.Println("create backup error:", err, string(message))
			_ = os.Remove(tempFilePath)
			continue
		}

		if err := provider.Save(item, filename, tempFilePath); err != nil {
			fmt.Println("save backup error:", err)
		} else {
			_ = os.Remove(tempFilePath)
		}

		if err := storage.Cleanup(provider, item, time.Now()); err != nil {
			log.Println("cleanup error:", err)
		}
	}
}

func GetBackupType() string {
	now := time.Now()
	day := now.Day()
	weekday := now.Weekday()

	if day == 1 {
		return "monthly"
	} else if weekday == time.Saturday {
		return "weekly"
	} else {
		return "daily"
	}
}
