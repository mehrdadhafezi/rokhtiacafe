#!/bin/sh
# Runs once when the local Docker volume is first initialised.
set -eu
psql --username "$POSTGRES_USER" --dbname postgres \
  -v owner_password="$ROKH_OWNER_PASSWORD" \
  -v app_password="$ROKH_APP_PASSWORD" \
  -v db_name="$ROKH_DB_NAME" \
  -f /rokh/roles.sql
