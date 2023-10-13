package utils

import (
	"log"
	"os"
)

func List(database, backupPath string) {
	files, err := os.ReadDir(backupPath + "/" + database)
	if err != nil {
		log.Println("read directory error:", err)
		return
	}

	for _, file := range files {
		log.Println(file.Name())
	}
}
