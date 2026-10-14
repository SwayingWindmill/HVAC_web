-- Report snapshots read a Site's valid energy facts with their own account (#483); the account
-- can read those facts and nothing else, and cannot write.
CREATE USER IF NOT EXISTS report_service_reader IDENTIFIED WITH no_password;

GRANT SELECT ON analytics.energy_interval_facts TO report_service_reader;
GRANT SELECT ON analytics.energy_interval_facts_visible TO report_service_reader;
