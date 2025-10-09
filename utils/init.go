package utils

import (
	"fmt"
	"os"
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

func Initialize(provider storage.Provider, databaseList []string) {
	for _, database := range databaseList {
		if err := provider.EnsureDatabase(database); err != nil {
			fmt.Println("ensure storage for database error:", err)
		}
	}
}
