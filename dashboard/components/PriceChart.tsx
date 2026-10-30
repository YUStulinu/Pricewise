'use client'

import { useMemo, useState } from 'react'
import { dayNumber, monthTicks, niceTicks, referenceRuns, stepPath, type Point } from '@/lib/chart'
import { euro } from '@/lib/format'

export interface Series {
  name: string
  slot: number // categorical colour slot, fixed per shop
  points: Point[]
}

export interface Marker {
  day: string
  series: string
  label: string
}

const W = 760
const H = 300
const PAD = { left: 56, right: 16, top: 12, bottom: 28 }

export default function PriceChart({ series, markers }: { series: Series[]; markers: Marker[] }) {
  const [hover, setHover] = useState<number | null>(null)

  const geo = useMemo(() => {
    const all = series.flatMap((s) => s.points)
    const days = all.map((p) => dayNumber(p.day))
    const values = all.flatMap((p) => (p.reference ? [p.price, p.reference] : [p.price]))
    const first = Math.min(...days)
    const last = Math.max(...days) + 1
    const ticks = niceTicks(Math.min(...values), Math.max(...values), 5)
    const [lo, hi] = [ticks[0], ticks[ticks.length - 1]]
    const x = (d: number) => PAD.left + ((d - first) / Math.max(last - first, 1)) * (W - PAD.left - PAD.right)
    const y = (v: number) => PAD.top + (1 - (v - lo) / (hi - lo)) * (H - PAD.top - PAD.bottom)
    return { first, last, ticks, x, y, months: monthTicks(first, last) }
  }, [series])

  if (!series.some((s) => s.points.length)) return <p className="muted">No prices collected yet.</p>
  const { x, y, ticks, months, first, last } = geo

  // The day under the pointer, and each shop's price that day (or its last before).
  const at = (s: Series, d: number) => {
    let found: Point | null = null
    for (const p of s.points) {
      if (dayNumber(p.day) <= d) found = p
      else break
    }
    return found && d - dayNumber(found.day) <= 3 ? found : null
  }

  function move(e: React.PointerEvent<SVGSVGElement>) {
    const r = e.currentTarget.getBoundingClientRect()
    const px = ((e.clientX - r.left) / r.width) * W
    const d = Math.round(first + ((px - PAD.left) / (W - PAD.left - PAD.right)) * (last - first))
    setHover(d >= first && d < last ? d : null)
  }

  const hoverDate = hover != null ? new Date(hover * 86_400_000).toISOString().slice(0, 10) : null

  return (
    <figure className="chart">
      <div className="legend" role="list">
        {series.map((s) => (
          <span key={s.name} role="listitem">
            <i className={`swatch s${s.slot}`} /> {s.name}
          </span>
        ))}
        <span className="legend-note">
          <i className="swatch dashed" /> crossed-out &ldquo;was&rdquo; price
          <i className="flag-key">!</i> flagged discount
        </span>
      </div>
      <div className="plot">
        <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Price per shop over time"
             onPointerMove={move} onPointerLeave={() => setHover(null)}>
          {ticks.map((t) => (
            <g key={t}>
              <line className="grid" x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} />
              <text className="tick" x={PAD.left - 8} y={y(t) + 4} textAnchor="end">{euro(t, 0)}</text>
            </g>
          ))}
          {months.map((m) => (
            <text key={m.d} className="tick" x={x(m.d)} y={H - 8} textAnchor="middle">{m.label}</text>
          ))}
          {series.map((s) => (
            <g key={s.name} className={`series s${s.slot}`}>
              {referenceRuns(s.points).map(([a, b, v]) => (
                <line key={a} className="was" x1={x(a)} x2={x(b + 1)} y1={y(v)} y2={y(v)} />
              ))}
              <path className="line" d={stepPath(s.points, x, y)} />
            </g>
          ))}
          {markers.map((m, i) => {
            const s = series.find((t) => t.name === m.series)
            const p = s && at(s, dayNumber(m.day))
            if (!s || !p) return null
            const cx = x(dayNumber(m.day))
            const cy = y(p.reference ?? p.price) - 14
            return (
              <g key={i} className="flag">
                <title>{m.label}</title>
                <circle cx={cx} cy={cy} r={8} />
                <text x={cx} y={cy + 4} textAnchor="middle">!</text>
              </g>
            )
          })}
          {hover != null && (
            <g className="cross">
              <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={H - PAD.bottom} />
              {series.map((s) => {
                const p = at(s, hover)
                return p ? <circle key={s.name} className={`dot s${s.slot}`} cx={x(hover)} cy={y(p.price)} r={4} /> : null
              })}
            </g>
          )}
        </svg>
        {hover != null && (
          <div className="tooltip" style={{ left: `${(x(hover) / W) * 100}%` }}>
            <strong>{hoverDate}</strong>
            {series.map((s) => {
              const p = at(s, hover)
              return (
                <div key={s.name}>
                  <i className={`swatch s${s.slot}`} /> {s.name}: {p ? euro(p.price) : '–'}
                  {p?.reference ? <span className="muted"> (was {euro(p.reference)})</span> : null}
                </div>
              )
            })}
          </div>
        )}
      </div>
      <details>
        <summary>Show as a table</summary>
        <table>
          <thead>
            <tr><th>Day</th>{series.map((s) => <th key={s.name}>{s.name}</th>)}</tr>
          </thead>
          <tbody>
            {[...new Set(series.flatMap((s) => s.points.map((p) => p.day)))].sort().reverse().map((day) => (
              <tr key={day}>
                <td>{day}</td>
                {series.map((s) => {
                  const p = s.points.find((q) => q.day === day)
                  return <td key={s.name}>{p ? euro(p.price) : ''}{p?.reference ? ` (was ${euro(p.reference)})` : ''}</td>
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  )
}
