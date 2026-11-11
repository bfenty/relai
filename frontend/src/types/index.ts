export interface Backend {
  id: number
  name: string
  base_url: string
  provider: 'openai-compatible' | 'openai' | 'anthropic'
  origin: 'local' | 'external'
  api_key?: string
  weight: number
  max_concurrent: number
  has_api_key?: boolean
  status: string
  color: string
  last_latency_ms: number
  last_health_check?: string
  created_at: string
  updated_at: string
}

export type ModelMode = 'chat' | 'embed' | 'rerank'

export interface APIKey {
  id: number
  key_prefix: string
  name: string
  owner: string
  user_id?: number
  scopes: string[]
  expires_at?: string
  rate_limit_rpm: number
  rate_limit_remaining: number
  rate_limit_reset_at?: string
  allowed_backends: number[]
  allowed_models: string[]
  is_active: boolean
  priority: number
  last_used_at?: string
  total_cost: number
  total_requests: number
  created_at: string
  updated_at: string
  is_test: boolean
}

export interface User {
  id: number
  name: string
  email: string
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface UserWithStats extends User {
  key_count: number
  total_requests: number
  total_cost: number
}

export interface RoutingRule {
  id: number
  name: string
  strategy: string
  priority: number
  conditions: Record<string, unknown>
  config: Record<string, unknown>
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface RequestLog {
  id: number
  api_key_id?: number
  backend_id?: number
  model: string
  endpoint: string
  request_tokens: number
  response_tokens: number
  thinking_tokens: number
  total_tokens: number
  cost: number
  status_code: number
  latency_ms: number
  error_message: string
  created_at: string
}

export interface DashboardStats {
  total_requests_today: number
  total_requests_week: number
  total_requests_month: number
  total_cost_today: number
  total_cost_week: number
  total_cost_month: number
  total_tokens_today: number
  avg_latency_ms: number
  avg_tokens_per_second_today: number
  error_count_today: number
  backend_stats: BackendStat[]
  top_api_keys: APIKeyStat[]
  top_users: UserStat[]
  top_models: ModelStat[]
  usage_series: UsagePoint[]
  series_interval?: 'hour' | 'day'
}

export interface BackendStat {
  backend_id: number
  backend_name: string
  requests: number
  cost: number
  avg_latency: number
  avg_tokens_per_second: number
}

export interface QueueEntry {
  id: string
  backend_id: number
  backend_name: string
  model: string
  status: 'queued' | 'processing_prompt' | 'thinking' | 'streaming'
  client_key?: string
  elapsed_ms: number
  phase_ms: number
  // Live throughput. prompt_tok_s is estimated (body-size prompt / measured
  // prefill duration); decode_tok_s is measured from stream deltas.
  prompt_tok_s: number
  decode_tok_s: number
  prompt_ms: number
  est_prompt_tokens: number
  generated_tokens: number
  // Real prefill progress from the engine (Halogen prompt_progress events).
  // When prompt_total > 0 the bar is engine-truthful: processed/total tokens
  // actually evaluated, prompt_cached served from KV, prompt_engine_ms of host
  // time. Zero means the backend doesn't report and the estimate applies.
  prompt_total: number
  prompt_processed: number
  prompt_cached: number
  prompt_engine_ms: number
}

export interface QueueSnapshot {
  active: QueueEntry[]
  count: number
}

// Learned per-backend throughput (EWMA over completed requests). Drives the
// prefill progress bar while the backend is still silent.
export interface BackendSpeed {
  backend_id: number
  prompt_tok_s: number
  decode_tok_s: number
  samples: number
}

export interface BackendThroughput {
  backend_id: number
  backend_name: string
  requests: number
  prefill_requests: number
  prefill_tokens: number
  prefill_ms: number
  prefill_tok_s: number
  decode_requests: number
  decode_tokens: number
  decode_ms: number
  decode_tok_s: number
  // Wall-clock aggregate: tokens per second the host actually spent decoding,
  // with concurrent requests sharing the clock. 0 until decode_start_at rows
  // exist (added alongside this field).
  decode_wall_tok_s: number
  // Speculation (Halogen MTP): draft proposals/accepts and their ratio.
  // spec_requests=0 means the backend never reported timings.
  spec_requests: number
  draft_n: number
  draft_n_accepted: number
  accept_rate: number
}

export interface ThroughputPoint {
  time: string
  requests: number
  prefill_requests: number
  prefill_tokens: number
  prefill_ms: number
  prefill_tok_s: number
  decode_requests: number
  decode_tokens: number
  decode_ms: number
  decode_tok_s: number
  spec_requests: number
  draft_n: number
  draft_n_accepted: number
  accept_rate: number
  by_backend?: BackendThroughput[]
}

export interface ThroughputSeries {
  from: string
  to: string
  interval: string
  points: ThroughputPoint[]
}

export interface BrandingSettings {
  app_name: string
  logo_url: string
  primary_color: string
  accent_color: string
}

export interface StorageDiagnostics {
  sqlite_path: string
  sqlite_path_absolute: boolean
  log_backend: 'sqlite' | 'postgres' | string
  request_log_rows: number
  latest_request_log_at?: string
  postgres_logs_enabled: boolean
  warnings: string[]
}

export interface APIKeyStat {
  api_key_id: number
  key_name: string
  requests: number
  cost: number
  tokens: number
}

export interface UserStat {
  user_id: number
  user_name: string
  requests: number
  tokens: number
  cost: number
}

export interface ModelStat {
  model: string
  requests: number
  tokens: number
  cost: number
}

export interface UsagePoint {
  time: string
  requests: number
  tokens: number
  cost: number
  errors: number
}

// ── Analytics ──────────────────────────────────────────────────────────────

export type AnalyticsGroupBy = 'model' | 'api_key' | 'user' | 'backend'
export type AnalyticsInterval = 'hour' | 'day' | 'week'
export type AnalyticsMetric = 'requests' | 'tokens' | 'cost'

export interface AnalyticsCell {
  requests: number
  tokens: number
  cost: number
  errors: number
}

export interface AnalyticsBucket {
  time: string
  groups: Record<string, AnalyticsCell>
}

export interface AnalyticsGroup {
  key: string
  label: string
  requests: number
  tokens: number
  cost: number
  errors: number
}

export interface AnalyticsResult {
  group_by: AnalyticsGroupBy
  interval: AnalyticsInterval
  start: string
  end: string
  buckets: AnalyticsBucket[]
  groups: AnalyticsGroup[]
  summary: AnalyticsCell
}

// Routing behaviour over a window: whether session affinity is holding, how much
// of each prompt the upstreams served from cache rather than reprocessing, and what
// compression saved. Requests logged before routing was recorded carry no cache data
// and are counted separately so the rates stay honest.
export interface RoutingHostStats {
  backend_id: number | null
  backend_name: string
  requests: number
  share: number
  cache_reuse_rate: number
  mean_latency_ms: number
  compression_bytes_saved: number
}

export interface DecisionStat {
  requests: number
  prompt_tokens: number
  cached_tokens: number
  reprocessed_tokens: number
  mean_wait_ms: number
  mean_latency_ms: number
}

export interface RoutingStats {
  from: string
  to: string
  total_requests: number
  decisions: Record<string, number>
  decision_stats?: Record<string, DecisionStat>
  sticky_hit_rate: number
  spill_rate: number
  cache_reuse_rate: number
  measured_requests: number
  unmeasured_requests: number
  prompt_tokens: number
  cached_tokens: number
  reprocessed_tokens: number
  mean_wait_ms: number
  p95_wait_ms: number
  compressed_requests: number
  compression_bytes_saved: number
  hosts: RoutingHostStats[]
}

export type PrefixCause =
  | 'append' | 'system' | 'head' | 'tail' | 'truncated'
  | 'host_change' | 'evicted' | 'new_conversation'

export interface PrefixCauseStat {
  cause: PrefixCause
  requests: number
  share: number
  reprocessed_tokens: number
  estimated_seconds: number
  mean_retained: number
}

export interface PrefixOffender {
  model: string
  api_key_name: string
  cause: PrefixCause
  requests: number
  reprocessed_tokens: number
  estimated_seconds: number
}

export interface PrefixStats {
  from: string
  to: string
  diagnosed_requests: number
  fault_requests: number
  reprocessed_tokens: number
  estimated_seconds: number
  causes: PrefixCauseStat[]
  offenders: PrefixOffender[]
}

export interface PrefixPoint {
  time: string
  diagnosed_requests: number
  fault_requests: number
  append: number
  system: number
  head: number
  tail: number
  truncated: number
  host_change: number
  evicted: number
  new_conversation: number
  reprocessed_tokens: number
  estimated_seconds: number
  fault_rate: number
}

export interface PrefixSeries {
  from: string
  to: string
  interval: string
  points: PrefixPoint[]
}

export interface InfluxSettings {
  enabled: boolean
  url: string
  org: string
  bucket: string
  token?: string
  has_token: boolean
  interval_seconds: number
}

export interface RoutingPoint {
  time: string
  requests: number
  measured_requests: number
  prompt_tokens: number
  cached_tokens: number
  reprocessed_tokens: number
  cache_reuse_rate: number
  cold_requests: number
  deep_requests: number
  cold_rate: number
  compressed_requests: number
  compression_bytes_saved: number
  mean_latency_ms: number
  sticky_hits: number
  spills: number
}

export interface RoutingSeries {
  from: string
  to: string
  interval: string
  points: RoutingPoint[]
}

export interface Router {
  id: number
  name: string
  strategy: string
  model_type: ModelType
  cache_enabled: boolean
  cache_ttl_seconds: number
  queue_timeout_seconds: number
  compression: string
  thinking_sidecar_url: string
  thinking_timeout_ms: number
  thinking_default_level: string
  thinking_confidence_floor: number
  thinking_mode: string
  thinking_param_template: string
  created_at: string
  updated_at: string
}

export interface RouterMember {
  id: number
  router_id: number
  model_map_id: number
  priority: number
}

export interface MemberStatus {
  member_id: number
  model_map_id: number
  local_model: string
  backend_name: string
  health: string
  active_requests: number
  queue_depth: number
}

export interface MCPServer {
  id: number
  name: string
  url: string
  transport: 'http' | 'stdio'
  api_key?: string
  description: string
  is_enabled: boolean
  created_at: string
  updated_at: string
}

export interface SystemEvent {
  id: number
  level: 'info' | 'warn' | 'error'
  event_type: 'auth' | 'proxy' | 'health' | 'config' | 'system'
  message: string
  metadata: Record<string, unknown>
  created_at: string
}

export interface EventFilters {
  level?: string   // comma-separated levels
  type?: string
  search?: string
  from?: string
  to?: string
  limit?: number
  offset?: number
}

export type ModelType = 'chat' | 'embedding' | 'rerank'

export interface ModelMap {
  id: number
  backend_id: number
  remote_model: string
  local_model: string
  base_url?: string
  status: string
  last_latency_ms: number
  last_health_check?: string
  params: string
  compression: string
  input_cost_per_million: number
  output_cost_per_million: number
  model_type: ModelType
  modes: ModelMode[]
}

export interface CacheTelemetry {
  model_map_id: number
  available: boolean
  token_hit_rate: number
  pool_usage_ratio: number
  pool_positions: number
  pool_used: number
  entries: number
  max_entries: number
  entry_bytes: number
  evicted: number
  dropped: number
  superseded: number
  prompt_tokens_saved: number
  hit_rate: number
  engine_queued: number
  engine_wait_s: number
  engine_queue_seen: boolean
  age_seconds: number
  backend_name: string
  model_name: string
}
