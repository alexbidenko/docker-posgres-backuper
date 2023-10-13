package utils

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func getDatabaseEnv(database, env string) string {
	value := os.Getenv(strings.ToUpper(strings.ReplaceAll(database, "-", "_")) + "_" + env)
	if value != "" {
		return value
	}
	return "postgres"
}

func Initialize(databaseList []string, backupPath, sharedPath string) {
	for _, database := range databaseList {
		createFolderCommand := exec.Command(
			"mkdir",
			backupPath+"/"+database,
			"-p",
		)
		if message, err := createFolderCommand.CombinedOutput(); err != nil {
			fmt.Println("create directory error:", err, string(message))
		}

		if os.Getenv("SERVER") == "production" {
			createFolderCommand = exec.Command(
				"mkdir",
				sharedPath+"/"+database,
				"-p",
			)
			if message, err := createFolderCommand.CombinedOutput(); err != nil {
				fmt.Println("create shared directory error:", err, string(message))
			}
		}
	}
}
