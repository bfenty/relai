import { useState, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Zap, Eye, EyeOff } from 'lucide-react'
import { authApi } from '../api/client'
import { useBranding } from '../branding'

export default function Login() {
  const nav = useNavigate()
  const { branding } = useBranding()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [showPw, setShowPw] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await authApi.login(username, password)
      nav('/dashboard', { replace: true })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-slate-900 flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center justify-center gap-3 mb-8">
          {branding.logo_url ? (
            <img src={branding.logo_url} alt="" className="w-8 h-8 object-contain" />
          ) : (
            <Zap className="w-8 h-8" style={{ color: branding.accent_color }} />
          )}
          <span className="text-2xl font-bold tracking-tight text-white">{branding.app_name}</span>
        </div>

        <div className="bg-slate-800 rounded-xl border border-slate-700 p-8">
          <h1 className="text-lg font-semibold text-white mb-6">Sign in</h1>

          <form onSubmit={submit} className="space-y-4">
            <div>
              <label className="block text-xs text-slate-400 mb-1.5">Username</label>
              <input
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                autoFocus
                autoComplete="username"
                required
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white
                           placeholder-slate-500 focus:outline-none focus:border-blue-500 text-sm"
              />
            </div>

            <div>
              <label className="block text-xs text-slate-400 mb-1.5">Password</label>
              <div className="relative">
                <input
                  type={showPw ? 'text' : 'password'}
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  autoComplete="current-password"
                  required
                  className="w-full px-3 py-2 pr-10 bg-slate-700 border border-slate-600 rounded-lg text-white
                             placeholder-slate-500 focus:outline-none focus:border-blue-500 text-sm"
                />
                <button
                  type="button"
                  onClick={() => setShowPw(s => !s)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 hover:text-white p-1"
                  tabIndex={-1}
                >
                  {showPw ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>
            </div>

            {error && (
              <p className="text-sm text-red-400 bg-red-900/20 border border-red-800/40 rounded-lg px-3 py-2">
                {error}
              </p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full py-2 px-4 disabled:opacity-50
                         disabled:cursor-not-allowed rounded-lg text-sm font-medium text-white
                         transition-colors"
              style={{ backgroundColor: branding.primary_color }}
            >
              {loading ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        </div>
      </div>
    </div>
  )
}
