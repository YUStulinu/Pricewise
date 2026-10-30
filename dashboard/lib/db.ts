import { Pool } from 'pg'

// One pool per server process (kept across hot reloads in development).
const globalForPool = globalThis as unknown as { pricewisePool?: Pool }

export const pool =
  globalForPool.pricewisePool ??
  new Pool({
    connectionString: process.env.PRICEWISE_DB ?? 'postgres://pricewise:pricewise@127.0.0.1:5433/pricewise',
    max: 8,
  })
globalForPool.pricewisePool = pool

export async function rows<T>(sql: string, params: unknown[] = []): Promise<T[]> {
  const r = await pool.query(sql, params)
  return r.rows as T[]
}
