// identity-service's PostgreSQL tests on a throwaway database with the production bootstrap and
// identity migrations (services/identity-service/internal/identity).
import { runGoTestsOnPhase1Postgres } from './lib/phase1-postgres-fixture.mjs';

await runGoTestsOnPhase1Postgres({
  name: 'identity',
  databases: ['hvac_identity'],
  logins: ['identity_runtime', 'identity_admin'],
  packages: ['./services/identity-service/internal/identity/...'],
  goArgs: process.argv.slice(2),
  env: (url) => ({
    IDENTITY_TEST_ADMIN_DATABASE_URL: url('identity_admin', 'hvac_identity'),
    IDENTITY_TEST_RUNTIME_DATABASE_URL: url('identity_runtime', 'hvac_identity'),
    // The rollback test takes a grant away from identity_admin and gives it back, as the schema owner would.
    IDENTITY_TEST_MIGRATOR_DATABASE_URL: url('postgres', 'hvac_identity'),
  }),
});
