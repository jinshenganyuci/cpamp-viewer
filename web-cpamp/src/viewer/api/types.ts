export interface SessionResponse {
  authenticated: boolean;
  public_access?: boolean;
  csrf_token?: string;
  expires_at?: number;
}

export interface MaintenanceResponse {
  databaseMaintenance?: {
    required: boolean;
    performanceDegraded: boolean;
    deferredIndexes: number;
    offlineJobs: number;
  };
}

export interface AliasItem {
  id: string;
  alias: string;
}

export interface AliasResponse {
  items: AliasItem[];
}

export interface QuotaWindow {
  id: string;
  label: string;
  pool?: "codex_main" | "codex_spark" | "codex_review" | "unknown";
  remaining_percent: number | null;
  used_percent: number | null;
  reset_at_ms?: number;
  window_minutes?: number;
  observed_at_ms?: number;
  stale?: boolean;
  window_kind?: string;
  model_scope?: string;
}

export interface QuotaAccount {
  id: string;
  source?: "cpamp" | "sub2api";
  provider: string;
  display_name: string;
  plan?: string;
  status: string;
  status_message?: string;
  disabled: boolean;
  windows: QuotaWindow[];
  updated_at_ms?: number;
  success?: number;
  failed?: number;
}

export interface QuotaResponse {
  generated_at_ms: number;
  accounts: QuotaAccount[];
  source: string;
  warnings?: string[];
}

export interface KeyQuotaItem {
  id: string;
  name: string;
  state: "active" | "unlimited" | "inactive" | "unavailable";
  weekly_limit_usd?: number;
  remaining_usd?: number;
  remaining_percent?: number;
  window_started?: boolean;
  reset_at?: string;
}

export interface KeyQuotaResponse {
  configured: boolean;
  updated_at?: string;
  stale: boolean;
  items: KeyQuotaItem[];
}

export interface AnalyticsSummary {
  total_calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  cache_hit_rate?: number;
  reasoning_tokens: number;
  total_tokens: number;
  total_cost: number;
  average_cost_per_call?: number;
  average_latency_ms: number | null;
  p95_latency_ms?: number | null;
  p95_ttft_ms?: number | null;
  rpm_30m: number;
  tpm_30m: number;
  zero_token_calls?: number;
}

export interface TimelinePoint {
  bucket_ms: number;
  bucket_end_ms?: number;
  label: string;
  calls: number;
  tokens: number;
  success: number;
  failure: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  cache_hit_rate?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  cost?: number;
  average_latency_ms?: number | null;
  p95_latency_ms?: number | null;
  p95_ttft_ms?: number | null;
  success_rate?: number;
  failure_rate?: number;
  calls_share?: number;
  tokens_share?: number;
}

export interface ModelStat {
  model: string;
  calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  cache_hit_rate?: number;
  total_tokens: number;
  cost: number;
  cost_share?: number;
}

export interface AccountStat {
  id: string;
  account_id?: string;
  account_display?: string;
  account_snapshot?: string;
  auth_label_id?: string;
  auth_label_display?: string;
  auth_label_snapshot?: string;
  auth_provider_snapshot?: string;
  account_subject_id?: string;
  auth_ids?: string[];
  source_ids?: string[];
  sources?: string[];
  calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  total_tokens: number;
  cost: number;
  average_latency_ms: number | null;
  last_seen_ms: number;
  models?: ModelStat[];
}

export interface ApiKeyStat extends AccountStat {
  api_key_id: string;
  api_key_alias?: string;
  api_key_selectable?: boolean;
  contexts?: Array<Record<string, unknown>>;
}

export interface MonitoringEvent {
  request_id?: string;
  event_hash: string;
  timestamp_ms: number;
  model: string;
  analytics_model?: string;
  requested_model?: string;
  resolved_model?: string;
  response_model?: string;
  generate?: boolean;
  stream?: boolean;
  endpoint: string;
  method: string;
  path: string;
  source: string;
  source_id?: string;
  source_display?: string;
  source_hash?: string;
  auth_id?: string;
  auth_index?: string;
  api_key_id?: string;
  api_key_alias?: string;
  api_key_selectable?: boolean;
  account_id?: string;
  account_display?: string;
  account_snapshot: string;
  auth_label_id?: string;
  auth_label_display?: string;
  auth_label_snapshot: string;
  auth_provider_snapshot: string;
  account_subject_id?: string;
  auth_file_id?: string;
  auth_file_display?: string;
  auth_file_snapshot?: string;
  project_id?: string;
  project_display?: string;
  auth_project_id_snapshot?: string;
  reasoning_effort?: string;
  service_tier?: string;
  request_service_tier?: string;
  response_service_tier?: string;
  executor_type?: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost?: number;
  latency_ms: number | null;
  ttft_ms?: number | null;
  failed: boolean;
  fail_status_code?: number | null;
  fail_summary?: string;
  header_quota_recover_at_ms?: number | null;
  header_quota_used_percent?: number | null;
  header_quota_plan_type?: string;
  header_error_kind?: string;
  header_error_code?: string;
  header_trace_id?: string;
}

export interface RecentFailure {
  timestamp_ms: number;
  model: string;
  api_key_id?: string;
  api_key_alias?: string;
  api_key_selectable?: boolean;
  source?: string;
  source_hash?: string;
  auth_index?: string;
  account_snapshot?: string;
  auth_label_snapshot?: string;
  auth_provider_snapshot?: string;
  account_subject_id?: string;
  endpoint: string;
  duration_ms: number | null;
  fail_status_code?: number | null;
  fail_summary?: string;
  header_quota_recover_at_ms?: number | null;
  header_quota_used_percent?: number | null;
  header_quota_plan_type?: string;
  header_error_kind?: string;
  header_error_code?: string;
  header_trace_id?: string;
}

export interface FilterOption {
  id: string;
  label: string;
  alias?: string;
}

export interface HeatmapContributor {
  key: string;
  label?: string;
  api_key_id?: string;
  api_key_selectable?: boolean;
  calls: number;
  success: number;
  failure: number;
  tokens: number;
  cost: number;
  failure_rate: number;
  share: number;
}

export interface HeatmapPoint {
  weekday?: number;
  hour?: number;
  calls?: number;
  success?: number;
  failure?: number;
  tokens?: number;
  cost?: number;
  failure_rate?: number;
  model_contributors?: HeatmapContributor[];
  api_key_contributors?: HeatmapContributor[];
  provider_contributors?: HeatmapContributor[];
}

export interface AnalyticsAnomalyPoint {
  bucket_ms?: number;
  bucket_end_ms?: number;
  timestamp_ms?: number;
  label?: string;
  severity?: string;
  metric?: string;
  metric_keys?: string[];
  reason?: string;
  score?: number;
  calls?: number;
  total_tokens?: number;
  cost?: number;
  failure_rate?: number;
  request_change?: number;
  cost_change?: number;
  tokens_per_request_change?: number;
  cache_hit_rate_change?: number;
  failure_rate_change?: number;
  latency_p95_change?: number;
}

export interface FailureSource {
  source?: string;
  source_hash?: string;
  auth_index?: string;
  provider?: string;
  calls?: number;
  failure?: number;
  failures?: number;
  failure_rate?: number;
  last_seen_ms?: number;
  average_latency_ms?: number | null;
}

export interface ChannelStat {
  channel?: string;
  provider?: string;
  auth_index?: string;
  source?: string;
  account_snapshot?: string;
  auth_label_snapshot?: string;
  auth_provider_snapshot?: string;
  calls?: number;
  success?: number;
  failure?: number;
  failures?: number;
  success_rate?: number;
  tokens?: number;
  average_latency_ms?: number | null;
  cost?: number;
}

export interface AnalyticsCoverageRange {
  scope: 'rolling_30m' | 'drilldown_preview' | string;
  from_ms: number;
  to_ms: number;
  raw_event_count?: number;
  raw_deleted_event_count: number;
  min_deleted_timestamp_ms?: number;
  max_deleted_timestamp_ms?: number;
}

export interface AnalyticsCoverage {
  scope: 'time_range' | string;
  mode: 'raw' | 'mixed' | 'aggregate_only' | string;
  raw_complete: boolean;
  core_aggregate_used: boolean;
  raw_event_count?: number;
  raw_deleted_event_count: number;
  min_deleted_timestamp_ms: number;
  max_deleted_timestamp_ms: number;
  comparison_raw_event_count?: number;
  comparison_raw_deleted_event_count?: number;
  comparison_min_deleted_timestamp_ms?: number;
  comparison_max_deleted_timestamp_ms?: number;
  auxiliary_ranges?: AnalyticsCoverageRange[];
  fidelity_limitations: string[];
}

export interface AnalyticsResponse {
  coverage?: AnalyticsCoverage;
  generated_at_ms: number;
  granularity: string;
  summary?: AnalyticsSummary;
  summary_comparison?: Record<string, number>;
  timeline?: TimelinePoint[];
  hourly_distribution?: Array<{ hour: number; calls: number; tokens: number }>;
  model_share?: Array<{
    model: string;
    calls: number;
    tokens: number;
    cost: number;
  }>;
  model_stats?: ModelStat[];
  channel_share?: ChannelStat[];
  failure_sources?: FailureSource[];
  account_stats?: AccountStat[];
  credential_stats?: Array<Record<string, unknown>>;
  credential_timeline?: Array<Record<string, unknown>>;
  api_key_timeline?: Array<Record<string, unknown>>;
  api_key_stats?: ApiKeyStat[];
  recent_failures?: RecentFailure[];
  events?: {
    items: MonitoringEvent[];
    has_more: boolean;
    next_before_ms: number;
    next_cursor?: string;
    total_count?: number;
  };
  drilldown_preview?: {
    items: MonitoringEvent[];
    has_more: boolean;
    next_before_ms: number;
    total_count?: number;
  };
  filter_options?: Record<string, unknown> & {
    models?: string[];
    providers?: string[];
    accounts?: string[];
    channels?: Array<string | FilterOption>;
    api_keys?: Array<string | FilterOption>;
    api_key_ids?: string[];
    account_count?: number;
    api_key_count?: number;
  };
  heatmap?: HeatmapPoint[];
  anomaly_points?: AnalyticsAnomalyPoint[];
  task_buckets?: Array<Record<string, unknown>>;
}

export interface AnalyticsFilters extends Record<string, unknown> {
  api_key_ids?: string[];
  credential_ids?: string[];
}

export interface ViewerModelPrice {
  prompt: number;
  completion: number;
  cache: number;
  cacheRead?: number;
  cacheCreation?: number;
  promptConfigured?: boolean;
  completionConfigured?: boolean;
  cacheReadConfigured?: boolean;
  cacheCreationConfigured?: boolean;
}

export interface ModelPricesResponse {
  prices?: Record<string, ViewerModelPrice>;
}

export interface AnalyticsRequest {
  from_ms: number;
  to_ms: number;
  now_ms?: number;
  time_zone?: string;
  search_query?: string;
  filters?: AnalyticsFilters;
  include: Record<string, unknown>;
}

export interface DashboardHealthPoint {
  bucket_ms: number;
  calls: number;
  tokens: number;
  success: number;
  failure: number;
  success_rate: number;
  failure_rate?: number;
  tone?: "success" | "warning" | "failure" | "empty" | "future";
  intensity?: number;
  future?: boolean;
}

export interface DashboardResponse {
  generated_at_ms: number;
  window?: {
    today_start_ms?: number;
    now_ms?: number;
    rolling_30m_start_ms?: number;
  };
  connection?: {
    connected?: boolean;
  };
  system?: {
    management_version?: string;
    server_version?: string;
    build_time?: string | number;
  };
  health?: {
    usage_monitor?: string;
    request_log?: string;
    data_source?: string;
    error_logs?: string | number;
  };
  today?: AnalyticsSummary;
  rolling_30m?: {
    rpm: number;
    tpm: number;
    total_calls: number;
    total_tokens: number;
  };
  traffic_timeline?: TimelinePoint[];
  today_request_health_timeline?: {
    from_ms?: number;
    to_ms?: number;
    bucket_ms?: number;
    success_rate?: number;
    success_calls?: number;
    failure_calls?: number;
    total_calls?: number;
    points?: DashboardHealthPoint[];
  };
  token_mix?: Array<{ key: string; tokens: number; share: number }>;
  top_models_today?: ModelStat[];
  model_cost_rank?: ModelStat[];
  channel_health?: ChannelStat[];
  failure_sources?: FailureSource[];
  recent_failures?: RecentFailure[];
}
