#!/bin/sh
set -eu

psql -v ON_ERROR_STOP=1 \
  -f /migrations/001_s3_command_runtime.sql \
  -f /migrations/002_s3_target_runtime.sql \
  -f /migrations/003_s3_tenant_scope.sql \
  -f /migrations/004_s3_command_point_identity.sql \
  -f /migrations/005_s11_edge_execution_evidence.sql
