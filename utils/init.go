package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"docker-postgres-backuper/storage"
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

func Initialize(provider storage.Provider, databaseList []string, sharedPath string, createShared bool) {
	for _, database := range databaseList {
		if err := provider.EnsureDatabase(database); err != nil {
			fmt.Println("ensure storage for database error:", err)
		}

		if createShared {
			if err := os.MkdirAll(filepath.Join(sharedPath, database), 0o755); err != nil {
				fmt.Println("create shared directory error:", err)
			}
		}
	}
}
