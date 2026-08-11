#!/usr/bin/env bash
set -euo pipefail

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
target="/backup/${stamp}"
mkdir -p "${target}"

pg_basebackup \
  --host="${PGHOST:-postgres}" \
  --port="${PGPORT:-5432}" \
  --username="${PGUSER:-postgres}" \
  --pgdata="${target}" \
  --format=tar \
  --gzip \
  --wal-method=stream \
  --checkpoint=fast \
  --progress

sha256sum "${target}"/* > "${target}/SHA256SUMS"
echo "PostgreSQL base backup completed: ${target}"
