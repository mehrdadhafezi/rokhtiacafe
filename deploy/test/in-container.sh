#!/usr/bin/env bash
# Entry point inside the throwaway container started by in-docker.sh. Do not run elsewhere.
set -euo pipefail
[ -f /.dockerenv ] || { echo "in-container.sh must run inside the test container" >&2; exit 1; }

useradd --system --no-create-home --shell /usr/sbin/nologin rokhtest
super="super$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
install -d -o postgres -m 700 /tmp/pgdata
printf '%s\n' "$super" > /tmp/pgpass && chown postgres /tmp/pgpass
gosu postgres initdb -D /tmp/pgdata -U postgres --pwfile=/tmp/pgpass \
  --auth-local=peer --auth-host=scram-sha-256 > /dev/null
rm -f /tmp/pgpass
gosu postgres pg_ctl -D /tmp/pgdata -l /tmp/pg.log -o "-c listen_addresses=127.0.0.1 -p 5432" -w start > /dev/null

export TEST_PG_DISPOSABLE=yes PG_CLIENT=host TEST_PG_HOST=127.0.0.1 TEST_PG_PORT=5432 \
  TEST_PG_SUPERPASS="$super" TEST_BACKUP_MOUNT=/backup TEST_RUN_AS=rokhtest ROKH_RELEASE_DIR=/release
echo "PostgreSQL $(psql --version | awk '{print $3}') client, server $(gosu postgres psql -XAtc 'SHOW server_version')"
bash /repo/deploy/test/run.sh
