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

	provider, err := storage.NewProvider(os.Getenv("BACKUP_TARGET"), storage.Config{
		Local: storage.LocalConfig{BasePath: backupPath},
		S3: storage.S3Config{
			Bucket:          os.Getenv("S3_BUCKET"),
			Prefix:          os.Getenv("S3_PREFIX"),
			Region:          os.Getenv("S3_REGION"),
			Endpoint:        os.Getenv("S3_ENDPOINT"),
			AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
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

	if command == "resetwal" {
		utils.Resetwal(os.Args[2], utils.BaseDatabaseDirectoryPath)
		return
	}

	if command == "restore" {
		utils.Restore(provider, os.Args[2], os.Args[3], []string{})
		return
	}

	if command == "restore-from-shared" {
		utils.RestoreFromShared(os.Args[2], sharedPath, "file.dump", databaseList)
		return
	}

	if command == "dump" {
		utils.Dump(provider, os.Args[2], "manual", len(os.Args) > 3 && os.Args[3] == "--shared", databaseList, sharedPath)
		return
	}

	utils.Initialize(provider, databaseList, sharedPath, os.Getenv("SERVER") == "production")

	fmt.Println("Program started...")

	for range time.Tick(time.Hour) {
		if time.Now().Hour()%utils.IntervalInHours == 3 && os.Getenv("MODE") == "production" {
			utils.Dump(provider, "--all", utils.GetBackupType(), os.Getenv("COPING_TO_SHARED") == "true", databaseList, sharedPath)
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
