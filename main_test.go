package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetEnvOrFileUsesFileOverEnv(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "s3_access_key")
	if err := os.WriteFile(secretPath, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	t.Setenv("S3_ACCESS_KEY_ID", "from-env")
	t.Setenv("S3_ACCESS_KEY_ID_FILE", secretPath)

	value, err := getEnvOrFile("S3_ACCESS_KEY_ID")
	if err != nil {
		t.Fatalf("getEnvOrFile returned error: %v", err)
	}
	if value != "from-file" {
		t.Fatalf("expected file value, got %q", value)
	}
}

func TestGetEnvOrFileFallsBackToEnv(t *testing.T) {
	t.Setenv("S3_SECRET_ACCESS_KEY", "from-env")

	value, err := getEnvOrFile("S3_SECRET_ACCESS_KEY")
	if err != nil {
		t.Fatalf("getEnvOrFile returned error: %v", err)
	}
	if value != "from-env" {
		t.Fatalf("expected env value, got %q", value)
	}
}

func TestGetEnvOrFileReturnsErrorWhenFileIsUnreadable(t *testing.T) {
	t.Setenv("S3_ACCESS_KEY_ID_FILE", filepath.Join(t.TempDir(), "missing-secret"))

	_, err := getEnvOrFile("S3_ACCESS_KEY_ID")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
