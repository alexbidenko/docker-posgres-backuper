package main

import (
	"docker-postgres-backuper/utils"
	"fmt"
	"os"
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
	if !(command == "start" || (len(os.Args) > 2 && ((command == "restore" && len(os.Args) > 3) || command == "list" || command == "dump" || command == "resetwal" || command == "restore-from-prod"))) {
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

	if command == "list" {
		utils.List(os.Args[2], backupPath)
		return
	}

	if command == "resetwal" {
		utils.Resetwal(os.Args[2], utils.BaseDatabaseDirectoryPath)
		return
	}

	if command == "restore" {
		utils.Restore(os.Args[2], backupPath, os.Args[3], []string{})
		return
	}

	if command == "restore-from-shared" {
		utils.Restore(os.Args[2], sharedPath, "file.dump", databaseList)
		return
	}

	if command == "dump" {
		utils.Dump(os.Args[2], backupPath, "manual", len(os.Args) > 3 && os.Args[3] == "--shared", databaseList)
		return
	}

	utils.Initialize(databaseList, backupPath, sharedPath)

	fmt.Println("Program started...")

	for range time.Tick(time.Hour) {
		if time.Now().Hour()%utils.IntervalInHours == 3 && os.Getenv("MODE") == "production" {
			utils.Dump("--all", backupPath, utils.GetBackupType(), os.Getenv("COPING_TO_SHARED") == "true", databaseList)
		}
	}
}
