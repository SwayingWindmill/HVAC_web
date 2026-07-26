#!/bin/sh
set -eu

psql -v ON_ERROR_STOP=1 \
  -f /migrations/001_s3_command_runtime.sql \
  -f /migrations/002_s3_target_runtime.sql
