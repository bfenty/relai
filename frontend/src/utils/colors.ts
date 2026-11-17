import { useQuery } from '@tanstack/react-query'
import { backendsApi } from '../api/client'
import { Backend } from '../types'
import { ENTITY } from '../theme'

// Host colors are the theme's entity palette (validated for color-blind separation).
export const HOST_PALETTE = ENTITY

// Colors offered by the previous palette. A host saved with one of them predates the
// redesign rather than choosing it, so it gets an entity color by id instead.
const LEGACY_PALETTE = new Set([
  '#3b82f6', '#22c55e', '#f59e0b', '#ec4899', '#8b5cf6',
  '#06b6d4', '#f97316', '#10b981', '#e11d48', '#a78bfa',
])

export function backendColor(b: Backend): string {
  const saved = b.color?.toLowerCase()
  if (saved && !LEGACY_PALETTE.has(saved)) return b.color
  return HOST_PALETTE[(b.id - 1 + HOST_PALETTE.length) % HOST_PALETTE.length]
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
