/** Compact human number: 1.2k, 3.4M, 5.6B. */
export function compact(n: number): string {
  const a = Math.abs(n)
  if (a >= 1e9) return `${(n / 1e9).toFixed(a >= 1e10 ? 0 : 1)}B`
  if (a >= 1e6) return `${(n / 1e6).toFixed(a >= 1e7 ? 0 : 1)}M`
  if (a >= 1e3) return `${(n / 1e3).toFixed(a >= 1e4 ? 0 : 1)}k`
  return Math.round(n).toLocaleString()
}

export function money(v: number): string {
  if (v === 0) return '$0'
  if (v < 0.01) return `$${v.toFixed(4)}`
  if (v < 100) return `$${v.toFixed(2)}`
  return `$${Math.round(v).toLocaleString()}`
}

/** A duration of lost GPU time: 45s, 12 min, 7.8 h. */
export function fmtLost(seconds: number): string {
  if (seconds < 90) return `${seconds.toFixed(0)}s`
  if (seconds < 5400) return `${(seconds / 60).toFixed(0)} min`
  return `${(seconds / 3600).toFixed(1)} h`
}

export const pct = (v: number) => `${(v * 100).toFixed(v < 0.1 ? 1 : 0)}%`
