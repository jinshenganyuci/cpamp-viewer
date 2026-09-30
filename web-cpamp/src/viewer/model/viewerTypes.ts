import type { AnalyticsCoverage } from "@/viewer/api/types";

export type ViewerAlias = {
  id: string;
  alias: string;
};

export type ViewerAliasResponse = {
  items?: ViewerAlias[];
};

export type ViewerMaintenanceResponse = {
  databaseMaintenance?: {
    required: boolean;
    performanceDegraded: boolean;
    deferredIndexes: number;
    offlineJobs: number;
  };
};

export type ViewerDashboardMetrics = {
  total_calls?: number;
  success_calls?: number;
  failure_calls?: number;
  success_rate?: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  total_cost?: number;
  average_latency_ms?: number | null;
  p95_latency_ms?: number | null;
  zero_token_calls?: number;
  rpm_30m?: number;
  tpm_30m?: number;
};

export type ViewerTimelinePoint = {
  bucket_ms: number;
  bucket_end_ms?: number;
  label?: string;
  calls?: number;
  tokens?: number;
  success?: number;
  failure?: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
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
};

export type ViewerModelStat = {
  model: string;
  calls?: number;
  success_calls?: number;
  failure_calls?: number;
  success_rate?: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  tokens?: number;
  cost?: number;
  cost_share?: number;
  average_latency_ms?: number | null;
  last_seen_ms?: number;
};

export type ViewerAccountStat = {
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
  calls?: number;
  success_calls?: number;
  failure_calls?: number;
  success_rate?: number;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  total_tokens?: number;
  cost?: number;
  average_latency_ms?: number | null;
  last_seen_ms?: number;
  models?: ViewerModelStat[];
};

export type ViewerApiKeyStat = ViewerAccountStat & {
  api_key_id?: string;
  api_key_alias?: string;
  api_key_selectable?: boolean;
};

export type ViewerFailure = {
  timestamp_ms: number;
  model?: string;
  api_key_id?: string;
  api_key_alias?: string;
  api_key_selectable?: boolean;
  source_id?: string;
  source_display?: string;
  auth_id?: string;
  account_snapshot?: string;
  account_id?: string;
  account_display?: string;
  auth_label_snapshot?: string;
  auth_label_id?: string;
  auth_label_display?: string;
  auth_provider_snapshot?: string;
  account_subject_id?: string;
  project_id?: string;
  project_display?: string;
  endpoint?: string;
  duration_ms?: number | null;
  latency_ms?: number | null;
  fail_status_code?: number | null;
  fail_summary?: string;
  header_error_kind?: string;
  header_error_code?: string;
  header_quota_plan_type?: string;
  header_quota_used_percent?: number | null;
};

export type ViewerMonitoringEvent = ViewerFailure & {
  event_hash: string;
  method?: string;
  path?: string;
  source?: string;
  source_id?: string;
  source_display?: string;
  auth_id?: string;
  account_id?: string;
  account_display?: string;
  auth_label_id?: string;
  auth_label_display?: string;
  auth_file_id?: string;
  auth_file_display?: string;
  project_id?: string;
  project_display?: string;
  input_tokens?: number;
  output_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  cost?: number;
  failed?: boolean;
  resolved_model?: string;
  response_model?: string;
  generate?: boolean;
  stream?: boolean;
  analytics_model?: string;
  requested_model?: string;
  reasoning_effort?: string;
  service_tier?: string;
  request_service_tier?: string;
  response_service_tier?: string;
  executor_type?: string;
  ttft_ms?: number | null;
  header_quota_plan_type?: string;
  header_quota_used_percent?: number | null;
  header_quota_recover_at_ms?: number | null;
};

export type ViewerHeatmapPoint = {
  weekday?: number;
  hour?: number;
  calls?: number;
  success?: number;
  failure?: number;
  tokens?: number;
  cost?: number;
  failure_rate?: number;
};

export type ViewerAnomalyPoint = {
  bucket_ms?: number;
  bucket_end_ms?: number;
  timestamp_ms?: number;
  label?: string;
  severity?: string;
  metric?: string;
  reason?: string;
  score?: number;
  calls?: number;
  total_tokens?: number;
  cost?: number;
  failure_rate?: number;
};

export type ViewerChannelStat = {
  id?: string;
  channel?: string;
  source?: string;
  source_id?: string;
  source_display?: string;
  provider?: string;
  account_id?: string;
  account_display?: string;
  account_snapshot?: string;
  auth_label_id?: string;
  auth_label_display?: string;
  auth_label_snapshot?: string;
  auth_provider_snapshot?: string;
  calls?: number;
  success?: number;
  failure?: number;
  failures?: number;
  success_rate?: number;
  failure_rate?: number;
  tokens?: number;
  cost?: number;
  average_latency_ms?: number | null;
  tone?: string;
};

export type ViewerAnalyticsResponse = {
  coverage?: AnalyticsCoverage;
  generated_at_ms?: number;
  granularity?: string;
  summary?: ViewerDashboardMetrics & {
    cache_hit_rate?: number;
    average_cost_per_call?: number;
    p95_ttft_ms?: number | null;
    avg_daily_requests?: number;
    avg_daily_tokens?: number;
    approx_tasks?: number;
    approx_task_failures?: number;
    approx_task_success_rate?: number;
  };
  summary_comparison?: Partial<ViewerDashboardMetrics> & {
    from_ms?: number;
    to_ms?: number;
  };
  timeline?: ViewerTimelinePoint[];
  hourly_distribution?: Array<{
    hour?: number;
    calls?: number;
    tokens?: number;
  }>;
  model_share?: ViewerModelStat[];
  channel_share?: ViewerChannelStat[];
  model_stats?: ViewerModelStat[];
  failure_sources?: ViewerChannelStat[];
  account_stats?: ViewerAccountStat[];
  credential_stats?: ViewerAccountStat[];
  credential_timeline?: Array<Record<string, unknown>>;
  api_key_timeline?: Array<Record<string, unknown>>;
  api_key_stats?: ViewerApiKeyStat[];
  recent_failures?: ViewerFailure[];
  heatmap?: ViewerHeatmapPoint[];
  anomaly_points?: ViewerAnomalyPoint[];
  events?: {
    items?: ViewerMonitoringEvent[];
    has_more?: boolean;
    next_before_ms?: number;
    next_cursor?: string;
    total_count?: number;
  };
  filter_options?: {
    models?: string[];
    providers?: string[];
    account_stats?: ViewerAccountStat[];
    api_key_stats?: ViewerApiKeyStat[];
    accounts?: string[];
    account_count?: number;
    api_key_count?: number;
    account_ids?: string[];
    auth_file_ids?: string[];
    auth_file_options?: Array<{ id: string; display?: string; label?: string }>;
    project_ids?: string[];
    project_options?: Array<{ id: string; display?: string; label?: string }>;
    api_keys?: Array<string | { id: string; label?: string; alias?: string }>;
  };
};

export type ViewerAnalyticsRequest = {
  from_ms: number;
  to_ms: number;
  now_ms?: number;
  time_zone?: string;
  search_query?: string;
  filters?: Record<string, unknown>;
  include: Record<string, unknown>;
};

export type ViewerModelPrice = {
  prompt: number;
  completion: number;
  cache: number;
  cacheRead?: number;
  cacheCreation?: number;
  promptConfigured?: boolean;
  completionConfigured?: boolean;
  cacheReadConfigured?: boolean;
  cacheCreationConfigured?: boolean;
};

export type ViewerModelPricesResponse = {
  prices?: Record<string, ViewerModelPrice>;
};

export type ViewerDashboardResponse = {
  generated_at_ms?: number;
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
  today?: ViewerDashboardMetrics;
  rolling_30m?: {
    rpm?: number;
    tpm?: number;
    total_calls?: number;
    total_tokens?: number;
  };
  traffic_timeline?: ViewerTimelinePoint[];
  today_request_health_timeline?: {
    from_ms?: number;
    to_ms?: number;
    bucket_ms?: number;
    success_calls?: number;
    failure_calls?: number;
    total_calls?: number;
    success_rate?: number;
    points?: Array<
      ViewerTimelinePoint & {
        tone?: string;
        intensity?: number;
        future?: boolean;
      }
    >;
  };
  token_mix?: Array<{ key: string; tokens?: number; share?: number }>;
  top_models_today?: ViewerModelStat[];
  model_cost_rank?: ViewerModelStat[];
  channel_health?: ViewerChannelStat[];
  failure_sources?: ViewerChannelStat[];
  recent_failures?: ViewerFailure[];
};

export type ViewerQuotaWindow = {
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
};

export type ViewerQuotaAccount = {
  id: string;
  source?: "cpamp" | "sub2api";
  provider: string;
  display_name: string;
  plan?: string;
  status: string;
  status_message?: string;
  disabled?: boolean;
  windows?: ViewerQuotaWindow[];
  updated_at_ms?: number;
  success?: number;
  failed?: number;
};

export type ViewerQuotaResponse = {
  generated_at_ms?: number;
  accounts?: ViewerQuotaAccount[];
  source?: string;
  warnings?: string[];
};
