package integration

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const dockerCommandTimeout = 5 * time.Minute

func TestControllerEndToEnd(t *testing.T) {
	requireDocker(t)

	repoRoot := projectRoot(t)

	networkName := fmt.Sprintf("pgbackup-test-%d", time.Now().UnixNano())
	t.Logf("creating temporary Docker network %s", networkName)
	runDockerCommand(t, repoRoot, "network", "create", networkName)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "network", "rm", networkName)
	})

	postgresName := networkName + "-postgres"
	t.Logf("starting postgres container %s", postgresName)
	runDockerCommand(t, repoRoot,
		"run", "-d",
		"--name", postgresName,
		"--network", networkName,
		"--network-alias", "postgres-db",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=postgres",
		"postgres:15-alpine",
	)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "rm", "-f", postgresName)
	})

	t.Log("waiting for postgres to accept connections")
	waitForPostgresReady(t, postgresName)

	runPSQL(t, repoRoot, postgresName, "CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	runPSQL(t, repoRoot, postgresName, "TRUNCATE items;")

	initialRows := []string{"alpha", "beta"}
	t.Log("inserting initial dataset into postgres")
	insertRows(t, repoRoot, postgresName, initialRows)

	imageName := fmt.Sprintf("controller-test:%d", time.Now().UnixNano())
	t.Logf("building controller image %s", imageName)
	runDockerCommand(t, repoRoot,
		"build", "-t", imageName,
		"--build-arg", "POSTGRES_VERSION=15",
		repoRoot,
	)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "rmi", "-f", imageName)
	})

	controllerName := networkName + "-controller"
	t.Logf("starting controller container %s", controllerName)
	runDockerCommand(t, repoRoot,
		"run", "-d",
		"--name", controllerName,
		"--network", networkName,
		"--network-alias", "controller",
		"-e", "DATABASE_LIST=testdb",
		"-e", "TESTDB_POSTGRES_USER=postgres",
		"-e", "TESTDB_POSTGRES_PASSWORD=postgres",
		"-e", "TESTDB_POSTGRES_HOST=postgres-db",
		"-e", "TESTDB_POSTGRES_DB=postgres",
		"-e", "MODE=production",
		"-e", "SERVER=production",
		"--entrypoint", "/bin/sh",
		imageName,
		"-c", "tail -f /dev/null",
	)
	t.Cleanup(func() {
		runDockerCommandAllowFailure(t, repoRoot, "rm", "-f", controllerName)
	})

	t.Log("preparing backup directories inside controller")
	createControllerDirectories(t, repoRoot, controllerName)

	t.Log("running manual dump")
	runController(t, repoRoot, controllerName, "./controller", "dump", "testdb")

	backupFile := latestBackup(t, repoRoot, controllerName, "/var/lib/postgresql/backup/data/testdb")
	if !strings.Contains(backupFile, "manual") {
		t.Fatalf("unexpected backup name: %s", backupFile)
	}

	t.Log("clearing table to validate restore")
	runPSQL(t, repoRoot, postgresName, "DELETE FROM items;")
	if rows := queryRows(t, repoRoot, postgresName); len(rows) != 0 {
		t.Fatalf("expected empty table after delete, got %v", rows)
	}

	t.Logf("restoring dump %s", backupFile)
	runController(t, repoRoot, controllerName, "./controller", "restore", "testdb", backupFile)

	if rows := queryRows(t, repoRoot, postgresName); !equalSlices(rows, initialRows) {
		t.Fatalf("unexpected data after restore: %v", rows)
	}

	sharedRows := []string{"charlie", "delta"}
	t.Log("preparing shared dump dataset")
	runPSQL(t, repoRoot, postgresName, "TRUNCATE items;")
	insertRows(t, repoRoot, postgresName, sharedRows)

	t.Log("creating shared dump")
	runController(t, repoRoot, controllerName, "./controller", "dump", "testdb", "--shared")

	ensureSharedDumpExists(t, repoRoot, controllerName, "/var/lib/postgresql/backup/shared/testdb/file.dump")

	t.Log("mutating table before restore-from-shared")
	runPSQL(t, repoRoot, postgresName, "TRUNCATE items;")
	insertRows(t, repoRoot, postgresName, []string{"mutated"})

	t.Log("restoring from shared dump")
	runController(t, repoRoot, controllerName, "./controller", "restore-from-shared", "testdb")

	if rows := queryRows(t, repoRoot, postgresName); !equalSlices(rows, sharedRows) {
		t.Fatalf("unexpected data after restore-from-shared: %v", rows)
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

func createControllerDirectories(t *testing.T, dir, container string) {
	t.Helper()
	runDockerCommand(t, dir, "exec", "-u", "postgres", container, "mkdir", "-p", "/var/lib/postgresql/backup/data/testdb")
	runDockerCommand(t, dir, "exec", "-u", "postgres", container, "mkdir", "-p", "/var/lib/postgresql/backup/shared/testdb")
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
