import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  BarChart, Bar, AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'
import type { TooltipProps } from 'recharts'
import {
  Activity, Zap, DollarSign, AlertTriangle, ChevronRight,
  ArrowDown, ArrowUp, RefreshCw,
} from 'lucide-react'
import { PageHeader, Panel, Segmented, KpiTile, KpiStrip, Badge } from '../components/ui'
import { RangePicker, useTimeRange } from '../hooks/useTimeRange'
import { compact, money } from '../utils/format'
import { analyticsApi } from '../api/client'
import { useDashboardStream } from '../hooks/useDashboardStream'
import { ENTITY, ENTITY_OTHER, ui, sem, status, axisTick, tooltipStyle } from '../theme'
import type { AnalyticsGroupBy, AnalyticsInterval, AnalyticsMetric, AnalyticsGroup, AnalyticsResult } from '../types'

// ── Options ─────────────────────────────────────────────────────────────────

const GROUP_OPTIONS: { value: AnalyticsGroupBy; label: string }[] = [
  { value: 'model', label: 'Model' },
  { value: 'user', label: 'User' },
  { value: 'api_key', label: 'API key' },
  { value: 'backend', label: 'Host' },
]

const METRIC_OPTIONS: { value: AnalyticsMetric; label: string }[] = [
  { value: 'tokens', label: 'Tokens' },
  { value: 'requests', label: 'Requests' },
  { value: 'cost', label: 'Cost' },
]

const INTERVAL_OPTIONS: { value: AnalyticsInterval | 'auto'; label: string }[] = [
  { value: 'auto', label: 'Auto' },
  { value: 'hour', label: 'Hour' },
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
]

type ChartKind = 'bar' | 'area'
const CHART_OPTIONS: { value: ChartKind; label: string }[] = [
  { value: 'bar', label: 'Bars' },
  { value: 'area', label: 'Area' },
]


// ── Formatting ──────────────────────────────────────────────────────────────

// The chart colors only as many groups as the entity palette has distinguishable
// colors; the rest (and the server's own long tail) stack as one gray "Other".
const CHART_OTHER = '__chart_other__'
type ChartGroup = { key: string; label: string; members: string[] }

function chartGroupsOf(groups: { key: string; label: string }[]): ChartGroup[] {
  const top = groups.filter(g => g.key !== '__other__').slice(0, ENTITY.length)
  const rest = groups.filter(g => !top.includes(g))
  const out: ChartGroup[] = top.map(g => ({ key: g.key, label: g.label, members: [g.key] }))
  if (rest.length) out.push({ key: CHART_OTHER, label: 'Other', members: rest.map(g => g.key) })
  return out
}





function formatMetric(value: number, metric: AnalyticsMetric): string {
  return metric === 'cost' ? money(value) : value.toLocaleString()
}

function formatAxis(value: number, metric: AnalyticsMetric): string {
  return metric === 'cost' ? money(value) : compact(value)
}

function formatBucketLabel(iso: string, interval: AnalyticsInterval): string {
  const d = new Date(iso)
  if (interval === 'hour') return d.toLocaleTimeString([], { hour: 'numeric' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}


function logsLinkFor(groupBy: AnalyticsGroupBy, g: AnalyticsGroup): string | null {
  if (g.key === '__other__' || g.key === 'none') return null
  switch (groupBy) {
    case 'model': return `/logs?model=${encodeURIComponent(g.label)}`
    case 'api_key': return `/logs?api_key_id=${g.key}`
    case 'backend': return `/logs?backend_id=${g.key}`
    default: return null
  }
}

/**
 * Fractional change; null when there is nothing meaningful to compare against (no
 * previous period, or one so thin that a percentage would just be noise).
 */
function change(now: number, before: number | undefined): number | null {
  if (before == null || before <= 0) return null
  if (now > 0 && before / now < 0.02) return null
  return (now - before) / before
}

type Cell = { requests: number; tokens: number; cost: number; errors: number }

/** Per-bucket totals across every group, for sparklines. */
function bucketTotals(data: AnalyticsResult | undefined, pick: (c: Cell) => number): number[] {
  if (!data) return []
  return data.buckets.map(b => Object.values(b.groups).reduce((sum, c) => sum + pick(c), 0))
}

// ── Chart tooltip ───────────────────────────────────────────────────────────

function UsageTooltip({ active, payload, metric }: TooltipProps<number, string> & { metric: AnalyticsMetric }) {
  if (!active || !payload?.length) return null
  const rows = payload.filter(p => (p.value ?? 0) > 0).sort((a, b) => (b.value ?? 0) - (a.value ?? 0))
  const total = rows.reduce((s, p) => s + (p.value ?? 0), 0)
  const time = payload[0]?.payload?.time as string | undefined
  const shown = rows.slice(0, 8)
  return (
    <div style={tooltipStyle.contentStyle} className="min-w-[220px] px-3 py-2.5">
      <div className="mb-2 flex items-center justify-between gap-4 border-b border-slate-700 pb-1.5">
        <span className="text-xs font-semibold text-slate-100">
          {time ? new Date(time).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : ''}
        </span>
        <span className="text-xs font-semibold text-slate-100 tabular">{formatMetric(total, metric)}</span>
      </div>
      <div className="space-y-1">
        {shown.map(p => (
          <div key={String(p.dataKey)} className="flex items-center gap-2 text-xs">
            <span className="h-2 w-2 shrink-0 rounded-sm" style={{ backgroundColor: p.color }} />
            <span className="min-w-0 flex-1 truncate text-slate-300">{p.name}</span>
            <span className="text-slate-100 tabular">{formatMetric(p.value ?? 0, metric)}</span>
          </div>
        ))}
        {rows.length > shown.length && <div className="pt-0.5 text-[11px] text-slate-500">+{rows.length - shown.length} more</div>}
      </div>
    </div>
  )
}

// ── KV cache tab ────────────────────────────────────────────────────────────


// ── Page ────────────────────────────────────────────────────────────────────

type SortKey = 'label' | 'requests' | 'tokens' | 'cost' | 'errors'

function SortHead({ k, label, right, sort, onSort }: {
  k: SortKey
  label: string
  right?: boolean
  sort: { key: SortKey; dir: 1 | -1 }
  onSort: (k: SortKey) => void
}) {
  const on = sort.key === k
  return (
    <th className={right ? '!text-right' : ''} aria-sort={on ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'}>
      <button onClick={() => onSort(k)} className={`inline-flex items-center gap-1 uppercase tracking-wider hover:text-slate-200 ${on ? 'text-slate-300' : ''}`}>
        {label}
        {on && (sort.dir === 1 ? <ArrowUp className="h-3 w-3" /> : <ArrowDown className="h-3 w-3" />)}
      </button>
    </th>
  )
}

export default function Usage() {
  const [groupBy, setGroupBy] = useState<AnalyticsGroupBy>('model')
  const [metric, setMetric] = useState<AnalyticsMetric>('tokens')
  const [interval, setInterval] = useState<AnalyticsInterval | 'auto'>('auto')
  const [chartKind, setChartKind] = useState<ChartKind>('bar')
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: 'tokens', dir: -1 })


  useDashboardStream()

  const { from, to, prev } = useTimeRange()

  const { data, isLoading, isError, error, dataUpdatedAt, isFetching, refetch } = useQuery({
    queryKey: ['analytics', groupBy, interval, from, to],
    queryFn: () => analyticsApi.usage({ from, to, group_by: groupBy, interval: interval === 'auto' ? undefined : interval }),
    refetchInterval: 30000,
    placeholderData: p => p,
  })
  // Only the summary is used; the previous period changes slowly, so it refreshes rarely.
  const { data: before } = useQuery({
    queryKey: ['analytics-prev', prev.from, prev.to],
    queryFn: () => analyticsApi.usage({ from: prev.from, to: prev.to, group_by: 'model' }),
    staleTime: 5 * 60_000,
    placeholderData: p => p,
  })

  const effectiveInterval: AnalyticsInterval = data?.interval ?? 'day'
  const groups = useMemo(() => data?.groups ?? [], [data])
  const chartGroups = useMemo(() => chartGroupsOf(groups), [groups])
  const colorOf = useMemo(() => {
    const m = new Map<string, string>()
    chartGroups.forEach((g, i) => m.set(g.key, g.key === CHART_OTHER ? ENTITY_OTHER : ENTITY[i]))
    return m
  }, [chartGroups])
  const visibleGroups = chartGroups.filter(g => !hidden.has(g.key))

  const chartData = useMemo(() => {
    if (!data) return []
    return data.buckets.map(b => {
      const row: Record<string, number | string> = { label: formatBucketLabel(b.time, data.interval), time: b.time }
      for (const g of chartGroups) row[g.key] = g.members.reduce((sum, k) => sum + (b.groups[k]?.[metric] ?? 0), 0)
      return row
    })
  }, [data, chartGroups, metric])

  const sortedGroups = useMemo(() => {
    const rows = [...groups]
    rows.sort((a, b) => {
      if (sort.key === 'label') return a.label.localeCompare(b.label) * sort.dir
      return (a[sort.key] - b[sort.key]) * sort.dir
    })
    return rows
  }, [groups, sort])

  const errorTrend = useMemo(() => (data ? data.buckets.map(b => {
    const t = Object.values(b.groups).reduce((a, c) => ({ r: a.r + c.requests, e: a.e + c.errors }), { r: 0, e: 0 })
    return t.r ? t.e / t.r : 0
  }) : []), [data])

  const summary = data?.summary
  const metricTotal = summary?.[metric] ?? 0
  const errorRate = summary && summary.requests > 0 ? summary.errors / summary.requests : 0
  const prevErrorRate = before && before.summary.requests > 0 ? before.summary.errors / before.summary.requests : undefined

  const toggleSeries = (key: string) =>
    setHidden(h => {
      const next = new Set(h)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const sortBy = (key: SortKey) =>
    setSort(s => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: key === 'label' ? 1 : -1 }))

  const groupLabel = GROUP_OPTIONS.find(g => g.value === groupBy)?.label ?? ''
  const metricLabel = METRIC_OPTIONS.find(m => m.value === metric)?.label ?? ''
  const updated = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' }) : null


  return (
    <div>
      <PageHeader
        title="Usage"
        description="Requests, tokens and cost by model, user, key and host"
        actions={
          <>
            {updated && <span className="hidden text-xs text-slate-500 lg:inline">Updated {updated}</span>}
            <RangePicker />
            <button onClick={() => refetch()} className="btn-secondary !px-2" title="Refresh" aria-label="Refresh">
              <RefreshCw className={`h-4 w-4 ${isFetching ? 'animate-spin' : ''}`} />
            </button>
          </>
        }
      />

      {/* ── Headline figures ── */}
      <KpiStrip className="mb-3 grid-cols-2 xl:grid-cols-4">
        <KpiTile label="Requests" icon={Activity} value={compact(summary?.requests ?? 0)}
          delta={change(summary?.requests ?? 0, before?.summary.requests)}
          trend={bucketTotals(data, c => c.requests)} color={sem.volume}
          sub={`${(summary?.requests ?? 0).toLocaleString()} total`} />
        <KpiTile label="Tokens" icon={Zap} value={compact(summary?.tokens ?? 0)}
          delta={change(summary?.tokens ?? 0, before?.summary.tokens)}
          trend={bucketTotals(data, c => c.tokens)} color={sem.volume}
          sub={summary && summary.requests ? `${compact(summary.tokens / summary.requests)} per request` : undefined} />
        <KpiTile label="Cost" icon={DollarSign} value={money(summary?.cost ?? 0)}
          delta={change(summary?.cost ?? 0, before?.summary.cost)} goodWhen="down"
          trend={bucketTotals(data, c => c.cost)} color={sem.volume}
          sub="at configured prices" />
        <KpiTile label="Error rate" icon={AlertTriangle} value={`${(errorRate * 100).toFixed(errorRate < 0.1 ? 2 : 1)}%`}
          delta={change(errorRate, prevErrorRate)} goodWhen="down"
          trend={errorTrend} color={status.bad}
          tone={errorRate > 0.05 ? 'bad' : errorRate > 0.01 ? 'warn' : undefined}
          sub={`${(summary?.errors ?? 0).toLocaleString()} failed`} />
      </KpiStrip>

      {/* ── Usage over time ── */}
      <Panel
        className="mb-3"
        title={`${metricLabel} by ${groupLabel.toLowerCase()}`}
        subtitle={`per ${effectiveInterval}`}
        actions={
          <>
            <Segmented label="Stack by" options={GROUP_OPTIONS} value={groupBy} onChange={g => { setGroupBy(g); setHidden(new Set()) }} />
            <Segmented label="Metric" options={METRIC_OPTIONS} value={metric} onChange={setMetric} />
          </>
        }
      >
        {isError ? (
          <div className="flex h-80 items-center justify-center text-sm text-crit-300">{(error as Error)?.message || 'Failed to load analytics'}</div>
        ) : isLoading && !data ? (
          <div className="flex h-80 items-center justify-center"><div className="h-7 w-7 animate-spin rounded-full border-2 border-accent-400 border-t-transparent" /></div>
        ) : chartData.length === 0 || groups.length === 0 ? (
          <div className="flex h-80 flex-col items-center justify-center gap-1 text-sm text-slate-500">
            <span className="text-slate-300">No usage in this range</span>
            <span className="text-xs">Requests appear here as soon as they're logged.</span>
          </div>
        ) : (
          <>
            <div className="mb-2 flex flex-wrap items-center gap-1.5">
              {chartGroups.map(g => {
                const off = hidden.has(g.key)
                return (
                  <button key={g.key} onClick={() => toggleSeries(g.key)} aria-pressed={!off}
                    title={off ? `Show ${g.label}` : `Hide ${g.label}`}
                    className={`inline-flex max-w-[220px] items-center gap-1.5 rounded border px-2 py-0.5 text-xs transition-colors ${
                      off ? 'border-slate-700 text-slate-500 line-through' : 'border-slate-600 text-slate-200 hover:border-slate-500'
                    }`}>
                    <span className="h-2 w-2 shrink-0 rounded-sm" style={{ backgroundColor: off ? ui.border : colorOf.get(g.key) }} />
                    <span className="truncate">{g.label}</span>
                  </button>
                )
              })}
              {hidden.size > 0 && <button onClick={() => setHidden(new Set())} className="px-1 text-xs text-accent-400 hover:text-accent-300">Show all</button>}
              <span className="ml-auto flex items-center gap-2">
                <Segmented label="Interval" options={INTERVAL_OPTIONS} value={interval} onChange={setInterval} />
                <Segmented label="Chart type" options={CHART_OPTIONS} value={chartKind} onChange={setChartKind} />
              </span>
            </div>
            <div className="h-80">
              <ResponsiveContainer width="100%" height="100%">
                {chartKind === 'bar' ? (
                  <BarChart data={chartData} margin={{ top: 4, right: 4, bottom: 0, left: 0 }} barCategoryGap="18%">
                    <CartesianGrid vertical={false} stroke={ui.grid} />
                    <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={{ stroke: ui.border }} interval="preserveStartEnd" minTickGap={24} />
                    <YAxis tick={axisTick} tickLine={false} axisLine={false} width={52} tickFormatter={v => formatAxis(v, metric)} />
                    <Tooltip cursor={tooltipStyle.cursor} content={<UsageTooltip metric={metric} />} />
                    {visibleGroups.map((g, i) => (
                      <Bar key={g.key} dataKey={g.key} name={g.label} stackId="a" fill={colorOf.get(g.key)} isAnimationActive={false}
                        radius={i === visibleGroups.length - 1 ? [3, 3, 0, 0] : undefined} />
                    ))}
                  </BarChart>
                ) : (
                  <AreaChart data={chartData} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
                    <CartesianGrid vertical={false} stroke={ui.grid} />
                    <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={{ stroke: ui.border }} interval="preserveStartEnd" minTickGap={24} />
                    <YAxis tick={axisTick} tickLine={false} axisLine={false} width={52} tickFormatter={v => formatAxis(v, metric)} />
                    <Tooltip cursor={{ stroke: ui.border }} content={<UsageTooltip metric={metric} />} />
                    {visibleGroups.map(g => (
                      <Area key={g.key} type="monotone" dataKey={g.key} name={g.label} stackId="a" isAnimationActive={false}
                        stroke={colorOf.get(g.key)} strokeWidth={1.5} fill={colorOf.get(g.key)} fillOpacity={0.22} />
                    ))}
                  </AreaChart>
                )}
              </ResponsiveContainer>
            </div>
          </>
        )}
      </Panel>

      {/* ── Breakdown ── */}
      <Panel
        className="overflow-hidden"
        bodyClassName="overflow-x-auto"
        title={`Breakdown by ${groupLabel.toLowerCase()}`}
        subtitle={`${groups.length} ${groups.length === 1 ? 'entry' : 'entries'} · share of ${metricLabel.toLowerCase()}`}
      >
        <table className="table-pro">
          <thead>
            <tr>
              <SortHead k="label" label={groupLabel} sort={sort} onSort={sortBy} />
              <SortHead k="requests" label="Requests" right sort={sort} onSort={sortBy} />
              <SortHead k="tokens" label="Tokens" right sort={sort} onSort={sortBy} />
              <SortHead k="cost" label="Cost" right sort={sort} onSort={sortBy} />
              <SortHead k="errors" label="Errors" right sort={sort} onSort={sortBy} />
              <th className="w-48 !text-right">Share</th>
            </tr>
          </thead>
          <tbody>
            {sortedGroups.length === 0 ? (
              <tr><td colSpan={6} className="py-8 text-center text-slate-500">No data</td></tr>
            ) : sortedGroups.map(g => {
              const share = metricTotal > 0 ? (g[metric] / metricTotal) * 100 : 0
              const link = logsLinkFor(groupBy, g)
              const color = colorOf.get(g.key) ?? ENTITY_OTHER
              const errRate = g.requests ? g.errors / g.requests : 0
              return (
                <tr key={g.key}>
                  <td>
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ backgroundColor: color }} />
                      {link ? (
                        <Link to={link} className="group flex min-w-0 items-center gap-1 font-medium text-slate-100 hover:text-accent-400">
                          <span className="truncate">{g.label}</span>
                          <ChevronRight className="h-3 w-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-70" />
                        </Link>
                      ) : (
                        <span className="truncate font-medium text-slate-100">{g.label}</span>
                      )}
                    </div>
                  </td>
                  <td className="text-right">{g.requests.toLocaleString()}</td>
                  <td className="text-right">{g.tokens.toLocaleString()}</td>
                  <td className="text-right">{money(g.cost)}</td>
                  <td className="text-right">
                    {g.errors > 0 ? (
                      <Badge tone={errRate > 0.05 ? 'bad' : 'warn'}>{g.errors.toLocaleString()} · {(errRate * 100).toFixed(1)}%</Badge>
                    ) : <span className="text-slate-500">0</span>}
                  </td>
                  <td>
                    <div className="flex items-center justify-end gap-2">
                      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-slate-700/70">
                        <div className="h-full rounded-full" style={{ width: `${share}%`, backgroundColor: color }} />
                      </div>
                      <span className="w-12 text-right text-xs text-slate-400 tabular">{share.toFixed(1)}%</span>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </Panel>

    </div>
  )
}
