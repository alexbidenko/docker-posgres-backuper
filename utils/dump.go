package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func Cleanup(database, backupPath string) {
	files, err := os.ReadDir(backupPath + "/" + database)
	if err != nil {
		log.Println("read directory error:", err)
		return
	}

	dailyRetention := time.Now().Add(-time.Hour * 24 * 7)
	weeklyRetention := time.Now().Add(-time.Hour * 24 * 30)
	monthlyRetention := time.Now().Add(-time.Hour * 24 * 365)

	for _, file := range files {
		i, err := file.Info()
		if err != nil {
			log.Println("read file info error:", err)
			break
		}

		backupType := strings.Split(file.Name(), "_")[1]

		if (backupType == "daily" && i.ModTime().Before(dailyRetention)) ||
			(backupType == "weekly" && i.ModTime().Before(weeklyRetention)) ||
			(backupType == "monthly" && i.ModTime().Before(monthlyRetention)) {

			if err = os.Remove(backupPath + "/" + database + "/" + file.Name()); err != nil {
				log.Println("remove file error:", err)
			}
		}
	}
}

func Dump(database, backupPath, backupType string, withCopying bool, databaseList []string) {
	filename := "file_" + backupType + "_" + time.Now().Format(time.RFC3339) + ".dump"

	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		dumpCommand := exec.Command(
			"pg_dump",
			"-c",
			"-Fc",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", getDatabaseEnv(item, "POSTGRES_HOST"),
			"-f", backupPath+"/"+item+"/"+filename,
		)
		dumpCommand.Env = append(dumpCommand.Env, "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			fmt.Println("create backup error:", err, string(message))
		}

		if withCopying {
			copyCommand := exec.Command(
				"cp",
				backupPath+"/"+item+"/"+filename,
				BaseSharedDirectoryPath+"/"+item+"/file.dump",
				"-f",
			)
			if message, err := copyCommand.CombinedOutput(); err != nil {
				fmt.Println("copying to shared directory error:", err, string(message))
			}
		}

		Cleanup(item, backupPath)
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
