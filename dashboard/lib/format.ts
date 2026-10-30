export function euro(v: number | null | undefined, digits = 2): string {
  if (v == null) return '–'
  return new Intl.NumberFormat('en-IE', { style: 'currency', currency: 'EUR', minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v)
}

export const FLAG_NAMES: Record<string, string> = {
  above_30_day_low: 'Above the 30-day low',
  never_charged: 'Never charged',
  inflate_then_drop: 'Raised, then "cut"',
}

export function change(now: number | null, before: number | null): string | null {
  if (now == null || before == null || before === 0) return null
  const pct = ((now - before) / before) * 100
  if (Math.abs(pct) < 0.5) return 'same as 30 days ago'
  return `${pct > 0 ? '+' : '−'}${Math.abs(pct).toFixed(0)}% in 30 days`
}
