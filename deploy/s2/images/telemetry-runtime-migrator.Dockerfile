FROM postgres:16-alpine@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql /migrations/001-s2-telemetry-baseline.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/002-s2-telemetry-runtime-snapshot.sql /migrations/002-s2-telemetry-runtime-snapshot.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/003-s2-telemetry-ingest.sql /migrations/003-s2-telemetry-ingest.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/004-s2-telemetry-history-outbox.sql /migrations/004-s2-telemetry-history-outbox.sql
COPY --chown=postgres:postgres infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql /migrations/005-s2-realtime-backend.sql
COPY --chown=postgres:postgres deploy/s2/images/run-telemetry-migrations.sh /usr/local/bin/run-telemetry-migrations
RUN rm -f /usr/local/bin/gosu \
    && chmod 0555 /usr/local/bin/run-telemetry-migrations
USER postgres
ENTRYPOINT ["/usr/local/bin/run-telemetry-migrations"]
