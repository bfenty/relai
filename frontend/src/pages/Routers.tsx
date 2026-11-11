import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { routersApi, modelMappingsApi, backendsApi } from '../api/client'
import { Router, RouterMember, MemberStatus, ModelMap, Backend, ModelType } from '../types'
import Routing from './Routing'
import { useBackendColorsByName } from '../utils/colors'
import {
  Plus, Trash2, ChevronRight, ChevronDown,
  Activity, CheckCircle, XCircle, AlertTriangle, Loader2,
} from 'lucide-react'
import CompressionSelect from '../components/CompressionSelect'
import { useState } from 'react'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { useToast } from '../components/Toast'

// ── Health badge ──────────────────────────────────────────────────────────────

function HealthBadge({ health }: { health: string }) {
  const map: Record<string, { cls: string; Icon: typeof CheckCircle }> = {
    active:   { cls: 'bg-green-500/20 text-green-400',  Icon: CheckCircle },
    degraded: { cls: 'bg-yellow-500/20 text-yellow-400', Icon: AlertTriangle },
    offline:  { cls: 'bg-red-500/20 text-red-400',      Icon: XCircle },
  }
  const { cls, Icon } = map[health] ?? map.offline
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs ${cls}`}>
      <Icon className="w-3 h-3" />{health}
    </span>
  )
}

// ── Router form ───────────────────────────────────────────────────────────────

interface RouterFormData {
  name: string; strategy: string; model_type: ModelType
  cache_enabled: boolean; cache_ttl_seconds: number; queue_timeout_seconds: number
  compression: string
  thinking_sidecar_url: string; thinking_timeout_ms: number; thinking_default_level: string
  thinking_confidence_floor: number; thinking_mode: string; thinking_param_template: string
}

const ROUTER_TYPES: { value: ModelType; label: string }[] = [
  { value: 'chat',      label: 'Chat' },
  { value: 'embedding', label: 'Embedding' },
  { value: 'rerank',    label: 'Rerank' },
]

function RouterForm({ initial, onSubmit, onClose }: {
  initial?: Router
  onSubmit: (d: RouterFormData) => void
  onClose: () => void
}) {
  const [name, setName]               = useState(initial?.name ?? '')
  const [strategy, setStrategy]       = useState(initial?.strategy ?? 'sticky')
  const [modelType, setModelType]     = useState<ModelType>(initial?.model_type ?? 'chat')
  const [cacheOn, setCacheOn]         = useState(initial?.cache_enabled ?? false)
  const [cacheTTL, setCacheTTL]       = useState(initial?.cache_ttl_seconds ?? 3600)
  const [queueTO, setQueueTO]         = useState(initial?.queue_timeout_seconds ?? 60)
  const [compression, setCompression] = useState<string>(initial?.compression ?? '')
  const [sidecarURL, setSidecarURL]   = useState(initial?.thinking_sidecar_url ?? '')
  const [sidecarTimeout, setSidecarTimeout] = useState(initial?.thinking_timeout_ms ?? 0)
  const [defaultLevel, setDefaultLevel] = useState(initial?.thinking_default_level ?? '')
  const [confFloor, setConfFloor]     = useState(initial?.thinking_confidence_floor ?? 0)
  const [thinkingMode, setThinkingMode] = useState(initial?.thinking_mode ?? 'shadow')
  const [paramTemplate, setParamTemplate] = useState(initial?.thinking_param_template ?? '')
  const adaptive = strategy === 'adaptive_thinking'

  return (
    <form onSubmit={e => { e.preventDefault(); onSubmit({ name, strategy, model_type: modelType, cache_enabled: cacheOn, cache_ttl_seconds: cacheTTL, queue_timeout_seconds: queueTO, compression,
      thinking_sidecar_url: sidecarURL, thinking_timeout_ms: sidecarTimeout, thinking_default_level: defaultLevel,
      thinking_confidence_floor: confFloor, thinking_mode: thinkingMode, thinking_param_template: paramTemplate }) }}
      className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Virtual model name <span className="text-slate-500">(what clients send as model)</span></label>
        <input value={name} onChange={e => setName(e.target.value)} required
          placeholder="chat"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Type <span className="text-slate-500">(how this router is reported to clients in /v1/models)</span></label>
        <select value={modelType} onChange={e => setModelType(e.target.value as ModelType)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
          {ROUTER_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Strategy</label>
        <select value={strategy} onChange={e => setStrategy(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
          <option value="sticky">Sticky — wait for session backend (recommended for Strix Halo)</option>
          <option value="cache_aware_sticky">Cache-aware sticky — persistent affinity + KV-cache telemetry</option>
          <option value="round_robin">Round-robin — rotate across members</option>
          <option value="least_connections">Least connections — choose lowest global load</option>
          <option value="adaptive_thinking">Adaptive thinking — classify request difficulty, steer thinking budget (issue #10)</option>
        </select>
      </div>
      {adaptive && (
        <div className="rounded-lg border border-slate-600 bg-slate-800/50 p-3 space-y-3">
          <p className="text-xs text-slate-400">Classifier sidecar (advisory only — the router falls back to the default level on any failure and never blocks a request on the classifier).</p>
          <div>
            <label className="block text-sm text-slate-400 mb-1">Sidecar URL <span className="text-slate-500">(empty = classifier disabled)</span></label>
            <input value={sidecarURL} onChange={e => setSidecarURL(e.target.value)} placeholder="http://classifier:8090/classify"
              className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono text-sm focus:outline-none focus:border-blue-500" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm text-slate-400 mb-1">Timeout (ms) <span className="text-slate-500">(0 = 50)</span></label>
              <input type="number" value={sidecarTimeout} onChange={e => setSidecarTimeout(Number(e.target.value))} min={0} max={5000}
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1">Confidence floor</label>
              <input type="number" value={confFloor} onChange={e => setConfFloor(Number(e.target.value))} min={0} max={1} step={0.05}
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm text-slate-400 mb-1">Default level <span className="text-slate-500">(fallback)</span></label>
              <select value={defaultLevel} onChange={e => setDefaultLevel(e.target.value)}
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
                <option value="">off</option>
                <option value="low">low</option>
                <option value="medium">medium</option>
                <option value="high">high</option>
              </select>
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1">Mode</label>
              <select value={thinkingMode} onChange={e => setThinkingMode(e.target.value)}
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
                <option value="shadow">shadow — log decisions, change nothing</option>
                <option value="enforce">enforce — inject thinking params</option>
              </select>
            </div>
          </div>
          <div>
            <label className="block text-sm text-slate-400 mb-1">Param template (JSON) <span className="text-slate-500">(level → backend params; required for enforce)</span></label>
            <textarea value={paramTemplate} onChange={e => setParamTemplate(e.target.value)} rows={3}
              placeholder='{"low":{"thinking":{"type":"disabled"}},"high":{"thinking":{"type":"enabled","budget_tokens":8192}}}'
              className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono text-xs focus:outline-none focus:border-blue-500" />
          </div>
        </div>
      )}
      <div>
        <label className="block text-sm text-slate-400 mb-1">Queue timeout <span className="text-slate-500">(seconds to wait for sticky backend)</span></label>
        <input type="number" value={queueTO} onChange={e => setQueueTO(Number(e.target.value))}
          min={5} max={600}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <CompressionSelect
        value={compression}
        onChange={setCompression}
        label="Compression (default for this router's members)"
      />
      <div className="flex items-center gap-3">
        <button type="button"
          onClick={() => setCacheOn(v => !v)}
          className={`relative w-10 h-5 rounded-full transition-colors ${cacheOn ? 'bg-blue-600' : 'bg-slate-600'}`}>
          <span className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${cacheOn ? 'translate-x-5' : 'translate-x-0.5'}`} />
        </button>
        <span className="text-sm text-slate-300">Semantic response cache</span>
      </div>
      {cacheOn && (
        <div>
          <label className="block text-sm text-slate-400 mb-1">Cache TTL <span className="text-slate-500">(seconds)</span></label>
          <input type="number" value={cacheTTL} onChange={e => setCacheTTL(Number(e.target.value))}
            min={60}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
        </div>
      )}
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Create router'}
        </button>
      </div>
    </form>
  )
}

// ── Add member form ───────────────────────────────────────────────────────────

function AddMemberForm({ routerId, existingMemberIds, onClose }: {
  routerId: number
  existingMemberIds: number[]
  onClose: () => void
}) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [modelMapId, setModelMapId] = useState<number | ''>('')
  const [priority, setPriority]     = useState(0)

  const { data: allMaps = [] }      = useQuery<ModelMap[]>({ queryKey: ['model-mappings'], queryFn: modelMappingsApi.list })
  const { data: backends = [] }     = useQuery<Backend[]>({ queryKey: ['backends'], queryFn: backendsApi.list })

  const backendName = (id: number) => backends.find(b => b.id === id)?.name ?? String(id)

  const available = allMaps.filter(m => !existingMemberIds.includes(m.id))

  const add = useMutation({
    mutationFn: () => routersApi.addMember(routerId, { model_map_id: Number(modelMapId), priority }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['router-members', routerId] })
      queryClient.invalidateQueries({ queryKey: ['router-status', routerId] })
      toast('Member added', 'success')
      onClose()
    },
    onError: (e: any) => toast(e.message, 'error'),
  })

  return (
    <form onSubmit={e => { e.preventDefault(); if (modelMapId !== '') add.mutate() }}
      className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Model</label>
        {available.length === 0
          ? <p className="text-sm text-slate-500">All models are already members of this router.</p>
          : <select value={modelMapId} onChange={e => setModelMapId(Number(e.target.value))} required
              className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
              <option value="">Select a model…</option>
              {available.map(m => (
                <option key={m.id} value={m.id}>
                  {backendName(m.backend_id)} / {m.local_model}
                </option>
              ))}
            </select>
        }
      </div>
      {available.length > 0 && (
        <>
          <div>
            <label className="block text-sm text-slate-400 mb-1">Priority <span className="text-slate-500">(higher = preferred for sticky fallback)</span></label>
            <input type="number" value={priority} onChange={e => setPriority(Number(e.target.value))}
              className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
          </div>
          <div className="flex gap-2 justify-end pt-1">
            <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
            <button type="submit" disabled={add.isPending}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm disabled:opacity-50">
              Add member
            </button>
          </div>
        </>
      )}
    </form>
  )
}

// ── Router members panel (expanded) ──────────────────────────────────────────

function RouterMembers({ router }: { router: Router }) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showAdd, setShowAdd]           = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<MemberStatus | null>(null)
  const colorsByName = useBackendColorsByName()

  const { data: members = [] } = useQuery<RouterMember[]>({
    queryKey: ['router-members', router.id],
    queryFn: () => routersApi.getMembers(router.id),
  })

  const { data: status = [] } = useQuery<MemberStatus[]>({
    queryKey: ['router-status', router.id],
    queryFn: () => routersApi.getStatus(router.id),
    refetchInterval: 5000,
  })

  const removeMember = useMutation({
    mutationFn: (memberId: number) => routersApi.removeMember(router.id, memberId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['router-members', router.id] })
      queryClient.invalidateQueries({ queryKey: ['router-status', router.id] })
      toast('Member removed', 'success')
    },
    onError: (e: any) => toast(e.message, 'error'),
  })

  const existingMemberIds = members.map(m => m.model_map_id)

  return (
    <div className="px-6 pb-4">
      <div className="ml-6 border-l-2 border-slate-700 pl-4">
        <div className="flex items-center justify-between mb-3">
          <span className="text-xs font-medium text-slate-400 uppercase tracking-wider">Members</span>
          <button onClick={() => setShowAdd(true)}
            className="flex items-center gap-1 px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 rounded text-slate-300">
            <Plus className="w-3 h-3" /> Add model
          </button>
        </div>

        {status.length === 0 && !showAdd && (
          <p className="text-sm text-slate-500 py-2">No models yet. Add one to start routing.</p>
        )}

        {status.length > 0 && (
          <table className="w-full text-sm mb-3">
            <thead>
              <tr>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Model</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Host</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Health</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Active</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {status.map(s => (
                <tr key={s.member_id} className="group border-t border-slate-700/40">
                  <td className="py-2 pr-4 font-mono text-slate-200">{s.local_model}</td>
                  <td className="py-2 pr-4 text-slate-400 text-xs">
                    <span className="inline-flex items-center gap-1.5">
                      {colorsByName.get(s.backend_name) && (
                        <span className="w-1.5 h-1.5 rounded-full flex-shrink-0"
                          style={{ backgroundColor: colorsByName.get(s.backend_name) }} />
                      )}
                      {s.backend_name}
                    </span>
                  </td>
                  <td className="py-2 pr-4"><HealthBadge health={s.health} /></td>
                  <td className="py-2 pr-4">
                    {s.active_requests > 0
                      ? <span className="flex items-center gap-1 text-yellow-400 text-xs">
                          <Loader2 className="w-3 h-3 animate-spin" /> busy
                        </span>
                      : <span className="text-slate-600 text-xs">idle</span>}
                  </td>
                  <td className="py-2 text-right">
                    <button
                      onClick={() => setDeleteTarget(s)}
                      className="p-1 text-slate-600 hover:text-red-400 opacity-0 group-hover:opacity-100 transition-opacity">
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {showAdd && (
          <div className="mt-2 p-3 bg-slate-700/40 rounded-lg">
            <AddMemberForm
              routerId={router.id}
              existingMemberIds={existingMemberIds}
              onClose={() => setShowAdd(false)}
            />
          </div>
        )}
      </div>

      {deleteTarget && (
        <ConfirmDialog
          title="Remove member"
          message={`Remove ${deleteTarget.local_model} from this router? Requests currently in flight will finish.`}
          onConfirm={() => {
            removeMember.mutate(deleteTarget.member_id)
            setDeleteTarget(null)
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}

// ── Router row ────────────────────────────────────────────────────────────────

function RouterRow({ router, onEdit, onDelete }: {
  router: Router
  onEdit: () => void
  onDelete: () => void
}) {
  const [open, setOpen] = useState(false)
  const { data: status = [] } = useQuery<MemberStatus[]>({
    queryKey: ['router-status', router.id],
    queryFn: () => routersApi.getStatus(router.id),
    refetchInterval: open ? 5000 : false,
  })

  const healthSummary = () => {
    if (status.length === 0) return null
    const offline = status.filter(s => s.health === 'offline').length
    const busy    = status.filter(s => s.active_requests > 0).length
    if (offline === status.length) return <span className="text-xs text-red-400">all offline</span>
    if (offline > 0) return <span className="text-xs text-yellow-400">{offline} offline</span>
    if (busy > 0) return <span className="text-xs text-yellow-400">{busy} busy</span>
    return <span className="text-xs text-green-400">all idle</span>
  }

  return (
    <>
      <tr
        className="group border-b border-slate-700/50 hover:bg-slate-700/30 cursor-pointer"
        onClick={() => setOpen(o => !o)}
      >
        <td className="px-4 py-3 w-6">
          {open
            ? <ChevronDown className="w-4 h-4 text-slate-400" />
            : <ChevronRight className="w-4 h-4 text-slate-400" />}
        </td>
        <td className="py-3 pr-4">
          <span className="font-mono text-blue-400 font-medium">{router.name}</span>
          {router.model_type && router.model_type !== 'chat' && (
            <span className="ml-2 px-1.5 py-0.5 bg-slate-700 rounded text-[10px] uppercase tracking-wide text-slate-300">{router.model_type}</span>
          )}
        </td>
        <td className="py-3 pr-4">
          <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{router.strategy}</span>
        </td>
        <td className="py-3 pr-4 text-slate-400 text-sm">{status.length} model{status.length !== 1 ? 's' : ''}</td>
        <td className="py-3 pr-4">{healthSummary()}</td>
        <td className="py-3 pr-4">
          {router.cache_enabled
            ? <span className="text-xs text-blue-400">cache {router.cache_ttl_seconds}s</span>
            : <span className="text-xs text-slate-600">no cache</span>}
        </td>
        <td className="py-3 text-right pr-4">
          <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity"
            onClick={e => e.stopPropagation()}>
            <button onClick={onEdit}
              className="px-2 py-1 text-xs text-slate-400 hover:text-white bg-slate-700 hover:bg-slate-600 rounded">
              Edit
            </button>
            <button onClick={onDelete}
              className="px-2 py-1 text-xs text-slate-400 hover:text-red-400 bg-slate-700 hover:bg-slate-600 rounded">
              Delete
            </button>
          </div>
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={7} className="bg-slate-800/60">
            <RouterMembers router={router} />
          </td>
        </tr>
      )}
    </>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

function TabBar({ active, onChange }: { active: string; onChange: (t: string) => void }) {
  const tabs = [{ id: 'routers', label: 'Virtual Routers' }, { id: 'routing', label: 'Routing Rules' }]
  return (
    <div className="flex gap-0 border-b border-slate-700 mb-0 px-6 pt-6">
      {tabs.map(t => (
        <button key={t.id} onClick={() => onChange(t.id)}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            active === t.id
              ? 'border-blue-400 text-blue-400'
              : 'border-transparent text-slate-400 hover:text-white'
          }`}>
          {t.label}
        </button>
      ))}
    </div>
  )
}

export default function Routers() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [tab, setTab]                 = useState<'routers' | 'routing'>('routers')
  const [showCreate, setShowCreate]   = useState(false)
  const [editing, setEditing]         = useState<Router | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Router | null>(null)

  const { data: routers = [], isLoading } = useQuery<Router[]>({
    queryKey: ['routers'],
    queryFn: routersApi.list,
  })

  const create = useMutation({
    mutationFn: routersApi.create,
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['routers'] }); setShowCreate(false); toast('Router created', 'success') },
    onError: (e: any) => toast(e.message, 'error'),
  })

  const update = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<Router> }) => routersApi.update(id, data),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['routers'] }); setEditing(null); toast('Router updated', 'success') },
    onError: (e: any) => toast(e.message, 'error'),
  })

  const del = useMutation({
    mutationFn: routersApi.delete,
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['routers'] }); setDeleteTarget(null); toast('Router deleted', 'success') },
    onError: (e: any) => toast(e.message, 'error'),
  })

  return (
    <div>
      <TabBar active={tab} onChange={t => setTab(t as 'routers' | 'routing')} />
      {tab === 'routing' ? <Routing /> : null}
      {tab !== 'routing' && <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold text-white">Routers</h1>
          <p className="text-slate-400 text-sm mt-1">
            Virtual model names that load-balance across backends with session affinity and health monitoring.
          </p>
        </div>
        <button onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm font-medium">
          <Plus className="w-4 h-4" /> New router
        </button>
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-slate-400 py-8">
          <Activity className="w-4 h-4 animate-pulse" /> Loading…
        </div>
      ) : routers.length === 0 ? (
        <EmptyState icon={Activity}
          message="No routers yet — create one to expose a virtual model name that balances across multiple backends." />
      ) : (
        <div className="bg-slate-800 rounded-xl border border-slate-700 overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-slate-700 bg-slate-800/50">
                <th className="w-6" />
                <th className="text-left px-4 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Virtual model</th>
                <th className="text-left py-3 pr-4 text-xs font-medium text-slate-400 uppercase tracking-wider">Strategy</th>
                <th className="text-left py-3 pr-4 text-xs font-medium text-slate-400 uppercase tracking-wider">Members</th>
                <th className="text-left py-3 pr-4 text-xs font-medium text-slate-400 uppercase tracking-wider">Status</th>
                <th className="text-left py-3 pr-4 text-xs font-medium text-slate-400 uppercase tracking-wider">Cache</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {routers.map(r => (
                <RouterRow
                  key={r.id}
                  router={r}
                  onEdit={() => setEditing(r)}
                  onDelete={() => setDeleteTarget(r)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showCreate && (
        <Modal title="New router" onClose={() => setShowCreate(false)} size="md">
          <RouterForm onSubmit={d => create.mutate(d)} onClose={() => setShowCreate(false)} />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit router" onClose={() => setEditing(null)} size="md">
          <RouterForm initial={editing}
            onSubmit={d => update.mutate({ id: editing.id, data: d })}
            onClose={() => setEditing(null)} />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete router"
          message={`Delete router "${deleteTarget.name}"? In-flight requests will complete but new requests to this virtual model will fail.`}
          onConfirm={() => del.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
      </div>}
    </div>
  )
}
