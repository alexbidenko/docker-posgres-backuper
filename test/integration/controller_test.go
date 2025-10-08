package integration

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"docker-postgres-backuper/internal/s3client"
)

const (
	dockerCommandTimeout = 5 * time.Minute
)

type controllerTestEnv struct {
	t            *testing.T
	repoRoot     string
	networkName  string
	postgresName string
	imageName    string
}

func newControllerTestEnv(t *testing.T) *controllerTestEnv {
	requireDocker(t)

	repoRoot := projectRoot(t)
	env := &controllerTestEnv{t: t, repoRoot: repoRoot}

	env.networkName = fmt.Sprintf("pgbackup-test-%d", time.Now().UnixNano())
	t.Logf("creating temporary Docker network %s", env.networkName)
	runDockerCommand(t, repoRoot, "network", "create", env.networkName)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "network", "rm", env.networkName)
	})

	env.postgresName = env.networkName + "-postgres"
	t.Logf("starting postgres container %s", env.postgresName)
	runDockerCommand(t, repoRoot,
		"run", "-d",
		"--name", env.postgresName,
		"--network", env.networkName,
		"--network-alias", "postgres-db",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=postgres",
		"postgres:15-alpine",
	)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "rm", "-f", env.postgresName)
	})

	t.Log("waiting for postgres to accept connections")
	waitForPostgresReady(t, env.postgresName)

	env.imageName = fmt.Sprintf("controller-test:%d", time.Now().UnixNano())
	t.Logf("building controller image %s", env.imageName)
	runDockerCommand(t, repoRoot,
		"build", "-t", env.imageName,
		"--build-arg", "POSTGRES_VERSION=15",
		repoRoot,
	)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "rmi", "-f", env.imageName)
	})

	return env
}

func (env *controllerTestEnv) startController(envVars map[string]string) string {
	name := fmt.Sprintf("%s-controller-%d", env.networkName, time.Now().UnixNano())
	args := []string{
		"run", "-d",
		"--name", name,
		"--network", env.networkName,
		"--network-alias", "controller",
	}
	for key, value := range envVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}
	args = append(args,
		"--entrypoint", "/bin/sh",
		env.imageName,
		"-c", "tail -f /dev/null",
	)
	runDockerCommand(env.t, env.repoRoot, args...)
	env.t.Cleanup(func() {
		runDockerCommandAllowFailure(env.t, env.repoRoot, "rm", "-f", name)
	})
	return name
}

func TestControllerLocalStorage(t *testing.T) {
	env := newControllerTestEnv(t)

	runPSQL(t, env.repoRoot, env.postgresName, "CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	runPSQL(t, env.repoRoot, env.postgresName, "TRUNCATE items;")

	initialRows := []string{"alpha", "beta"}
	t.Log("inserting initial dataset into postgres")
	insertRows(t, env.repoRoot, env.postgresName, initialRows)

	controllerName := env.startController(map[string]string{
		"DATABASE_LIST":            "testdb",
		"TESTDB_POSTGRES_USER":     "postgres",
		"TESTDB_POSTGRES_PASSWORD": "postgres",
		"TESTDB_POSTGRES_HOST":     "postgres-db",
		"TESTDB_POSTGRES_DB":       "postgres",
		"MODE":                     "production",
		"SERVER":                   "production",
	})

	t.Log("running manual dump")
	runController(t, env.repoRoot, controllerName, "./controller", "dump", "testdb")

	backupFile := latestBackup(t, env.repoRoot, controllerName, "/var/lib/postgresql/backup/data/testdb")
	if !strings.Contains(backupFile, "manual") {
		t.Fatalf("unexpected backup name: %s", backupFile)
	}

	t.Log("clearing table to validate restore")
	runPSQL(t, env.repoRoot, env.postgresName, "DELETE FROM items;")
	if rows := queryRows(t, env.repoRoot, env.postgresName); len(rows) != 0 {
		t.Fatalf("expected empty table after delete, got %v", rows)
	}

	t.Logf("restoring dump %s", backupFile)
	runController(t, env.repoRoot, controllerName, "./controller", "restore", "testdb", backupFile)

	if rows := queryRows(t, env.repoRoot, env.postgresName); !equalSlices(rows, initialRows) {
		t.Fatalf("unexpected data after restore: %v", rows)
	}

	sharedRows := []string{"charlie", "delta"}
	t.Log("preparing shared dump dataset")
	runPSQL(t, env.repoRoot, env.postgresName, "TRUNCATE items;")
	insertRows(t, env.repoRoot, env.postgresName, sharedRows)

	t.Log("creating shared dump")
	runController(t, env.repoRoot, controllerName, "./controller", "dump", "testdb", "--shared")

	ensureSharedDumpExists(t, env.repoRoot, controllerName, "/var/lib/postgresql/backup/shared/testdb/file.dump")

	t.Log("mutating table before restore-from-shared")
	runPSQL(t, env.repoRoot, env.postgresName, "TRUNCATE items;")
	insertRows(t, env.repoRoot, env.postgresName, []string{"mutated"})

	t.Log("restoring from shared dump")
	runController(t, env.repoRoot, controllerName, "./controller", "restore-from-shared", "testdb")

	if rows := queryRows(t, env.repoRoot, env.postgresName); !equalSlices(rows, sharedRows) {
		t.Fatalf("unexpected data after restore-from-shared: %v", rows)
	}
}

func TestControllerS3Storage(t *testing.T) {
	env := newControllerTestEnv(t)

	cfg, ok := loadS3TestConfig(t)
	if !ok {
		t.Skip("skipping S3 storage test: TEST_S3_* variables are not configured")
	}

	runPSQL(t, env.repoRoot, env.postgresName, "CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	runPSQL(t, env.repoRoot, env.postgresName, "TRUNCATE items;")

	initialRows := []string{"echo", "foxtrot"}
	insertRows(t, env.repoRoot, env.postgresName, initialRows)

	prefix := fmt.Sprintf("integration-tests/%d", time.Now().UnixNano())
	client := newS3Client(t, cfg)
	cleanup := func() { cleanupS3Prefix(t, client, cfg.Bucket, prefix) }
	cleanup()
	t.Cleanup(cleanup)

	controllerName := env.startController(map[string]string{
		"DATABASE_LIST":            "testdb",
		"TESTDB_POSTGRES_USER":     "postgres",
		"TESTDB_POSTGRES_PASSWORD": "postgres",
		"TESTDB_POSTGRES_HOST":     "postgres-db",
		"TESTDB_POSTGRES_DB":       "postgres",
		"MODE":                     "production",
		"SERVER":                   "production",
		"BACKUP_TARGET":            "s3",
		"S3_BUCKET":                cfg.Bucket,
		"S3_PREFIX":                prefix,
		"S3_REGION":                cfg.Region,
		"S3_ENDPOINT":              cfg.Endpoint,
		"S3_ACCESS_KEY_ID":         cfg.AccessKeyID,
		"S3_SECRET_ACCESS_KEY":     cfg.SecretAccessKey,
		"S3_USE_TLS":               strconv.FormatBool(cfg.UseTLS),
		"S3_FORCE_PATH_STYLE":      strconv.FormatBool(cfg.ForcePathStyle),
	})

	t.Log("running manual dump to s3")
	runController(t, env.repoRoot, controllerName, "./controller", "dump", "testdb")

	backups := listS3Backups(t, client, cfg.Bucket, prefix, "testdb")
	if len(backups) == 0 {
		t.Fatalf("no backups found in s3 for prefix %s", prefix)
	}
	sort.Strings(backups)
	backupFile := backups[len(backups)-1]
	if !strings.Contains(backupFile, "manual") {
		t.Fatalf("unexpected backup filename: %s", backupFile)
	}

	t.Log("clearing table before s3 restore")
	runPSQL(t, env.repoRoot, env.postgresName, "DELETE FROM items;")

	t.Log("restoring from s3 backup")
	runController(t, env.repoRoot, controllerName, "./controller", "restore", "testdb", backupFile)

	if rows := queryRows(t, env.repoRoot, env.postgresName); !equalSlices(rows, initialRows) {
		t.Fatalf("unexpected data after s3 restore: %v", rows)
	}
}

func requireDocker(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	if err := cmd.Run(); err != nil {
		t.Skipf("docker is required for integration tests: %v", err)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine caller information")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	return root
}

func runDockerCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	t.Logf("docker %s", strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("docker %v timed out after %s\n%s", args, dockerCommandTimeout, string(output))
		}
		t.Fatalf("docker %v failed: %v\n%s", args, err, string(output))
	}
	return string(output)
}

func runDockerCommandAllowFailure(t *testing.T, dir string, args ...string) {
	t.Helper()
	t.Logf("cleanup: docker %s", strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	_ = cmd.Run()
}

func waitForPostgresReady(t *testing.T, container string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		cmd := exec.Command("docker", "exec", container, "pg_isready", "-U", "postgres")
		if err := cmd.Run(); err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("postgres container %s did not become ready in time", container)
}

func runPSQL(t *testing.T, dir, container, sql string) {
	t.Helper()
	runDockerCommand(t, dir, "exec", "-u", "postgres", container, "psql", "-d", "postgres", "-c", sql)
}

func insertRows(t *testing.T, dir, container string, rows []string) {
	t.Helper()
	for _, row := range rows {
		runPSQL(t, dir, container, fmt.Sprintf("INSERT INTO items(name) VALUES ('%s');", row))
	}
}

func queryRows(t *testing.T, dir, container string) []string {
	t.Helper()
	output := runDockerCommand(t, dir, "exec", "-u", "postgres", container, "psql", "-d", "postgres", "-At", "-c", "SELECT name FROM items ORDER BY id;")
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func runController(t *testing.T, dir, container string, args ...string) {
	t.Helper()
	command := append([]string{"exec", "-u", "postgres", container}, args...)
	runDockerCommand(t, dir, command...)
}

func latestBackup(t *testing.T, dir, container, path string) string {
	t.Helper()
	output := runDockerCommand(t, dir, "exec", "-u", "postgres", container, "ls", "-1", path)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	sort.Strings(lines)
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no backups found in %s", path)
	}
	return lines[len(lines)-1]
}

func ensureSharedDumpExists(t *testing.T, dir, container, path string) {
	t.Helper()
	runDockerCommand(t, dir, "exec", "-u", "postgres", container, "test", "-f", path)
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newS3Client(t *testing.T, cfg s3TestConfig) *s3client.Client {
	t.Helper()
	client, err := s3client.New(s3client.Config{
		Endpoint:        cfg.Endpoint,
		Region:          cfg.Region,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		ForcePathStyle:  cfg.ForcePathStyle,
		UseTLS:          cfg.UseTLS,
	})
	if err != nil {
		t.Fatalf("failed to create s3 client: %v", err)
	}
	return client
}

func listS3Backups(t *testing.T, client *s3client.Client, bucket, prefix, database string) []string {
	t.Helper()
	keyPrefix := s3KeyPrefix(prefix, database)
	var results []string
	token := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		output, err := client.ListObjectsV2(ctx, bucket, keyPrefix, token)
		cancel()
		if err != nil {
			t.Fatalf("list objects: %v", err)
		}
		for _, object := range output.Objects {
			base := pathBase(object.Key)
			if base == "" {
				continue
			}
			results = append(results, base)
		}
		if !output.IsTruncated || output.NextContinuationToken == "" {
			break
		}
		token = output.NextContinuationToken
	}
	return results
}

func cleanupS3Prefix(t *testing.T, client *s3client.Client, bucket, prefix string) {
	t.Helper()
	keyPrefix := strings.Trim(prefix, "/")
	token := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		output, err := client.ListObjectsV2(ctx, bucket, keyPrefix, token)
		cancel()
		if err != nil {
			t.Fatalf("list objects for cleanup: %v", err)
		}
		if len(output.Objects) == 0 {
			break
		}
		for _, object := range output.Objects {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := client.DeleteObject(ctx, bucket, object.Key); err != nil {
				cancel()
				t.Fatalf("delete object: %v", err)
			}
			cancel()
		}
		if !output.IsTruncated || output.NextContinuationToken == "" {
			break
		}
		token = output.NextContinuationToken
	}
}

func s3KeyPrefix(prefix, database string) string {
	base := strings.Trim(prefix, "/")
	if base != "" {
		base += "/"
	}
	return fmt.Sprintf("%s%s/", base, strings.Trim(database, "/"))
}

func pathBase(key string) string {
	if idx := strings.LastIndex(key, "/"); idx != -1 {
		return key[idx+1:]
	}
	return key
}
