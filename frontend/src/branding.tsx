import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from 'react'
import { brandingApi } from './api/client'
import { BrandingSettings } from './types'

export const defaultBranding: BrandingSettings = {
  app_name: 'Relai',
  logo_url: '',
  primary_color: '#fa7c20',
  accent_color: '#ffa068',
}

// The previous defaults. A server still returning them never chose them, so the
// current theme's colors apply instead.
const LEGACY_DEFAULTS: Record<string, string> = { primary_color: '#2563eb', accent_color: '#60a5fa' }

function withoutLegacyDefaults(b: BrandingSettings): BrandingSettings {
  const out = { ...b }
  for (const key of ['primary_color', 'accent_color'] as const) {
    if (out[key]?.toLowerCase() === LEGACY_DEFAULTS[key]) out[key] = defaultBranding[key]
  }
  return out
}

/** Black or white, whichever reads better on the given background color. */
export function readableTextOn(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex)
  if (!m) return '#ffffff'
  const n = parseInt(m[1], 16)
  const lin = (c: number) => { const v = c / 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }
  const L = 0.2126 * lin(n >> 16) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  return (1.05 / (L + 0.05)) >= ((L + 0.05) / 0.05) ? '#ffffff' : '#0b0f14'
}

type BrandingContextValue = {
  branding: BrandingSettings
  refreshBranding: () => Promise<void>
  setBranding: (branding: BrandingSettings) => void
}

const BrandingContext = createContext<BrandingContextValue | null>(null)

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [branding, setBranding] = useState(defaultBranding)

  const refreshBranding = async () => {
    try {
      const next = await brandingApi.get()
      setBranding(withoutLegacyDefaults({ ...defaultBranding, ...next }))
    } catch {
      setBranding(defaultBranding)
    }
  }

  useEffect(() => {
    refreshBranding()
  }, [])

  useEffect(() => {
    document.documentElement.style.setProperty('--relai-primary', branding.primary_color || defaultBranding.primary_color)
    document.documentElement.style.setProperty('--relai-accent', branding.accent_color || defaultBranding.accent_color)
    document.title = branding.app_name || defaultBranding.app_name
  }, [branding])

  const value = useMemo(() => ({ branding, refreshBranding, setBranding }), [branding])
  return <BrandingContext.Provider value={value}>{children}</BrandingContext.Provider>
}

export function useBranding() {
  const ctx = useContext(BrandingContext)
  if (!ctx) {
    return {
      branding: defaultBranding,
      refreshBranding: async () => {},
      setBranding: () => {},
    }
  }
  return ctx
}
