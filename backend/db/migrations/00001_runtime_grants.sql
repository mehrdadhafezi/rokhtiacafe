-- Runtime privileges for the application role (docs/DATABASE.md §6.1).
-- Migrations run as rokh_owner, which owns schema "rokh" and every object in it.
-- The server connects as rokh_app: DML only, never DELETE by default, no DDL.
-- Tables that legitimately need DELETE (sessions, push_subscriptions, notification_events)
-- get an explicit GRANT in the migration that creates them.

-- +goose Up
GRANT USAGE ON SCHEMA rokh TO rokh_app;
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    GRANT SELECT, INSERT, UPDATE ON TABLES TO rokh_app;
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    GRANT USAGE, SELECT ON SEQUENCES TO rokh_app;
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    GRANT EXECUTE ON FUNCTIONS TO rokh_app;

-- +goose Down
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    REVOKE EXECUTE ON FUNCTIONS FROM rokh_app;
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    REVOKE USAGE, SELECT ON SEQUENCES FROM rokh_app;
ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner IN SCHEMA rokh
    REVOKE SELECT, INSERT, UPDATE ON TABLES FROM rokh_app;
REVOKE USAGE ON SCHEMA rokh FROM rokh_app;
