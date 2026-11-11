import { useQuery } from '@tanstack/react-query'
import { analyticsApi } from '../api/client'
import { CacheTelemetry } from '../types'

// KV cache telemetry pulled from each backend's Halogen /cache endpoint.
// relai polls it every 30s; `available` is false when the backend never
// exposed /cache or went quiet for >5 minutes.

function fmtBytes(n: number): string {
  if (n <= 0) return '0'
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GiB'
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MiB'
  return (n / 1024).toFixed(0) + ' KiB'
}

function fmtK(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(n)
}

function ratioColor(r: number): string {
  if (r >= 0.9) return 'bg-red-500'
  if (r >= 0.75) return 'bg-yellow-500'
  return 'bg-green-500'
}

export function PoolBar({ ratio }: { ratio: number }) {
  return (
    <div className="h-1.5 w-full bg-slate-700 rounded overflow-hidden">
      <div className={`h-full ${ratioColor(ratio)}`} style={{ width: `${Math.min(ratio * 100, 100)}%` }} />
    </div>
  )
}

export function CacheTelemetryCard({ t }: { t: CacheTelemetry }) {
  if (!t.available) {
    return (
      <div className="border border-slate-700/60 rounded-lg p-3 text-slate-500 text-xs">
        <div className="font-mono text-slate-400">{t.model_name}</div>
        <div>No KV-cache telemetry{t.age_seconds > 0 ? ` (silent ${Math.round(t.age_seconds / 60)}m)` : ''}</div>
      </div>
    )
  }
  return (
    <div className="border border-slate-700/60 rounded-lg p-3 space-y-2">
      <div className="flex items-center justify-between">
        <div className="font-mono text-slate-200 text-sm">{t.model_name}</div>
        <div className="text-xs text-slate-500">{t.backend_name}</div>
      </div>
      <div>
        <div className="flex justify-between text-xs text-slate-400 mb-1">
          <span>KV pool</span>
          <span className="font-mono">{fmtK(t.pool_used)} / {fmtK(t.pool_positions)} pos ({(t.pool_usage_ratio * 100).toFixed(0)}%)</span>
        </div>
        <PoolBar ratio={t.pool_usage_ratio} />
      </div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
        <div className="flex justify-between"><span className="text-slate-500">Token hit rate</span>
          <span className="font-mono text-green-400">{(t.token_hit_rate * 100).toFixed(1)}%</span></div>
        <div className="flex justify-between"><span className="text-slate-500">Tokens saved</span>
          <span className="font-mono text-slate-300">{fmtK(t.prompt_tokens_saved)}</span></div>
        <div className="flex justify-between"><span className="text-slate-500">Entries</span>
          <span className="font-mono text-slate-300">{t.entries}/{t.max_entries}</span></div>
        <div className="flex justify-between"><span className="text-slate-500">Cache bytes</span>
          <span className="font-mono text-slate-300">{fmtBytes(t.entry_bytes)}</span></div>
        <div className="flex justify-between"><span className="text-slate-500">Evicted</span>
          <span className="font-mono text-slate-300">{fmtK(t.evicted)}</span></div>
        <div className="flex justify-between"><span className="text-slate-500">Dropped</span>
          <span className="font-mono text-slate-300">{fmtK(t.dropped)}</span></div>
        {t.engine_queue_seen && (
          <div className="flex justify-between"><span className="text-slate-500">Engine queue</span>
            <span className={`font-mono ${t.engine_queued > 0 ? 'text-amber-400' : 'text-slate-300'}`}>
              {t.engine_queued}{t.engine_wait_s > 0 ? ` · ${t.engine_wait_s.toFixed(1)}s wait` : ''}
            </span></div>
        )}
      </div>
    </div>
  )
}

// Per-model-map telemetry keyed by model map id, for embedding in host rows.
export function useCacheTelemetry() {
  const { data } = useQuery({
    queryKey: ['cache-telemetry'],
    queryFn: () => analyticsApi.cache(),
    refetchInterval: 30_000,
  })
  const byMap = new Map<number, CacheTelemetry>()
  for (const t of data ?? []) byMap.set(t.model_map_id, t)
  return { byMap, all: data ?? [] }
}
