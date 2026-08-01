CREATE ROLE s5_work_order_migrator NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s5_work_order_runtime NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s5_work_order_writer NOLOGIN NOINHERIT NOBYPASSRLS;
CREATE ROLE s5_work_order_service LOGIN PASSWORD 'local-fixture-only' NOINHERIT NOBYPASSRLS;
CREATE ROLE s5_work_order_mutation_service LOGIN PASSWORD 'local-mutation-fixture-only' NOINHERIT NOBYPASSRLS;
GRANT s5_work_order_runtime TO s5_work_order_service;
GRANT s5_work_order_writer TO s5_work_order_mutation_service;
CREATE SCHEMA work_order_runtime AUTHORIZATION s5_work_order_migrator;
REVOKE ALL ON SCHEMA work_order_runtime FROM PUBLIC;
