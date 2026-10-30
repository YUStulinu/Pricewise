// Geometry for the price chart, kept apart from React so it can be tested.

export interface Point {
  day: string // YYYY-MM-DD
  price: number
  reference: number | null
}

/** Round axis ticks covering [min, max]: steps of 1, 2 or 5 times a power of ten. */
export function niceTicks(min: number, max: number, count = 5): number[] {
  if (!(max > min)) {
    const pad = Math.abs(min) * 0.1 || 1
    return niceTicks(min - pad, max + pad, count)
  }
  const raw = (max - min) / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? 10 * mag
  const ticks = []
  for (let v = Math.floor(min / step) * step; v <= max + step * 1e-9; v += step) {
    ticks.push(Math.round(v / step) * step)
  }
  if (ticks[ticks.length - 1] < max) ticks.push(ticks[ticks.length - 1] + step)
  return ticks
}

export function dayNumber(day: string): number {
  return Math.round(Date.parse(day + 'T00:00:00Z') / 86_400_000)
}

/** A step line: a price holds until the next observation. Gaps longer than `maxGap` days break the line. */
export function stepPath(points: Point[], x: (d: number) => number, y: (p: number) => number, maxGap = 3): string {
  let path = ''
  let prev: { d: number; p: number } | null = null
  for (const pt of points) {
    const d = dayNumber(pt.day)
    if (prev && d - prev.d <= maxGap) {
      path += ` H${x(d).toFixed(1)} V${y(pt.price).toFixed(1)}`
    } else {
      path += ` M${x(d).toFixed(1)} ${y(pt.price).toFixed(1)}`
    }
    prev = { d, p: pt.price }
  }
  if (prev) path += ` H${x(prev.d + 1).toFixed(1)}`
  return path.trim()
}

/** Runs of consecutive days showing a "was" price, as [first day, last day, reference]. */
export function referenceRuns(points: Point[]): [number, number, number][] {
  const runs: [number, number, number][] = []
  for (const pt of points) {
    if (pt.reference == null) continue
    const d = dayNumber(pt.day)
    const last = runs[runs.length - 1]
    if (last && d - last[1] <= 3 && last[2] === pt.reference) last[1] = d
    else runs.push([d, d, pt.reference])
  }
  return runs
}

export function monthTicks(first: number, last: number): { d: number; label: string }[] {
  const out = []
  const start = new Date(first * 86_400_000)
  let m = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + (start.getUTCDate() > 1 ? 1 : 0), 1))
  while (m.getTime() / 86_400_000 <= last) {
    out.push({ d: m.getTime() / 86_400_000, label: m.toLocaleString('en-GB', { month: 'short', timeZone: 'UTC' }) })
    m = new Date(Date.UTC(m.getUTCFullYear(), m.getUTCMonth() + 1, 1))
  }
  return out
}
