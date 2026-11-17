import { useQuery } from '@tanstack/react-query'
import { analyticsApi, backendsApi } from '../api/client'
import { Plus, Cpu } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { PageHeader, Panel, KpiTile, KpiStrip } from '../components/ui'
import { LiveRequestsTable } from '../components/LiveRequests'
import { sem } from '../theme'
import { pct } from '../utils/format'
import { EmptyState } from '../components/EmptyState'
import { backendColor } from '../utils/colors'
import { HostState, useHostLive, HostActions, useHostDialogs } from './hosts/shared'
import { HostModels } from './hosts/HostModels'

// ── List ──────────────────────────────────────────────────────────────────────

export default function Hosts() {
  const navigate = useNavigate()
  const { data: backends = [], isLoading } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
    refetchInterval: 15000,
  })
  const live = useHostLive()
  const { dialogs, create, edit, remove } = useHostDialogs(backends)

  return (
    <div>
      <PageHeader
        title="Hosts"
        description="Inference endpoints. Open a host for its models, KV cache and live requests."
        actions={<button onClick={create} className="btn-primary"><Plus className="h-4 w-4" /> Add host</button>}
      />

      <div className="panel overflow-hidden">
        {isLoading ? (
          <div className="flex h-48 items-center justify-center">
            <div className="h-7 w-7 animate-spin rounded-full border-2 border-accent-400 border-t-transparent" />
          </div>
        ) : backends.length === 0 ? (
          <EmptyState icon={Cpu} message="No hosts configured" hint="Add a host to start routing LLM requests." />
        ) : (
          <div className="overflow-x-auto">
            <table className="table-pro min-w-[760px]">
              <thead>
                <tr>
                  <th>Host</th>
                  <th>URL</th>
                  <th>State</th>
                  <th className="!text-right">Active</th>
                  <th className="!text-right">Prefill tok/s</th>
                  <th className="!text-right">Decode tok/s</th>
                  <th className="!text-right">Health check</th>
                  <th className="!text-right">Weight</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {backends.map(b => {
                  const speed = live.speed(b.id)
                  return (
                    <tr key={b.id} className="cursor-pointer" onClick={() => navigate(`/hosts/${b.id}`)}>
                      <td>
                        <Link to={`/hosts/${b.id}`} onClick={e => e.stopPropagation()} className="inline-flex items-center gap-2 font-medium text-slate-100 hover:text-accent-400">
                          <i className="h-2 w-2 shrink-0 rounded-sm" style={{ backgroundColor: backendColor(b) }} />
                          {b.name}
                        </Link>
                      </td>
                      <td className="font-mono text-xs text-slate-400">{b.base_url}</td>
                      <td><HostState status={b.status} /></td>
                      <td className="text-right font-mono tabular">{live.active(b.id)}{b.max_concurrent > 0 ? `/${b.max_concurrent}` : ''}</td>
                      <td className="text-right font-mono tabular">{speed ? speed.prompt_tok_s.toFixed(0) : '—'}</td>
                      <td className="text-right font-mono tabular">{speed ? speed.decode_tok_s.toFixed(1) : '—'}</td>
                      <td className="text-right font-mono tabular text-slate-400">{b.last_latency_ms ? `${b.last_latency_ms} ms` : '—'}</td>
                      <td className="text-right font-mono tabular text-slate-400">{b.weight}</td>
                      <td><HostActions backend={b} onEdit={edit} onDelete={remove} /></td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {dialogs}
    </div>
  )
}

// ── Detail ────────────────────────────────────────────────────────────────────

export function HostDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const hostId = Number(id)
  const { data: backends = [], isLoading } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
    refetchInterval: 15000,
  })
  const { data: routing } = useQuery({
    queryKey: ['analytics-routing', 24],
    queryFn: () => analyticsApi.routing(24),
    refetchInterval: 30000,
  })
  const live = useHostLive()
  const { dialogs, edit, remove } = useHostDialogs(backends, () => navigate('/hosts'))
  const backend = backends.find(b => b.id === hostId)

  if (isLoading) {
    return <div className="flex h-48 items-center justify-center"><div className="h-7 w-7 animate-spin rounded-full border-2 border-accent-400 border-t-transparent" /></div>
  }
  if (!backend) {
    return <EmptyState icon={Cpu} message="Host not found" hint="It may have been deleted." />
  }

  const speed = live.speed(backend.id)
  const active = live.active(backend.id)
  const reuse = routing?.hosts.find(h => h.backend_id === backend.id)
  const entries = live.entries.filter(e => e.backend_id === backend.id)

  return (
    <div>
      <div className="mb-1 text-xs text-slate-500">
        <Link to="/hosts" className="hover:text-slate-300">Hosts</Link> / {backend.name}
      </div>
      <PageHeader
        title={backend.name}
        description={<span className="font-mono">{backend.base_url}</span>}
        actions={<HostActions backend={backend} onEdit={edit} onDelete={remove} />}
      />

      <KpiStrip className="mb-3 grid-cols-2 md:grid-cols-3 xl:grid-cols-5">
        <div className="panel px-4 py-3.5">
          <div className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">State</div>
          <div className="mt-2 text-[15px]"><HostState status={backend.status} /></div>
          <div className="mt-1 text-xs text-slate-500">{backend.last_latency_ms ? `health check ${backend.last_latency_ms} ms` : 'not checked yet'}</div>
        </div>
        <KpiTile label="Active" value={`${active}${backend.max_concurrent > 0 ? `/${backend.max_concurrent}` : ''}`}
          sub="requests in flight" color={sem.attention} hero={active > 0 && backend.max_concurrent > 0 && active >= backend.max_concurrent} />
        <KpiTile label="Prefill" value={speed ? speed.prompt_tok_s.toFixed(0) : '—'} sub="tok/s, learned" color={sem.input} />
        <KpiTile label="Decode" value={speed ? speed.decode_tok_s.toFixed(1) : '—'} sub="tok/s, learned" color={sem.output} />
        <KpiTile label="Cache reuse" value={reuse ? pct(reuse.cache_reuse_rate) : '—'}
          sub={reuse ? `${reuse.requests.toLocaleString()} requests, last 24h` : 'last 24h'} color={sem.cached} />
      </KpiStrip>

      <Panel title="Models" className="mb-3" bodyClassName=""
        info="The models this host serves, with their parameters and KV-cache telemetry where the engine reports it.">
        <HostModels backend={backend} />
      </Panel>

      <Panel title="Live requests" subtitle={`${entries.length} in flight`} bodyClassName="">
        <LiveRequestsTable entries={entries} colorsById={new Map([[backend.id, backendColor(backend)]])} learnedPromptTokS={live.learnedPromptTokS} />
      </Panel>
      {dialogs}
    </div>
  )
}
