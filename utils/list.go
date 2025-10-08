package utils

import (
	"log"
	"sort"

	"docker-postgres-backuper/storage"
)

func List(provider storage.Provider, database string) {
	files, err := provider.List(database)
	if err != nil {
		log.Println("list backups error:", err)
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	for _, file := range files {
		log.Println(file.Name)
	}
}
