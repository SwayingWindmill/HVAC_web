\set ON_ERROR_STOP on

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE hvac_operations_agent FROM PUBLIC;

CREATE ROLE operations_agent_operations_migrator
  LOGIN
  NOINHERIT
  NOBYPASSRLS
  PASSWORD 'operations-migrator-local-only';

CREATE ROLE operations_agent_operations_runtime
  LOGIN
  NOINHERIT
  NOBYPASSRLS
  PASSWORD 'operations-runtime-local-only';

CREATE ROLE operations_agent_checkpoints_migrator
  LOGIN
  NOINHERIT
  NOBYPASSRLS
  PASSWORD 'checkpoints-migrator-local-only';

CREATE ROLE operations_agent_checkpoints_runtime
  LOGIN
  NOINHERIT
  NOBYPASSRLS
  PASSWORD 'checkpoints-runtime-local-only';

GRANT CONNECT ON DATABASE hvac_operations_agent TO
  operations_agent_operations_migrator,
  operations_agent_operations_runtime,
  operations_agent_checkpoints_migrator,
  operations_agent_checkpoints_runtime;

GRANT CREATE ON DATABASE hvac_operations_agent TO
  operations_agent_operations_migrator,
  operations_agent_checkpoints_migrator;
