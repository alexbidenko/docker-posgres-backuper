package utils

import (
	"log"
	"os"
	"path/filepath"

	"docker-postgres-backuper/internal/storage"
)

func List(database string, local *storage.Local) {
	if local == nil {
		log.Println("local storage is disabled; listing is unavailable")
		return
	}

	directory := filepath.Join(local.BasePath(), database)
	files, err := os.ReadDir(directory)
	if err != nil {
		log.Println("read directory error:", err)
		return
	}

	for _, file := range files {
		log.Println(file.Name())
	}
}
