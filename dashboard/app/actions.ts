'use server'

import { revalidatePath } from 'next/cache'
import { redirect } from 'next/navigation'
import { pool } from '@/lib/db'

/** Sets a price alert and records at once the days on which it would already have fired. */
export async function createAlert(form: FormData) {
  const productId = Number(form.get('product_id'))
  const threshold = Number(String(form.get('threshold') ?? '').replace(',', '.'))
  const label = String(form.get('label') ?? '').trim() || null
  if (!Number.isInteger(productId) || !(threshold > 0)) return
  const client = await pool.connect()
  try {
    await client.query('BEGIN')
    const { rows } = await client.query(
      'INSERT INTO alerts (product_id, threshold, label) VALUES ($1, $2, $3) RETURNING id',
      [productId, threshold, label],
    )
    // The same rule as ml/pricewise_ml/analyse.py, for this one alert.
    await client.query(
      `INSERT INTO alert_events (alert_id, offer_id, day, price)
       SELECT $1, o.id, d.day::date, d.price
       FROM offers o JOIN offer_daily d ON d.offer_id = o.id
       WHERE o.product_id = $2 AND d.price <= $3
       ON CONFLICT DO NOTHING`,
      [rows[0].id, productId, threshold],
    )
    await client.query('COMMIT')
  } catch (e) {
    await client.query('ROLLBACK')
    throw e
  } finally {
    client.release()
  }
  revalidatePath(`/product/${productId}`)
  revalidatePath('/alerts')
  redirect(`/product/${productId}`)
}

export async function deleteAlert(form: FormData) {
  const id = Number(form.get('id'))
  if (!Number.isInteger(id)) return
  await pool.query('DELETE FROM alerts WHERE id = $1', [id])
  revalidatePath('/alerts')
  revalidatePath(`/product/${Number(form.get('product_id'))}`)
  // Back to the page the alert was removed from.
  redirect(form.get('from') === 'alerts' ? '/alerts' : `/product/${Number(form.get('product_id'))}`)
}
