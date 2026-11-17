import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Gauge, Share2, SearchCheck, Database, Recycle, Timer, Shuffle, Hourglass } from 'lucide-react'
import RoutingHealth from '../components/RoutingHealth'
import RoutingCharts from '../components/RoutingCharts'
import PrefixHealth, { CAUSES } from '../components/PrefixHealth'
import ThroughputCharts from '../components/ThroughputCharts'
import { RoutingSummary, LostByCause } from '../components/RoutingSummary'
import { CacheTelemetryCard, useCacheTelemetry } from '../components/CacheTelemetry'
import { PageHeader, KpiTile, KpiStrip, Callout, Tabs, Badge, InfoTip, openTab } from '../components/ui'
import { analyticsApi, throughputApi } from '../api/client'
import { useDashboardStream } from '../hooks/useDashboardStream'
import { RangePicker, useTimeRange } from '../hooks/useTimeRange'
import { useBackendColorsByName } from '../utils/colors'
import { compact, fmtLost, pct } from '../utils/format'
import { sem } from '../theme'

// "Why is it slow?" Throughput is decided by prefill, and prefill by how much of
// each prompt comes from cache, so this page leads with speed and cache reuse, then
// where cache misses come from. Detail lives in the tabs.

const TABS_ID = 'performance'

function KVCacheSection() {
  const { all } = useCacheTelemetry()
  const live = all.filter(t => t.available)
  if (all.length === 0) {
    return <p className="text-sm text-slate-500">No backend exposes KV-cache telemetry yet.</p>
  }
  const saved = live.reduce((a, t) => a + t.prompt_tokens_saved, 0)
  return (
    <div>
      <div className="mb-3 flex items-center gap-2 text-xs text-slate-400">
        {saved > 0 ? <>Across live hosts the cache has saved {(saved / 1_000_000).toFixed(1)}M prompt tokens of prefill.</> : 'Per-host KV cache'}
        <InfoTip>
          Counters are cumulative since each backend started. Pool occupancy is the binding memory
          constraint: when it approaches 100% the engine evicts conversation caches.
        </InfoTip>
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {all.map(t => <CacheTelemetryCard key={t.model_map_id} t={t} />)}
      </div>
    </div>
  )
}

/** Token-weighted rate over a run of buckets: Σ tokens ÷ Σ time. */
function weightedRate(points: { tokens: number; ms: number }[]): number | null {
  const t = points.reduce((a, p) => ({ tokens: a.tokens + p.tokens, ms: a.ms + p.ms }), { tokens: 0, ms: 0 })
  return t.ms > 0 ? (t.tokens / t.ms) * 1000 : null
}

export default function Performance() {
  const { hours } = useTimeRange()
  const hostColors = useBackendColorsByName()
  useDashboardStream()

  // Same query keys as the components in the tabs, so react-query shares each fetch.
  const { data: routing } = useQuery({
    queryKey: ['analytics-routing', hours],
    queryFn: () => analyticsApi.routing(hours),
    refetchInterval: 30000,
    placeholderData: p => p,
  })
  const { data: prefix } = useQuery({
    queryKey: ['prefix-analytics', hours],
    queryFn: () => analyticsApi.prefix(hours),
    refetchInterval: 30000,
    placeholderData: p => p,
  })
  const { data: speed } = useQuery({
    queryKey: ['analytics-throughput-series', hours],
    queryFn: () => throughputApi.series(hours),
    refetchInterval: 30000,
    placeholderData: p => p,
  })

  const rates = useMemo(() => {
    const pts = speed?.points ?? []
    return {
      prefill: weightedRate(pts.map(p => ({ tokens: p.prefill_tokens, ms: p.prefill_ms }))),
      decode: weightedRate(pts.map(p => ({ tokens: p.decode_tokens, ms: p.decode_ms }))),
      prefillTrend: pts.map(p => p.prefill_tok_s),
      decodeTrend: pts.map(p => p.decode_tok_s),
    }
  }, [speed])

  const reuse = routing && routing.measured_requests > 0 ? routing.cache_reuse_rate : null
  const lostSeconds = prefix?.estimated_seconds ?? 0
  // The callout names the single costliest cache-miss cause, once misses add up to
  // more than ten minutes of prefill in the window.
  const worstCause = lostSeconds > 600
    ? (prefix?.causes ?? [])
        .filter(c => c.cause !== 'append' && c.cause !== 'new_conversation')
        .sort((a, b) => b.estimated_seconds - a.estimated_seconds)[0]
    : undefined

  return (
    <div>
      <PageHeader
        title="Performance"
        description="Prefill and decode speed, cache reuse, and where prefill time goes"
        actions={<RangePicker />}
      />

      {worstCause && (
        <div className="mb-3">
          <Callout
            title={`${fmtLost(lostSeconds)} of prefill lost to cache misses.`}
            action={<button onClick={() => openTab(TABS_ID, 'prefix')} className="shrink-0 text-[13px] font-semibold text-accent-400 hover:text-accent-300">Inspect →</button>}
          >
            Biggest cause: {CAUSES[worstCause.cause]?.label ?? worstCause.cause} ({fmtLost(worstCause.estimated_seconds)} across {worstCause.requests.toLocaleString()} requests).
          </Callout>
        </div>
      )}

      <KpiStrip className="mb-3 grid-cols-2 md:grid-cols-3 xl:grid-cols-6">
        <KpiTile label="Prefill" icon={Gauge} value={rates.prefill == null ? '—' : `${rates.prefill.toFixed(0)}`}
          sub="tok/s, fresh prompt only" trend={rates.prefillTrend} color={sem.input} />
        <KpiTile label="Decode" icon={Gauge} value={rates.decode == null ? '—' : `${rates.decode.toFixed(1)}`}
          sub="tok/s per stream" trend={rates.decodeTrend} color={sem.output} />
        <KpiTile label="Cache reuse" icon={Recycle} value={reuse == null ? '—' : pct(reuse)}
          sub={routing ? `${compact(routing.cached_tokens)} of ${compact(routing.prompt_tokens)} prompt tokens` : undefined} color={sem.cached} />
        <KpiTile label="Spill rate" icon={Shuffle} value={routing ? pct(routing.spill_rate) : '—'}
          sub="left the warm host" color={sem.attention} />
        <KpiTile label="p95 wait" icon={Hourglass} value={routing ? fmtLost(routing.p95_wait_ms / 1000) : '—'}
          sub={routing ? `mean ${fmtLost(routing.mean_wait_ms / 1000)}` : undefined} color={sem.neutral} />
        <KpiTile label="Prefill lost" icon={Timer} value={prefix && prefix.diagnosed_requests > 0 ? fmtLost(lostSeconds) : '—'}
          color={sem.attention} hero
          sub={prefix ? `${compact(prefix.reprocessed_tokens)} tokens reprocessed` : undefined} />
      </KpiStrip>

      <div className="mb-3 grid grid-cols-1 gap-3 xl:grid-cols-2">
        <RoutingSummary routing={routing} hostColors={hostColors} />
        <LostByCause prefix={prefix} onInspect={() => openTab(TABS_ID, 'prefix')} />
      </div>

      <Tabs
        storageId={TABS_ID}
        tabs={[
          { id: 'speed', label: 'Throughput', icon: Gauge, content: () => <ThroughputCharts hours={hours} /> },
          {
            id: 'routing', label: 'Cache & routing', icon: Share2,
            badge: reuse != null ? <Badge tone={reuse < 0.3 ? 'attention' : 'neutral'}>{pct(reuse)}</Badge> : null,
            content: () => (
              <>
                <RoutingHealth hours={hours} />
                <div className="mt-4 border-t border-slate-700 pt-4"><RoutingCharts hours={hours} /></div>
              </>
            ),
          },
          {
            id: 'prefix', label: 'Miss diagnosis', icon: SearchCheck,
            badge: prefix && prefix.diagnosed_requests > 0 ? <Badge tone={lostSeconds > 600 ? 'attention' : 'neutral'}>{fmtLost(lostSeconds)} lost</Badge> : null,
            content: () => <PrefixHealth hours={hours} />,
          },
          { id: 'kv', label: 'KV cache', icon: Database, content: () => <KVCacheSection /> },
        ]}
      />
    </div>
  )
}
