import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export interface ErrorLogEntry {
  model: string
  error_message: string
  status_code: number
  created_at: string
}

/**
 * useDashboardStream connects to /api/dashboard/stream via SSE and refreshes the
 * stats queries when requests complete, so cards and charts show real numbers
 * without waiting for the polling interval. Also pushes error log entries to the
 * 'live-errors' query key. Falls back to the existing polling interval if SSE fails.
 *
 * Every completed request emits a stat event, so refreshes are throttled per query:
 * the dashboard's small "today" aggregate keeps ~1s freshness, while the analytics
 * query, which aggregates up to 90 days of logs on the server, refreshes from the
 * stream at most every ANALYTICS_MIN_MS. Hidden tabs never refetch from the stream;
 * react-query's own polling catches them up when they're shown again.
 */
const DASHBOARD_MIN_MS = 1000
const ANALYTICS_MIN_MS = 15000

export function useDashboardStream() {
  const queryClient = useQueryClient()
  const reconnectDelay = useRef(0)
  const refetchTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastAnalytics = useRef(0)

  useEffect(() => {
    const baseUrl = window.location.origin
    const es = new EventSource(`${baseUrl}/api/dashboard/stream`)

    es.addEventListener('stat', () => {
      reconnectDelay.current = 0
      if (refetchTimer.current || document.visibilityState !== 'visible') return
      refetchTimer.current = setTimeout(() => {
        refetchTimer.current = null
        queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
        const now = Date.now()
        if (now - lastAnalytics.current >= ANALYTICS_MIN_MS) {
          lastAnalytics.current = now
          queryClient.invalidateQueries({ queryKey: ['analytics'] })
        }
      }, DASHBOARD_MIN_MS)
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
