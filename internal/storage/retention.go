package storage

import (
	"strings"
	"time"
)

func parseBackupType(filename string) (string, bool) {
	parts := strings.Split(filename, "_")
	if len(parts) < 3 {
		return "", false
	}
	return parts[1], true
}

func shouldRemove(backupType string, modifiedAt, now time.Time) bool {
	switch backupType {
	case "daily":
		return modifiedAt.Before(now.Add(-7 * 24 * time.Hour))
	case "weekly":
		return modifiedAt.Before(now.Add(-30 * 24 * time.Hour))
	case "monthly":
		return modifiedAt.Before(now.Add(-365 * 24 * time.Hour))
	default:
		return false
	}
}
