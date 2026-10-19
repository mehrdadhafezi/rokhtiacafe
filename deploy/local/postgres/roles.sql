-- Local/CI database bootstrap (run as a superuser with psql).
-- Creates the two roles from docs/DATABASE.md §6.1 and an app database owned by rokh_owner.
-- Required psql variables: owner_password, app_password, db_name.
-- Production roles are created separately on the server with their own credentials.
\set ON_ERROR_STOP on

SELECT format('CREATE ROLE rokh_owner LOGIN PASSWORD %L', :'owner_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rokh_owner') \gexec

SELECT format('CREATE ROLE rokh_app LOGIN PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rokh_app') \gexec

ALTER ROLE rokh_owner NOSUPERUSER NOCREATEDB NOCREATEROLE;
ALTER ROLE rokh_app   NOSUPERUSER NOCREATEDB NOCREATEROLE;
ALTER ROLE rokh_owner SET search_path = rokh;
ALTER ROLE rokh_app   SET search_path = rokh;

SELECT format('CREATE DATABASE %I OWNER rokh_owner', :'db_name')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') \gexec

\connect :db_name

-- Keep this block in sync with backend/internal/testdb (per-test databases).
CREATE SCHEMA IF NOT EXISTS rokh AUTHORIZATION rokh_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db_name') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO rokh_app', :'db_name') \gexec
