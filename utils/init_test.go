package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetDatabasePasswordUsesSecretFileOverEnv(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "database_password")
	if err := os.WriteFile(secretPath, []byte("from-secret\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	t.Setenv("TESTDB_POSTGRES_PASSWORD", "from-env")
	t.Setenv("TESTDB_POSTGRES_PASSWORD_FILE", secretPath)

	password, err := getDatabasePassword("testdb")
	if err != nil {
		t.Fatalf("getDatabasePassword returned error: %v", err)
	}
	if password != "from-secret" {
		t.Fatalf("expected secret password, got %q", password)
	}
}

func TestGetDatabasePasswordFallsBackToEnv(t *testing.T) {
	t.Setenv("TESTDB_POSTGRES_PASSWORD", "from-env")

	password, err := getDatabasePassword("testdb")
	if err != nil {
		t.Fatalf("getDatabasePassword returned error: %v", err)
	}
	if password != "from-env" {
		t.Fatalf("expected env password, got %q", password)
	}
}

func TestGetDatabasePasswordReturnsErrorWhenSecretFileIsUnreadable(t *testing.T) {
	t.Setenv("TESTDB_POSTGRES_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing-secret"))

	_, err := getDatabasePassword("testdb")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
