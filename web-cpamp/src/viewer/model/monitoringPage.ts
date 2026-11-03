import type { TFunction } from "i18next";
import {
  resolveAccountDisplayText,
  type AccountDisplayMode,
  type AccountSortKey,
} from "@/features/monitoring/accountOverviewState";
import { formatPercent } from "@/features/monitoring/components/accountOverviewPresentation";
import type { SummaryCardProps } from "@/features/monitoring/components/MonitoringShared";
import type {
  MonitoringAccountRow,
  MonitoringEventRow,
  MonitoringSummary,
  MonitoringTimeRange,
} from "@/features/monitoring/model/types";
import { formatStatusWindowLabel } from "@/features/monitoring/model/statusWindow";
import {
  calculateCacheHitRateFromTotals,
  formatCompactNumber,
  formatDurationMs,
  formatUsd,
} from "@/utils/usage";

export type ViewerMonitoringColumn = {
  key: string;
  label: string;
  fullLabel?: string;
  sortKey?: AccountSortKey;
};

export type ViewerMonitoringOption = {
  value: string;
  label: string;
};

export type ViewerMonitoringPagination<T> = {
  currentPage: number;
  totalPages: number;
  pageItems: T[];
  startItem: number;
  endItem: number;
};

export type ViewerRealtimeLogRow = MonitoringEventRow & {
  requestCount: number;
  successRate: number;
  streamKey: string;
  recentPattern: boolean[];
};

export type ViewerMonitoringInitialQuery = {
  timeRange: MonitoringTimeRange;
  customStartInput: string;
  customEndInput: string;
  searchInput: string;
  selectedAccount: string;
  selectedProvider: string;
  selectedModel: string;
  selectedApiKeyId: string;
  selectedStatus: "all" | "success" | "failed";
  selectedAuthFile: string;
  selectedProjectId: string;
  selectedRequestType: string;
  openRealtime: boolean;
};

const shortLabel = (t: TFunction, shortKey: string, fallbackKey: string) => {
  const fallback = t(fallbackKey);
  const label = t(shortKey, { defaultValue: fallback });
  return label === shortKey ? fallback : label;
};

const padDateUnit = (value: number) => String(value).padStart(2, "0");

export const formatViewerDateTimeLocalValue = (date: Date) =>
  `${date.getFullYear()}-${padDateUnit(date.getMonth() + 1)}-${padDateUnit(date.getDate())}T${padDateUnit(date.getHours())}:${padDateUnit(date.getMinutes())}`;

export const getViewerTodayStartInputValue = () => {
  const date = new Date();
  date.setHours(0, 0, 0, 0);
  return formatViewerDateTimeLocalValue(date);
};

export const getViewerCurrentInputValue = () =>
  formatViewerDateTimeLocalValue(new Date());

export const parseViewerDateTimeLocalValue = (value: string) => {
  if (!value) return null;
  const timestamp = new Date(value).getTime();
  return Number.isFinite(timestamp) ? timestamp : null;
};

const isViewerId = (value: string) => /^view_[a-f0-9]{12}$/i.test(value);

const readViewerIdParam = (params: URLSearchParams, key: string) => {
  const value = params.get(key)?.trim() || "";
  return isViewerId(value) ? value : "";
};

const readTextParam = (params: URLSearchParams, key: string) =>
  (params.get(key)?.trim() || "").slice(0, 180);

export const buildViewerMonitoringInitialQuery = (
  search: string,
): ViewerMonitoringInitialQuery => {
  const params = new URLSearchParams(search);
  const fromMs = Number(params.get("from_ms"));
  const toMs = Number(params.get("to_ms"));
  const hasRange =
    Number.isFinite(fromMs) &&
    Number.isFinite(toMs) &&
    fromMs > 0 &&
    toMs > fromMs;
  const selectedStatusValue = readTextParam(params, "status");
  const selectedStatus =
    selectedStatusValue === "success" || selectedStatusValue === "failed"
      ? selectedStatusValue
      : "all";
  const selectedAccount = readViewerIdParam(params, "account") || "all";
  const selectedApiKeyId = readViewerIdParam(params, "api_key_hash") || "all";
  const selectedAuthFile = readViewerIdParam(params, "auth_file");
  const selectedProjectId = readViewerIdParam(params, "project_id");
  const selectedRequestType = readTextParam(params, "request_type");
  const selectedProvider = readTextParam(params, "provider") || "all";
  const selectedModel = readTextParam(params, "model") || "all";
  const searchInput = readTextParam(params, "search");
  return {
    timeRange: hasRange ? "custom" : "today",
    customStartInput: hasRange
      ? formatViewerDateTimeLocalValue(new Date(fromMs))
      : getViewerTodayStartInputValue(),
    customEndInput: hasRange
      ? formatViewerDateTimeLocalValue(new Date(toMs))
      : getViewerCurrentInputValue(),
    searchInput,
    selectedAccount,
    selectedProvider,
    selectedModel,
    selectedApiKeyId,
    selectedStatus,
    selectedAuthFile,
    selectedProjectId,
    selectedRequestType,
    openRealtime: Boolean(
      hasRange ||
      searchInput ||
      selectedAccount !== "all" ||
      selectedProvider !== "all" ||
      selectedModel !== "all" ||
      selectedApiKeyId !== "all" ||
      selectedStatus !== "all" ||
      selectedAuthFile ||
      selectedProjectId ||
      selectedRequestType,
    ),
  };
};

const formatFullNumber = (value: number, locale?: string) => {
  const number = Number(value);
  if (!Number.isFinite(number)) return "0";
  try {
    return new Intl.NumberFormat(locale || undefined, {
      maximumFractionDigits: 0,
    }).format(number);
  } catch {
    return String(Math.round(number));
  }
};

const ensureSelectedOption = <T extends ViewerMonitoringOption>(
  options: T[],
  value: string,
  label = value,
): T[] => {
  if (
    !value ||
    value === "all" ||
    options.some((option) => option.value === value)
  ) {
    return options;
  }
  return [...options, { value, label } as T];
};

const buildSortedValueOptions = (values: string[]): ViewerMonitoringOption[] =>
  Array.from(new Set(values))
    .filter(Boolean)
    .sort((left, right) => left.localeCompare(right))
    .map((value) => ({ value, label: value }));

export const buildViewerProviderOptions = (
  providers: string[],
  selectedProvider: string,
  t: TFunction,
) =>
  ensureSelectedOption(
    [
      {
        value: "all",
        label: shortLabel(
          t,
          "monitoring.filter_all_providers_short",
          "monitoring.filter_all_providers",
        ),
      },
      ...buildSortedValueOptions(providers),
    ],
    selectedProvider,
  );

export const buildViewerAccountOptions = (
  rows: MonitoringAccountRow[],
  selectedAccount: string,
  t: TFunction,
  accountDisplayMode: AccountDisplayMode = "masked",
) =>
  ensureSelectedOption(
    [
      {
        value: "all",
        label: shortLabel(
          t,
          "monitoring.filter_all_accounts_short",
          "monitoring.filter_all_accounts",
        ),
      },
      ...Array.from(
        new Map(
          rows.map((row) => {
            const display = resolveAccountDisplayText(row, accountDisplayMode);
            const label =
              display.secondary && display.secondary !== display.primary
                ? `${display.primary} / ${display.secondary}`
                : display.primary;
            return [row.id, label] as const;
          }),
        ).entries(),
      )
        .sort((left, right) => left[1].localeCompare(right[1]))
        .map(([value, label]) => ({ value, label })),
    ],
    selectedAccount,
  );

export const buildViewerModelOptions = (
  models: string[],
  selectedModel: string,
  t: TFunction,
) =>
  ensureSelectedOption(
    [
      {
        value: "all",
        label: shortLabel(
          t,
          "monitoring.filter_all_models_short",
          "monitoring.filter_all_models",
        ),
      },
      ...buildSortedValueOptions(models),
    ],
    selectedModel,
  );

export const buildViewerStatusOptions = (
  t: TFunction,
): ViewerMonitoringOption[] => [
  {
    value: "all",
    label: shortLabel(
      t,
      "monitoring.filter_all_statuses_short",
      "monitoring.filter_all_statuses",
    ),
  },
  {
    value: "success",
    label: shortLabel(
      t,
      "monitoring.filter_status_success_short",
      "monitoring.filter_status_success",
    ),
  },
  {
    value: "failed",
    label: shortLabel(
      t,
      "monitoring.filter_status_failed_short",
      "monitoring.filter_status_failed",
    ),
  },
];

export const buildViewerAccountColumns = (
  t: TFunction,
): ViewerMonitoringColumn[] => [
  {
    key: "account",
    label: shortLabel(
      t,
      "monitoring.account_overview_col_account_short",
      "monitoring.account_overview_col_account",
    ),
    fullLabel: t("monitoring.account_overview_col_account"),
  },
  { key: "status", label: t("monitoring.column_status") },
  {
    key: "total-calls",
    label: shortLabel(
      t,
      "monitoring.total_calls_short",
      "monitoring.total_calls",
    ),
    fullLabel: t("monitoring.total_calls"),
    sortKey: "totalCalls",
  },
  {
    key: "success-calls",
    label: shortLabel(
      t,
      "monitoring.success_calls_short",
      "monitoring.success_calls",
    ),
    fullLabel: t("monitoring.success_calls"),
    sortKey: "successCalls",
  },
  {
    key: "failure-calls",
    label: shortLabel(
      t,
      "monitoring.failure_calls_short",
      "monitoring.failure_calls",
    ),
    fullLabel: t("monitoring.failure_calls"),
    sortKey: "failureCalls",
  },
  {
    key: "success-rate",
    label: shortLabel(
      t,
      "monitoring.column_success_rate_short",
      "monitoring.column_success_rate",
    ),
    fullLabel: t("monitoring.column_success_rate"),
    sortKey: "successRate",
  },
  {
    key: "total-tokens",
    label: shortLabel(
      t,
      "monitoring.total_tokens_short",
      "monitoring.total_tokens",
    ),
    fullLabel: t("monitoring.total_tokens"),
    sortKey: "totalTokens",
  },
  {
    key: "estimated-cost",
    label: shortLabel(
      t,
      "monitoring.account_overview_col_cost_short",
      "monitoring.account_overview_col_cost",
    ),
    fullLabel: t("monitoring.account_overview_col_cost"),
    sortKey: "totalCost",
  },
  {
    key: "latest-request-time",
    label: shortLabel(
      t,
      "monitoring.latest_request_time_short",
      "monitoring.latest_request_time",
    ),
    fullLabel: t("monitoring.latest_request_time"),
    sortKey: "lastSeenAt",
  },
  { key: "action", label: t("common.action") },
];

export const buildViewerApiKeyColumns = (
  t: TFunction,
): ViewerMonitoringColumn[] => [
  {
    key: "api-key",
    label: shortLabel(
      t,
      "monitoring.api_key_summary_col_key_short",
      "monitoring.api_key_summary_col_key",
    ),
    fullLabel: t("monitoring.api_key_summary_col_key"),
  },
  ...[
    ["total-calls", "monitoring.total_calls_short", "monitoring.total_calls"],
    [
      "success-calls",
      "monitoring.success_calls_short",
      "monitoring.success_calls",
    ],
    [
      "failure-calls",
      "monitoring.failure_calls_short",
      "monitoring.failure_calls",
    ],
    [
      "total-tokens",
      "monitoring.total_tokens_short",
      "monitoring.total_tokens",
    ],
    [
      "estimated-cost",
      "monitoring.account_overview_col_cost_short",
      "monitoring.account_overview_col_cost",
    ],
    [
      "latest-request-time",
      "monitoring.latest_request_time_short",
      "monitoring.latest_request_time",
    ],
  ].map(([key, shortKey, fullKey]) => ({
    key,
    label: shortLabel(t, shortKey, fullKey),
    fullLabel: t(fullKey),
  })),
];

export const buildViewerAccountSortOptions = (
  columns: ViewerMonitoringColumn[],
  t: TFunction,
): ViewerMonitoringOption[] => {
  const prefix = t("monitoring.account_overview_sort_prefix");
  return columns
    .filter(
      (
        column,
      ): column is ViewerMonitoringColumn & { sortKey: AccountSortKey } =>
        Boolean(column.sortKey),
    )
    .map((column) => ({
      value: column.sortKey,
      label: `${prefix}${column.label}`,
    }));
};

export const buildViewerPrimarySummaryCards = ({
  summary,
  accountCount,
  failedGroupCount,
  hasPrices,
  locale,
  t,
}: {
  summary: MonitoringSummary;
  accountCount: number;
  failedGroupCount: number;
  hasPrices: boolean;
  locale: string;
  t: TFunction;
}): SummaryCardProps[] => [
  {
    label: shortLabel(
      t,
      "monitoring.total_calls_short",
      "monitoring.total_calls",
    ),
    fullLabel: t("monitoring.total_calls"),
    value: formatCompactNumber(summary.totalCalls),
    valueTitle: formatFullNumber(summary.totalCalls, locale),
    meta: `${accountCount} ${t("monitoring.accounts_suffix")}`,
    icon: "calls",
    accent: "blue",
  },
  {
    label: shortLabel(
      t,
      "monitoring.call_success_rate_short",
      "monitoring.call_success_rate",
    ),
    fullLabel: t("monitoring.call_success_rate"),
    value: formatPercent(summary.successRate),
    meta: formatDurationMs(summary.averageLatencyMs, { locale }),
    tone:
      summary.successRate === null
        ? undefined
        : summary.successRate >= 0.95
          ? "good"
          : summary.successRate >= 0.85
            ? "warn"
            : "bad",
    icon: "success",
    accent: "green",
  },
  {
    label: shortLabel(
      t,
      "monitoring.failure_calls_short",
      "monitoring.failure_calls",
    ),
    fullLabel: t("monitoring.failure_calls"),
    value: formatCompactNumber(summary.failureCalls),
    valueTitle: formatFullNumber(summary.failureCalls, locale),
    meta: `${failedGroupCount} ${t("monitoring.groups_suffix")}`,
    tone: summary.failureCalls > 0 ? "bad" : "good",
    icon: "failure",
    accent: "red",
  },
  {
    label: shortLabel(
      t,
      "monitoring.estimated_cost_short",
      "monitoring.estimated_cost",
    ),
    fullLabel: t("monitoring.estimated_cost"),
    value: hasPrices ? formatUsd(summary.totalCost) : "--",
    valueTitle: hasPrices ? formatUsd(summary.totalCost) : undefined,
    meta: hasPrices
      ? t("monitoring.estimated_cost_hint")
      : t("monitoring.estimated_cost_missing"),
    tone: hasPrices ? undefined : "warn",
    icon: "cost",
    accent: "amber",
  },
];

export const buildViewerSecondarySummaryCards = (
  summary: MonitoringSummary,
  locale: string,
  t: TFunction,
): SummaryCardProps[] => {
  const totalCacheTokens =
    summary.cachedTokens +
    summary.cacheCreationTokens +
    summary.cacheReadTokens;
  const fallbackCacheInputTokens =
    Math.max(summary.inputTokens, summary.cachedTokens) +
    summary.cacheReadTokens +
    summary.cacheCreationTokens;
  const cacheHitRate =
    summary.cacheHitRate === undefined
      ? calculateCacheHitRateFromTotals(
          summary.cachedTokens + summary.cacheReadTokens,
          fallbackCacheInputTokens,
        )
      : calculateCacheHitRateFromTotals(summary.cacheHitRate, 1);

  return [
    {
      label: shortLabel(
        t,
        "monitoring.total_tokens_short",
        "monitoring.total_tokens",
      ),
      fullLabel: t("monitoring.total_tokens"),
      value: formatCompactNumber(summary.totalTokens),
      valueTitle: formatFullNumber(summary.totalTokens, locale),
      meta: `${t("monitoring.reasoning_tokens")} ${formatCompactNumber(summary.reasoningTokens)}`,
      variant: "secondary",
      icon: "tokens",
      accent: "indigo",
    },
    {
      label: shortLabel(
        t,
        "monitoring.input_tokens_short",
        "monitoring.input_tokens",
      ),
      fullLabel: t("monitoring.input_tokens"),
      value: formatCompactNumber(summary.inputTokens),
      valueTitle: formatFullNumber(summary.inputTokens, locale),
      meta: `${t("monitoring.of_token_mix")} ${formatPercent(summary.totalTokens > 0 ? summary.inputTokens / summary.totalTokens : 0)}`,
      variant: "secondary",
      icon: "input",
      accent: "cyan",
    },
    {
      label: shortLabel(
        t,
        "monitoring.output_tokens_short",
        "monitoring.output_tokens",
      ),
      fullLabel: t("monitoring.output_tokens"),
      value: formatCompactNumber(summary.outputTokens),
      valueTitle: formatFullNumber(summary.outputTokens, locale),
      meta: `${t("monitoring.of_token_mix")} ${formatPercent(summary.totalTokens > 0 ? summary.outputTokens / summary.totalTokens : 0)}`,
      variant: "secondary",
      icon: "output",
      accent: "violet",
    },
    {
      label: shortLabel(
        t,
        "monitoring.cached_tokens_short",
        "monitoring.cached_tokens",
      ),
      fullLabel: t("monitoring.cached_tokens"),
      value: formatCompactNumber(totalCacheTokens),
      valueTitle: formatFullNumber(totalCacheTokens, locale),
      meta: `${t("monitoring.cache_hit_rate")} ${summary.cacheHitRate === null ? "--" : formatPercent(cacheHitRate)}`,
      variant: "secondary",
      icon: "cache",
      accent: "teal",
    },
  ];
};

export const buildViewerPaginationState = <T>(
  items: readonly T[],
  page: number,
  pageSize: number,
): ViewerMonitoringPagination<T> => {
  const safePageSize = Math.max(1, pageSize);
  const totalPages = Math.max(1, Math.ceil(items.length / safePageSize));
  const currentPage = Math.min(Math.max(1, page), totalPages);
  const startIndex = (currentPage - 1) * safePageSize;
  const endIndex = Math.min(startIndex + safePageSize, items.length);
  return {
    currentPage,
    totalPages,
    pageItems: items.slice(startIndex, endIndex),
    startItem: items.length > 0 ? startIndex + 1 : 0,
    endItem: endIndex,
  };
};

export const buildViewerEventStreamKey = (row: MonitoringEventRow) =>
  [
    row.accountIdentity || row.sourceKey || row.account,
    row.provider,
    row.model,
    row.channel,
  ].join("::");

export const buildViewerRealtimeLogRows = (
  rows: MonitoringEventRow[],
): ViewerRealtimeLogRow[] => {
  const sortedAsc = [...rows].sort(
    (left, right) =>
      left.timestampMs - right.timestampMs || left.id.localeCompare(right.id),
  );
  const metricsByStream = new Map<
    string,
    { total: number; success: number; pattern: boolean[] }
  >();
  const enriched = sortedAsc.map((row) => {
    const streamKey = buildViewerEventStreamKey(row);
    const previous = metricsByStream.get(streamKey) ?? {
      total: 0,
      success: 0,
      pattern: [],
    };
    const nextPattern = [...previous.pattern, !row.failed].slice(-10);
    const next = {
      total: previous.total + (row.statsIncluded ? 1 : 0),
      success: previous.success + (row.statsIncluded && !row.failed ? 1 : 0),
      pattern: nextPattern,
    };
    metricsByStream.set(streamKey, next);
    return {
      ...row,
      streamKey,
      requestCount: next.total,
      successRate: next.total > 0 ? next.success / next.total : 1,
      recentPattern: nextPattern,
    };
  });
  return enriched.sort(
    (left, right) =>
      right.timestampMs - left.timestampMs ||
      right.requestCount - left.requestCount ||
      right.id.localeCompare(left.id),
  );
};

export const formatViewerAccountScopeText = (
  bounds: { startMs: number; endMs: number } | null,
  locale: string,
  t: TFunction,
) => {
  if (!bounds) return t("monitoring.account_overview_scope_current_filters");
  const rangeLabel =
    Number.isFinite(bounds.startMs) && Number.isFinite(bounds.endMs)
      ? formatStatusWindowLabel(bounds.startMs, bounds.endMs, locale)
      : t("monitoring.range_all");
  return t("monitoring.account_overview_scope_range", { range: rangeLabel });
};
