import Link from 'next/link'
import { notFound } from 'next/navigation'
import PriceChart, { type Series } from '@/components/PriceChart'
import { createAlert, deleteAlert } from '@/app/actions'
import { alertEvents, alerts, product } from '@/lib/queries'
import { FLAG_NAMES, euro } from '@/lib/format'

export default async function ProductPage({ params }: { params: Promise<{ id: string }> }) {
  const id = Number((await params).id)
  const p = Number.isInteger(id) ? await product(id) : null
  if (!p) notFound()
  const productAlerts = await alerts(id)
  const events = await Promise.all(productAlerts.map((a) => alertEvents(a.id)))

  // Colour follows the shop: slots in the order of shop ids, never by rank.
  const shopIds = [...new Set(p.offers.map((o) => o.shop_id))].sort((a, b) => a - b)
  const series: Series[] = p.offers.map((o) => ({
    name: o.shop,
    slot: (shopIds.indexOf(o.shop_id) % 8) + 1,
    points: p.points.filter((pt) => pt.offer_id === o.id).map((pt) => ({ day: pt.day, price: pt.price, reference: pt.reference_price })),
  }))
  const firstFlags = new Map<string, (typeof p.flags)[number]>()
  for (const f of p.flags) if (!firstFlags.has(`${f.offer_id}/${f.day}`)) firstFlags.set(`${f.offer_id}/${f.day}`, f)
  const markers = [...firstFlags.values()].map((f) => ({ day: f.day, series: f.shop, label: f.explanation }))
  const lowest = Math.min(...p.offers.map((o) => o.price ?? Infinity))

  return (
    <>
      <p><Link href="/">← all products</Link></p>
      <h1>{p.title}</h1>
      <PriceChart series={series} markers={markers} />

      <h2>Where it is sold</h2>
      <table>
        <thead>
          <tr><th>Shop</th><th>Title in the shop</th><th className="num">Price now</th><th className="num">Was</th><th>Matched</th></tr>
        </thead>
        <tbody>
          {p.offers.map((o) => (
            <tr key={o.id}>
              <td>{o.shop}</td>
              <td><a href={o.url} rel="nofollow noreferrer">{o.title}</a></td>
              <td className="num strong">
                {euro(o.price)} {o.price === lowest && p.offers.length > 1 && <span className="badge good">✓ lowest</span>}
              </td>
              <td className="num muted">{o.reference_price ? <s>{euro(o.reference_price)}</s> : ''}</td>
              <td className="muted">
                {o.match_method === 'single' ? 'only offer' : o.match_score != null ? `${(o.match_score * 100).toFixed(0)}%${o.match_method === 'model+llm' ? ', checked by the LLM' : ''}` : ''}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2>Discounts we do not believe</h2>
      {p.flags.length === 0 ? (
        <p className="muted">No suspicious discounts on this product.</p>
      ) : (
        <ul className="flags">
          {p.flags.map((f, i) => (
            <li key={i}>
              <span className="badge critical">⚠ {FLAG_NAMES[f.kind] ?? f.kind}</span> <strong>{f.shop}</strong>, from {f.day}:{' '}
              {euro(f.price)} instead of <s>{euro(f.reference_price)}</s>. {f.explanation}
            </li>
          ))}
        </ul>
      )}

      <h2>Price alerts</h2>
      <form action={createAlert} className="filters">
        <input type="hidden" name="product_id" value={id} />
        <label>Tell me when it costs at most <input name="threshold" inputMode="decimal" required size={8} placeholder="€" /></label>
        <input name="label" placeholder="note (optional)" />
        <button type="submit">Set alert</button>
      </form>
      {productAlerts.map((a, i) => (
        <div key={a.id} className="alert">
          <form action={deleteAlert}>
            <input type="hidden" name="id" value={a.id} />
            <input type="hidden" name="product_id" value={id} />
            <strong>≤ {euro(a.threshold)}</strong> {a.label && <span className="muted">({a.label})</span>}{' '}
            {a.events > 0 ? <span className="badge good">✓ reached {a.events} times</span> : <span className="muted">not reached yet</span>}{' '}
            <button type="submit" className="link">remove</button>
          </form>
          {events[i].length > 0 && (
            <p className="muted small">{events[i].slice(0, 6).map((e) => `${e.day} ${e.shop} ${euro(e.price)}`).join(' · ')}</p>
          )}
        </div>
      ))}
    </>
  )
}
