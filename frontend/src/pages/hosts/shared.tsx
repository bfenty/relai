import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { backendsApi, queueApi } from '../../api/client'
import { Backend } from '../../types'
import { Pencil, Trash2, RefreshCw, CheckCircle, XCircle } from 'lucide-react'
import { useState } from 'react'
import { status as statusColors } from '../../theme'
import { Modal } from '../../components/Modal'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { useToast } from '../../components/Toast'
import { backendColor, nextColor } from '../../utils/colors'
import { HostForm } from './HostForm'

// ── Host row ──────────────────────────────────────────────────────────────────

type Health = 'good' | 'warn' | 'crit'
function healthOf(s: string): { tone: Health; label: string } {
  switch (s) {
    case 'active': return { tone: 'good', label: 'Healthy' }
    case 'offline': case 'error': return { tone: 'crit', label: s === 'error' ? 'Error' : 'Offline' }
    default: return { tone: 'warn', label: s ? s[0].toUpperCase() + s.slice(1) : 'Unknown' }
  }
}

/** Host state: a shape and a word as well as a color. */
export function HostState({ status }: { status: string }) {
  const { tone, label } = healthOf(status)
  const fill = tone === 'good' ? statusColors.good : tone === 'warn' ? statusColors.warn : statusColors.bad
  return (
    <span className="inline-flex items-center gap-1.5 text-slate-300">
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden fill={fill}>
        {tone === 'good' ? <circle cx="5" cy="5" r="4" /> : tone === 'warn' ? <path d="M5 .8 9.5 9H.5z" /> : <rect x="1" y="1" width="8" height="8" rx="1" />}
      </svg>
      {label}
    </span>
  )
}

/** Live numbers per host: requests in flight and the speeds relai has learned. */
export function useHostLive() {
  const { data: queue } = useQuery({ queryKey: ['queue'], queryFn: queueApi.snapshot, refetchInterval: 2000 })
  const { data: speedData } = useQuery({ queryKey: ['queue-speeds'], queryFn: queueApi.speeds, refetchInterval: 5000 })
  const entries = queue?.active ?? []
  const speeds = new Map((speedData?.speeds ?? []).map(s => [s.backend_id, s]))
  return {
    entries,
    active: (id: number) => entries.filter(e => e.backend_id === id).length,
    speed: (id: number) => {
      const s = speeds.get(id)
      return s && s.samples > 0 ? s : undefined
    },
    learnedPromptTokS: new Map((speedData?.speeds ?? []).map(s => [s.backend_id, s.prompt_tok_s])),
  }
}

export function HostActions({ backend, onEdit, onDelete }: {
  backend: Backend
  onEdit: (b: Backend) => void
  onDelete: (b: Backend) => void
}) {
  const { toast } = useToast()
  const queryClient = useQueryClient()

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
    <div className="flex items-center justify-end gap-1" onClick={e => e.stopPropagation()}>
      <button onClick={() => healthMutation.mutate()} disabled={healthMutation.isPending} title="Health check"
        className="rounded p-1.5 text-slate-400 hover:text-white disabled:opacity-40">
        <RefreshCw className={`h-4 w-4 ${healthMutation.isPending ? 'animate-spin' : ''}`} />
      </button>
      <button onClick={() => toggleMutation.mutate()} title={backend.status === 'active' ? 'Pause' : 'Activate'}
        className="rounded p-1.5 text-slate-400 hover:text-white">
        {backend.status === 'active' ? <XCircle className="h-4 w-4" /> : <CheckCircle className="h-4 w-4" />}
      </button>
      <button onClick={() => onEdit(backend)} title="Edit host" className="rounded p-1.5 text-slate-400 hover:text-white">
        <Pencil className="h-4 w-4" />
      </button>
      <button onClick={() => onDelete(backend)} title="Delete host" className="rounded p-1.5 text-slate-400 hover:text-crit-400">
        <Trash2 className="h-4 w-4" />
      </button>
    </div>
  )
}

/** Create / edit / delete dialogs shared by the list and the detail page. */
export function useHostDialogs(backends: Backend[], afterDelete?: () => void) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [editing, setEditing] = useState<Backend | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Backend | null>(null)

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
      afterDelete?.()
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const dialogs = (
    <>
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
    </>
  )
  return { dialogs, create: () => setShowCreate(true), edit: setEditing, remove: setDeleteTarget }
}
