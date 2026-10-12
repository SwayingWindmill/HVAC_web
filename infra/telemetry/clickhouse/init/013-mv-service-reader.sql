-- Savings verification reads a Site's released hourly Metric results with its own account
-- (ADR 0021); it can read those results and nothing else, and cannot write.
CREATE USER IF NOT EXISTS mv_service_reader IDENTIFIED WITH no_password;

GRANT SELECT ON analytics.metric_result_facts TO mv_service_reader;
