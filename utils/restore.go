package utils

import (
	"fmt"
	"os/exec"
)

func Restore(database, backupPath, filename string, databaseList []string) {
	list := []string{database}
	if database == "--all" {
		list = databaseList
	}

	for _, item := range list {
		dumpCommand := exec.Command(
			"pg_restore",
			"-c",
			"-U", getDatabaseEnv(item, "POSTGRES_USER"),
			"-h", item,
			"-d", getDatabaseEnv(item, "POSTGRES_DB"),
			backupPath+"/"+item+"/"+filename,
		)
		dumpCommand.Env = append(dumpCommand.Env, "PGPASSWORD="+getDatabaseEnv(item, "POSTGRES_PASSWORD"))
		if message, err := dumpCommand.CombinedOutput(); err != nil {
			fmt.Println("restore backup error:", err, string(message))
		}
	}
}
