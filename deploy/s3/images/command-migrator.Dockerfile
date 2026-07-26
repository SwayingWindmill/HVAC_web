FROM postgres:16.4-bookworm
COPY --chown=postgres:postgres services/command-service/migrations/001_s3_command_runtime.sql /migrations/001_s3_command_runtime.sql
COPY --chown=postgres:postgres services/command-service/migrations/002_s3_target_runtime.sql /migrations/002_s3_target_runtime.sql
COPY --chown=postgres:postgres deploy/s3/images/run-command-migrations.sh /usr/local/bin/run-command-migrations
RUN chmod 0555 /usr/local/bin/run-command-migrations
USER postgres
ENTRYPOINT ["/usr/local/bin/run-command-migrations"]
