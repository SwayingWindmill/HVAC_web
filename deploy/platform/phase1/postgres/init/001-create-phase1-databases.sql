SELECT 'CREATE DATABASE hvac_s0'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s0')\gexec

SELECT 'CREATE DATABASE hvac_s1'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s1')\gexec

SELECT 'CREATE DATABASE hvac_s2'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s2')\gexec

SELECT 'CREATE DATABASE hvac_s3'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s3')\gexec

SELECT 'CREATE DATABASE hvac_s4'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s4')\gexec

SELECT 'CREATE DATABASE hvac_s5'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hvac_s5')\gexec
