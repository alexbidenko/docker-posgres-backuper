# Docker Postgres Backup Manager

Docker Postgres Backup Manager is a lightweight controller container that orchestrates
regular backups for one or many PostgreSQL instances. It exposes a small CLI that can
be used both for scheduled operations (inside the container) and for manual backup or
restore tasks.

## Key Features

- **Multi-database support.** One controller container can maintain backups for
  several PostgreSQL services defined in `DATABASE_LIST`.
- **Configurable credentials per service.** Override host, database, user and password
  for each database through environment variables.
- **Retention policy.** Old archives are cleaned up automatically (daily backups are
  kept for 7 days, weekly for 30 days and monthly/manual for 365 days).
- **Operational tooling.** Includes commands to list available backups, restore dumps
  and run `pg_resetwal` against a problematic database instance.

## Container Layout

The container expects the following mount points:

| Mount | Purpose |
| --- | --- |
| `/var/lib/postgresql/backup/data/<database>` | Primary backup storage for each database. |
| `/var/lib/postgresql/databases/<database>` | PostgreSQL data directories (needed for WAL reset operations). |

When `MODE=production`, the controller writes directly to the `/var/lib/postgresql/backup/*`
paths above. Otherwise backups are stored inside the working directory (`./backup-data`).

## Adding a New Database Service

1. Define a new named volume for the database data in your `docker-compose.yml`.
2. Create a new database service that uses that volume.
3. Append the service name to the `DATABASE_LIST` environment variable of the
   controller (comma-separated).
4. Mount the database volume and a dedicated backup volume into the controller.
5. (Optional) Provide custom credentials via environment variables (see below).

A complete example is available in [`compose.example.yaml`](compose.example.yaml).

## Environment Variables

### General settings

| Variable | Description |
| --- | --- |
| `BACKUP_TARGET` | Storage provider used for backups. Set to `local` (default) or `s3`. |
| `DATABASE_LIST` | Comma-separated list of database service identifiers that the controller manages. |
| `MODE` | Set to `production` to use the predefined `/var/lib/postgresql/backup/*` locations and enable scheduled dumps. |
| `TZ` | Optional timezone used by cron-like scheduling inside the container. |

### Database connection overrides

| Variable | Description |
| --- | --- |
| `<SERVICE>_POSTGRES_HOST` | Hostname of the database service (defaults to the service name). |
| `<SERVICE>_POSTGRES_USER` | Username for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_PASSWORD` | Password for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_DB` | Database name used for restores (defaults to `postgres`). |

> **Note**: Environment variable prefixes are derived from the service identifier in
> `DATABASE_LIST`. For example, a service named `users` uses `USERS_POSTGRES_USER`,
> `USERS_POSTGRES_PASSWORD`, etc. Hyphens (`-`) in service names are converted to underscores.

### S3 storage options

| Variable | Description |
| --- | --- |
| `S3_BUCKET` | Bucket name used when `BACKUP_TARGET=s3`. |
| `S3_PREFIX` | Optional prefix inside the S3 bucket where backups are stored. |
| `S3_REGION` | AWS region of the S3 endpoint. |
| `S3_ENDPOINT` | Endpoint URL of the S3-compatible service. |
| `S3_ACCESS_KEY_ID` | Access key for the S3 service. |
| `S3_SECRET_ACCESS_KEY` | Secret key for the S3 service. |
| `S3_USE_TLS` | Set to `true` to use HTTPS (recommended). |
| `S3_FORCE_PATH_STYLE` | Set to `true` for S3-compatible services that require path-style addressing. |

## S3-compatible storage

Set `BACKUP_TARGET=s3` to store backups in an S3-compatible bucket. The controller will
upload each dump using the credentials and endpoint supplied via the `S3_*` variables
listed above. Backups remain fully compatible with all other commands (listing and
restoring downloads the dump to a temporary location inside the container).

### Integration tests

The integration test suite uses Docker to spin up PostgreSQL and controller containers.
Tests that exercise S3 storage require credentials supplied through the `TEST_S3_*`
environment variables. You can create a local `.env` file (ignored by Git) by copying
`.env.example` and filling in the required values. The test harness automatically loads
the file when present.

## Controller CLI

The controller binary is available inside the container as `/controller`. All commands
must be executed as the `postgres` user.

### Automated mode

```
./controller start
```

Runs an infinite loop (designed for container start-up) that performs a dump every
6 hours at HH:03, HH:09, HH:15 and HH:21 when `MODE=production`. The backup type is
selected automatically:

- **Monthly** on the first day of the month.
- **Weekly** on Saturdays.
- **Daily** for all other runs.

Retention is enforced after each run according to the policy described above.

### Manual operations

```
./controller dump <database-name|--all>
```
Creates a dump for a single database, or for every database listed in `DATABASE_LIST`
when `--all` is provided.

```
./controller restore <database-name> <backup-file>
```
Restores a dump located in the database backup directory. Use the filename listed by
`./controller list` (for example `file_daily_2025-07-04T09:00:00Z.dump`).

./controller list <database-name>
```
Lists available backup files for the given database.

```
./controller resetwal <database-name>
```
Executes `pg_resetwal` against the specified database directory. This is helpful when
PostgreSQL refuses to start because of corrupted or inconsistent WAL segments.

## Permissions

The image runs the controller as the `postgres` user, matching the default user in
the upstream PostgreSQL image. Mounted volumes must already be writable by that
user—use `user: postgres` in your Compose configuration (or adjust ownership on the
host) so that both the backup controller and the databases share the same UID/GID.

## License

This project is distributed under the terms of the [MIT License](LICENSE.txt).

## Testing

Integration tests cover the full backup and restore flow by orchestrating
PostgreSQL and the controller inside Docker containers. To run them locally:

```bash
go clean -testcache ; go test ./test/... -v
```

> **Tip**: run with `-v` to see a step-by-step log of every Docker command that the
> suite executes.

> **Note**: Docker must be available in the environment. When it is missing the
> integration suite is skipped.
