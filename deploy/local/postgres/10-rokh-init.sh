#!/bin/sh
# Runs once when the Docker volume is first initialised (local development and
# deploy/postgres/compose.yaml). The values come from the container environment (postgres.env) and
# are read inside psql with \getenv (PostgreSQL 15 or later), so no password ever appears as a
# command-line argument in the process list. The SQL is shared with CI: roles.sql.
set -eu
: "${ROKH_OWNER_PASSWORD:?}" "${ROKH_APP_PASSWORD:?}" "${ROKH_DB_NAME:?}"
psql -X --username "$POSTGRES_USER" --dbname postgres << 'EOF'
\set ON_ERROR_STOP on
\getenv owner_password ROKH_OWNER_PASSWORD
\getenv app_password ROKH_APP_PASSWORD
\getenv db_name ROKH_DB_NAME
\i /rokh/roles.sql
EOF
