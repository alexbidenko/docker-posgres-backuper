package utils

import (
	"fmt"
	"os"
	"strings"

	"docker-postgres-backuper/storage"
)

func getDatabaseEnv(database, env string) string {
	value := os.Getenv(databaseEnvKey(database, env))
	if value != "" {
		return value
	}
	if env == "POSTGRES_HOST" {
		return database
	}
	return "postgres"
}

func getDatabasePassword(database string) (string, error) {
	secretPath := os.Getenv(databaseEnvKey(database, "POSTGRES_PASSWORD_FILE"))
	if secretPath != "" {
		password, err := os.ReadFile(secretPath)
		if err != nil {
			return "", fmt.Errorf("read password secret file %s: %w", secretPath, err)
		}
		return strings.TrimRight(string(password), "\r\n"), nil
	}

	return getDatabaseEnv(database, "POSTGRES_PASSWORD"), nil
}

func databaseEnvKey(database, env string) string {
	return strings.ToUpper(strings.ReplaceAll(database, "-", "_")) + "_" + env
}

func Initialize(provider storage.Provider, databaseList []string) {
	for _, database := range databaseList {
		if err := provider.EnsureDatabase(database); err != nil {
			fmt.Println("ensure storage for database error:", err)
		}
	}
}
