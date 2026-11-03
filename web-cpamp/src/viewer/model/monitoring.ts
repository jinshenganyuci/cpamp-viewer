import type {
  MonitoringAccountModelSpendRow,
  MonitoringAccountRow,
  MonitoringApiKeyRow,
  MonitoringEventRow,
  MonitoringSummary,
} from "@/features/monitoring/hooks/useMonitoringData";
import { withViewerInstance } from "./instance";
import { maskEmailLike } from "@/features/monitoring/model/base";
import { calculateCost, type ModelPrice } from "@/utils/usage";
import type {
  ViewerAccountStat,
  ViewerAlias,
  ViewerAnalyticsResponse,
  ViewerApiKeyStat,
  ViewerModelStat,
  ViewerMonitoringEvent,
  ViewerInstanceIdentity,
  ViewerQuotaAccount,
} from "@/viewer/model/viewerTypes";

const text = (...values: unknown[]) => {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
};

const number = (value: unknown) => {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
};

const nullableNumber = (value: unknown) => {
  if (value === null || value === undefined || value === "") return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
};

const unique = (values: unknown[]) =>
  Array.from(
    new Set(
      values
        .map((value) => (typeof value === "string" ? value.trim() : ""))
        .filter(Boolean),
    ),
  );

const modelRows = (
  rows: ViewerModelStat[] | undefined,
  fallbackLastSeenAt: number,
): MonitoringAccountModelSpendRow[] =>
  (rows || []).map((row) => ({
    model: text(row.model, "unknown"),
    totalCalls: number(row.calls),
    successCalls: number(row.success_calls),
    failureCalls: number(row.failure_calls),
    successRate: number(row.success_rate),
    inputTokens: number(row.input_tokens),
    outputTokens: number(row.output_tokens),
    cachedTokens: number(row.cached_tokens),
    cacheReadTokens: number(row.cache_read_tokens),
    cacheCreationTokens: number(row.cache_creation_tokens),
    totalTokens: number(row.total_tokens ?? row.tokens),
    totalCost: number(row.cost),
    lastSeenAt: number(row.last_seen_ms) || fallbackLastSeenAt,
  }));

const resolveAccountDisplay = (row: ViewerAccountStat) =>
  text(
    row.account_display,
    row.account_snapshot,
    row.auth_label_display,
    row.auth_label_snapshot,
    row.account_id,
    row.id,
    "未识别账号",
  );

export const buildViewerMonitoringSummary = (
  data: ViewerAnalyticsResponse | null,
): MonitoringSummary => {
  const summary = data?.summary;
  return {
    totalCalls: number(summary?.total_calls),
    successCalls: number(summary?.success_calls),
    failureCalls: number(summary?.failure_calls),
    successRate:
      summary?.success_rate === null ? null : number(summary?.success_rate),
    inputTokens: number(summary?.input_tokens),
    outputTokens: number(summary?.output_tokens),
    reasoningTokens: number(summary?.reasoning_tokens),
    cachedTokens: number(summary?.cached_tokens),
    cacheReadTokens: number(summary?.cache_read_tokens),
    cacheCreationTokens: number(summary?.cache_creation_tokens),
    cacheHitRate:
      summary?.cache_hit_rate === null
        ? null
        : summary?.cache_hit_rate === undefined
          ? undefined
          : number(summary.cache_hit_rate),
    totalTokens: number(summary?.total_tokens),
    totalCost: number(summary?.total_cost),
    averageLatencyMs: nullableNumber(summary?.average_latency_ms),
    rpm30m: number(summary?.rpm_30m),
    tpm30m: number(summary?.tpm_30m),
    avgDailyRequests:
      summary?.avg_daily_requests === null
        ? null
        : number(summary?.avg_daily_requests),
    avgDailyTokens:
      summary?.avg_daily_tokens === null
        ? null
        : number(summary?.avg_daily_tokens),
    approxTasks: number(summary?.approx_tasks),
    approxTaskFailures: number(summary?.approx_task_failures),
    approxTaskSuccessRate:
      summary?.approx_task_success_rate === null
        ? null
        : number(summary?.approx_task_success_rate),
    zeroTokenCalls: number(summary?.zero_token_calls),
    zeroTokenModels: [],
  };
};

export type ViewerMonitoringAccountRow = MonitoringAccountRow &
  ViewerInstanceIdentity & {
    rawAccount?: string;
  };

export type ViewerMonitoringApiKeyRow = MonitoringApiKeyRow & {
  apiKeyOriginalLabel?: string;
};

export const buildViewerMonitoringAccountRows = (
  rows: ViewerAccountStat[] | undefined,
): ViewerMonitoringAccountRow[] =>
  (rows || [])
    .map((row) => {
      const rawAccount = resolveAccountDisplay(row);
      const account = withViewerInstance(rawAccount, row);
      const id = text(row.id, row.account_id, account);
      const lastSeenAt = number(row.last_seen_ms);
      return {
        id,
        account,
        rawAccount,
        instance_id: row.instance_id,
        instance_name: row.instance_name,
        provider: text(row.auth_provider_snapshot).toLocaleLowerCase(),
        filterValue: text(row.account_id, id),
        displayAccount: account,
        accountMasked: withViewerInstance(maskEmailLike(rawAccount), row),
        authLabels: unique([
          row.auth_label_display,
          row.auth_label_snapshot,
          row.auth_provider_snapshot,
        ]),
        authIndices: unique(row.auth_ids || []),
        sourceKeys: unique(row.source_ids || []),
        channels: unique([row.auth_provider_snapshot, ...(row.sources || [])]),
        totalCalls: number(row.calls),
        successCalls: number(row.success_calls),
        failureCalls: number(row.failure_calls),
        successRate: number(row.success_rate),
        inputTokens: number(row.input_tokens),
        outputTokens: number(row.output_tokens),
        cachedTokens: number(row.cached_tokens),
        cacheReadTokens: number(row.cache_read_tokens),
        cacheCreationTokens: number(row.cache_creation_tokens),
        totalTokens: number(row.total_tokens),
        totalCost: number(row.cost),
        averageLatencyMs: nullableNumber(row.average_latency_ms),
        lastSeenAt,
        recentPattern: [],
        models: modelRows(row.models, lastSeenAt),
      } satisfies ViewerMonitoringAccountRow;
    })
    .sort(
      (left, right) =>
        right.lastSeenAt - left.lastSeenAt ||
        right.totalCalls - left.totalCalls ||
        left.account.localeCompare(right.account),
    );

const resolveApiKeyId = (row: ViewerApiKeyStat) => text(row.api_key_id, row.id);

export const buildViewerMonitoringApiKeyRows = (
  rows: ViewerApiKeyStat[] | undefined,
  aliases: ViewerAlias[],
): ViewerMonitoringApiKeyRow[] => {
  const aliasMap = new Map(aliases.map((item) => [item.id, item.alias]));
  return (rows || [])
    .map((row) => {
      const id = resolveApiKeyId(row);
      const selectable =
        row.api_key_selectable !== false && /^view_[a-f0-9]{12}$/i.test(id);
      const label = text(
        row.api_key_selectable === false ? "未识别 Key" : "",
        row.api_key_alias,
        aliasMap.get(id),
        id ? `Key ${id.slice(-6)}` : "未识别 Key",
      );
      const lastSeenAt = number(row.last_seen_ms);
      return {
        id: id || row.id,
        apiKeyHash: selectable ? id : "",
        apiKeyLabel: withViewerInstance(label, row),
        apiKeyMasked: withViewerInstance(label, row),
        apiKeyOriginalLabel: label,
        isUnknown: !selectable,
        authLabels: unique([row.auth_label_display, row.auth_label_snapshot]),
        sourceLabels: unique([
          row.account_display,
          row.account_snapshot,
          ...(row.sources || []),
        ]),
        channels: unique([row.auth_provider_snapshot]),
        totalCalls: number(row.calls),
        successCalls: number(row.success_calls),
        failureCalls: number(row.failure_calls),
        successRate: number(row.success_rate),
        inputTokens: number(row.input_tokens),
        outputTokens: number(row.output_tokens),
        cachedTokens: number(row.cached_tokens),
        cacheReadTokens: number(row.cache_read_tokens),
        cacheCreationTokens: number(row.cache_creation_tokens),
        totalTokens: number(row.total_tokens),
        totalCost: number(row.cost),
        averageLatencyMs: nullableNumber(row.average_latency_ms),
        lastSeenAt,
        models: modelRows(row.models, lastSeenAt),
      } satisfies ViewerMonitoringApiKeyRow;
    })
    .sort(
      (left, right) =>
        right.totalCalls - left.totalCalls ||
        right.lastSeenAt - left.lastSeenAt,
    );
};

export const findViewerApiKeyIdsByAlias = (
  rows: ViewerMonitoringApiKeyRow[],
  query: string,
) => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  if (!normalizedQuery) return [];
  return rows
    .filter(
      (row) =>
        /^view_[a-f0-9]{12}$/i.test(row.id) &&
        [row.apiKeyLabel, row.apiKeyOriginalLabel].some(
          (label) => label?.trim().toLocaleLowerCase() === normalizedQuery,
        ),
    )
    .map((row) => row.id);
};

export const resolveViewerApiKeySearchScope = (
  rows: ViewerMonitoringApiKeyRow[],
  query: string,
  selectedApiKeyId: string,
) => {
  const normalizedQuery = query.trim();
  const exactAliasIds = findViewerApiKeyIdsByAlias(rows, normalizedQuery);
  const selectedId = /^view_[a-f0-9]{12}$/i.test(selectedApiKeyId)
    ? selectedApiKeyId
    : "";

  if (exactAliasIds.length === 0) {
    return {
      apiKeyIds: selectedId ? [selectedId] : [],
      searchQuery: normalizedQuery,
    };
  }
  if (selectedId && !exactAliasIds.includes(selectedId)) {
    // Match the original UI's AND semantics: keep the selected key and retain
    // the unmatched text query so the combination resolves to no rows instead
    // of accidentally dropping both constraints.
    return { apiKeyIds: [selectedId], searchQuery: normalizedQuery };
  }
  return {
    apiKeyIds: selectedId ? [selectedId] : exactAliasIds,
    searchQuery: "",
  };
};

const formatApiKeyLabel = (
  event: ViewerMonitoringEvent,
  aliases: Map<string, string>,
) => {
  const id = text(event.api_key_id);
  if (event.api_key_selectable === false) return "未识别 Key";
  return text(
    event.api_key_alias,
    aliases.get(id),
    id ? `Key ${id.slice(-6)}` : "未识别 Key",
  );
};

export type ViewerMonitoringEventRow = MonitoringEventRow &
  ViewerInstanceIdentity;

export const buildViewerMonitoringEventRows = (
  rows: ViewerMonitoringEvent[] | undefined,
  aliases: ViewerAlias[],
  modelPrices: Record<string, ModelPrice> = {},
  instancePrices: Record<string, Record<string, ModelPrice>> = {},
): ViewerMonitoringEventRow[] => {
  const aliasMap = new Map(aliases.map((item) => [item.id, item.alias]));
  return (rows || [])
    .map((row) => {
      const timestampMs = number(row.timestamp_ms);
      const source = text(
        row.source_display,
        row.auth_label_display,
        row.auth_file_display,
        row.source,
        row.source_id,
        row.auth_id,
        "未识别来源",
      );
      const account = text(
        row.account_display,
        row.account_snapshot,
        row.auth_label_display,
        row.auth_file_display,
        row.auth_label_snapshot,
        row.account_id,
        source,
      );
      const authLabel = text(
        row.auth_label_display,
        row.auth_file_display,
        row.auth_label_snapshot,
        source,
      );
      const apiKeyLabel = withViewerInstance(
        formatApiKeyLabel(row, aliasMap),
        row,
      );
      const latencyMs = nullableNumber(row.latency_ms ?? row.duration_ms);
      const outputTokens = number(row.output_tokens);
      const provider = text(
        row.auth_provider_snapshot,
        row.source_display,
        "-",
      );
      const displaySource =
        source === provider || /^view_[a-f0-9]{12}$/i.test(source)
          ? source
          : withViewerInstance(source, row);
      const maskedSource =
        source === provider || /^view_[a-f0-9]{12}$/i.test(source)
          ? maskEmailLike(source)
          : withViewerInstance(maskEmailLike(source), row);
      const endpointPath = text(row.path, row.endpoint, "-");
      const endpointMethod = text(row.method, "POST");
      const id = text(row.event_hash, `${timestampMs}-${row.model}`);
      const totalTokens = number(row.total_tokens);
      const inputTokens = number(row.input_tokens);
      const failed = Boolean(row.failed);
      const analyticsModel = text(row.analytics_model, row.model, "unknown");
      const requestedModel = text(
        row.requested_model,
        row.model,
        analyticsModel,
      );
      const prices = row.instance_id
        ? instancePrices[row.instance_id]
        : modelPrices;
      const hasInstancePrice = Boolean(
        prices && (prices[analyticsModel] || prices[requestedModel]),
      );
      const totalCost =
        row.cost === undefined && (!row.instance_id || hasInstancePrice)
          ? calculateCost(
              {
                __modelName: analyticsModel,
                __requestedModel: requestedModel,
                __resolvedModel: text(row.resolved_model),
                service_tier: text(row.service_tier),
                request_service_tier: text(row.request_service_tier),
                response_service_tier: text(row.response_service_tier),
                executor_type: text(row.executor_type),
                auth_provider_snapshot: text(row.auth_provider_snapshot),
                tokens: {
                  input_tokens: inputTokens,
                  output_tokens: outputTokens,
                  reasoning_tokens: number(row.reasoning_tokens),
                  cached_tokens: number(row.cached_tokens),
                  cache_read_tokens: number(row.cache_read_tokens),
                  cache_creation_tokens: number(row.cache_creation_tokens),
                  total_tokens: totalTokens,
                },
              },
              prices ?? {},
            )
          : number(row.cost);

      return {
        id,
        instance_id: row.instance_id,
        instance_name: row.instance_name,
        timestamp: new Date(timestampMs).toISOString(),
        timestampMs,
        dayKey: new Date(timestampMs).toLocaleDateString("sv-SE"),
        hourLabel: `${String(new Date(timestampMs).getHours()).padStart(2, "0")}:00`,
        model: analyticsModel,
        requestedModel,
        resolvedModel: text(row.resolved_model) || undefined,
        responseModel: text(row.response_model) || undefined,
        generate: typeof row.generate === "boolean" ? row.generate : undefined,
        stream: typeof row.stream === "boolean" ? row.stream : undefined,
        endpoint: endpointPath,
        endpointMethod,
        endpointPath,
        sourceKey: text(row.source_id, row.auth_id, id),
        source: displaySource,
        sourceIdentity: text(row.source_id),
        sourceHashIdentity: text(row.source_id),
        sourceMasked: maskedSource,
        account: withViewerInstance(account, row),
        accountIdentity: text(row.account_id),
        accountMasked: withViewerInstance(maskEmailLike(account), row),
        authIndex: text(row.account_id, row.auth_id, row.source_id, "-"),
        authIndexIdentity: text(row.auth_id),
        authIndexMasked: text(row.account_id, row.auth_id, row.source_id, "-"),
        authLabel,
        authLabelIdentity: text(row.auth_label_id),
        accountId: text(row.account_subject_id) || undefined,
        projectId: text(row.project_display, row.project_id),
        apiKeyHash:
          row.api_key_selectable === false ? "" : text(row.api_key_id),
        apiKeyLabel,
        apiKeyMasked: apiKeyLabel,
        provider,
        providerIdentity: provider,
        planType: text(row.header_quota_plan_type, "-"),
        channel: text(row.source_display, provider, "-"),
        channelHost: "-",
        channelDisabled: false,
        failed,
        statsIncluded: failed || totalTokens > 0 || inputTokens > 0,
        latencyMs,
        ttftMs: nullableNumber(row.ttft_ms),
        tokensPerSecond:
          latencyMs && latencyMs > 0 && outputTokens > 0
            ? outputTokens / (latencyMs / 1000)
            : null,
        inputTokens,
        outputTokens,
        reasoningTokens: number(row.reasoning_tokens),
        cachedTokens: number(row.cached_tokens),
        cacheReadTokens: number(row.cache_read_tokens),
        cacheCreationTokens: number(row.cache_creation_tokens),
        totalTokens,
        totalCost,
        costKnown:
          row.cost !== undefined
            ? true
            : row.instance_id
              ? hasInstancePrice
              : undefined,
        reasoningEffort: text(row.reasoning_effort) || undefined,
        serviceTier: text(row.service_tier) || undefined,
        requestServiceTier: text(row.request_service_tier) || undefined,
        responseServiceTier: text(row.response_service_tier) || undefined,
        executorType: text(row.executor_type) || undefined,
        failStatusCode: nullableNumber(row.fail_status_code),
        failSummary: text(row.fail_summary) || undefined,
        responseMetadata: undefined,
        headerQuotaRecoverAtMs: nullableNumber(row.header_quota_recover_at_ms),
        headerQuotaUsedPercent: nullableNumber(row.header_quota_used_percent),
        headerQuotaPlanType: text(row.header_quota_plan_type) || undefined,
        headerErrorKind: text(row.header_error_kind) || undefined,
        headerErrorCode: text(row.header_error_code) || undefined,
        headerTraceId: "",
        taskKey: id,
        searchText: [
          analyticsModel,
          requestedModel,
          row.resolved_model,
          row.response_model,
          source,
          account,
          authLabel,
          apiKeyLabel,
          row.instance_name,
          provider,
          endpointMethod,
          endpointPath,
          row.reasoning_effort,
          row.service_tier,
          row.request_service_tier,
          row.response_service_tier,
          row.executor_type,
          row.fail_summary,
          row.header_error_code,
        ]
          .filter(Boolean)
          .join(" ")
          .toLowerCase(),
      } satisfies ViewerMonitoringEventRow;
    })
    .filter((row) => row.timestampMs > 0)
    .sort((left, right) => right.timestampMs - left.timestampMs);
};

export const mergeViewerMonitoringEvents = (
  previous: ViewerMonitoringEvent[],
  next: ViewerMonitoringEvent[],
) => {
  const seen = new Set(previous.map((row) => row.event_hash));
  return [
    ...previous,
    ...next.filter((row) => {
      if (seen.has(row.event_hash)) return false;
      seen.add(row.event_hash);
      return true;
    }),
  ].sort(
    (left, right) => number(right.timestamp_ms) - number(left.timestamp_ms),
  );
};

export const buildViewerApiKeyFilterRows = (
  aliases: ViewerAlias[],
  ...responses: Array<ViewerAnalyticsResponse | null | undefined>
): ViewerMonitoringApiKeyRow[] => {
  const candidates: ViewerApiKeyStat[] = [];
  for (const response of responses) {
    for (const options of [
      response?.filter_options,
      response?.filter_selectors,
    ]) {
      candidates.push(...(options?.api_key_stats ?? []));
      for (const option of options?.api_keys ?? []) {
        candidates.push(
          typeof option === "string"
            ? { id: option }
            : { ...option, api_key_alias: option.alias || option.label },
        );
      }
    }
    candidates.push(...(response?.api_key_stats ?? []));
    for (const event of response?.events?.items ?? []) {
      if (event.api_key_id) candidates.push({ ...event, id: event.api_key_id });
    }
  }
  candidates.push(
    ...aliases.map((alias) => ({ ...alias, api_key_alias: alias.alias })),
  );
  const uniqueRows = new Map<string, ViewerApiKeyStat>();
  for (const candidate of candidates) {
    const id = resolveApiKeyId(candidate);
    if (
      !/^view_[a-f0-9]{12}$/i.test(id) ||
      candidate.api_key_selectable === false
    )
      continue;
    const previous = uniqueRows.get(id);
    uniqueRows.set(
      id,
      previous
        ? {
            ...candidate,
            ...previous,
            instance_id: previous.instance_id || candidate.instance_id,
            instance_name: previous.instance_name || candidate.instance_name,
            api_key_alias: previous.api_key_alias || candidate.api_key_alias,
          }
        : candidate,
    );
  }
  return buildViewerMonitoringApiKeyRows([...uniqueRows.values()], aliases);
};

export const findViewerQuotaAccounts = (
  row: ViewerMonitoringAccountRow,
  accounts: Array<ViewerQuotaAccount & ViewerInstanceIdentity>,
) => {
  const normalize = (value: string | undefined) =>
    value?.trim().toLowerCase() || "";
  const eligible = accounts
    .filter((account) => account.source !== "sub2api")
    .filter((account) =>
      row.instance_id || account.instance_id
        ? row.instance_id === account.instance_id
        : true,
    );
  const provider = normalize(row.provider);
  const identities = new Set(
    [row.rawAccount || row.account, ...row.authLabels].map(normalize),
  );
  const matched = eligible.filter(
    (account) =>
      identities.has(normalize(account.display_name)) &&
      (!provider ||
        !account.provider ||
        normalize(account.provider) === provider),
  );
  if (matched.length) return matched;
  if (!provider) return [];
  const providerMatches = eligible.filter(
    (account) => normalize(account.provider) === provider,
  );
  return providerMatches.length === 1 ? providerMatches : [];
};

export const viewerEventPageFeedback = (response: ViewerAnalyticsResponse) =>
  (response.warnings ?? []).join("；") ||
  (response.partial_failed ? "部分实例加载失败，可重试加载更多。" : "");
