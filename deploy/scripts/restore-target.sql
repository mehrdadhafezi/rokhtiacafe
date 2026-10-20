-- Create a NEW, empty restore target for deploy/scripts/restore.sh (deploy/README.md §9).
-- Run as the container superuser (peer authentication, no password):
--   docker exec -i -u postgres rokh-postgres psql -X -v db_name=rokhtiacafe_restore_20261020 < restore-target.sql
-- Refuses names without "_restore_", and refuses an existing database (CREATE DATABASE fails).
-- It never drops anything. Same hardening as deploy/local/postgres/roles.sql, but no schema:
-- the dump creates schema rokh itself.
\set ON_ERROR_STOP on

SELECT format('DO $$BEGIN RAISE EXCEPTION %L; END$$',
              'db_name must look like rokhtiacafe_restore_<date> (lower case, digits, _)')
WHERE NOT (:'db_name' ~ '^[a-z][a-z0-9_]{0,62}$' AND :'db_name' LIKE '%\_restore\_%') \gexec

SELECT format('DO $$BEGIN RAISE EXCEPTION %L; END$$', 'roles rokh_owner and rokh_app must exist')
WHERE (SELECT count(*) FROM pg_roles WHERE rolname IN ('rokh_owner', 'rokh_app')) <> 2 \gexec

SELECT format('CREATE DATABASE %I OWNER rokh_owner', :'db_name') \gexec

\connect :db_name
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db_name') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO rokh_app', :'db_name') \gexec
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
\echo restore target ready: :db_name
