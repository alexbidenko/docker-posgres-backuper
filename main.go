package main

import (
	"docker-postgres-backuper/storage"
	"docker-postgres-backuper/utils"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 1 {
		panic("uncorrected command")
	}

	s3AccessKeyID, err := getEnvOrFile("S3_ACCESS_KEY_ID")
	if err != nil {
		panic(err)
	}
	s3SecretAccessKey, err := getEnvOrFile("S3_SECRET_ACCESS_KEY")
	if err != nil {
		panic(err)
	}

	var databaseList []string
	if os.Getenv("DATABASE_LIST") != "" {
		databaseList = strings.Split(os.Getenv("DATABASE_LIST"), ",")
	}

	command := os.Args[1]
	if !(command == "start" || (len(os.Args) > 2 && ((command == "restore" && len(os.Args) > 3) || command == "list" || command == "dump"))) {
		panic("uncorrected command")
	}

	backupPath := "backup-data"
	if os.Getenv("MODE") == "production" {
		backupPath = utils.BaseBackupDirectoryPath
	}
	provider, err := storage.NewProvider(os.Getenv("BACKUP_TARGET"), storage.Config{
		Local: storage.LocalConfig{BasePath: backupPath},
		S3: storage.S3Config{
			Bucket:          os.Getenv("S3_BUCKET"),
			Prefix:          os.Getenv("S3_PREFIX"),
			Region:          os.Getenv("S3_REGION"),
			Endpoint:        os.Getenv("S3_ENDPOINT"),
			AccessKeyID:     s3AccessKeyID,
			SecretAccessKey: s3SecretAccessKey,
			UseTLS:          boolEnv("S3_USE_TLS", true),
			ForcePathStyle:  boolEnv("S3_FORCE_PATH_STYLE", false),
		},
	})
	if err != nil {
		panic(err)
	}

	if command == "list" {
		utils.List(provider, os.Args[2])
		return
	}

	if command == "restore" {
		utils.Restore(provider, os.Args[2], os.Args[3], []string{})
		return
	}

	if command == "dump" {
		utils.Dump(provider, os.Args[2], "manual", databaseList)
		return
	}

	utils.Initialize(provider, databaseList)

	fmt.Println("Program started...")

	for range time.Tick(time.Hour) {
		if time.Now().Hour()%utils.IntervalInHours == 3 && os.Getenv("MODE") == "production" {
			utils.Dump(provider, "--all", utils.GetBackupType(), databaseList)
		}
	}
}

func boolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func getEnvOrFile(key string) (string, error) {
	filePath := os.Getenv(key + "_FILE")
	if filePath != "" {
		value, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", key+"_FILE", err)
		}
		return strings.TrimRight(string(value), "\r\n"), nil
	}

	return os.Getenv(key), nil
}
