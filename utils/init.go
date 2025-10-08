package utils

import (
	"fmt"
	"os"
	"strings"

	"docker-postgres-backuper/internal/storage"
)

func getDatabaseEnv(database, env string) string {
	value := os.Getenv(strings.ToUpper(strings.ReplaceAll(database, "-", "_")) + "_" + env)
	if value != "" {
		return value
	}
	if env == "POSTGRES_HOST" {
		return database
	}
	return "postgres"
}

func Initialize(databaseList []string, storages []storage.Storage) {
	for _, destination := range storages {
		if err := destination.Prepare(databaseList); err != nil {
			fmt.Println("prepare destination error:", destination.Name(), err)
		}
	}
}
