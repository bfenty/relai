import { useQuery } from '@tanstack/react-query'
import { logsApi, eventsApi, LogFilters } from '../api/client'
import { backendsApi, apiKeysApi } from '../api/client'
import { RequestLog, SystemEvent, EventFilters } from '../types'
import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ScrollText, RefreshCw, ChevronDown, ChevronRight, AlertCircle } from 'lucide-react'
import { EmptyState } from '../components/EmptyState'
import { useBackendColors } from '../utils/colors'

// ── Filter bar ────────────────────────────────────────────────────────────────

function FilterBar({ filters, onChange }: {
  filters: LogFilters
  onChange: (f: LogFilters) => void
}) {
  const { data: backends = [] } = useQuery({ queryKey: ['backends'], queryFn: backendsApi.list })
  const { data: keys = [] }     = useQuery({ queryKey: ['api-keys'], queryFn: apiKeysApi.list })

  return (
    <div className="flex flex-wrap gap-3 mb-4 p-4 bg-slate-800/60 rounded-lg border border-slate-700">
      <input
        type="text"
        placeholder="Filter by model…"
        value={filters.model ?? ''}
        onChange={e => onChange({ ...filters, model: e.target.value || undefined, offset: 0 })}
        className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500 w-48"
      />
      <select
        value={filters.backend_id ?? ''}
        onChange={e => onChange({ ...filters, backend_id: e.target.value ? Number(e.target.value) : undefined, offset: 0 })}
        className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500"
      >
        <option value="">All hosts</option>
        {backends.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}
      </select>
      <select
        value={filters.api_key_id ?? ''}
        onChange={e => onChange({ ...filters, api_key_id: e.target.value ? Number(e.target.value) : undefined, offset: 0 })}
        className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500"
      >
        <option value="">All API keys</option>
        {keys.map(k => <option key={k.id} value={k.id}>{k.name}</option>)}
      </select>
      <label className="flex items-center gap-2 text-sm text-slate-400 cursor-pointer select-none">
        <input
          type="checkbox"
          checked={filters.errors_only ?? false}
          onChange={e => onChange({ ...filters, errors_only: e.target.checked || undefined, offset: 0 })}
          className="rounded border-slate-500 bg-slate-600 text-red-500 focus:ring-red-500 focus:ring-offset-slate-800"
        />
        Errors only
      </label>
      <input
        type="date"
        value={filters.from ? filters.from.slice(0, 10) : ''}
        onChange={e => onChange({ ...filters, from: e.target.value ? e.target.value + 'T00:00:00Z' : undefined, offset: 0 })}
        className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500"
      />
      <span className="text-slate-500 self-center text-sm">to</span>
      <input
        type="date"
        value={filters.to ? filters.to.slice(0, 10) : ''}
        onChange={e => onChange({ ...filters, to: e.target.value ? e.target.value + 'T23:59:59Z' : undefined, offset: 0 })}
        className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500"
      />
      <button
        onClick={() => onChange({ limit: filters.limit })}
        className="px-3 py-1.5 text-sm text-slate-400 hover:text-white"
      >
        Clear
      </button>
    </div>
  )
}

// ── Log row ───────────────────────────────────────────────────────────────────

function LogRow({ log, backendName, keyName, backendColor }: {
  log: RequestLog
  backendName: string
  keyName: string
  backendColor?: string
}) {
  const [expanded, setExpanded] = useState(false)
  const hasError = log.error_message || log.status_code >= 400

  return (
    <>
      <tr
        className={`border-b border-slate-700/50 hover:bg-slate-700/20 ${hasError ? 'bg-red-900/10' : ''} cursor-pointer`}
        onClick={() => hasError && setExpanded(e => !e)}
      >
        <td className="px-4 py-3 text-xs text-slate-500 whitespace-nowrap">
          {new Date(log.created_at).toLocaleString()}
        </td>
        <td className="px-4 py-3 font-mono text-sm text-slate-200">{log.model}</td>
        <td className="px-4 py-3 text-sm text-slate-400">
          <span className="inline-flex items-center gap-1.5">
            {backendColor && <span className="w-1.5 h-1.5 rounded-full flex-shrink-0" style={{ backgroundColor: backendColor }} />}
            {backendName}
          </span>
        </td>
        <td className="px-4 py-3 text-sm text-slate-500">{keyName}</td>
        <td className="px-4 py-3 text-sm text-slate-400 whitespace-nowrap">
          <span>{log.request_tokens} / {log.response_tokens}</span>
          {log.thinking_tokens > 0 && (
            <span className="ml-1.5 text-purple-400/70 text-xs">+{log.thinking_tokens}✦</span>
          )}
        </td>
        <td className="px-4 py-3 text-sm text-slate-400">${log.cost.toFixed(6)}</td>
        <td className="px-4 py-3 text-sm text-slate-400">{log.latency_ms}ms</td>
        <td className="px-4 py-3">
          <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs ${
            log.status_code >= 500 ? 'bg-red-500/20 text-red-400' :
            log.status_code >= 400 ? 'bg-yellow-500/20 text-yellow-400' :
            !log.status_code       ? 'bg-slate-500/20 text-slate-400' :
                                     'bg-green-500/20 text-green-400'
          }`}>
            {hasError && <AlertCircle className="w-3 h-3" />}
            {log.status_code || '—'}
          </span>
        </td>
        <td className="px-4 py-3 w-6">
          {hasError && (
            expanded ? <ChevronDown className="w-3.5 h-3.5 text-slate-400" />
                     : <ChevronRight className="w-3.5 h-3.5 text-slate-400" />
          )}
        </td>
      </tr>
      {expanded && log.error_message && (
        <tr className="bg-red-900/10 border-b border-slate-700/50">
          <td colSpan={9} className="px-6 py-3">
            <p className="text-sm text-red-400 font-mono">{log.error_message}</p>
          </td>
        </tr>
      )}
    </>
  )
}

// ── System Events tab ─────────────────────────────────────────────────────────

const LEVEL_COLORS: Record<string, string> = {
  info:  'bg-blue-500/20 text-blue-400',
  warn:  'bg-yellow-500/20 text-yellow-400',
  error: 'bg-red-500/20 text-red-400',
}

const TYPE_LABELS: Record<string, string> = {
  auth: 'Auth', proxy: 'Proxy', health: 'Health', config: 'Config', system: 'System',
}

function EventsTab() {
  const [filters, setFilters] = useState<EventFilters>({ limit: 50 })
  const [levels, setLevels]   = useState<Record<string, boolean>>({ info: true, warn: true, error: true })

  const levelParam = Object.keys(levels).filter(l => levels[l]).join(',')

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['events', filters, levelParam],
    queryFn: () => eventsApi.list({ ...filters, level: levelParam || undefined }),
    refetchInterval: 30000,
  })

  const events  = data?.events ?? []
  const total   = data?.total ?? 0
  const offset  = filters.offset ?? 0
  const limit   = filters.limit ?? 50

  return (
    <div>
      {/* Filter bar */}
      <div className="flex flex-wrap gap-3 mb-4 p-4 bg-slate-800/60 rounded-lg border border-slate-700">
        {/* Level checkboxes */}
        <div className="flex items-center gap-3">
          {(['info', 'warn', 'error'] as const).map(l => (
            <label key={l} className="flex items-center gap-1.5 text-sm cursor-pointer select-none">
              <input type="checkbox" checked={levels[l]} onChange={e => setLevels(v => ({ ...v, [l]: e.target.checked }))}
                className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800" />
              <span className={`px-1.5 py-0.5 rounded text-xs ${LEVEL_COLORS[l]}`}>{l}</span>
            </label>
          ))}
        </div>
        {/* Event type */}
        <select value={filters.type ?? ''} onChange={e => setFilters(f => ({ ...f, type: e.target.value || undefined, offset: 0 }))}
          className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500">
          <option value="">All types</option>
          {Object.entries(TYPE_LABELS).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </select>
        {/* Search */}
        <input type="text" placeholder="Search message…" value={filters.search ?? ''}
          onChange={e => setFilters(f => ({ ...f, search: e.target.value || undefined, offset: 0 }))}
          className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-white focus:outline-none focus:border-blue-500 w-52" />
        <button onClick={() => refetch()} className="p-1.5 text-slate-400 hover:text-white rounded" title="Refresh">
          <RefreshCw className={`w-4 h-4 ${isLoading ? 'animate-spin' : ''}`} />
        </button>
        <button onClick={() => { setFilters({ limit: 50 }); setLevels({ info: true, warn: true, error: true }) }}
          className="px-3 py-1.5 text-sm text-slate-400 hover:text-white">Clear</button>
      </div>

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : events.length === 0 ? (
          <EmptyState icon={ScrollText} message="No events found" hint="Events are logged for auth, config changes, and proxy errors." />
        ) : (
          <div className="divide-y divide-slate-700/50">
            {events.map((e: SystemEvent) => (
              <div key={e.id} className="px-4 py-3 flex items-start gap-3">
                <span className="mt-0.5 text-xs text-slate-500 whitespace-nowrap w-36 shrink-0">
                  {new Date(e.created_at).toLocaleString()}
                </span>
                <span className={`shrink-0 px-1.5 py-0.5 rounded text-xs ${LEVEL_COLORS[e.level] ?? 'bg-slate-500/20 text-slate-400'}`}>
                  {e.level}
                </span>
                <span className="shrink-0 px-1.5 py-0.5 rounded text-xs bg-slate-700 text-slate-300">
                  {TYPE_LABELS[e.event_type] ?? e.event_type}
                </span>
                <span className="text-sm text-slate-200 flex-1">{e.message}</span>
                {e.metadata && Object.keys(e.metadata).length > 0 && (
                  <details className="text-xs text-slate-500 shrink-0">
                    <summary className="cursor-pointer hover:text-slate-300">meta</summary>
                    <pre className="mt-1 text-xs text-slate-400 bg-slate-900 p-2 rounded max-w-xs overflow-auto">
                      {JSON.stringify(e.metadata, null, 2)}
                    </pre>
                  </details>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {total > limit && (
        <div className="flex items-center justify-between mt-4">
          <button onClick={() => setFilters(f => ({ ...f, offset: Math.max(0, offset - limit) }))}
            disabled={offset === 0}
            className="px-4 py-2 text-sm bg-slate-700 rounded-lg disabled:opacity-40 hover:bg-slate-600">
            Previous
          </button>
          <span className="text-sm text-slate-400">{offset + 1}–{Math.min(offset + limit, total)} of {total.toLocaleString()}</span>
          <button onClick={() => setFilters(f => ({ ...f, offset: offset + limit }))}
            disabled={offset + limit >= total}
            className="px-4 py-2 text-sm bg-slate-700 rounded-lg disabled:opacity-40 hover:bg-slate-600">
            Next
          </button>
        </div>
      )}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Logs() {
  const [searchParams] = useSearchParams()
  const [activeTab, setActiveTab] = useState<'requests' | 'events'>('requests')
  const [filters, setFilters] = useState<LogFilters>(() => {
    const init: LogFilters = { limit: 50 }
    const model = searchParams.get('model')
    const backendId = searchParams.get('backend_id')
    const apiKeyId = searchParams.get('api_key_id')
    if (model) init.model = model
    if (backendId) init.backend_id = Number(backendId)
    if (apiKeyId) init.api_key_id = Number(apiKeyId)
    if (searchParams.get('errors_only') === 'true') init.errors_only = true
    return init
  })
  const [autoRefresh, setAutoRefresh] = useState(false)

  const { data: backends = [] } = useQuery({ queryKey: ['backends'], queryFn: backendsApi.list })
  const { data: keys = [] }     = useQuery({ queryKey: ['api-keys'], queryFn: apiKeysApi.list })
  const colorsById = useBackendColors()

  const backendName = (id?: number | null) => backends.find(b => b.id === id)?.name ?? '—'
  const keyName     = (id?: number | null) => keys.find(k => k.id === id)?.name ?? '—'

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['logs', filters],
    queryFn: () => logsApi.list(filters),
    refetchInterval: autoRefresh ? 10000 : false,
  })

  // reset offset when filters change (except offset itself)
  useEffect(() => {}, [filters])

  const total  = data?.total ?? 0
  const offset = filters.offset ?? 0
  const limit  = filters.limit ?? 50

  return (
    <div>
      {/* Tab bar */}
      <div className="flex gap-0 border-b border-slate-700 mb-6">
        {[{ id: 'requests', label: 'Request Logs' }, { id: 'events', label: 'System Events' }].map(t => (
          <button key={t.id} onClick={() => setActiveTab(t.id as 'requests' | 'events')}
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              activeTab === t.id ? 'border-blue-400 text-blue-400' : 'border-transparent text-slate-400 hover:text-white'
            }`}>
            {t.label}
          </button>
        ))}
      </div>

      {activeTab === 'events' ? <EventsTab /> : null}
      {activeTab === 'requests' && <>
      <div className="flex flex-wrap items-start justify-between gap-3 mb-4">
        <div>
          <h2 className="text-2xl font-bold">Request Logs</h2>
          <p className="text-slate-400 text-sm mt-1">{total.toLocaleString()} matching entries</p>
        </div>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 text-sm text-slate-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={e => setAutoRefresh(e.target.checked)}
              className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800"
            />
            Auto-refresh
          </label>
          <select
            value={limit}
            onChange={e => setFilters(f => ({ ...f, limit: Number(e.target.value), offset: 0 }))}
            className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm"
          >
            <option value={25}>25 / page</option>
            <option value={50}>50 / page</option>
            <option value={100}>100 / page</option>
          </select>
          <button
            onClick={() => refetch()}
            className="p-1.5 text-slate-400 hover:text-white rounded"
            title="Refresh"
          >
            <RefreshCw className={`w-4 h-4 ${isLoading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      <FilterBar filters={filters} onChange={setFilters} />

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : !data?.data?.length ? (
          <EmptyState icon={ScrollText} message="No logs found" hint="Try adjusting your filters." />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-slate-700">
                  <th className="text-left px-4 py-3 text-xs text-slate-400 whitespace-nowrap">Time</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">Model</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">Host</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">API Key</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400 whitespace-nowrap">Tokens (in/out · ✦think)</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">Cost</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">Latency</th>
                  <th className="text-left px-4 py-3 text-xs text-slate-400">Status</th>
                  <th className="w-6" />
                </tr>
              </thead>
              <tbody>
                {data.data.map(log => (
                  <LogRow
                    key={log.id}
                    log={log}
                    backendName={backendName(log.backend_id)}
                    keyName={keyName(log.api_key_id)}
                    backendColor={log.backend_id ? colorsById.get(log.backend_id) : undefined}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {total > limit && (
        <div className="flex items-center justify-between mt-4">
          <button
            onClick={() => setFilters(f => ({ ...f, offset: Math.max(0, offset - limit) }))}
            disabled={offset === 0}
            className="px-4 py-2 text-sm bg-slate-700 rounded-lg disabled:opacity-40 hover:bg-slate-600"
          >
            Previous
          </button>
          <span className="text-sm text-slate-400">
            {offset + 1}–{Math.min(offset + limit, total)} of {total.toLocaleString()}
          </span>
          <button
            onClick={() => setFilters(f => ({ ...f, offset: offset + limit }))}
            disabled={offset + limit >= total}
            className="px-4 py-2 text-sm bg-slate-700 rounded-lg disabled:opacity-40 hover:bg-slate-600"
          >
            Next
          </button>
        </div>
      )}
      </>}
    </div>
  )
}
