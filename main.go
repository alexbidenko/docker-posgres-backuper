package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"docker-postgres-backuper/internal/storage"
	"docker-postgres-backuper/utils"
)

func main() {
	if len(os.Args) == 1 {
		panic("uncorrected command")
	}

	var databaseList []string
	if os.Getenv("DATABASE_LIST") != "" {
		databaseList = strings.Split(os.Getenv("DATABASE_LIST"), ",")
	}

	command := os.Args[1]
	if !(command == "start" || (len(os.Args) > 2 && ((command == "restore" && len(os.Args) > 3) || command == "list" || command == "dump" || command == "resetwal" || command == "restore-from-shared"))) {
		panic("uncorrected command")
	}

	backupPath := "backup-data"
	if os.Getenv("MODE") == "production" {
		backupPath = utils.BaseBackupDirectoryPath
	}
	sharedPath := "backup-data"
	if os.Getenv("MODE") == "production" {
		sharedPath = utils.BaseSharedDirectoryPath
	}

	localStorage := storage.NewLocal(backupPath)
	sharedStorage := storage.NewLocal(sharedPath)

	storages, localEnabled := buildStorages(localStorage)

	if command == "list" {
		utils.List(os.Args[2], localEnabled)
		return
	}

	if command == "resetwal" {
		utils.Resetwal(os.Args[2], utils.BaseDatabaseDirectoryPath)
		return
	}

	if command == "restore" {
		utils.Restore(os.Args[2], localEnabled, os.Args[3], []string{})
		return
	}

	if command == "restore-from-shared" {
		utils.Restore(os.Args[2], sharedStorage, "file.dump", databaseList)
		return
	}

	if command == "dump" {
		utils.Dump(os.Args[2], "manual", storages, sharedStorage, len(os.Args) > 3 && os.Args[3] == "--shared", databaseList)
		return
	}

	initDestinations := append([]storage.Storage{}, storages...)
	if os.Getenv("SERVER") == "production" {
		initDestinations = append(initDestinations, sharedStorage)
	}
	utils.Initialize(databaseList, initDestinations)

	fmt.Println("Program started...")

	for range time.Tick(time.Hour) {
		if time.Now().Hour()%utils.IntervalInHours == 3 && os.Getenv("MODE") == "production" {
			utils.Dump("--all", utils.GetBackupType(), storages, sharedStorage, os.Getenv("COPING_TO_SHARED") == "true", databaseList)
		}
	}
}

func buildStorages(local *storage.Local) ([]storage.Storage, *storage.Local) {
	targets := parseTargets(os.Getenv("BACKUP_TARGET"))
	if len(targets) == 0 {
		targets = []string{"local"}
	}

	storages := make([]storage.Storage, 0, len(targets))
	var localEnabled *storage.Local

	for _, target := range targets {
		switch target {
		case "local":
			storages = append(storages, local)
			localEnabled = local
		case "s3":
			s3Storage := createS3Storage()
			storages = append(storages, s3Storage)
		default:
			log.Printf("unknown BACKUP_TARGET value: %s", target)
		}
	}

	return storages, localEnabled
}

func parseTargets(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.ToLower(part))
		if trimmed != "" {
			targets = append(targets, trimmed)
		}
	}
	return targets
}

func createS3Storage() storage.Storage {
	cfg := storage.S3Config{
		Bucket:          os.Getenv("S3_BUCKET"),
		Prefix:          os.Getenv("S3_PREFIX"),
		Region:          os.Getenv("S3_REGION"),
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("S3_SESSION_TOKEN"),
		UseTLS:          parseBool(os.Getenv("S3_USE_TLS"), true),
		ForcePathStyle:  parseBool(os.Getenv("S3_FORCE_PATH_STYLE"), false),
		StorageClass:    os.Getenv("S3_STORAGE_CLASS"),
		MaxRetries:      parseInt(os.Getenv("S3_MAX_RETRIES")),
	}

	s3Storage, err := storage.NewS3(cfg)
	if err != nil {
		log.Fatalf("failed to configure S3 storage: %v", err)
	}
	return s3Storage
}

func parseBool(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func parseInt(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
