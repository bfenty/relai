import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'
import { Segmented } from '../components/ui'

// One time range for every monitoring page: switching from Usage to Performance keeps
// the window you were looking at. The choice is remembered per browser.

const PRESETS: { value: string; label: string; days: number }[] = [
  { value: '24h', label: '24h', days: 1 },
  { value: '7d', label: '7d', days: 7 },
  { value: '30d', label: '30d', days: 30 },
  { value: '90d', label: '90d', days: 90 },
]
const RANGE_OPTIONS = [...PRESETS.map(p => ({ value: p.value, label: p.label })), { value: 'custom', label: 'Custom' }]
const STORAGE_KEY = 'relai.range'

export function toDateInput(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function computeRange(preset: string, fromDate: string, toDate: string): { from: string; to: string } {
  const now = new Date()
  if (preset === 'custom' && fromDate && toDate) {
    return {
      from: new Date(`${fromDate}T00:00:00`).toISOString(),
      to: new Date(`${toDate}T23:59:59`).toISOString(),
    }
  }
  const days = PRESETS.find(p => p.value === preset)?.days ?? 7
  return { from: new Date(now.getTime() - days * 86400000).toISOString(), to: now.toISOString() }
}

type TimeRange = {
  preset: string
  setPreset: (p: string) => void
  fromDate: string
  setFromDate: (d: string) => void
  toDate: string
  setToDate: (d: string) => void
  /** ISO bounds of the window. */
  from: string
  to: string
  /** The same-length window immediately before, for period-over-period change. */
  prev: { from: string; to: string }
  /** The window in whole hours, for the endpoints that take `hours`. */
  hours: number
}

const Ctx = createContext<TimeRange | null>(null)

function readPreset(): string {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v && RANGE_OPTIONS.some(o => o.value === v && v !== 'custom')) return v
  } catch { /* storage unavailable */ }
  return '7d'
}

export function TimeRangeProvider({ children }: { children: ReactNode }) {
  const [preset, setPresetState] = useState(readPreset)
  const today = new Date()
  const [fromDate, setFromDate] = useState(toDateInput(new Date(today.getTime() - 7 * 86400000)))
  const [toDate, setToDate] = useState(toDateInput(today))

  const setPreset = (p: string) => {
    setPresetState(p)
    try { if (p !== 'custom') localStorage.setItem(STORAGE_KEY, p) } catch { /* not essential */ }
  }

  const value = useMemo<TimeRange>(() => {
    const { from, to } = computeRange(preset, fromDate, toDate)
    const span = new Date(to).getTime() - new Date(from).getTime()
    return {
      preset, setPreset, fromDate, setFromDate, toDate, setToDate, from, to,
      prev: { from: new Date(new Date(from).getTime() - span).toISOString(), to: from },
      hours: Math.max(1, Math.min(24 * 90, Math.round(span / 3600000))),
    }
    // setPreset is stable in behavior; recomputing on the three inputs is what matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [preset, fromDate, toDate])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useTimeRange(): TimeRange {
  const v = useContext(Ctx)
  if (!v) throw new Error('useTimeRange needs a TimeRangeProvider')
  return v
}

/** The shared range control, for a page header. */
export function RangePicker() {
  const r = useTimeRange()
  return (
    <>
      <Segmented label="Time range" options={RANGE_OPTIONS} value={r.preset} onChange={r.setPreset} />
      {r.preset === 'custom' && (
        <div className="flex items-center gap-1.5">
          <input type="date" value={r.fromDate} max={r.toDate} onChange={e => r.setFromDate(e.target.value)} className="input" aria-label="From" />
          <span className="text-xs text-slate-500">–</span>
          <input type="date" value={r.toDate} min={r.fromDate} max={toDateInput(new Date())} onChange={e => r.setToDate(e.target.value)} className="input" aria-label="To" />
        </div>
      )}
    </>
  )
}
