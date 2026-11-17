import type { PrefixStats, RoutingStats } from '../types'
import { Panel, type LegendItem } from './ui'
import { CAUSES } from './PrefixHealth'
import { sem } from '../theme'
import { fmtLost, pct } from '../utils/format'

// Compact, at-a-glance companions to the usage chart: where prompt tokens went,
// how requests were routed, and what cache misses cost. The diagnostics tabs below
// hold the full tables; these answer "is it healthy?" in one look.

const DECISIONS: Record<string, { label: string; color: string; hint: string }> = {
  sticky_hit: { label: 'Sticky', color: sem.cached, hint: 'Went straight back to the host holding the conversation’s cache.' },
  sticky_wait: { label: 'Waited', color: '#5f87ab', hint: 'Waited for the busy warm host rather than reprocessing elsewhere.' },
  prefix_home: { label: 'Prefix home', color: '#b3cde4', hint: 'A new conversation placed on the host that already holds its tools and system prompt.' },
  fresh: { label: 'Fresh', color: sem.neutral, hint: 'No host had this conversation yet; placed by load.' },
  sticky_spill: { label: 'Spilled', color: sem.attention, hint: 'Gave up on the warm host and paid a full reprocess elsewhere.' },
}
const decisionMeta = (k: string) => DECISIONS[k] ?? { label: k.replace(/_/g, ' '), color: sem.neutral, hint: '' }


function Bar({ value, color, title }: { value: number; color: string; title?: string }) {
  return (
    <div className="h-2 flex-1 overflow-hidden rounded-sm bg-slate-700/70" title={title}>
      <div className="h-full rounded-sm" style={{ width: `${Math.max(0, Math.min(1, value)) * 100}%`, background: color }} />
    </div>
  )
}

function Row({ label, swatch, value, bar, barColor, hint }: {
  label: string; swatch?: string; value: string; bar: number; barColor: string; hint?: string
}) {
  return (
    <div className="flex items-center gap-3 py-1 text-[13px]" title={hint}>
      <span className="flex w-40 shrink-0 items-center gap-1.5 truncate text-slate-300">
        {swatch && <i className="h-2 w-2 shrink-0 rounded-sm" style={{ background: swatch }} />}
        {label}
      </span>
      <Bar value={bar} color={barColor} />
      <span className="w-12 shrink-0 text-right font-mono text-xs text-slate-200 tabular">{value}</span>
    </div>
  )
}

export function RoutingSummary({ routing, hostColors }: { routing?: RoutingStats; hostColors: Map<string, string> }) {
  const decisions = Object.entries(routing?.decisions ?? {}).filter(([, n]) => n > 0)
  const total = decisions.reduce((s, [, n]) => s + n, 0)
  const order = Object.keys(DECISIONS)
  decisions.sort((a, b) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99))
  const legend: LegendItem[] = [
    { label: 'Kept cache', color: sem.cached },
    { label: 'Fresh', color: sem.neutral },
    { label: 'Spilled', color: sem.attention },
  ]
  const prompt = routing?.prompt_tokens ?? 0
  const reused = prompt ? (routing?.cached_tokens ?? 0) / prompt : 0

  return (
    <Panel
      title="Routing & cache"
      info="How requests were placed, and how much of each prompt the hosts read from cache instead of prefilling again. Blues kept a conversation’s cache; orange lost it."
      legend={legend}
      className="flex flex-col"
    >
      {!routing || routing.total_requests === 0 ? (
        <div className="py-10 text-center text-sm text-slate-500">No routed requests in this range</div>
      ) : (
        <div className="space-y-4">
          <div>
            <div className="mb-1.5 flex items-baseline justify-between text-xs text-slate-500">
              <span>Prompt tokens</span>
              <span className="font-mono tabular">
                <span className="text-cached">{pct(reused)} reused</span> · <span className="text-input-400">{pct(1 - reused)} prefilled</span>
              </span>
            </div>
            <div className="flex h-3 gap-0.5 overflow-hidden rounded-sm">
              <div style={{ width: `${reused * 100}%`, background: sem.cached }} title="Read from cache" />
              <div style={{ width: `${(1 - reused) * 100}%`, background: sem.input }} title="Prefilled again" />
            </div>
          </div>

          <div>
            <div className="mb-0.5 text-xs text-slate-500">Decisions</div>
            {decisions.map(([k, n]) => {
              const m = decisionMeta(k)
              return <Row key={k} label={m.label} swatch={m.color} value={pct(n / total)} bar={n / total} barColor={m.color} hint={m.hint} />
            })}
          </div>

          {routing.hosts.length > 0 && (
            <div>
              <div className="mb-0.5 flex justify-between text-xs text-slate-500"><span>Cache reuse by host</span></div>
              {routing.hosts.map(h => (
                <Row key={h.backend_name} label={h.backend_name} swatch={hostColors.get(h.backend_name)}
                  value={pct(h.cache_reuse_rate)} bar={h.cache_reuse_rate}
                  barColor={h.cache_reuse_rate < 0.3 ? sem.attention : sem.cached}
                  hint={`${h.requests.toLocaleString()} requests · ${pct(h.share)} of traffic`} />
              ))}
            </div>
          )}
        </div>
      )}
    </Panel>
  )
}

/** Prefill time lost to each cache-miss cause, largest first. */
export function LostByCause({ prefix, onInspect }: { prefix?: PrefixStats; onInspect?: () => void }) {
  const faults = (prefix?.causes ?? [])
    .filter(c => c.cause !== 'append' && c.cause !== 'new_conversation' && c.estimated_seconds > 0)
    .sort((a, b) => b.estimated_seconds - a.estimated_seconds)
  const max = faults[0]?.estimated_seconds ?? 0
  return (
    <Panel
      title="Prefill lost by cause"
      info="GPU time spent prefilling prompt tokens that could have come from cache, by why the cache was missed. Blues are changes the client made to the prompt; orange is relai’s side (routing or slot pressure)."
      legend={[{ label: 'Client', color: sem.input }, { label: 'Relai', color: sem.attention }]}
      actions={onInspect && faults.length > 0
        ? <button onClick={onInspect} className="text-xs font-medium text-accent-400 hover:text-accent-300">Diagnose →</button>
        : undefined}
    >
      {faults.length === 0 ? (
        <div className="py-10 text-center text-sm text-slate-500">No cache misses attributed in this range</div>
      ) : (
        <div>
          {faults.map(c => {
            const meta = CAUSES[c.cause]
            return (
              <Row key={c.cause} label={meta?.label ?? c.cause} swatch={meta?.color} value={fmtLost(c.estimated_seconds)}
                bar={max ? c.estimated_seconds / max : 0} barColor={meta?.color ?? sem.neutral}
                hint={`${meta?.blurb ?? ''} ${c.requests.toLocaleString()} requests.`} />
            )
          })}
        </div>
      )}
    </Panel>
  )
}
