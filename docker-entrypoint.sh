#!/bin/bash

set -e

fix_permissions() {
  for dir in /var/lib/postgresql/backup/* /var/lib/postgresql/databases/*; do
    if [ -d "$dir" ]; then
      echo "Updating permissions for $dir"
      chown -R postgres:postgres "$dir"
    fi
  done
}

fix_permissions

exec "$@"
