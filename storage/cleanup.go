package storage

import (
	"strings"
	"time"
)

// Cleanup applies the retention policy shared across providers.
func Cleanup(p Provider, database string, now time.Time) error {
	files, err := p.List(database)
	if err != nil {
		return err
	}

	dailyRetention := now.Add(-7 * 24 * time.Hour)
	weeklyRetention := now.Add(-30 * 24 * time.Hour)
	monthlyRetention := now.Add(-365 * 24 * time.Hour)
	manualRetention := now.Add(-365 * 24 * time.Hour)

	for _, file := range files {
		parts := strings.Split(file.Name, "_")
		if len(parts) < 2 {
			continue
		}
		backupType := parts[1]
		cutoff := time.Time{}
		switch backupType {
		case "daily":
			cutoff = dailyRetention
		case "weekly":
			cutoff = weeklyRetention
		case "monthly":
			cutoff = monthlyRetention
		case "manual":
			cutoff = manualRetention
		default:
			continue
		}
		if !file.Modified.IsZero() && file.Modified.Before(cutoff) {
			_ = p.Delete(database, file.Name)
		}
	}

	return nil
}
