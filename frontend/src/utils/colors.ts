import { useQuery } from '@tanstack/react-query'
import { backendsApi } from '../api/client'
import { Backend } from '../types'

export const HOST_PALETTE = [
  '#3b82f6', // blue
  '#22c55e', // green
  '#f59e0b', // amber
  '#ec4899', // pink
  '#8b5cf6', // violet
  '#06b6d4', // cyan
  '#f97316', // orange
  '#10b981', // emerald
  '#e11d48', // rose
  '#a78bfa', // lavender
]

export function backendColor(b: Backend): string {
  return b.color || HOST_PALETTE[(b.id - 1) % HOST_PALETTE.length]
}

export function nextColor(usedColors: string[]): string {
  return HOST_PALETTE.find(c => !usedColors.includes(c)) ?? HOST_PALETTE[0]
}

export function useBackendColors(): Map<number, string> {
  const { data: backends = [] } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
    staleTime: 30000,
  })
  return new Map(backends.map(b => [b.id, backendColor(b)]))
}

export function useBackendColorsByName(): Map<string, string> {
  const { data: backends = [] } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
    staleTime: 30000,
  })
  return new Map(backends.map(b => [b.name, backendColor(b)]))
}
