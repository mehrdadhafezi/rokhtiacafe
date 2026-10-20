# Shared helpers for the ops scripts (backup, restore, migrate, preflight). Sourced, never run.
#
# Rules kept here so every script follows them:
# - env files are parsed one key at a time and never `source`d (values are data, not shell);
# - passwords travel in PGPASSWORD (environment), never in command arguments, which other users
#   can read in the process list;
# - nothing is ever deleted except Rokh backup sets that the retention rule selects (backup.sh).

# die MESSAGE — print to stderr and exit with $EXIT_FAIL (default 1).
die() {
  echo "error: $*" >&2
  exit "${EXIT_FAIL:-1}"
}

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S%z') $*"; }

# env_get FILE KEY — print KEY's value from a KEY=VALUE file, parsed like rokh's own .env loader:
# first occurrence wins, surrounding whitespace and one pair of matching quotes are removed.
env_get() {
  local file=$1 key=$2 line val
  [ -r "$file" ] || die "cannot read $file"
  line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?${key}[[:space:]]*=" "$file" | head -n 1)" || true
  [ -n "$line" ] || return 0
  val="${line#*=}"
  val="${val#"${val%%[![:space:]]*}"}"
  val="${val%"${val##*[![:space:]]}"}"
  if [ "${#val}" -ge 2 ]; then
    case $val in
      \"*\" | \'*\') val="${val:1:${#val}-2}" ;;
    esac
  fi
  printf '%s' "$val"
}

# env_export_file FILE — export every KEY=VALUE of FILE that is not already set (rokh semantics).
env_export_file() {
  local file=$1 line key
  [ -r "$file" ] || die "cannot read $file"
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"
    case $line in '' | '#'*) continue ;; esac
    key="${line%%=*}"
    key="${key#export }"
    key="${key%"${key##*[![:space:]]}"}"
    [[ $key =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "$file: invalid line (expected KEY=VALUE)"
    if [ -z "${!key+x}" ]; then
      export "$key=$(env_get "$file" "$key")"
    fi
  done < "$file"
}

# opt KEY DEFAULT — environment first, then the ops file, then DEFAULT.
opt() {
  local key=$1 def=${2-} v=${!1-}
  if [ -z "$v" ] && [ -n "${OPS_FILE:-}" ]; then
    v="$(env_get "$OPS_FILE" "$key")"
  fi
  printf '%s' "${v:-$def}"
}

# load_ops — read the ops settings (deploy/env/ops.env.example).
load_ops() {
  OPS_FILE="${ROKH_OPS_ENV:-/etc/rokh/ops.env}"
  if [ ! -e "$OPS_FILE" ]; then
    [ -z "${ROKH_OPS_ENV:-}" ] || die "ROKH_OPS_ENV=$ROKH_OPS_ENV does not exist"
    OPS_FILE=""
  fi
  ROKH_BIN="$(opt ROKH_BIN /opt/rokh/bin/rokh)"
  ROKH_ENV_FILE="$(opt ROKH_ENV_FILE /etc/rokh/rokh.env)"
  ROKH_MIGRATE_ENV_FILE="$(opt ROKH_MIGRATE_ENV_FILE /etc/rokh/migrate.env)"
  ROKH_RUN_AS="$(opt ROKH_RUN_AS "")"
  PG_CLIENT="$(opt PG_CLIENT docker)"
  PG_CONTAINER="$(opt PG_CONTAINER rokh-postgres)"
  case $PG_CLIENT in
    docker | host) ;;
    *) die "PG_CLIENT must be docker or host, got '$PG_CLIENT'" ;;
  esac
  [ -x "$ROKH_BIN" ] || die "rokh binary not found or not executable: $ROKH_BIN"
}

# parse_pg_url URL PREFIX — split a postgres:// URL into PREFIX_USER, _PASS, _HOST, _PORT, _DB,
# _SSLMODE. The password is percent-decoded. Unsupported shapes are rejected, not guessed.
parse_pg_url() {
  local url=$1 p=$2 re pass
  re='^postgres(ql)?://([^:@/?#]+)(:([^@/?#]*))?@([^:/?#]+)(:([0-9]+))?/([^/?#]+)(\?([^#]*))?$'
  [[ $url =~ $re ]] || die "unsupported database URL in $3 (expected postgres://user:password@host:port/db)"
  pass="${BASH_REMATCH[4]}"
  printf -v "${p}_USER" '%s' "${BASH_REMATCH[2]}"
  printf -v "${p}_PASS" '%b' "${pass//%/\\x}"
  printf -v "${p}_HOST" '%s' "${BASH_REMATCH[5]}"
  printf -v "${p}_PORT" '%s' "${BASH_REMATCH[7]:-5432}"
  printf -v "${p}_DB" '%s' "${BASH_REMATCH[8]}"
  local q="${BASH_REMATCH[10]}" ssl=prefer
  if [[ "&$q" =~ \&sslmode=([a-z-]+) ]]; then ssl="${BASH_REMATCH[1]}"; fi
  printf -v "${p}_SSLMODE" '%s' "$ssl"
}

# with_db URL PREFIX — replace the database name of URL with $3 (validated by the caller).
url_with_db() {
  local url=$1 db=$2
  [[ $url =~ ^([^?]*/)[^/?]+(\?.*)?$ ]] || die "cannot rewrite database URL"
  printf '%s%s%s' "${BASH_REMATCH[1]}" "$db" "${BASH_REMATCH[2]}"
}

valid_db_name() { [[ $1 =~ ^[a-z_][a-z0-9_]{0,62}$ ]]; }

# pg_exec USER PASS DB HOST PORT SSLMODE -- TOOL ARGS... — run a PostgreSQL client tool.
# docker: inside $PG_CONTAINER over its own loopback (always the server's exact version);
# host: the tool on PATH, connecting to HOST:PORT. stdin and stdout are passed through.
pg_exec() {
  local user=$1 pass=$2 db=$3 host=$4 port=$5 ssl=$6
  shift 6
  [ "$1" = -- ] && shift
  if [ "$PG_CLIENT" = docker ]; then
    PGUSER="$user" PGPASSWORD="$pass" PGDATABASE="$db" \
      docker exec -i -e PGUSER -e PGPASSWORD -e PGDATABASE \
      -e PGHOST=127.0.0.1 -e PGPORT=5432 -e PGCONNECT_TIMEOUT=10 "$PG_CONTAINER" "$@"
  else
    PGUSER="$user" PGPASSWORD="$pass" PGDATABASE="$db" PGHOST="$host" PGPORT="$port" \
      PGSSLMODE="$ssl" PGCONNECT_TIMEOUT=10 "$@"
  fi
}

# pg_offline TOOL ARGS... — run a client tool that makes no connection (pg_restore --list).
pg_offline() {
  if [ "$PG_CLIENT" = docker ]; then
    docker exec -i "$PG_CONTAINER" "$@"
  else
    "$@"
  fi
}

# rokh_cmd ARGS... — run the rokh binary, as $ROKH_RUN_AS when the script runs as root.
# Callers pass settings as VAR=value prefixes (environment, not arguments). Runs from / so the
# binary's .env lookup (./.env, ../.env) cannot pick up a stray file.
rokh_cmd() {
  if [ -n "$ROKH_RUN_AS" ] && [ "$(id -u)" = 0 ]; then
    (cd / && runuser -u "$ROKH_RUN_AS" -- "$ROKH_BIN" "$@")
  else
    (cd / && "$ROKH_BIN" "$@")
  fi
}

# mount_of PATH — the mount point PATH lives on.
mount_of() { df -P "$1" | awk 'NR == 2 { print $6 }'; }

# free_mb PATH — free megabytes on PATH's filesystem.
free_mb() { df -Pk "$1" | awk 'NR == 2 { print int($4 / 1024) }'; }

is_uint() { [[ $1 =~ ^[0-9]+$ ]]; }
