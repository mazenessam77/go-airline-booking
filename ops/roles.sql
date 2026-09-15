-- Run as a PostgreSQL administrator in the target database, before migrations.
-- Provision login credentials outside this file through your secret manager.
CREATE ROLE airline_owner NOLOGIN;
CREATE ROLE airline_runtime NOLOGIN;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO airline_owner;
GRANT USAGE ON SCHEMA public TO airline_runtime;
-- The migration login must SET ROLE airline_owner before running goose.
-- Grant a separately provisioned runtime login membership in airline_runtime.
-- Never grant runtime logins membership in airline_owner.
