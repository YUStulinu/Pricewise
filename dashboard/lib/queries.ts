import { rows } from './db'

// "Today" is the last day with prices: with the simulated shops that is the simulation's last day.
const LATEST = `(SELECT max(day) FROM offer_daily)`

export interface ProductRow {
  id: number
  title: string
  shops: number
  lowest: number | null
  highest: number | null
  lowest_30_days_ago: number | null
  flags: number
}

export async function products(q: string, onlyFlagged: boolean, offset = 0): Promise<ProductRow[]> {
  return rows<ProductRow>(
    `WITH latest AS (SELECT ${LATEST} AS day),
     now AS (
       SELECT DISTINCT ON (d.offer_id) d.offer_id, d.price
       FROM offer_daily d, latest WHERE d.day > latest.day - INTERVAL '3 days'
       ORDER BY d.offer_id, d.day DESC),
     before AS (
       SELECT d.offer_id, min(d.price) AS price
       FROM offer_daily d, latest
       WHERE d.day BETWEEN latest.day - INTERVAL '33 days' AND latest.day - INTERVAL '30 days'
       GROUP BY d.offer_id),
     flagged AS (
       SELECT o.product_id, count(*) AS n
       FROM discount_flags f JOIN offers o ON o.id = f.offer_id, latest
       WHERE f.day > latest.day - INTERVAL '60 days'
       GROUP BY o.product_id)
     SELECT p.id, p.title, count(o.id)::int AS shops,
            min(now.price)::float AS lowest, max(now.price)::float AS highest,
            min(before.price)::float AS lowest_30_days_ago,
            coalesce(max(flagged.n), 0)::int AS flags
     FROM products p
     JOIN offers o ON o.product_id = p.id
     LEFT JOIN now ON now.offer_id = o.id
     LEFT JOIN before ON before.offer_id = o.id
     LEFT JOIN flagged ON flagged.product_id = p.id
     WHERE p.title ILIKE $1
     GROUP BY p.id
     HAVING $2::boolean = false OR coalesce(max(flagged.n), 0) > 0
     ORDER BY count(o.id) DESC, coalesce(max(flagged.n), 0) DESC, p.title
     LIMIT 60 OFFSET $3`,
    [`%${q}%`, onlyFlagged, offset],
  )
}

export async function overview() {
  const [r] = await rows<{
    products: number; offers: number; shops: number; observations: number; flags: number; first: string; last: string
    simulated: boolean
  }>(
    `SELECT (SELECT count(*) FROM products)::int AS products,
            (SELECT count(*) FROM offers)::int AS offers,
            (SELECT count(*) FROM shops)::int AS shops,
            (SELECT count(*) FROM price_observations)::int AS observations,
            (SELECT count(DISTINCT (offer_id, day)) FROM discount_flags)::int AS flags,
            (SELECT min(day)::date::text FROM offer_daily) AS first,
            (SELECT max(day)::date::text FROM offer_daily) AS last,
            coalesce((SELECT bool_or(simulated) FROM shops), false) AS simulated`,
  )
  return r
}

export interface OfferRow {
  id: number
  shop_id: number
  shop: string
  url: string
  title: string
  price: number | null
  reference_price: number | null
  match_score: number | null
  match_method: string | null
}

export async function product(id: number) {
  const [p] = await rows<{ id: number; title: string }>(`SELECT id, title FROM products WHERE id = $1`, [id])
  if (!p) return null
  const offers = await rows<OfferRow>(
    `SELECT o.id, o.shop_id, s.name AS shop, o.url, o.title, o.match_score, o.match_method,
            now.price::float AS price, now.reference_price::float AS reference_price
     FROM offers o JOIN shops s ON s.id = o.shop_id
     LEFT JOIN LATERAL (
       SELECT price, reference_price FROM offer_daily d WHERE d.offer_id = o.id ORDER BY day DESC LIMIT 1
     ) now ON true
     WHERE o.product_id = $1 ORDER BY now.price NULLS LAST`,
    [id],
  )
  const points = await rows<{ offer_id: number; day: string; price: number; reference_price: number | null }>(
    `SELECT d.offer_id, d.day::date::text AS day, d.price::float AS price, d.reference_price::float AS reference_price
     FROM offer_daily d JOIN offers o ON o.id = d.offer_id
     WHERE o.product_id = $1 ORDER BY d.day`,
    [id],
  )
  const flags = await rows<FlagRow>(
    `SELECT f.offer_id, s.name AS shop, f.day::text AS day, f.kind, f.price::float AS price,
            f.reference_price::float AS reference_price, f.lowest_30_days::float AS lowest_30_days, f.explanation,
            p.id AS product_id, p.title AS product
     FROM discount_flags f JOIN offers o ON o.id = f.offer_id JOIN shops s ON s.id = o.shop_id
     JOIN products p ON p.id = o.product_id
     WHERE o.product_id = $1 ORDER BY f.day DESC, f.kind`,
    [id],
  )
  return { ...p, offers, points, flags }
}

export interface FlagRow {
  offer_id: number
  shop: string
  day: string
  kind: string
  price: number
  reference_price: number
  lowest_30_days: number | null
  explanation: string
  product_id: number
  product: string
}

export async function flags(kind: string | null): Promise<FlagRow[]> {
  return rows<FlagRow>(
    `SELECT f.offer_id, s.name AS shop, f.day::text AS day, f.kind, f.price::float AS price,
            f.reference_price::float AS reference_price, f.lowest_30_days::float AS lowest_30_days, f.explanation,
            p.id AS product_id, p.title AS product
     FROM discount_flags f JOIN offers o ON o.id = f.offer_id JOIN shops s ON s.id = o.shop_id
     JOIN products p ON p.id = o.product_id
     WHERE $1::text IS NULL OR f.kind = $1
     ORDER BY f.day DESC, p.title LIMIT 300`,
    [kind],
  )
}

export interface AlertRow {
  id: number
  product_id: number
  product: string
  threshold: number
  label: string | null
  created_at: string
  lowest: number | null
  events: number
  last_event: string | null
}

export async function alerts(productId?: number): Promise<AlertRow[]> {
  return rows<AlertRow>(
    `SELECT a.id, a.product_id, p.title AS product, a.threshold::float AS threshold, a.label,
            a.created_at::text AS created_at,
            (SELECT min(price)::float FROM offer_daily d JOIN offers o ON o.id = d.offer_id
              WHERE o.product_id = a.product_id AND d.day = ${LATEST}) AS lowest,
            (SELECT count(*)::int FROM alert_events e WHERE e.alert_id = a.id) AS events,
            (SELECT max(day)::text FROM alert_events e WHERE e.alert_id = a.id) AS last_event
     FROM alerts a JOIN products p ON p.id = a.product_id
     WHERE $1::int IS NULL OR a.product_id = $1
     ORDER BY a.created_at DESC`,
    [productId ?? null],
  )
}

export async function alertEvents(alertId: number) {
  return rows<{ day: string; shop: string; price: number }>(
    `SELECT e.day::text AS day, s.name AS shop, e.price::float AS price
     FROM alert_events e JOIN offers o ON o.id = e.offer_id JOIN shops s ON s.id = o.shop_id
     WHERE e.alert_id = $1 ORDER BY e.day DESC, e.price LIMIT 20`,
    [alertId],
  )
}
