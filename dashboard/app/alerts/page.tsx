import Link from 'next/link'
import { deleteAlert } from '@/app/actions'
import { alerts } from '@/lib/queries'
import { euro } from '@/lib/format'

export default async function Alerts() {
  const list = await alerts()
  return (
    <>
      <h1>Price alerts</h1>
      <p className="muted">Set an alert on a product&rsquo;s page. It is reached on every day an offer of the product costs no more than the threshold.</p>
      <table>
        <thead>
          <tr><th>Product</th><th className="num">Threshold</th><th className="num">Lowest now</th><th>Status</th><th>Last reached</th><th /></tr>
        </thead>
        <tbody>
          {list.map((a) => (
            <tr key={a.id}>
              <td><Link href={`/product/${a.product_id}`}>{a.product}</Link> {a.label && <span className="muted">({a.label})</span>}</td>
              <td className="num">{euro(a.threshold)}</td>
              <td className="num strong">{euro(a.lowest)}</td>
              <td>
                {a.lowest != null && a.lowest <= a.threshold ? (
                  <span className="badge good">✓ below the threshold now</span>
                ) : a.events > 0 ? (
                  <span className="badge warning">reached before</span>
                ) : (
                  <span className="muted">waiting</span>
                )}
              </td>
              <td>{a.last_event ?? ''}</td>
              <td>
                <form action={deleteAlert}>
                  <input type="hidden" name="id" value={a.id} />
                  <input type="hidden" name="product_id" value={a.product_id} />
                  <input type="hidden" name="from" value="alerts" />
                  <button type="submit" className="link">remove</button>
                </form>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <p className="muted">No alerts yet.</p>}
    </>
  )
}
