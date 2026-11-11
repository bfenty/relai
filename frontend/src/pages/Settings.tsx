import { useEffect, useState, useRef } from 'react'
import InfluxSettingsPanel from '../components/InfluxSettings'
import { useQuery } from '@tanstack/react-query'
import { Settings as SettingsIcon, Download, Upload, Check, AlertCircle, Palette, Image as ImageIcon, RotateCcw, Save, Zap, Database } from 'lucide-react'
import { brandingApi, configApi, settingsApi } from '../api/client'
import { useToast } from '../components/Toast'
import { defaultBranding, useBranding } from '../branding'
import { BrandingSettings } from '../types'

export default function Settings() {
  const { toast } = useToast()
  const { branding, setBranding, refreshBranding } = useBranding()
  const fileRef = useRef<HTMLInputElement>(null)
  const logoFileRef = useRef<HTMLInputElement>(null)
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<Record<string, number> | null>(null)
  const [importError, setImportError] = useState<string | null>(null)
  const [brandingForm, setBrandingForm] = useState<BrandingSettings>(branding)
  const [savingBranding, setSavingBranding] = useState(false)
  const { data: storage } = useQuery({
    queryKey: ['settings-storage'],
    queryFn: settingsApi.storage,
    refetchInterval: 10000,
  })

  useEffect(() => {
    setBrandingForm(branding)
  }, [branding])

  const updateBrandingField = (key: keyof BrandingSettings, value: string) => {
    setBrandingForm(prev => ({ ...prev, [key]: value }))
  }

  const handleLogoFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    if (!file.type.startsWith('image/')) {
      toast('Logo must be an image file', 'error')
      return
    }
    const reader = new FileReader()
    reader.onload = () => updateBrandingField('logo_url', String(reader.result ?? ''))
    reader.readAsDataURL(file)
    if (logoFileRef.current) logoFileRef.current.value = ''
  }

  const saveBranding = async () => {
    setSavingBranding(true)
    try {
      const saved = await brandingApi.update(brandingForm)
      setBranding(saved)
      setBrandingForm(saved)
      toast('Branding saved', 'success')
    } catch (e: any) {
      toast(e.message ?? 'Branding save failed', 'error')
    } finally {
      setSavingBranding(false)
    }
  }

  const resetBranding = () => {
    setBrandingForm(defaultBranding)
  }

  const colorValue = (value: string, fallback: string) => (
    /^#[0-9a-fA-F]{6}$/.test(value) ? value : fallback
  )

  const handleExport = async () => {
    try {
      const res = await configApi.export()
      if (!res.ok) throw new Error(await res.text())
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'relai-config.json'
      a.click()
      URL.revokeObjectURL(url)
      toast('Config exported', 'success')
    } catch (e: any) {
      toast(e.message ?? 'Export failed', 'error')
    }
  }

  const handleImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    setImportResult(null)
    setImportError(null)
    try {
      const text = await file.text()
      const data = JSON.parse(text)
      const result = await configApi.import(data)
      setImportResult(result.imported)
      toast('Config imported', 'success')
    } catch (e: any) {
      setImportError(e.message ?? 'Import failed')
      toast(e.message ?? 'Import failed', 'error')
    } finally {
      setImporting(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  return (
    <div className="max-w-2xl space-y-4">
      <div className="flex items-center gap-2 mb-6">
        <h2 className="text-2xl font-bold">Settings</h2>
      </div>

      <InfluxSettingsPanel />

      <div className="bg-slate-800 rounded-lg p-6 border border-slate-700">
        <h3 className="text-base font-semibold mb-1 flex items-center gap-2">
          <Palette className="w-4 h-4 text-slate-400" /> Branding
        </h3>
        <p className="text-sm text-slate-400 mb-5">
          Customize the app name, logo, and interface colors. Empty values fall back to the default Relai branding.
        </p>

        <div className="grid gap-4 sm:grid-cols-[1fr_auto] items-start">
          <div className="space-y-4">
            <div>
              <label className="block text-xs text-slate-400 mb-1.5">App name</label>
              <input
                value={brandingForm.app_name}
                onChange={e => updateBrandingField('app_name', e.target.value)}
                placeholder="Relai"
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
              />
            </div>

            <div>
              <label className="block text-xs text-slate-400 mb-1.5">Logo URL or data image</label>
              <div className="flex gap-2">
                <input
                  value={brandingForm.logo_url}
                  onChange={e => updateBrandingField('logo_url', e.target.value)}
                  placeholder="https://example.com/logo.svg"
                  className="flex-1 min-w-0 px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm"
                />
                <button
                  type="button"
                  onClick={() => logoFileRef.current?.click()}
                  className="inline-flex items-center gap-2 px-3 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm text-slate-200"
                >
                  <ImageIcon className="w-4 h-4" /> Pick
                </button>
                <input ref={logoFileRef} type="file" accept="image/*" className="hidden" onChange={handleLogoFile} />
              </div>
            </div>

            <div className="grid sm:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs text-slate-400 mb-1.5">Primary color</label>
                <div className="flex gap-2">
                  <input
                    type="color"
                    value={colorValue(brandingForm.primary_color, defaultBranding.primary_color)}
                    onChange={e => updateBrandingField('primary_color', e.target.value)}
                    className="h-10 w-12 rounded bg-slate-700 border border-slate-600"
                  />
                  <input
                    value={brandingForm.primary_color}
                    onChange={e => updateBrandingField('primary_color', e.target.value)}
                    className="flex-1 min-w-0 px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm font-mono"
                  />
                </div>
              </div>
              <div>
                <label className="block text-xs text-slate-400 mb-1.5">Accent color</label>
                <div className="flex gap-2">
                  <input
                    type="color"
                    value={colorValue(brandingForm.accent_color, defaultBranding.accent_color)}
                    onChange={e => updateBrandingField('accent_color', e.target.value)}
                    className="h-10 w-12 rounded bg-slate-700 border border-slate-600"
                  />
                  <input
                    value={brandingForm.accent_color}
                    onChange={e => updateBrandingField('accent_color', e.target.value)}
                    className="flex-1 min-w-0 px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500 text-sm font-mono"
                  />
                </div>
              </div>
            </div>
          </div>

          <div className="w-full sm:w-44 rounded-lg border border-slate-700 bg-slate-900 p-4">
            <div className="flex items-center gap-2 mb-3">
              {brandingForm.logo_url ? (
                <img src={brandingForm.logo_url} alt="" className="w-7 h-7 object-contain" />
              ) : (
                <Zap className="w-7 h-7" style={{ color: brandingForm.accent_color }} />
              )}
              <span className="font-semibold truncate">{brandingForm.app_name || 'Relai'}</span>
            </div>
            <div className="h-2 rounded-full mb-2" style={{ backgroundColor: brandingForm.primary_color }} />
            <div className="h-2 rounded-full w-2/3" style={{ backgroundColor: brandingForm.accent_color }} />
          </div>
        </div>

        <div className="flex items-center gap-3 mt-5">
          <button
            onClick={saveBranding}
            disabled={savingBranding}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm text-white disabled:opacity-50"
            style={{ backgroundColor: brandingForm.primary_color }}
          >
            <Save className="w-4 h-4" /> {savingBranding ? 'Saving...' : 'Save branding'}
          </button>
          <button
            onClick={resetBranding}
            className="inline-flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm text-slate-200"
          >
            <RotateCcw className="w-4 h-4" /> Reset defaults
          </button>
          <button
            onClick={refreshBranding}
            className="px-3 py-2 text-sm text-slate-400 hover:text-white"
          >
            Reload
          </button>
        </div>
      </div>

      {/* Auth section */}
      <div className="bg-slate-800 rounded-lg p-6 border border-slate-700">
        <h3 className="text-base font-semibold mb-1 flex items-center gap-2">
          <SettingsIcon className="w-4 h-4 text-slate-400" /> Authentication
        </h3>
        <p className="text-sm text-slate-400">
          You are signed in. To change your password, set the{' '}
          <code className="text-slate-300 bg-slate-700 px-1 rounded">RELAI_ADMIN_PASSWORD</code>{' '}
          environment variable and restart the server.
        </p>
      </div>

      <div className="bg-slate-800 rounded-lg p-6 border border-slate-700">
        <h3 className="text-base font-semibold mb-1 flex items-center gap-2">
          <Database className="w-4 h-4 text-slate-400" /> Storage
        </h3>
        <p className="text-sm text-slate-400 mb-4">
          Production deployments should use PostgreSQL for request logs. SQLite remains the local app-state store and test fallback.
        </p>
        <div className="grid sm:grid-cols-2 gap-3 text-sm">
          <div className="rounded-lg bg-slate-900/60 border border-slate-700 p-3">
            <div className="text-xs text-slate-500 mb-1">SQLite app DB</div>
            <div className="font-mono text-slate-200 break-all">{storage?.sqlite_path ?? '...'}</div>
          </div>
          <div className="rounded-lg bg-slate-900/60 border border-slate-700 p-3">
            <div className="text-xs text-slate-500 mb-1">Request log backend</div>
            <div className={storage?.postgres_logs_enabled ? 'text-green-400 font-medium' : 'text-amber-300 font-medium'}>
              {storage?.log_backend ?? '...'}
            </div>
          </div>
          <div className="rounded-lg bg-slate-900/60 border border-slate-700 p-3">
            <div className="text-xs text-slate-500 mb-1">Request log rows</div>
            <div className="text-slate-200">{storage?.request_log_rows?.toLocaleString() ?? '...'}</div>
          </div>
          <div className="rounded-lg bg-slate-900/60 border border-slate-700 p-3">
            <div className="text-xs text-slate-500 mb-1">Latest request log</div>
            <div className="text-slate-200">
              {storage?.latest_request_log_at ? new Date(storage.latest_request_log_at).toLocaleString() : 'none'}
            </div>
          </div>
        </div>
        {storage?.warnings?.length ? (
          <div className="mt-4 space-y-2">
            {storage.warnings.map(warning => (
              <div key={warning} className="flex items-start gap-2 rounded-lg border border-amber-700/50 bg-amber-900/20 px-3 py-2 text-sm text-amber-200">
                <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
                {warning}
              </div>
            ))}
          </div>
        ) : null}
      </div>

      {/* Config export/import */}
      <div className="bg-slate-800 rounded-lg p-6 border border-slate-700">
        <h3 className="text-base font-semibold mb-1 flex items-center gap-2">
          <Download className="w-4 h-4 text-slate-400" /> Config Export / Import
        </h3>
        <p className="text-sm text-slate-400 mb-4">
          Export all backends, model mappings, routers, routing rules, and MCP servers to a JSON file.
          API keys and secrets are stripped from exports.
        </p>
        <div className="flex items-center gap-3">
          <button onClick={handleExport}
            className="flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm text-slate-200">
            <Download className="w-4 h-4" /> Export config
          </button>
          <button onClick={() => fileRef.current?.click()} disabled={importing}
            className="flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm text-slate-200 disabled:opacity-50">
            <Upload className="w-4 h-4" /> {importing ? 'Importing…' : 'Import config'}
          </button>
          <input ref={fileRef} type="file" accept=".json" className="hidden" onChange={handleImport} />
        </div>

        {importResult && (
          <div className="mt-4 p-3 bg-green-900/30 border border-green-700/50 rounded-lg">
            <div className="flex items-center gap-2 text-green-400 text-sm font-medium mb-1">
              <Check className="w-4 h-4" /> Import complete
            </div>
            <ul className="text-sm text-slate-300 space-y-0.5">
              {Object.entries(importResult).map(([k, v]) => (
                <li key={k}><span className="text-slate-400">{k}:</span> {v} record{v !== 1 ? 's' : ''}</li>
              ))}
            </ul>
          </div>
        )}

        {importError && (
          <div className="mt-4 p-3 bg-red-900/30 border border-red-700/50 rounded-lg flex items-start gap-2 text-sm text-red-400">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            {importError}
          </div>
        )}
      </div>
    </div>
  )
}
