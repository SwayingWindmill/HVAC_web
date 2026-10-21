// notification-service's PostgreSQL tests on a throwaway database with the production bootstrap
// and every hvac_s1 migration (services/notification-service/pkg/notificationservice).
import { runGoTestsOnPhase1Postgres } from './lib/phase1-postgres-fixture.ts';

await runGoTestsOnPhase1Postgres({
  name: 'notification',
  databases: ['hvac_s1'],
  logins: ['s16_notification_service'],
  packages: ['./services/notification-service/pkg/notificationservice/...'],
  goArgs: process.argv.slice(2),
  env: (url: (role: string, database: string) => string) => ({ S16_NOTIFICATION_TEST_DATABASE_URL: url('s16_notification_service', 'hvac_s1') }),
});
