import { createContext, useContext, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { CheckCircle, XCircle, Info, X } from 'lucide-react'

type ToastVariant = 'success' | 'error' | 'info'

interface Toast {
  id: number
  message: string
  variant: ToastVariant
}

interface ToastContextValue {
  toast: (message: string, variant?: ToastVariant) => void
}

const ToastContext = createContext<ToastContextValue>({ toast: () => {} })

let nextId = 0

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])

  const dismiss = useCallback((id: number) => {
    setToasts(t => t.filter(x => x.id !== id))
  }, [])

  const toast = useCallback((message: string, variant: ToastVariant = 'info') => {
    const id = ++nextId
    setToasts(t => [...t, { id, message, variant }])
    setTimeout(() => dismiss(id), 4000)
  }, [dismiss])

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      {createPortal(
        <div className="fixed top-4 right-4 z-50 flex flex-col gap-2 w-80">
          {toasts.map(t => <ToastItem key={t.id} toast={t} onDismiss={dismiss} />)}
        </div>,
        document.body
      )}
    </ToastContext.Provider>
  )
}

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (id: number) => void }) {
  const styles: Record<ToastVariant, string> = {
    success: 'bg-green-900/90 border-green-700 text-green-100',
    error:   'bg-red-900/90 border-red-700 text-red-100',
    info:    'bg-slate-800/90 border-slate-600 text-slate-100',
  }
  const icons: Record<ToastVariant, React.ElementType> = {
    success: CheckCircle,
    error:   XCircle,
    info:    Info,
  }
  const Icon = icons[toast.variant]

  return (
    <div className={`flex items-start gap-3 px-4 py-3 rounded-lg border shadow-lg ${styles[toast.variant]}`}>
      <Icon className="w-5 h-5 mt-0.5 shrink-0" />
      <span className="flex-1 text-sm">{toast.message}</span>
      <button onClick={() => onDismiss(toast.id)} className="shrink-0 opacity-60 hover:opacity-100">
        <X className="w-4 h-4" />
      </button>
    </div>
  )
}

export function useToast() {
  return useContext(ToastContext)
}
