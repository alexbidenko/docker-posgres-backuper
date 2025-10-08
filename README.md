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

A complete example is available in [`docker-compose.example.yml`](docker-compose.example.yml).

## Environment Variables

| Variable | Description |
| --- | --- |
| `DATABASE_LIST` | Comma-separated list of database service identifiers that the controller manages. |
| `<SERVICE>_POSTGRES_USER` | Username for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_PASSWORD` | Password for the target database (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_DB` | Database name used for restores (defaults to `postgres`). |
| `<SERVICE>_POSTGRES_HOST` | Hostname of the database service (defaults to the service name). |
| `MODE` | Set to `production` to use the predefined `/var/lib/postgresql/backup/*` locations and enable scheduled dumps. |
| `SERVER` | When set to `production`, shared directories are created during initialisation. |
| `COPING_TO_SHARED` | When `true`, automated dumps triggered in production are also copied to the shared directory. |
| `TZ` | Optional timezone used by cron-like scheduling inside the container. |

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
`./controller list` (for example `file_daily_2024-07-04T09:00:00Z.dump`).

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

The container entrypoint (`docker-entrypoint.sh`) fixes ownership of the mounted
backup and database directories to `postgres:postgres` before executing the main
process. Ensure your volumes are writable by that user.

## License

This project is distributed under the terms of the [MIT License](LICENSE.txt).
