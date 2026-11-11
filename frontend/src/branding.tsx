import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from 'react'
import { brandingApi } from './api/client'
import { BrandingSettings } from './types'

export const defaultBranding: BrandingSettings = {
  app_name: 'Relai',
  logo_url: '',
  primary_color: '#2563eb',
  accent_color: '#60a5fa',
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
      setBranding({ ...defaultBranding, ...next })
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
