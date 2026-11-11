import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { backendsApi, modelMappingsApi } from '../api/client'
import { Backend, ModelMap } from '../types'
import {
  Plus, Pencil, Trash2, RefreshCw, CheckCircle, XCircle,
  ChevronRight, ChevronDown, Cpu, Activity, Brain,
} from 'lucide-react'
import { useState } from 'react'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { useToast } from '../components/Toast'
import { HOST_PALETTE, backendColor, nextColor } from '../utils/colors'
import { CacheTelemetryCard, useCacheTelemetry } from '../components/CacheTelemetry'

// ── Status badge ──────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    active: 'bg-green-500/20 text-green-400',
    paused: 'bg-yellow-500/20 text-yellow-400',
    error:  'bg-red-500/20 text-red-400',
  }
  const Icon = status === 'active' ? CheckCircle : XCircle
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs ${styles[status] ?? 'bg-slate-500/20 text-slate-400'}`}>
      <Icon className="w-3 h-3" />{status}
    </span>
  )
}

// ── Host form (create / edit) ─────────────────────────────────────────────────

interface HostFormData {
  name: string
  base_url: string
  api_key: string
  provider: Backend['provider']
  origin: Backend['origin']
  weight: number
  max_concurrent: number
  color: string
}

function HostForm({ initial, defaultColor, onSubmit, onClose }: {
  initial?: Backend
  defaultColor?: string
  onSubmit: (data: HostFormData) => void
  onClose: () => void
}) {
  const [name, setName]           = useState(initial?.name           ?? '')
  const [url, setUrl]             = useState(initial?.base_url       ?? '')
  const [apiKey, setApiKey]       = useState(initial?.api_key        ?? '')
  const [provider, setProvider]   = useState<Backend['provider']>(initial?.provider ?? 'openai-compatible')
  const [origin, setOrigin]       = useState<Backend['origin']>(initial?.origin ?? 'local')
  const [weight, setWeight]       = useState(initial?.weight         ?? 1)
  const [maxConcurrent, setMaxConcurrent] = useState(initial?.max_concurrent ?? 1)
  const [color, setColor]         = useState(initial ? backendColor(initial) : (defaultColor ?? HOST_PALETTE[0]))

  return (
    <form onSubmit={e => { e.preventDefault(); onSubmit({ name, base_url: url, api_key: apiKey, provider, origin, weight, max_concurrent: maxConcurrent, color }) }}
      className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Base URL</label>
        <input type="url" value={url} onChange={e => setUrl(e.target.value)} required
          placeholder="https://api.openai.com"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm text-slate-400 mb-1">Provider</label>
          <select value={provider} onChange={e => setProvider(e.target.value as Backend['provider'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
            <option value="openai-compatible">OpenAI-compatible</option>
            <option value="openai">OpenAI API</option>
          </select>
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Origin</label>
          <select value={origin} onChange={e => setOrigin(e.target.value as Backend['origin'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
            <option value="local">Local</option>
            <option value="external">External / paid fallback</option>
          </select>
        </div>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">API Key</label>
        <input type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
          placeholder={initial?.has_api_key ? '(unchanged — leave blank to keep)' : 'sk-…'}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Weight <span className="text-slate-500">(for weighted routing)</span></label>
        <input type="number" value={weight} onChange={e => setWeight(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Max concurrent requests <span className="text-slate-500">(per host, across all its models)</span></label>
        <input type="number" value={maxConcurrent} onChange={e => setMaxConcurrent(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-2">Color</label>
        <div className="flex gap-2 flex-wrap">
          {HOST_PALETTE.map(c => (
            <button
              key={c}
              type="button"
              onClick={() => setColor(c)}
              title={c}
              className={`w-6 h-6 rounded-full transition-transform ${color === c ? 'ring-2 ring-white ring-offset-2 ring-offset-slate-800 scale-110' : 'opacity-70 hover:opacity-100'}`}
              style={{ backgroundColor: c }}
            />
          ))}
        </div>
      </div>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Add host'}
        </button>
      </div>
    </form>
  )
}

// ── Model status badge ────────────────────────────────────────────────────────

function ModelStatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    active:  'bg-green-500/20 text-green-400',
    error:   'bg-red-500/20 text-red-400',
    unknown: 'bg-slate-500/20 text-slate-400',
  }
  return (
    <span className={`inline-flex px-1.5 py-0.5 rounded text-xs ${styles[status] ?? styles.unknown}`}>
      {status}
    </span>
  )
}

// ── Helpers for port-based URL override ───────────────────────────────────────

function extractPort(url: string): string {
  try { return new URL(url).port } catch { return '' }
}

function urlWithPort(hostUrl: string, port: string): string {
  try {
    const u = new URL(hostUrl)
    u.port = port
    return u.origin
  } catch { return '' }
}

type ParamsObject = Record<string, unknown>

function parseParams(value: string): ParamsObject | null {
  try {
    const parsed = JSON.parse(value.trim() || '{}')
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') return null
    return parsed as ParamsObject
  } catch {
    return null
  }
}

function formatParams(value: ParamsObject) {
  return JSON.stringify(value, null, 2)
}

function setThinkingValue(params: string, value: boolean | null) {
  const parsed = parseParams(params) ?? {}
  const kwargs = parsed.chat_template_kwargs && typeof parsed.chat_template_kwargs === 'object' && !Array.isArray(parsed.chat_template_kwargs)
    ? { ...(parsed.chat_template_kwargs as ParamsObject) }
    : {}

  if (value === null) {
    delete kwargs.enable_thinking
  } else {
    kwargs.enable_thinking = value
  }

  if (Object.keys(kwargs).length === 0) {
    delete parsed.chat_template_kwargs
  } else {
    parsed.chat_template_kwargs = kwargs
  }
  return formatParams(parsed)
}

function thinkingValue(params: string): 'default' | 'on' | 'off' {
  const kwargs = parseParams(params)?.chat_template_kwargs
  if (!kwargs || typeof kwargs !== 'object' || Array.isArray(kwargs)) return 'default'
  const value = (kwargs as ParamsObject).enable_thinking
  if (value === true) return 'on'
  if (value === false) return 'off'
  return 'default'
}

// ── Model form (add / edit model) ─────────────────────────────────────────────

function ModelForm({ backendId, initial, hostBaseUrl, onSubmit, onClose }: {
  backendId: number
  initial?: ModelMap
  hostBaseUrl: string
  onSubmit: (data: Partial<ModelMap>) => void
  onClose: () => void
}) {
  const [modelName, setModelName]     = useState(initial?.local_model  ?? '')
  const [backendModel, setBackend]    = useState(initial?.remote_model ?? '')
  const [port, setPort]               = useState(extractPort(initial?.base_url ?? ''))
  const [params, setParams]           = useState(initial?.params && initial.params !== '{}' ? initial.params : '')
  const [paramsError, setParamsError] = useState('')
  const [inputCost, setInputCost]     = useState(String(initial?.input_cost_per_million  ?? ''))
  const [outputCost, setOutputCost]   = useState(String(initial?.output_cost_per_million ?? ''))

  const hostPort = extractPort(hostBaseUrl)
  const selectedThinking = thinkingValue(params)

  function validateParams(val: string) {
    if (!val.trim()) { setParamsError(''); return true }
    if (parseParams(val)) { setParamsError(''); return true }
    setParamsError('Params must be a JSON object')
    return false
  }

  function updateParams(next: string) {
    setParams(next === '{}' ? '' : next)
    validateParams(next)
  }

  return (
    <form onSubmit={e => {
      e.preventDefault()
      if (!validateParams(params)) return
      const computedUrl = port.trim() ? urlWithPort(hostBaseUrl, port.trim()) : ''
      let parsedParams = '{}'
      if (params.trim()) {
        try { JSON.parse(params); parsedParams = params.trim() } catch { return }
      }
      onSubmit({
        backend_id:             backendId,
        local_model:            modelName,
        remote_model:           backendModel.trim() || modelName,
        base_url:               computedUrl,
        params:                 parsedParams,
        input_cost_per_million:  parseFloat(inputCost)  || 0,
        output_cost_per_million: parseFloat(outputCost) || 0,
      })
    }} className="space-y-3">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Model name <span className="text-slate-500">(what clients send)</span></label>
        <input value={modelName} onChange={e => setModelName(e.target.value)} required
          placeholder="llama3.1"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          Backend model <span className="text-slate-500">(what the host expects — leave blank if same)</span>
        </label>
        <input value={backendModel} onChange={e => setBackend(e.target.value)}
          placeholder={modelName || 'llama3.1:8b-instruct'}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          Port <span className="text-slate-500">(optional — leave blank to use host port{hostPort ? ` ${hostPort}` : ''})</span>
        </label>
        <input type="number" value={port} onChange={e => setPort(e.target.value)}
          placeholder={hostPort || '11434'}
          min={1} max={65535}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono focus:outline-none focus:border-blue-500" />
        {port.trim() && (
          <p className="text-xs text-slate-500 mt-1">
            Effective URL: <span className="text-blue-400 font-mono">{urlWithPort(hostBaseUrl, port.trim())}</span>
          </p>
        )}
      </div>
      <div>
        <div className="flex items-center justify-between gap-3 mb-1">
          <label className="block text-sm text-slate-400">
            Forced params <span className="text-slate-500">(LiteLLM-style JSON kwargs)</span>
          </label>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => updateParams(setThinkingValue(params, false))}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-slate-600 bg-slate-800 text-slate-300 hover:text-white"
            >
              <Brain className="w-3 h-3" /> Off
            </button>
            <button
              type="button"
              onClick={() => updateParams(setThinkingValue(params, true))}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-purple-700/60 bg-purple-950/30 text-purple-300 hover:text-purple-200"
            >
              <Brain className="w-3 h-3" /> On
            </button>
          </div>
        </div>
        <div className="mb-2">
          <label className="block text-xs text-slate-500 mb-1">Thinking</label>
          <select
            value={selectedThinking}
            onChange={e => updateParams(setThinkingValue(params, e.target.value === 'default' ? null : e.target.value === 'on'))}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white text-sm focus:outline-none focus:border-blue-500"
          >
            <option value="default">Model default</option>
            <option value="off">Off</option>
            <option value="on">On</option>
          </select>
        </div>
        <textarea
          value={params}
          onChange={e => { setParams(e.target.value); validateParams(e.target.value) }}
          placeholder={'{\n  "temperature": 0.7,\n  "chat_template_kwargs": {\n    "enable_thinking": true\n  }\n}'}
          rows={4}
          spellCheck={false}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white font-mono text-xs focus:outline-none focus:border-blue-500 resize-y" />
        {paramsError && <p className="text-xs text-red-400 mt-1">{paramsError}</p>}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="block text-sm text-slate-400 mb-1">Input cost <span className="text-slate-500">$/M tokens</span></label>
          <input type="number" value={inputCost} onChange={e => setInputCost(e.target.value)}
            placeholder="0.00" step="any" min={0}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Output cost <span className="text-slate-500">$/M tokens</span></label>
          <input type="number" value={outputCost} onChange={e => setOutputCost(e.target.value)}
            placeholder="0.00" step="any" min={0}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
        </div>
      </div>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save' : 'Add model'}
        </button>
      </div>
    </form>
  )
}

// ── Expanded model list per host ──────────────────────────────────────────────

function HostModels({ backend }: { backend: Backend }) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showAdd, setShowAdd]           = useState(false)
  const [editing, setEditing]           = useState<ModelMap | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ModelMap | null>(null)
  const [checkingId, setCheckingId]     = useState<number | null>(null)

  const { data: allMappings = [] } = useQuery<ModelMap[]>({
    queryKey: ['model-mappings'],
    queryFn: modelMappingsApi.list,
  })
  const models = allMappings.filter(m => m.backend_id === backend.id)
  const { byMap } = useCacheTelemetry()
  const kvTelemetry = models
    .map(m => byMap.get(m.id))
    .filter((t): t is NonNullable<typeof t> => t !== undefined)

  const createModel = useMutation({
    mutationFn: modelMappingsApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      toast('Model added', 'success')
      setShowAdd(false)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const updateModel = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<ModelMap> }) =>
      modelMappingsApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      toast('Model updated', 'success')
      setEditing(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const deleteModel = useMutation({
    mutationFn: modelMappingsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      toast('Model removed', 'success')
      setDeleteTarget(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const healthCheck = async (m: ModelMap) => {
    setCheckingId(m.id)
    try {
      const updated = await modelMappingsApi.healthCheck(m.id)
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      if (updated.status === 'error') {
        toast(`${m.local_model}: backend unreachable (${updated.last_latency_ms > 0 ? updated.last_latency_ms + 'ms' : 'no response'})`, 'error')
      } else {
        toast(`${m.local_model}: ${updated.status} (${updated.last_latency_ms}ms)`, 'success')
      }
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setCheckingId(null)
    }
  }

  const color = backendColor(backend)

  return (
    <div className="px-6 pb-4">
      <div className="ml-6 pl-4" style={{ borderLeft: `2px solid ${color}50` }}>
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs font-medium text-slate-400 uppercase tracking-wider">Models</span>
          <button onClick={() => setShowAdd(true)}
            className="flex items-center gap-1 px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 rounded text-slate-300">
            <Plus className="w-3 h-3" /> Add model
          </button>
        </div>

        {models.length === 0 && !showAdd && (
          <p className="text-sm text-slate-500 py-2">No models yet. Add one to start routing requests to this host.</p>
        )}

        {models.length > 0 && (
          <table className="w-full text-sm mb-2">
            <thead>
              <tr>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Model name</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Backend model</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Port</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Status</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Latency</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {models.map(m => (
                <tr key={m.id} className="group border-t border-slate-700/40">
                  <td className="py-2 pr-4 font-mono text-slate-200">{m.local_model}</td>
                  <td className="py-2 pr-4 font-mono text-slate-400">
                    {m.remote_model === m.local_model
                      ? <span className="text-slate-600 text-xs">same</span>
                      : m.remote_model}
                  </td>
                  <td className="py-2 pr-4 text-slate-500 text-xs font-mono">
                    {m.base_url
                      ? <span className="text-blue-400">{extractPort(m.base_url)}</span>
                      : <span className="text-slate-600">{extractPort(backend.base_url)}</span>}
                  </td>
                  <td className="py-2 pr-4">
                    <ModelStatusBadge status={m.status} />
                  </td>
                  <td className="py-2 pr-4 text-slate-500 text-xs">
                    {m.last_latency_ms > 0 ? `${m.last_latency_ms}ms` : '—'}
                  </td>
                  <td className="py-2 text-right">
                    <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                      <button
                        onClick={() => healthCheck(m)}
                        disabled={checkingId === m.id}
                        title="Health check"
                        className="p-1 text-slate-400 hover:text-green-400 disabled:opacity-40"
                      >
                        <Activity className={`w-3.5 h-3.5 ${checkingId === m.id ? 'animate-pulse' : ''}`} />
                      </button>
                      <button onClick={() => setEditing(m)}
                        title="Edit"
                        className="p-1 text-slate-400 hover:text-white">
                        <Pencil className="w-3.5 h-3.5" />
                      </button>
                      <button onClick={() => setDeleteTarget(m)}
                        title="Remove"
                        className="p-1 text-slate-400 hover:text-red-400">
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {kvTelemetry.length > 0 && (
          <div className="mb-2">
            <p className="text-xs text-slate-500 uppercase tracking-wide mb-2">KV cache utilization</p>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {kvTelemetry.map(t => <CacheTelemetryCard key={t.model_map_id} t={t} />)}
            </div>
          </div>
        )}

        {showAdd && (
          <div className="mt-2 p-4 bg-slate-700/40 rounded-lg border border-slate-600">
            <ModelForm
              backendId={backend.id}
              hostBaseUrl={backend.base_url}
              onSubmit={createModel.mutate}
              onClose={() => setShowAdd(false)}
            />
          </div>
        )}

        {editing && (
          <div className="mt-2 p-4 bg-slate-700/40 rounded-lg border border-slate-600">
            <p className="text-xs text-slate-400 mb-3">Editing <span className="font-mono text-slate-300">{editing.local_model}</span></p>
            <ModelForm
              backendId={backend.id}
              initial={editing}
              hostBaseUrl={backend.base_url}
              onSubmit={data => updateModel.mutate({ id: editing.id, data })}
              onClose={() => setEditing(null)}
            />
          </div>
        )}
      </div>

      {deleteTarget && (
        <ConfirmDialog
          title="Remove model"
          message={`Remove "${deleteTarget.local_model}" from this host?`}
          confirmLabel="Remove"
          onConfirm={() => deleteModel.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}

// ── Host row ──────────────────────────────────────────────────────────────────

function HostRow({ backend, onEdit, onDelete }: {
  backend: Backend
  onEdit: (b: Backend) => void
  onDelete: (b: Backend) => void
}) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [expanded, setExpanded] = useState(false)

  const healthMutation = useMutation({
    mutationFn: () => backendsApi.healthCheck(backend.id),
    onSuccess: (updated) => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      if (updated.status === 'error') {
        toast(`${backend.name}: backend returned error from /v1/models`, 'error')
      } else {
        toast(`${backend.name}: ${updated.status} (${updated.last_latency_ms}ms)`, 'success')
      }
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const toggleMutation = useMutation({
    mutationFn: () => backendsApi.update(backend.id, {
      status: backend.status === 'active' ? 'paused' : 'active',
    }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      toast(`Host ${backend.status === 'active' ? 'paused' : 'activated'}`, 'success')
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  return (
    <>
      <tr className="border-b border-slate-700/50 hover:bg-slate-700/20 cursor-pointer"
        onClick={() => setExpanded(e => !e)}>
        <td className="px-3 py-4 w-8">
          {expanded
            ? <ChevronDown className="w-4 h-4 text-slate-400" />
            : <ChevronRight className="w-4 h-4 text-slate-400" />}
        </td>
        <td className="px-4 py-4 font-medium">
          <span className="inline-flex items-center gap-2">
            <span className="w-2 h-2 rounded-full flex-shrink-0" style={{ backgroundColor: backendColor(backend) }} />
            {backend.name}
          </span>
        </td>
        <td className="px-4 py-4 text-slate-400 text-sm">{backend.base_url}</td>
        <td className="px-4 py-4"><StatusBadge status={backend.status} /></td>
        <td className="px-4 py-4 text-slate-400 text-sm">
          {backend.last_latency_ms ? `${backend.last_latency_ms}ms` : '—'}
        </td>
        <td className="px-4 py-4 text-slate-400 text-sm">{backend.weight}</td>
        <td className="px-4 py-4" onClick={e => e.stopPropagation()}>
          <div className="flex items-center justify-end gap-1">
            <button
              onClick={() => healthMutation.mutate()}
              disabled={healthMutation.isPending}
              title="Health check"
              className="p-1.5 text-slate-400 hover:text-white rounded disabled:opacity-40"
            >
              <RefreshCw className={`w-4 h-4 ${healthMutation.isPending ? 'animate-spin' : ''}`} />
            </button>
            <button
              onClick={() => toggleMutation.mutate()}
              title={backend.status === 'active' ? 'Pause' : 'Activate'}
              className="p-1.5 text-slate-400 hover:text-yellow-400 rounded"
            >
              {backend.status === 'active'
                ? <XCircle className="w-4 h-4" />
                : <CheckCircle className="w-4 h-4" />}
            </button>
            <button onClick={() => onEdit(backend)} className="p-1.5 text-slate-400 hover:text-white rounded">
              <Pencil className="w-4 h-4" />
            </button>
            <button onClick={() => onDelete(backend)} className="p-1.5 text-slate-400 hover:text-red-400 rounded">
              <Trash2 className="w-4 h-4" />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr className="bg-slate-900/40 border-b border-slate-700/50">
          <td colSpan={7} className="py-2">
            <HostModels backend={backend} />
          </td>
        </tr>
      )}
    </>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Hosts() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate]   = useState(false)
  const [editing, setEditing]         = useState<Backend | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Backend | null>(null)

  const { data: backends = [], isLoading } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
  })

  const createMutation = useMutation({
    mutationFn: backendsApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      toast('Host created', 'success')
      setShowCreate(false)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<Backend> }) => backendsApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      toast('Host updated', 'success')
      setEditing(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const deleteMutation = useMutation({
    mutationFn: backendsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      toast('Host deleted', 'success')
      setDeleteTarget(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <div>
          <h2 className="text-2xl font-bold">Hosts</h2>
          <p className="text-slate-400 text-sm mt-1">LLM backend endpoints. Expand a host to manage its models.</p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm"
        >
          <Plus className="w-4 h-4" /> Add host
        </button>
      </div>

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : backends.length === 0 ? (
          <EmptyState
            icon={Cpu}
            message="No hosts configured"
            hint="Add a host to start routing LLM requests."
          />
        ) : (
          <div className="overflow-x-auto">
          <table className="w-full min-w-[640px]">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="w-8 px-3 py-3" />
                <th className="text-left px-4 py-3 text-sm text-slate-400">Name</th>
                <th className="text-left px-4 py-3 text-sm text-slate-400">URL</th>
                <th className="text-left px-4 py-3 text-sm text-slate-400">Status</th>
                <th className="text-left px-4 py-3 text-sm text-slate-400">Latency</th>
                <th className="text-left px-4 py-3 text-sm text-slate-400">Weight</th>
                <th className="text-right px-4 py-3 text-sm text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {backends.map(b => (
                <HostRow
                  key={b.id}
                  backend={b}
                  onEdit={setEditing}
                  onDelete={setDeleteTarget}
                />
              ))}
            </tbody>
          </table>
          </div>
        )}
      </div>

      {showCreate && (
        <Modal title="Add host" onClose={() => setShowCreate(false)}>
          <HostForm
            defaultColor={nextColor(backends.map(b => backendColor(b)))}
            onSubmit={createMutation.mutate}
            onClose={() => setShowCreate(false)}
          />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit host" onClose={() => setEditing(null)}>
          <HostForm
            initial={editing}
            onSubmit={data => updateMutation.mutate({ id: editing.id, data })}
            onClose={() => setEditing(null)}
          />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete host"
          message={`Delete "${deleteTarget.name}"? All its model mappings will also be removed.`}
          onConfirm={() => deleteMutation.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
