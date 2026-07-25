FROM postgres:16.4-bookworm
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql /migrations/001-s2-telemetry-baseline.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql /migrations/002-s2-telemetry-runtime-snapshot.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql /migrations/003-s2-telemetry-ingest.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql /migrations/005-s2-realtime-backend.sql
COPY --chown=postgres:postgres deploy/s2/images/run-telemetry-migrations.sh /usr/local/bin/run-telemetry-migrations
RUN chmod 0555 /usr/local/bin/run-telemetry-migrations
USER postgres
ENTRYPOINT ["/usr/local/bin/run-telemetry-migrations"]
