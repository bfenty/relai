import { Backend, APIKey, RoutingRule, RequestLog, DashboardStats, ModelMap, Router, RouterMember, MemberStatus, User, UserWithStats, MCPServer, SystemEvent, EventFilters, BrandingSettings, StorageDiagnostics, AnalyticsResult, AnalyticsGroupBy, AnalyticsInterval } from '../types'

const API_BASE = '/api'

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${url}`, {
    ...options,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  })

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: { message: res.statusText } }))
    throw new Error(error.error?.message || res.statusText)
  }

  if (res.status === 204) {
    return null as T
  }

  return res.json()
}

// Auth
export const authApi = {
  login: (username: string, password: string) =>
    request<{ status: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  me: () => request<{ authenticated: boolean }>('/auth/me'),
}

// Backends
export const backendsApi = {
  list: () => request<Backend[]>('/backends'),
  get: (id: number) => request<Backend>(`/backends/${id}`),
  create: (data: Partial<Backend>) => request<Backend>('/backends', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<Backend>) => request<Backend>(`/backends/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/backends/${id}`, { method: 'DELETE' }),
  healthCheck: (id: number) => request<Backend>(`/backends/${id}/health`, { method: 'POST' }),
}

// API Keys
export const apiKeysApi = {
  list: () => request<APIKey[]>('/keys'),
  get: (id: number) => request<APIKey>(`/keys/${id}`),
  create: (data: Partial<APIKey>) => request<{ key: APIKey; value: string }>('/keys', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<APIKey>) => request<APIKey>(`/keys/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/keys/${id}`, { method: 'DELETE' }),
  createTestKey: () => request<{ key: APIKey; value: string }>('/keys/test', { method: 'POST' }),
  cleanupTestKeys: () => request<{ deleted: number }>('/keys/cleanup', { method: 'POST' }),
}

// Routing Rules
export const routingApi = {
  list: () => request<RoutingRule[]>('/routing-rules'),
  get: (id: number) => request<RoutingRule>(`/routing-rules/${id}`),
  create: (data: Partial<RoutingRule>) => request<RoutingRule>('/routing-rules', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<RoutingRule>) => request<RoutingRule>(`/routing-rules/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/routing-rules/${id}`, { method: 'DELETE' }),
}

// Logs
export interface LogFilters {
  limit?: number
  offset?: number
  model?: string
  backend_id?: number
  api_key_id?: number
  errors_only?: boolean
  from?: string
  to?: string
}

export const logsApi = {
  list: (filters: LogFilters = {}) => {
    const p = new URLSearchParams()
    if (filters.limit)      p.set('limit',      String(filters.limit))
    if (filters.offset)     p.set('offset',     String(filters.offset))
    if (filters.model)      p.set('model',      filters.model)
    if (filters.backend_id) p.set('backend_id', String(filters.backend_id))
    if (filters.api_key_id) p.set('api_key_id', String(filters.api_key_id))
    if (filters.errors_only) p.set('errors_only', 'true')
    if (filters.from)       p.set('from',       filters.from)
    if (filters.to)         p.set('to',         filters.to)
    return request<{ data: RequestLog[]; total: number }>(`/logs?${p}`)
  },
}

// Dashboard
export const dashboardApi = {
  stats: (range?: string) => {
    const url = range ? `/dashboard/stats?range=${range}` : '/dashboard/stats'
    return request<DashboardStats>(url)
  },
}

// Analytics
export interface AnalyticsParams {
  from?: string
  to?: string
  group_by?: AnalyticsGroupBy
  interval?: AnalyticsInterval
}

export const analyticsApi = {
  usage: (params: AnalyticsParams = {}) => {
    const p = new URLSearchParams()
    if (params.from)     p.set('from',     params.from)
    if (params.to)       p.set('to',       params.to)
    if (params.group_by) p.set('group_by', params.group_by)
    if (params.interval) p.set('interval', params.interval)
    return request<AnalyticsResult>(`/analytics/usage?${p}`)
  },
  routing: (hours = 24) =>
    request<import('../types').RoutingStats>(`/analytics/routing?hours=${hours}`),
  routingSeries: (hours = 24, interval?: string) =>
    request<import('../types').RoutingSeries>(
      `/analytics/routing/series?hours=${hours}${interval ? `&interval=${interval}` : ''}`),
  prefix: (hours = 24) =>
    request<import('../types').PrefixStats>(`/analytics/prefix?hours=${hours}`),
  prefixSeries: (hours = 24, interval?: string) =>
    request<import('../types').PrefixSeries>(
      `/analytics/prefix/series?hours=${hours}${interval ? `&interval=${interval}` : ''}`),
  cache: () => request<import('../types').CacheTelemetry[]>('/analytics/cache'),
}

// Live queue
export const queueApi = {
  snapshot: () => request<import('../types').QueueSnapshot>('/queue'),
  speeds: () => request<{ speeds: import('../types').BackendSpeed[] }>('/queue/speeds'),
}

export const throughputApi = {
  series: (hours = 24, interval?: string) =>
    request<import('../types').ThroughputSeries>(
      `/analytics/throughput/series?hours=${hours}${interval ? `&interval=${interval}` : ''}`),
}

// Virtual Routers
export const routersApi = {
  list: () => request<Router[]>('/routers'),
  create: (data: Partial<Router>) => request<Router>('/routers', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<Router>) => request<Router>(`/routers/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/routers/${id}`, { method: 'DELETE' }),
  getMembers: (id: number) => request<RouterMember[]>(`/routers/${id}/members`),
  addMember: (id: number, data: { model_map_id: number; priority: number }) =>
    request<RouterMember>(`/routers/${id}/members`, { method: 'POST', body: JSON.stringify(data) }),
  removeMember: (routerId: number, memberId: number) =>
    request<void>(`/routers/${routerId}/members/${memberId}`, { method: 'DELETE' }),
  getStatus: (id: number) => request<MemberStatus[]>(`/routers/${id}/status`),
}

// Users
export const usersApi = {
  list: () => request<UserWithStats[]>('/users'),
  create: (data: Partial<User>) => request<User>('/users', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<User>) => request<User>(`/users/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/users/${id}`, { method: 'DELETE' }),
  getKeys: (id: number) => request<APIKey[]>(`/users/${id}/keys`),
  createKey: (id: number, data: Partial<APIKey>) =>
    request<{ key: APIKey; value: string }>(`/users/${id}/keys`, { method: 'POST', body: JSON.stringify(data) }),
}

// MCP Servers
export const mcpApi = {
  list: () => request<MCPServer[]>('/mcp-servers'),
  get: (id: number) => request<MCPServer>(`/mcp-servers/${id}`),
  create: (data: Partial<MCPServer>) => request<MCPServer>('/mcp-servers', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<MCPServer>) => request<MCPServer>(`/mcp-servers/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/mcp-servers/${id}`, { method: 'DELETE' }),
}

// System Events
export const eventsApi = {
  list: (filters: EventFilters = {}) => {
    const p = new URLSearchParams()
    if (filters.level)  p.set('level',  filters.level)
    if (filters.type)   p.set('type',   filters.type)
    if (filters.search) p.set('search', filters.search)
    if (filters.from)   p.set('from',   filters.from)
    if (filters.to)     p.set('to',     filters.to)
    if (filters.limit)  p.set('limit',  String(filters.limit))
    if (filters.offset) p.set('offset', String(filters.offset))
    return request<{ events: SystemEvent[]; total: number }>(`/events?${p}`)
  },
}

// Config export / import
export const configApi = {
  export: () =>
    fetch(`${API_BASE}/config/export`, { credentials: 'include' }),
  import: (data: unknown) =>
    request<{ imported: Record<string, number> }>('/config/import', {
      method: 'POST',
      body: JSON.stringify(data),
    }),
}

export const brandingApi = {
  get: () => request<BrandingSettings>('/settings/branding'),
  update: (data: BrandingSettings) =>
    request<BrandingSettings>('/settings/branding', {
      method: 'PUT',
      body: JSON.stringify(data),
    }),
}

export const settingsApi = {
  storage: () => request<StorageDiagnostics>('/settings/storage'),
  influx: () => request<import('../types').InfluxSettings>('/settings/influx'),
  updateInflux: (data: Partial<import('../types').InfluxSettings>) =>
    request<import('../types').InfluxSettings>('/settings/influx', {
      method: 'PUT', body: JSON.stringify(data),
    }),
  testInflux: (data: Partial<import('../types').InfluxSettings>) =>
    request<{ ok: boolean; message: string }>('/settings/influx/test', {
      method: 'POST', body: JSON.stringify(data),
    }),
}

// Direct proxy call — returns raw Response so callers can stream or parse
export function proxyChat(
  model: string,
  messages: { role: string; content: string }[],
  stream: boolean,
  signal?: AbortSignal,
  extraParams?: Record<string, unknown>,
): Promise<Response> {
  return fetch('/v1/chat/completions', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model, messages, stream, ...extraParams }),
    signal,
  })
}

// Model Mappings
export const modelMappingsApi = {
  list: () => request<ModelMap[]>('/model-mappings'),
  create: (data: Partial<ModelMap>) => request<ModelMap>('/model-mappings', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<ModelMap>) => request<ModelMap>(`/model-mappings/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: number) => request<void>(`/model-mappings/${id}`, { method: 'DELETE' }),
  healthCheck: (id: number) => request<ModelMap>(`/model-mappings/${id}/health`, { method: 'POST' }),
}
