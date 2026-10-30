import Link from 'next/link'
import { flags } from '@/lib/queries'
import { FLAG_NAMES, euro } from '@/lib/format'

export default async function Discounts({ searchParams }: { searchParams: Promise<{ kind?: string }> }) {
  const { kind } = await searchParams
  const list = await flags(kind && kind in FLAG_NAMES ? kind : null)
  return (
    <>
      <h1>Suspicious discounts</h1>
      <p className="muted">
        A discount is flagged when its crossed-out &ldquo;was&rdquo; price does not match the prices Pricewise observed: above the
        lowest price of the 30 days before (the EU Omnibus rule), a price never charged, or a price raised shortly before
        so that the &ldquo;discount&rdquo; only takes it back.
      </p>
      <nav className="filters">
        <Link href="/discounts" className={!kind ? 'on' : ''}>All</Link>
        {Object.entries(FLAG_NAMES).map(([k, name]) => (
          <Link key={k} href={`/discounts?kind=${k}`} className={kind === k ? 'on' : ''}>{name}</Link>
        ))}
      </nav>
      <table>
        <thead>
          <tr><th>From</th><th>Product</th><th>Shop</th><th className="num">Price</th><th className="num">Was</th><th className="num">30-day low</th><th>Why</th></tr>
        </thead>
        <tbody>
          {list.map((f, i) => (
            <tr key={i}>
              <td>{f.day}</td>
              <td><Link href={`/product/${f.product_id}`}>{f.product}</Link></td>
              <td>{f.shop}</td>
              <td className="num strong">{euro(f.price)}</td>
              <td className="num"><s>{euro(f.reference_price)}</s></td>
              <td className="num">{euro(f.lowest_30_days)}</td>
              <td><span className="badge critical">⚠ {FLAG_NAMES[f.kind] ?? f.kind}</span></td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <p className="muted">No suspicious discounts.</p>}
    </>
  )
}
