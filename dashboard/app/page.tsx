import Link from 'next/link'
import { products } from '@/lib/queries'
import { change, euro } from '@/lib/format'

export default async function Products({ searchParams }: { searchParams: Promise<Record<string, string | undefined>> }) {
  const sp = await searchParams
  const q = sp.q ?? ''
  const flagged = sp.flagged === '1'
  const page = Math.max(0, Number(sp.page ?? 0) || 0)
  const list = await products(q, flagged, page * 60)
  const link = (p: number) => `/?${new URLSearchParams({ ...(q && { q }), ...(flagged && { flagged: '1' }), page: String(p) })}`

  return (
    <>
      <form className="filters">
        <input name="q" defaultValue={q} placeholder="Search products (any language)…" aria-label="Search products" />
        <label>
          <input type="checkbox" name="flagged" value="1" defaultChecked={flagged} /> only with suspicious discounts
        </label>
        <button type="submit">Search</button>
      </form>
      <table className="products">
        <thead>
          <tr>
            <th>Product</th>
            <th className="num">Shops</th>
            <th className="num">Lowest price now</th>
            <th className="num">Highest</th>
            <th>Trend</th>
            <th>Discounts</th>
          </tr>
        </thead>
        <tbody>
          {list.map((p) => (
            <tr key={p.id}>
              <td><Link href={`/product/${p.id}`}>{p.title}</Link></td>
              <td className="num">{p.shops}</td>
              <td className="num strong">{euro(p.lowest)}</td>
              <td className="num muted">{p.shops > 1 ? euro(p.highest) : ''}</td>
              <td className="muted">{change(p.lowest, p.lowest_30_days_ago)}</td>
              <td>{p.flags > 0 && <span className="badge critical">⚠ {p.flags} suspicious</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <p className="muted">Nothing found.</p>}
      <p className="pager">
        {page > 0 && <Link href={link(page - 1)}>← previous</Link>}
        {list.length === 60 && <Link href={link(page + 1)}>next →</Link>}
      </p>
    </>
  )
}
