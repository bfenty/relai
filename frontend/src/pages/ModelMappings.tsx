import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import CompressionSelect from '../components/CompressionSelect'
import { backendsApi, modelMappingsApi } from '../api/client'
import { ModelMap, ModelType } from '../types'
import { Brain, Edit3, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'

function guessModelType(name: string): ModelType {
  const n = name.toLowerCase()
  if (/rerank|reranker|cross-encoder/.test(n)) return 'rerank'
  if (/embed|e5-|bge-|instructor-|mxbai|jina-embed|text-embedding|nomic-embed|gte-/.test(n)) return 'embedding'
  return 'chat'
}

const MODEL_TYPES: { value: ModelType; label: string; color: string }[] = [
  { value: 'chat',      label: 'Chat',      color: 'bg-blue-500/15 text-blue-400 ring-blue-500/20' },
  { value: 'embedding', label: 'Embedding', color: 'bg-purple-500/15 text-purple-400 ring-purple-500/20' },
  { value: 'rerank',    label: 'Rerank',    color: 'bg-amber-500/15 text-amber-400 ring-amber-500/20' },
]

function TypeBadge({ type }: { type: ModelType }) {
  const t = MODEL_TYPES.find(t => t.value === type) ?? MODEL_TYPES[0]
  return (
    <span className={`inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${t.color}`}>
      {t.label}
    </span>
  )
}

type ParamsObject = Record<string, unknown>

const NUMBER_PARAMS = [
  { key: 'temperature', label: 'Temperature', step: '0.05', placeholder: '1' },
  { key: 'top_p', label: 'Top P', step: '0.01', placeholder: '0.95' },
  { key: 'min_p', label: 'Min P', step: '0.01', placeholder: '0' },
  { key: 'top_k', label: 'Top K', step: '1', placeholder: '20' },
  { key: 'max_tokens', label: 'Max Tokens', step: '1', placeholder: '248000' },
  { key: 'presence_penalty', label: 'Presence Penalty', step: '0.1', placeholder: '1.5' },
  { key: 'repetition_penalty', label: 'Repetition Penalty', step: '0.1', placeholder: '1' },
]

function parseParams(value: string): ParamsObject | null {
  try {
    const parsed = JSON.parse(value || '{}')
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') return null
    return parsed as ParamsObject
  } catch {
    return null
  }
}

function formatParams(value: ParamsObject) {
  return JSON.stringify(value, null, 2)
}

function compactParams(value: string) {
  const parsed = parseParams(value)
  return parsed ? JSON.stringify(parsed) : value
}

function setParamValue(params: string, key: string, value: number | boolean | null) {
  const parsed = parseParams(params) ?? {}
  if (value === null) {
    delete parsed[key]
  } else {
    parsed[key] = value
  }
  return formatParams(parsed)
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
  const parsed = parseParams(params)
  const kwargs = parsed?.chat_template_kwargs
  if (!kwargs || typeof kwargs !== 'object' || Array.isArray(kwargs)) return 'default'
  const value = (kwargs as ParamsObject).enable_thinking
  if (value === true) return 'on'
  if (value === false) return 'off'
  return 'default'
}

function MappingForm({ backends, initial, onSubmit, onCancel }: {
  backends: { id: number; name: string }[]
  initial?: ModelMap | null
  onSubmit: (data: Partial<ModelMap>) => void
  onCancel: () => void
}) {
  const [backendId, setBackendId] = useState(initial?.backend_id ? String(initial.backend_id) : '')
  const [remoteModel, setRemoteModel] = useState(initial?.remote_model ?? '')
  const [localModel, setLocalModel] = useState(initial?.local_model ?? '')
  const [modelType, setModelType] = useState<ModelType>(initial?.model_type ?? 'chat')
  const [typeManuallySet, setTypeManuallySet] = useState(!!initial)
  const [params, setParams] = useState(initial?.params ? formatParams(parseParams(initial.params) ?? {}) : '{}')
  const [paramsError, setParamsError] = useState('')
  const [compression, setCompression] = useState<string>(initial?.compression ?? '')

  const handleRemoteModelChange = (value: string) => {
    setRemoteModel(value)
    if (!typeManuallySet) setModelType(guessModelType(value))
  }

  const setThinkingPreset = (enabled: boolean) => {
    setParams(JSON.stringify({ chat_template_kwargs: { enable_thinking: enabled } }, null, 2))
    setParamsError('')
  }

  const setQwenVLLMPreset = (enabled: boolean) => {
    setParams(JSON.stringify({
      temperature: 1,
      top_p: 0.95,
      min_p: 0,
      top_k: 20,
      max_tokens: 248000,
      presence_penalty: 1.5,
      repetition_penalty: 1,
      chat_template_kwargs: { enable_thinking: enabled },
    }, null, 2))
    setParamsError('')
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setParamsError('')
    try {
      const parsed = JSON.parse(params || '{}')
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
        setParamsError('Params must be a JSON object.')
        return
      }
    } catch {
      setParamsError('Params must be valid JSON.')
      return
    }
    onSubmit({
      backend_id: parseInt(backendId),
      remote_model: remoteModel,
      local_model: localModel,
      model_type: modelType,
      params: compactParams(params),
      compression,
    })
  }

  const parsedParams = parseParams(params)
  const selectedThinking = thinkingValue(params)

  return (
    <form onSubmit={handleSubmit} className="space-y-4 p-4 bg-slate-800/50 rounded-lg border border-slate-700">
      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1">Backend</label>
        <select
          value={backendId}
          onChange={e => setBackendId(e.target.value)}
          required
          className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          <option value="">Select backend</option>
          {backends.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}
        </select>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1">Remote Model</label>
          <input
            type="text"
            value={remoteModel}
            onChange={e => handleRemoteModelChange(e.target.value)}
            placeholder="nomic-embed-text"
            required
            className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1">Local Model</label>
          <input
            type="text"
            value={localModel}
            onChange={e => setLocalModel(e.target.value)}
            placeholder="my-embed-model"
            required
            className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
      </div>
      <div>
        <label className="block text-sm font-medium text-slate-300 mb-2">
          Model Type
          {!typeManuallySet && remoteModel && (
            <span className="ml-2 text-xs text-slate-500 font-normal">auto-detected</span>
          )}
        </label>
        <div className="flex gap-2">
          {MODEL_TYPES.map(t => (
            <button
              key={t.value}
              type="button"
              onClick={() => { setModelType(t.value); setTypeManuallySet(true) }}
              className={`flex-1 py-1.5 rounded-md text-sm font-medium border transition-colors ${
                modelType === t.value
                  ? 'border-blue-500 bg-blue-600 text-white'
                  : 'border-slate-600 bg-slate-900 text-slate-400 hover:text-white'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>
      <div>
        <label className="block text-sm font-medium text-slate-300 mb-2">Generation Params</label>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          {NUMBER_PARAMS.map(field => (
            <div key={field.key}>
              <label className="block text-xs text-slate-500 mb-1">{field.label}</label>
              <input
                type="number"
                step={field.step}
                value={typeof parsedParams?.[field.key] === 'number' ? String(parsedParams[field.key]) : ''}
                onChange={e => setParams(setParamValue(params, field.key, e.target.value === '' ? null : Number(e.target.value)))}
                placeholder={field.placeholder}
                className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          ))}
          <div>
            <label className="block text-xs text-slate-500 mb-1">Thinking</label>
            <select
              value={selectedThinking}
              onChange={e => setParams(setThinkingValue(params, e.target.value === 'default' ? null : e.target.value === 'on'))}
              className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="default">Model default</option>
              <option value="off">Off</option>
              <option value="on">On</option>
            </select>
          </div>
        </div>
      </div>

      <CompressionSelect
        value={compression}
        onChange={setCompression}
        inherit="Falls back to the router's compression setting, if this mapping is a router member."
      />

      <div>
        <div className="flex items-center justify-between gap-3 mb-1">
          <label className="block text-sm font-medium text-slate-300">Extra Params JSON</label>
          <div className="flex gap-1">
            <button
              type="button"
              onClick={() => setThinkingPreset(false)}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-slate-600 bg-slate-900 text-slate-300 hover:text-white"
            >
              <Brain className="w-3 h-3" /> Thinking off
            </button>
            <button
              type="button"
              onClick={() => setThinkingPreset(true)}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-purple-700/60 bg-purple-950/30 text-purple-300 hover:text-purple-200"
            >
              <Brain className="w-3 h-3" /> Thinking on
            </button>
            <button
              type="button"
              onClick={() => setQwenVLLMPreset(false)}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-cyan-700/60 bg-cyan-950/20 text-cyan-300 hover:text-cyan-200"
            >
              Qwen OCR
            </button>
          </div>
        </div>
        <textarea
          value={params}
          onChange={e => setParams(e.target.value)}
          rows={5}
          spellCheck={false}
          className="w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white font-mono text-xs focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        {paramsError ? (
          <p className="mt-1 text-xs text-red-400">{paramsError}</p>
        ) : (
          <p className="mt-1 text-xs text-slate-500">
            Merged into proxied requests after model rewrite. LiteLLM extra_body fields should be top-level here.
          </p>
        )}
      </div>
      <div className="flex gap-2 justify-end">
        <button type="button" onClick={onCancel} className="px-3 py-1.5 text-sm text-slate-300 hover:text-white">Cancel</button>
        <button type="submit" className="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-md">{initial ? 'Save' : 'Create'}</button>
      </div>
    </form>
  )
}

export default function ModelMappings() {
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<ModelMap | null>(null)
  const [typeFilter, setTypeFilter] = useState<ModelType | ''>('')
  const queryClient = useQueryClient()

  const { data: mappings = [], isLoading: loadingMappings } = useQuery<ModelMap[]>({
    queryKey: ['model-mappings'],
    queryFn: modelMappingsApi.list,
  })

  const { data: backends = [] } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
    select: (data) => data.map(b => ({ id: b.id, name: b.name })),
  })

  const createMutation = useMutation({
    mutationFn: modelMappingsApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      setShowForm(false)
    },
  })

  const updateMutation = useMutation({
    mutationFn: (data: Partial<ModelMap> & { id: number }) => {
      const { id, ...payload } = data
      return modelMappingsApi.update(id, payload)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['model-mappings'] })
      setEditing(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: modelMappingsApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['model-mappings'] }),
  })

  const getBackendName = (id: number) => backends.find(b => b.id === id)?.name || `#${id}`

  const filtered = typeFilter ? mappings.filter(m => m.model_type === typeFilter) : mappings

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold text-white">Model Mappings</h1>
          <p className="text-slate-400 text-sm mt-1">Map local model names to remote models on each backend</p>
        </div>
        <button
          onClick={() => { setEditing(null); setShowForm(true) }}
          className="flex items-center gap-2 px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm rounded-md"
        >
          <Plus className="w-4 h-4" /> Add Mapping
        </button>
      </div>

      {showForm && (
        <MappingForm
          backends={backends}
          onSubmit={createMutation.mutate}
          onCancel={() => setShowForm(false)}
        />
      )}

      {editing && (
        <MappingForm
          key={editing.id}
          backends={backends}
          initial={editing}
          onSubmit={data => updateMutation.mutate({ ...data, id: editing.id })}
          onCancel={() => setEditing(null)}
        />
      )}

      {/* Type filter tabs */}
      <div className="flex gap-1 border-b border-slate-700 pb-0">
        {[{ value: '' as const, label: 'All' }, ...MODEL_TYPES.map(t => ({ value: t.value, label: t.label }))].map(t => (
          <button
            key={t.value}
            onClick={() => setTypeFilter(t.value as ModelType | '')}
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              typeFilter === t.value
                ? 'border-blue-500 text-white'
                : 'border-transparent text-slate-400 hover:text-slate-200'
            }`}
          >
            {t.label}
            <span className="ml-1.5 text-xs text-slate-500">
              {t.value === '' ? mappings.length : mappings.filter(m => m.model_type === t.value).length}
            </span>
          </button>
        ))}
      </div>

      {loadingMappings ? (
        <p className="text-slate-400">Loading...</p>
      ) : filtered.length === 0 ? (
        <div className="text-center py-12 text-slate-400">
          <p>No {typeFilter || ''} model mappings yet.</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="text-left py-3 px-4 text-slate-400 font-medium">Type</th>
                <th className="text-left py-3 px-4 text-slate-400 font-medium">Backend</th>
                <th className="text-left py-3 px-4 text-slate-400 font-medium">Remote Model</th>
                <th className="text-left py-3 px-4 text-slate-400 font-medium">Local Model</th>
                <th className="text-left py-3 px-4 text-slate-400 font-medium">Params</th>
                <th className="text-right py-3 px-4 text-slate-400 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(m => (
                <tr key={m.id} className="border-b border-slate-800 hover:bg-slate-800/50">
                  <td className="py-3 px-4"><TypeBadge type={m.model_type ?? 'chat'} /></td>
                  <td className="py-3 px-4 text-white">{getBackendName(m.backend_id)}</td>
                  <td className="py-3 px-4 text-slate-300 font-mono">{m.remote_model}</td>
                  <td className="py-3 px-4 text-slate-300 font-mono">{m.local_model}</td>
                  <td className="py-3 px-4 text-slate-400 font-mono text-xs max-w-xs truncate">
                    {m.params && m.params !== '{}' ? m.params : '—'}
                  </td>
                  <td className="py-3 px-4 text-right">
                    <button
                      onClick={() => { setShowForm(false); setEditing(m) }}
                      className="text-slate-400 hover:text-blue-400 mr-3"
                    >
                      <Edit3 className="w-4 h-4" />
                    </button>
                    <button
                      onClick={() => deleteMutation.mutate(m.id)}
                      className="text-slate-400 hover:text-red-400"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
