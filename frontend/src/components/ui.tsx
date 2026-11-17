import { useEffect, useRef, useState, type ReactNode } from 'react'
import { AlertTriangle, ArrowDownRight, ArrowUpRight, Info, Minus } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

// Building blocks for the console look (see index.css for the matching classes).
// Kept deliberately small: pages compose these rather than growing a component zoo.

export function PageHeader({ title, description, actions }: {
  title: string
  description?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="page-header">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
        <h1 className="page-title">{title}</h1>
        {description && <p className="page-description">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

/** An explanation kept out of the way: an (i) that shows the text on hover or focus. */
export function InfoTip({ children, align = 'left' }: { children: ReactNode; align?: 'left' | 'right' }) {
  return (
    <span className="info-tip">
      <button type="button" aria-label="About this" className="info-tip-button">
        <Info className="h-3.5 w-3.5" />
      </button>
      <span role="tooltip" className={`info-tip-body ${align === 'right' ? 'right-0' : 'left-0'}`}>{children}</span>
    </span>
  )
}

export type LegendItem = { label: string; color: string; dashed?: boolean; hint?: string }

/** A compact legend for a panel header: a swatch and a word per series; details on hover. */
export function Legend({ items }: { items: LegendItem[] }) {
  return (
    <span className="legend">
      {items.map(i => (
        <span key={i.label} className="legend-item" title={i.hint}>
          <i className="legend-swatch" style={i.dashed
            ? { background: `repeating-linear-gradient(90deg, ${i.color} 0 3px, transparent 3px 5px)`, height: 2, width: 12, borderRadius: 0 }
            : { background: i.color }} />
          {i.label}
        </span>
      ))}
    </span>
  )
}

export function Panel({ title, subtitle, info, legend, actions, children, className = '', bodyClassName = 'panel-body' }: {
  title?: ReactNode
  subtitle?: ReactNode
  /** Longer explanation, shown in a tooltip beside the title. */
  info?: ReactNode
  legend?: LegendItem[]
  actions?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
}) {
  return (
    <section className={`panel ${className}`}>
      {(title || actions || legend) && (
        <header className="panel-header">
          <div className="flex min-w-0 items-center gap-2">
            {title && <h2 className="panel-title">{title}</h2>}
            {subtitle && <span className="panel-subtitle">{subtitle}</span>}
            {info && <InfoTip>{info}</InfoTip>}
          </div>
          {(legend || actions) && (
            <div className="ml-auto flex flex-wrap items-center gap-3">
              {legend && <Legend items={legend} />}
              {actions}
            </div>
          )}
        </header>
      )}
      <div className={bodyClassName}>{children}</div>
    </section>
  )
}

/** The one thing on the page that needs attention, as a banner. Render nothing when all is well. */
export function Callout({ title, children, action }: { title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="callout" role="status">
      <AlertTriangle className="h-4 w-4 shrink-0 text-accent-500" />
      <div className="min-w-0 flex-1 text-[13px]">
        <strong className="font-semibold text-accent-400">{title}</strong>
        {children && <span className="ml-1.5 text-slate-300">{children}</span>}
      </div>
      {action}
    </div>
  )
}

/** KPI tiles joined into one strip with hairline dividers. */
export function KpiStrip({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`kpi-strip ${className}`}>{children}</div>
}

export function Segmented<T extends string>({ options, value, onChange, label }: {
  options: { value: T; label: string }[]
  value: T
  onChange: (v: T) => void
  label?: string
}) {
  return (
    <div role="group" aria-label={label} className="segmented">
      {options.map(o => (
        <button key={o.value} type="button" aria-pressed={value === o.value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

/** A tiny trend line; no axes, just shape. Draws nothing for fewer than two points. */
export function Sparkline({ values, color, height = 28 }: { values: number[]; color: string; height?: number }) {
  if (values.length < 2) return <div style={{ height }} />
  const w = 100
  const max = Math.max(...values)
  const min = Math.min(...values)
  const span = max - min || 1
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, height - 2 - ((v - min) / span) * (height - 4)])
  const line = pts.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(' ')
  const area = `0,${height} ${line} ${w},${height}`
  const id = `spark-${color.replace('#', '')}`
  return (
    <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" className="w-full" style={{ height }} aria-hidden>
      <defs>
        <linearGradient id={id} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.28} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <polygon points={area} fill={`url(#${id})`} />
      <polyline points={line} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
  )
}

/**
 * One headline figure. `delta` is the fractional change against the previous period;
 * `goodWhen` says which direction is good news (errors going up is bad, requests going
 * up is neutral, so it gets no color at all).
 */
export function KpiTile({ label, value, sub, icon: Icon, delta, goodWhen = 'neutral', trend, color, tone, hero }: {
  label: string
  value: string
  sub?: ReactNode
  icon?: LucideIcon
  delta?: number | null
  goodWhen?: 'up' | 'down' | 'neutral'
  trend?: number[]
  color: string
  tone?: 'warn' | 'bad'
  /** The one figure to watch on the page: marked with the attention color. */
  hero?: boolean
}) {
  let deltaEl: ReactNode = null
  if (delta != null && Number.isFinite(delta)) {
    const flat = Math.abs(delta) < 0.005
    const up = delta > 0
    const good = goodWhen === 'neutral' || flat ? null : (up === (goodWhen === 'up'))
    const cls = good == null ? 'text-slate-400' : good ? 'text-good-400' : 'text-crit-400'
    const Arrow = flat ? Minus : up ? ArrowUpRight : ArrowDownRight
    deltaEl = (
      <span className={`inline-flex items-center gap-0.5 text-xs font-medium tabular ${cls}`} title="Change against the previous period of the same length">
        <Arrow className="h-3.5 w-3.5" />
        {flat ? '0%' : Math.abs(delta) >= 10 ? '>999%' : `${Math.abs(delta * 100).toFixed(Math.abs(delta) < 0.1 ? 1 : 0)}%`}
      </span>
    )
  }
  return (
    <div className={`panel flex flex-col overflow-hidden ${tone === 'bad' ? 'border-crit-500/40' : tone === 'warn' ? 'border-warn-500/40' : ''} ${hero ? 'shadow-[inset_0_2px_0_theme(colors.accent.500)]' : ''}`}>
      <div className="flex items-start justify-between gap-2 px-4 pt-3.5">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">{label}</span>
        {Icon && <Icon className="h-4 w-4 text-slate-600" />}
      </div>
      <div className="flex items-baseline gap-2 px-4 pt-1">
        <span className={`font-mono text-2xl font-medium tracking-tight tabular ${hero ? 'text-accent-400' : 'text-slate-50'}`}>{value}</span>
        {deltaEl}
      </div>
      <div className="min-h-[18px] px-4 text-xs text-slate-500">{sub}</div>
      <div className="mt-auto pt-1">{trend && <Sparkline values={trend} color={color} />}</div>
    </div>
  )
}

export type TabDef = { id: string; label: string; icon?: LucideIcon; badge?: ReactNode; content: () => ReactNode }

/** Tabs whose selection is remembered per `storageId`. Only the active tab renders. */
const OPEN_TAB_EVENT = 'relai:open-tab'

/** Select a tab of the <Tabs storageId=…> on this page and scroll to it. */
export function openTab(storageId: string, id: string) {
  window.dispatchEvent(new CustomEvent(OPEN_TAB_EVENT, { detail: { storageId, id } }))
}

export function Tabs({ storageId, tabs }: { storageId: string; tabs: TabDef[] }) {
  const key = `relai.tabs.${storageId}`
  const [active, setActive] = useState<string>(() => {
    try {
      const v = localStorage.getItem(key)
      if (v && tabs.some(t => t.id === v)) return v
    } catch { /* storage unavailable */ }
    return tabs[0]?.id ?? ''
  })
  const select = (id: string) => {
    setActive(id)
    try { localStorage.setItem(key, id) } catch { /* storage unavailable */ }
  }
  // openTab() elsewhere on the page selects a tab and brings it into view.
  const ref = useRef<HTMLElement>(null)
  useEffect(() => {
    const onOpen = (e: Event) => {
      const { storageId: target, id } = (e as CustomEvent<{ storageId: string; id: string }>).detail
      if (target !== storageId) return
      select(id)
      ref.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
    window.addEventListener(OPEN_TAB_EVENT, onOpen)
    return () => window.removeEventListener(OPEN_TAB_EVENT, onOpen)
  })
  const current = tabs.find(t => t.id === active) ?? tabs[0]
  return (
    <section ref={ref} className="panel scroll-mt-14 overflow-hidden">
      <div role="tablist" className="flex overflow-x-auto border-b border-slate-700 px-1">
        {tabs.map(t => {
          const on = t.id === current?.id
          const Icon = t.icon
          return (
            <button
              key={t.id}
              role="tab"
              aria-selected={on}
              onClick={() => select(t.id)}
              className={`relative flex shrink-0 items-center gap-2 px-3 py-2.5 text-[13px] font-medium transition-colors ${
                on ? 'text-slate-50' : 'text-slate-400 hover:text-slate-100'
              }`}
            >
              {Icon && <Icon className={`h-4 w-4 ${on ? 'text-accent-400' : 'text-slate-500'}`} />}
              {t.label}
              {t.badge}
              {on && <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-t bg-accent-500" />}
            </button>
          )
        })}
      </div>
      <div role="tabpanel" className="p-3">{current?.content()}</div>
    </section>
  )
}

export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'info' | 'attention' }) {
  const cls = {
    neutral: 'bg-slate-700/70 text-slate-300',
    good: 'bg-good-500/15 text-good-300',
    warn: 'bg-warn-500/15 text-warn-300',
    bad: 'bg-crit-500/15 text-crit-300',
    info: 'bg-input-500/15 text-input-300',
    attention: 'bg-accent-500/15 text-accent-300',
  }[tone]
  return <span className={`inline-flex items-center rounded px-1.5 py-px text-[11px] font-medium tabular ${cls}`}>{children}</span>
}
