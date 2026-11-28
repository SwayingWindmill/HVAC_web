import type { Pool, PoolClient } from 'pg';

/**
 * Runs one database transaction with PostgreSQL session-local `app.tenant_id`
 * populated so row-level security policies scope every statement to `tenantId`.
 */
export const withTenantTransaction = async <T>(
  pool: Pool,
  tenantId: string,
  work: (client: PoolClient) => Promise<T>,
): Promise<T> => {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query("SELECT set_config('app.tenant_id', $1, true)", [tenantId]);
    const result = await work(client);
    await client.query('COMMIT');
    return result;
  } catch (error) {
    try { await client.query('ROLLBACK'); } catch {}
    throw error;
  } finally {
    client.release();
  }
};
