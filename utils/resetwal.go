package utils

import (
	"fmt"
	"os/exec"
	"strings"
)

func Resetwal(database, databasePath string) {
	resetwalCommand := exec.Command(
		"su",
		"postgres",
		"-c",
		strings.Join([]string{"pg_resetwal", databasePath + "/" + database, "-f"}, " "),
	)
	if message, err := resetwalCommand.CombinedOutput(); err != nil {
		fmt.Println("resetwal database error:", err, string(message))
	}
}
