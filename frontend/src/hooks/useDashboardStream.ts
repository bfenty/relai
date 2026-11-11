import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export interface ErrorLogEntry {
  model: string
  error_message: string
  status_code: number
  created_at: string
}

/**
 * useDashboardStream connects to /api/dashboard/stream via SSE and
 * invalidates the stats query on incoming stat events so the charts
 * and cards refetch real numbers. Also pushes error log entries to
 * the 'live-errors' query key. Falls back to the existing polling
 * interval if SSE fails.
 */
export function useDashboardStream() {
  const queryClient = useQueryClient()
  const reconnectDelay = useRef(0)
  const refetchTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    const baseUrl = window.location.origin
    const es = new EventSource(`${baseUrl}/api/dashboard/stream`)

    es.addEventListener('stat', () => {
      if (refetchTimer.current) return
      refetchTimer.current = setTimeout(() => {
        refetchTimer.current = null
        queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
        queryClient.invalidateQueries({ queryKey: ['analytics'] })
      }, 1000)
      reconnectDelay.current = 0
    })

    es.addEventListener('error', (ev: MessageEvent) => {
      try {
        const entry: ErrorLogEntry = JSON.parse(ev.data as string)
        queryClient.setQueryData<ErrorLogEntry[]>(['live-errors'], (old) => {
          const list = old ? [...old] : []
          list.unshift(entry)
          return list.slice(0, 50)
        })
      } catch {
        // SSE connection error event (no data payload) — EventSource auto-reconnects
        reconnectDelay.current = Math.min((reconnectDelay.current || 1000) * 2, 30000)
      }
    })

    return () => {
      if (refetchTimer.current) {
        clearTimeout(refetchTimer.current)
      }
      es.close()
    }
  }, [queryClient])
}
