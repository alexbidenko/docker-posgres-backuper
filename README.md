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
  kept for 7 days, weekly for 30 days and monthly for 365 days).
- **Shared backup replication.** Optional copying of backups into a shared directory
  that can be mounted by other environments (for example to synchronise staging and
  production).
- **Pluggable storage destinations.** Store archives on the local volume, push them
  to S3-compatible object storage or use both destinations at once.
- **Operational tooling.** Includes commands to list available backups, restore dumps
  and run `pg_resetwal` against a problematic database instance.

## Container Layout

The container expects the following mount points:

| Mount | Purpose |
| --- | --- |
| `/var/lib/postgresql/backup/data/<database>` | Primary backup storage for each database. |
| `/var/lib/postgresql/backup/shared/<database>` | Optional shared storage used when `--shared` flag is provided. |
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

| Variable | Description |
| --- | --- |
| `DATABASE_LIST` | Comma-separated list of database service identifiers that the controller manages. |
| `MODE` | Set to `production` to use the predefined `/var/lib/postgresql/backup/*` locations and enable scheduled dumps. |
| `SERVER` | When set to `production`, shared directories are created during initialisation. |
| `COPING_TO_SHARED` | When `true`, automated dumps triggered in production are also copied to the shared directory. |
| `BACKUP_TARGET` | Comma-separated list of destinations (`local`, `s3`). Defaults to `local`. |
| `TZ` | Optional timezone used by cron-like scheduling inside the container. |
| `<SERVICE>_POSTGRES_USER` | Username for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_PASSWORD` | Password for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_DB` | Database name used for restores (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_HOST` | Hostname of the database service (defaults to the service name). |
| `S3_BUCKET` | Name of the bucket that receives dumps when `BACKUP_TARGET` includes `s3`. |
| `S3_PREFIX` | Optional directory/prefix inside the bucket. Useful for isolating backups per project. |
| `S3_REGION` | AWS region for the bucket (for example `eu-central-1`). |
| `S3_ENDPOINT` | Optional custom endpoint for S3-compatible storage. Leave empty for AWS. |
| `S3_ACCESS_KEY_ID` | Access key for S3 authentication. |
| `S3_SECRET_ACCESS_KEY` | Secret key for S3 authentication. |
| `S3_SESSION_TOKEN` | Optional session token when using temporary credentials. |
| `S3_USE_TLS` | `true`/`false` toggle controlling TLS usage for custom endpoints (default `true`). |
| `S3_FORCE_PATH_STYLE` | `true` to force path-style requests (handy for MinIO and similar services). |
| `S3_STORAGE_CLASS` | Optional storage class for uploaded objects (for example `STANDARD_IA`). |
| `S3_MAX_RETRIES` | Number of retry attempts for failed S3 operations (default `0`, meaning a single attempt). |

> **Note**: Environment variable prefixes are derived from the service identifier in
> `DATABASE_LIST`. For example, a service named `users` uses `USERS_POSTGRES_USER`,
> `USERS_POSTGRES_PASSWORD`, etc. Hyphens (`-`) in service names are converted to underscores.

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
./controller dump <database-name|--all> [--shared]
```
Creates a dump for a single database, or for every database listed in `DATABASE_LIST`
when `--all` is provided. If `--shared` is passed, the resulting archives are also
copied into the shared directory as `file.dump`.

```
./controller restore <database-name> <backup-file>
```
Restores a dump located in the database backup directory. Use the filename listed by
`./controller list` (for example `file_daily_2025-07-04T09:00:00Z.dump`).

```
./controller restore-from-shared <database-name|--all>
```
Restores from the `file.dump` archive located in the shared directory. Useful for
synchronising environments from production exports.

```
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
go test ./test/... -v
```

> **Tip**: run with `-v` to see a step-by-step log of every Docker command that the
> suite executes.

> **Note**: Docker must be available in the environment. When it is missing the
> integration suite is skipped.
A local destination is required for the `list`, `restore` and `restore-from-shared`
commands. When `BACKUP_TARGET` excludes `filesystem`, those commands will report
that local storage is disabled.

## S3 configuration

When `BACKUP_TARGET` contains `s3`, the controller uploads every dump to the
configured bucket in addition to any local copies. Use `S3_PREFIX` to keep
archives for different projects separated:

```
BACKUP_TARGET="filesystem,s3"
S3_BUCKET=my-company-backups
S3_PREFIX=sites/prod-blog
```

With the example above, dumps for a database named `users` are stored at
`s3://my-company-backups/sites/prod-blog/users/...`.

For S3-compatible providers supply `S3_ENDPOINT` (for example
`https://minio.internal:9000`) and flip `S3_FORCE_PATH_STYLE=true` when virtual host
style URLs are not supported. Without the path-style flag the controller asks the
endpoint for `https://<bucket>.<host>/...`; providers that do not create wildcard
bucket DNS records (Timeweb Cloud, some MinIO setups, etc.) will answer with `no
such host` and the upload is skipped.

### Troubleshooting S3 uploads

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `SignatureDoesNotMatch` even with the correct credentials | Clock drift inside the container | Compare `docker exec controller date` with your host time. If they differ by more than a few minutes, restart the container once the host clock is synced (Timeweb Cloud rejects requests whose signatures are in the future/past). |
| `SignatureDoesNotMatch` for Timeweb Cloud | Missing path-style flag | Set `S3_FORCE_PATH_STYLE=true` so the request is sent to `https://s3.twcstorage.ru/<bucket>/...`. |
| `SignatureDoesNotMatch` on other S3-compatible services | Incorrect region | Some providers expect `S3_REGION=us-east-1` regardless of the bucket location. Try matching the value used by their AWS CLI examples. |
