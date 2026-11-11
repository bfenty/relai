import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { mcpApi } from '../api/client'
import { MCPServer } from '../types'
import { Plus, Pencil, Trash2, Puzzle, ExternalLink } from 'lucide-react'
import { useState } from 'react'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { useToast } from '../components/Toast'

// ── MCP server form ───────────────────────────────────────────────────────────

function MCPForm({ initial, onSubmit, onClose }: {
  initial?: MCPServer
  onSubmit: (d: Partial<MCPServer>) => void
  onClose: () => void
}) {
  const [name, setName]         = useState(initial?.name ?? '')
  const [url, setUrl]           = useState(initial?.url ?? '')
  const [transport, setTransport] = useState<'http' | 'stdio'>(initial?.transport ?? 'http')
  const [apiKey, setApiKey]     = useState('')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [isEnabled, setIsEnabled] = useState(initial?.is_enabled ?? true)

  return (
    <form onSubmit={e => {
      e.preventDefault()
      const data: Partial<MCPServer> = { name, url, transport, description, is_enabled: isEnabled }
      if (apiKey) data.api_key = apiKey
      onSubmit(data)
    }} className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          placeholder="my-rag-server"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Transport</label>
        <select value={transport} onChange={e => setTransport(e.target.value as 'http' | 'stdio')}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
          <option value="http">HTTP / SSE</option>
          <option value="stdio">stdio</option>
        </select>
      </div>
      {transport === 'http' && (
        <div>
          <label className="block text-sm text-slate-400 mb-1">URL</label>
          <input value={url} onChange={e => setUrl(e.target.value)}
            placeholder="http://localhost:3001/mcp"
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
        </div>
      )}
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          API Key {initial && <span className="text-slate-500">(leave blank to keep existing)</span>}
        </label>
        <input type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
          placeholder={initial ? '••••••••' : 'Optional'}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Description</label>
        <input value={description} onChange={e => setDescription(e.target.value)}
          placeholder="Optional notes about this server's capabilities"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <label className="flex items-center gap-2 cursor-pointer select-none">
        <input type="checkbox" checked={isEnabled} onChange={e => setIsEnabled(e.target.checked)}
          className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800" />
        <span className="text-sm text-slate-300">Enabled</span>
      </label>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Add server'}
        </button>
      </div>
    </form>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function MCP() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate]     = useState(false)
  const [editing, setEditing]           = useState<MCPServer | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<MCPServer | null>(null)

  const { data: servers = [], isLoading } = useQuery<MCPServer[]>({
    queryKey: ['mcp-servers'],
    queryFn: mcpApi.list,
  })

  const create = useMutation({
    mutationFn: mcpApi.create,
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['mcp-servers'] }); setShowCreate(false); toast('MCP server added', 'success') },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const update = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<MCPServer> }) => mcpApi.update(id, data),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['mcp-servers'] }); setEditing(null); toast('MCP server updated', 'success') },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const del = useMutation({
    mutationFn: mcpApi.delete,
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['mcp-servers'] }); setDeleteTarget(null); toast('MCP server deleted', 'success') },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  return (
    <div>
      <div className="flex flex-wrap items-start justify-between gap-3 mb-6">
        <div>
          <h2 className="text-2xl font-bold">MCP Servers</h2>
          <p className="text-slate-400 text-sm mt-1">
            Manage Model Context Protocol servers for tool use and RAG access.
            Relai can proxy and gate access to these servers per API key.
          </p>
        </div>
        <button onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          <Plus className="w-4 h-4" /> Add server
        </button>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-48">
          <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
        </div>
      ) : servers.length === 0 ? (
        <EmptyState icon={Puzzle}
          message="No MCP servers configured"
          hint="Add an MCP server to enable tool use and RAG capabilities for your models." />
      ) : (
        <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="text-left px-4 py-3 text-xs text-slate-400">Name</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Transport</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">URL</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Description</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Status</th>
                <th className="text-right px-4 py-3 text-xs text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {servers.map(s => (
                <tr key={s.id} className="border-b border-slate-700/50 hover:bg-slate-700/20">
                  <td className="px-4 py-3 font-medium text-slate-200">{s.name}</td>
                  <td className="px-4 py-3">
                    <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{s.transport}</span>
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-400 font-mono">
                    {s.url
                      ? <span className="flex items-center gap-1">{s.url}<ExternalLink className="w-3 h-3 shrink-0" /></span>
                      : <span className="text-slate-600">—</span>}
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-400">{s.description || <span className="text-slate-600">—</span>}</td>
                  <td className="px-4 py-3">
                    <span className={`px-2 py-0.5 rounded-full text-xs ${s.is_enabled ? 'bg-green-500/20 text-green-400' : 'bg-slate-500/20 text-slate-400'}`}>
                      {s.is_enabled ? 'enabled' : 'disabled'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button onClick={() => setEditing(s)} className="p-1.5 text-slate-400 hover:text-white rounded">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button onClick={() => setDeleteTarget(s)} className="p-1.5 text-slate-400 hover:text-red-400 rounded">
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showCreate && (
        <Modal title="Add MCP server" onClose={() => setShowCreate(false)}>
          <MCPForm onSubmit={create.mutate} onClose={() => setShowCreate(false)} />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit MCP server" onClose={() => setEditing(null)}>
          <MCPForm
            initial={editing}
            onSubmit={data => update.mutate({ id: editing.id, data })}
            onClose={() => setEditing(null)}
          />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete MCP server"
          message={`Delete "${deleteTarget.name}"? Any API keys referencing it will lose access.`}
          onConfirm={() => del.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
