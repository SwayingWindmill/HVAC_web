CREATE SCHEMA core_registry;

CREATE TABLE core_registry.organizations (
  id uuid PRIMARY KEY,
  code text NOT NULL,
  display_name text NOT NULL,
  status text NOT NULL,
  revision bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE core_registry.sites (
  id uuid PRIMARY KEY,
  organization_id uuid NOT NULL,
  code text NOT NULL,
  display_name text NOT NULL,
  timezone text NOT NULL,
  status text NOT NULL,
  revision bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE core_registry.equipment (
  id uuid PRIMARY KEY,
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  code text NOT NULL,
  display_name text NOT NULL,
  equipment_type text NOT NULL,
  status text NOT NULL,
  revision bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE core_registry.devices (
  id uuid PRIMARY KEY,
  organization_id uuid NOT NULL,
  site_id uuid NOT NULL,
  code text NOT NULL,
  display_name text NOT NULL,
  device_type text NOT NULL,
  status text NOT NULL,
  revision bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
