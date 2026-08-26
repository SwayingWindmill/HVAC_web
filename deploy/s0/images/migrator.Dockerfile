FROM postgres:16.4-bookworm
COPY --chown=postgres:postgres infra/durability/postgres/init/001-s0-durable.sql /migrations/001-s0-durable.sql
USER postgres
ENTRYPOINT ["psql", "-v", "ON_ERROR_STOP=1", "-f", "/migrations/001-s0-durable.sql"]
