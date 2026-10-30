import { describe, expect, it } from 'vitest'
import { dayNumber, monthTicks, niceTicks, referenceRuns, stepPath } from '../lib/chart'

describe('niceTicks', () => {
  it('covers the range with round steps', () => {
    expect(niceTicks(83, 141)).toEqual([80, 100, 120, 140, 160])
    expect(niceTicks(0.4, 2.2, 4)).toEqual([0, 0.5, 1, 1.5, 2, 2.5])
  })
  it('handles a flat series', () => {
    const t = niceTicks(100, 100)
    expect(t[0]).toBeLessThan(100)
    expect(t[t.length - 1]).toBeGreaterThan(100)
  })
})

describe('stepPath', () => {
  const x = (d: number) => d - dayNumber('2026-06-01')
  const y = (p: number) => p
  it('holds each price until the next day and breaks on long gaps', () => {
    const pts = [
      { day: '2026-06-01', price: 10, reference: null },
      { day: '2026-06-02', price: 12, reference: null },
      { day: '2026-06-10', price: 9, reference: null },
    ]
    expect(stepPath(pts, x, y)).toBe('M0.0 10.0 H1.0 V12.0 M9.0 9.0 H10.0')
  })
})

describe('referenceRuns', () => {
  it('groups days that show the same was-price', () => {
    const pts = [
      { day: '2026-06-01', price: 10, reference: null },
      { day: '2026-06-02', price: 8, reference: 12 },
      { day: '2026-06-03', price: 8, reference: 12 },
      { day: '2026-06-05', price: 8, reference: 12 },
    ]
    const d = dayNumber('2026-06-02')
    expect(referenceRuns(pts)).toEqual([[d, d + 3, 12]])
  })
})

describe('monthTicks', () => {
  it('labels the first of each month', () => {
    expect(monthTicks(dayNumber('2026-06-01'), dayNumber('2026-08-15')).map((t) => t.label)).toEqual(['Jun', 'Jul', 'Aug'])
  })
})
