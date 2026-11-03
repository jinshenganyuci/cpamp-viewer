import {
  useCallback,
  useDeferredValue,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";
import type { SelectOption } from "@/components/ui/Select";
import { IconInbox } from "@/components/ui/icons";
import { UsageCoverageWarning } from "@/components/usage/UsageCoverageWarning";
import { buildViewerMonitoringQuotaEntry } from "@/viewer/model/monitoringQuota";
import { useDatabaseMaintenance } from "@/components/common/useDatabaseMaintenance";
import {
  ACCOUNT_OVERVIEW_CARD_PAGE_SIZE_OPTIONS,
  ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS,
  DEFAULT_ACCOUNT_SORT,
  buildEmptyMonitoringStatusData,
  buildMonitoringAccountStatusDataMap,
  sortAccountRows,
  type AccountDisplayMode,
  type AccountSortKey,
  type AccountSortState,
  type MonitoringAccountAuthState,
  type MonitoringAccountOverviewMode,
} from "@/features/monitoring/accountOverviewState";
import { MonitoringActionBar } from "@/features/monitoring/components/MonitoringActionBar";
import {
  AccountOverviewPanel,
  AccountOverviewPanelActions,
} from "@/features/monitoring/components/AccountOverviewPanel";
import {
  ApiKeySummaryPanel,
  ApiKeySummaryPanelActions,
} from "@/features/monitoring/components/ApiKeySummaryPanel";
import { MonitoringCustomRangeModal } from "@/features/monitoring/components/MonitoringCustomRangeModal";
import { MonitoringDataPanel } from "@/features/monitoring/components/MonitoringDataPanel";
import { MonitoringDatabaseMaintenanceHint } from "@/features/monitoring/components/MonitoringDatabaseMaintenanceHint";
import { MonitoringFiltersPanel } from "@/features/monitoring/components/MonitoringFiltersPanel";
import { MonitoringStatusHeader } from "@/features/monitoring/components/MonitoringStatusHeader";
import { MonitoringStatusSummary } from "@/features/monitoring/components/MonitoringStatusHeader";
import { MonitoringSummarySection } from "@/features/monitoring/components/MonitoringSummarySection";
import type { MonitoringTab } from "@/features/monitoring/components/MonitoringTabsBar";
import {
  RealtimeEventsPanel,
  RealtimeEventsPanelActions,
} from "@/features/monitoring/components/RealtimeEventsPanel";
import type { AccountQuotaState } from "@/features/monitoring/components/accountOverviewPresentation";
import { DEFAULT_MONITORING_REALTIME_PAGE_SIZE } from "@/features/monitoring/monitoringCenterUiState";
import type {
  MonitoringAccountRow,
  MonitoringStatusTone,
  MonitoringTimeRange,
} from "@/features/monitoring/hooks/useMonitoringData";
import styles from "@/features/monitoring/MonitoringCenterPage.module.scss";
import { downloadBlob } from "@/utils/download";
import {
  useViewerAliases,
  useViewerAnalytics,
  useViewerModelPrices,
  useViewerQuota,
  useViewerRefresh,
} from "@/viewer/hooks/useViewerData";
import {
  buildAnalyticsRequest,
  getRangeBounds,
  type CustomRange,
  type ViewerTimeRange,
} from "@/viewer/model/analytics";
import {
  buildViewerMonitoringAccountRows,
  buildViewerApiKeyFilterRows,
  findViewerQuotaAccounts,
  buildViewerMonitoringApiKeyRows,
  buildViewerMonitoringEventRows,
  buildViewerMonitoringSummary,
  mergeViewerMonitoringEvents,
  viewerEventPageFeedback,
  resolveViewerApiKeySearchScope,
} from "@/viewer/model/monitoring";
import {
  buildViewerAccountColumns,
  buildViewerAccountOptions,
  buildViewerAccountSortOptions,
  buildViewerApiKeyColumns,
  buildViewerModelOptions,
  buildViewerMonitoringInitialQuery,
  buildViewerPaginationState,
  buildViewerPrimarySummaryCards,
  buildViewerProviderOptions,
  buildViewerRealtimeLogRows,
  buildViewerEventStreamKey,
  buildViewerSecondarySummaryCards,
  buildViewerStatusOptions,
  formatViewerAccountScopeText,
  getViewerCurrentInputValue,
  getViewerTodayStartInputValue,
  parseViewerDateTimeLocalValue,
} from "@/viewer/model/monitoringPage";
import type {
  ViewerAnalyticsResponse,
  ViewerMonitoringEvent,
} from "@/viewer/model/viewerTypes";
import {
  viewerSourceWarnings,
  withViewerInstance,
} from "@/viewer/model/instance";
import { viewerApi } from "@/viewer/viewerApi";

type MonitoringDataTab = "accounts" | "apiKeys" | "realtime";
type StatusFilter = "all" | "success" | "failed";

type LoadedEventPages = {
  scopeKey: string;
  baseSignature: string;
  items: ViewerMonitoringEvent[];
  hasMore: boolean;
  nextCursor: string;
  totalCount: number;
};

const MONITORING_EVENTS_PAGE_LIMIT = 500;
const MONITORING_EVENTS_RETENTION_LIMIT = 2_000;
const DATABASE_MAINTENANCE_LONG_RANGE_MS = 7 * 24 * 60 * 60 * 1000;

const shortLabel = (t: TFunction, shortKey: string, fallbackKey: string) => {
  const fallback = t(fallbackKey);
  const label = t(shortKey, { defaultValue: fallback });
  return label === shortKey ? fallback : label;
};

const toViewerRange = (range: MonitoringTimeRange): ViewerTimeRange => {
  if (range === "all") return "1y";
  return range;
};

const uniqueValues = (values: Array<string | null | undefined>) =>
  Array.from(
    new Set(values.map((value) => String(value || "").trim()).filter(Boolean)),
  );

const isViewerId = (value: string) => /^view_[a-f0-9]{12}$/i.test(value);

const buildSafeOptionList = (
  allLabel: string,
  items: Iterable<readonly [string, string]>,
  selectedValue: string,
): SelectOption[] => {
  const optionMap = new Map<string, string>();
  for (const [value, label] of items) {
    const safeValue = value.trim();
    if (!safeValue || !isViewerId(safeValue) || optionMap.has(safeValue))
      continue;
    optionMap.set(safeValue, label.trim() || safeValue);
  }
  if (
    selectedValue !== "all" &&
    isViewerId(selectedValue) &&
    !optionMap.has(selectedValue)
  ) {
    optionMap.set(selectedValue, selectedValue);
  }
  return [
    { value: "all", label: allLabel },
    ...Array.from(optionMap.entries())
      .sort((left, right) => left[1].localeCompare(right[1]))
      .map(([value, label]) => ({ value, label })),
  ];
};

export function ViewerMonitoringPage() {
  const { t, i18n } = useTranslation();
  const { status: managerStatus } = useDatabaseMaintenance();
  const location = useLocation();
  const [initialQuery] = useState(() =>
    buildViewerMonitoringInitialQuery(location.search),
  );
  const [initialNowMs] = useState(() => Date.now());
  const [timeRange, setTimeRange] = useState<MonitoringTimeRange>(
    initialQuery.timeRange,
  );
  const [customStartInput, setCustomStartInput] = useState(
    initialQuery.customStartInput,
  );
  const [customEndInput, setCustomEndInput] = useState(
    initialQuery.customEndInput,
  );
  const [customDraftStartInput, setCustomDraftStartInput] = useState(
    initialQuery.customStartInput,
  );
  const [customDraftEndInput, setCustomDraftEndInput] = useState(
    initialQuery.customEndInput,
  );
  const [isCustomRangeModalOpen, setIsCustomRangeModalOpen] = useState(false);
  const [searchInput, setSearchInput] = useState(initialQuery.searchInput);
  const deferredSearch = useDeferredValue(searchInput);
  const [autoRefreshMs, setAutoRefreshMs] = useState("30000");
  const [selectedAccount, setSelectedAccount] = useState(
    initialQuery.selectedAccount,
  );
  const [selectedProvider, setSelectedProvider] = useState(
    initialQuery.selectedProvider,
  );
  const [selectedModel, setSelectedModel] = useState(
    initialQuery.selectedModel,
  );
  const [selectedChannel, setSelectedChannel] = useState("all");
  const [selectedApiKeyId, setSelectedApiKeyId] = useState(
    initialQuery.selectedApiKeyId,
  );
  const [selectedStatus, setSelectedStatus] = useState<StatusFilter>(
    initialQuery.selectedStatus,
  );
  const [selectedAuthFile, setSelectedAuthFile] = useState(
    initialQuery.selectedAuthFile,
  );
  const [selectedProjectId, setSelectedProjectId] = useState(
    initialQuery.selectedProjectId,
  );
  const [selectedRequestType, setSelectedRequestType] = useState(
    initialQuery.selectedRequestType,
  );
  const [activeDataTab, setActiveDataTab] = useState<MonitoringDataTab>(
    initialQuery.openRealtime ? "realtime" : "accounts",
  );
  const [accountOverviewMode, setAccountOverviewMode] =
    useState<MonitoringAccountOverviewMode>("table");
  const [accountDisplayMode, setAccountDisplayMode] =
    useState<AccountDisplayMode>("masked");
  const [accountSort, setAccountSort] =
    useState<AccountSortState>(DEFAULT_ACCOUNT_SORT);
  const [accountPage, setAccountPage] = useState(1);
  const [accountPageSize, setAccountPageSize] = useState<number>(
    ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS[0],
  );
  const [apiKeyPage, setApiKeyPage] = useState(1);
  const [apiKeyPageSize, setApiKeyPageSize] = useState<number>(
    ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS[0],
  );
  const [realtimePage, setRealtimePage] = useState(1);
  const [realtimePageSize, setRealtimePageSize] = useState(
    DEFAULT_MONITORING_REALTIME_PAGE_SIZE,
  );
  const [expandedAccounts, setExpandedAccounts] = useState<
    Record<string, boolean>
  >({});
  const [expandedApiKeys, setExpandedApiKeys] = useState<
    Record<string, boolean>
  >({});
  const [focusedAccountId, setFocusedAccountId] = useState<string | null>(null);
  const [loadedEventPages, setLoadedEventPages] =
    useState<LoadedEventPages | null>(null);
  const [eventsLoadingMore, setEventsLoadingMore] = useState(false);
  const [eventsLoadError, setEventsLoadError] = useState<{
    scopeKey: string;
    message: string;
  } | null>(null);
  const usageImportInputRef = useRef<HTMLInputElement | null>(null);
  const [usageExporting, setUsageExporting] = useState(false);

  const {
    aliases,
    warnings: aliasWarnings,
    refresh: refreshAliases,
  } = useViewerAliases();
  const {
    prices: modelPrices,
    instancePrices,
    error: modelPricesError,
    refresh: refreshModelPrices,
  } = useViewerModelPrices();
  const {
    data: quotaData,
    loading: quotaLoading,
    error: quotaError,
    refresh: refreshQuota,
  } = useViewerQuota({ registerGlobalRefresh: false });

  const customStartMs = useMemo(
    () => parseViewerDateTimeLocalValue(customStartInput),
    [customStartInput],
  );
  const customEndMs = useMemo(
    () => parseViewerDateTimeLocalValue(customEndInput),
    [customEndInput],
  );
  const customDraftStartMs = useMemo(
    () => parseViewerDateTimeLocalValue(customDraftStartInput),
    [customDraftStartInput],
  );
  const customDraftEndMs = useMemo(
    () => parseViewerDateTimeLocalValue(customDraftEndInput),
    [customDraftEndInput],
  );
  const customDraftTimeRangeError = useMemo(() => {
    if (customDraftStartMs === null || customDraftEndMs === null) {
      return t("monitoring.custom_range_required");
    }
    if (customDraftStartMs > customDraftEndMs) {
      return t("monitoring.custom_range_invalid");
    }
    return "";
  }, [customDraftEndMs, customDraftStartMs, t]);
  const customRange = useMemo<CustomRange | null>(() => {
    if (
      timeRange !== "custom" ||
      customStartMs === null ||
      customEndMs === null ||
      customEndMs <= customStartMs
    ) {
      return null;
    }
    return { fromMs: customStartMs, toMs: customEndMs };
  }, [customEndMs, customStartMs, timeRange]);
  const monitoringMaintenanceWarning =
    managerStatus?.databaseMaintenance?.performanceDegraded === true;
  const monitoringMaintenanceLongRange =
    timeRange === "7d" ||
    timeRange === "14d" ||
    timeRange === "30d" ||
    timeRange === "all" ||
    (timeRange === "custom" &&
      customRange !== null &&
      customRange.toMs - customRange.fromMs >=
        DATABASE_MAINTENANCE_LONG_RANGE_MS);

  const analyticsRange = toViewerRange(timeRange);
  const filterSelectorsRequestFactory = useCallback(
    () =>
      buildAnalyticsRequest({
        range: analyticsRange,
        customRange,
        granularity: "auto",
        include: {
          filter_options: true,
          filter_selectors: true,
        },
      }),
    [analyticsRange, customRange],
  );
  const filterSelectorsAnalytics = useViewerAnalytics(
    filterSelectorsRequestFactory,
    {
      autoRefreshMs: Number(autoRefreshMs),
      registerGlobalRefresh: false,
      requestKey: JSON.stringify({ analyticsRange, customRange }),
    },
  );
  const filterApiKeyRows = useMemo(
    () => buildViewerApiKeyFilterRows(aliases, filterSelectorsAnalytics.data),
    [aliases, filterSelectorsAnalytics.data],
  );
  const filterAccountRows = useMemo(
    () =>
      buildViewerMonitoringAccountRows(
        filterSelectorsAnalytics.data?.filter_options?.account_stats ??
          filterSelectorsAnalytics.data?.filter_selectors?.account_stats,
      ),
    [filterSelectorsAnalytics.data],
  );
  const selectedAccountRow = useMemo(
    () =>
      filterAccountRows.find((row) => row.id === selectedAccount) ??
      filterAccountRows.find((row) => row.filterValue === selectedAccount),
    [filterAccountRows, selectedAccount],
  );

  const apiKeySearchScope = useMemo(
    () =>
      resolveViewerApiKeySearchScope(
        filterApiKeyRows,
        deferredSearch,
        selectedApiKeyId,
      ),
    [deferredSearch, filterApiKeyRows, selectedApiKeyId],
  );

  const analyticsFilters = useMemo(() => {
    const filters: Record<string, unknown> = { include_failed: true };
    if (selectedAccount !== "all") {
      if (selectedAccountRow?.authIndices.length) {
        filters.auth_indices = selectedAccountRow.authIndices;
      } else {
        filters.accounts = [selectedAccountRow?.filterValue || selectedAccount];
      }
    }
    if (selectedProvider !== "all") {
      filters.providers = [selectedProvider];
    } else if (selectedAccountRow?.provider) {
      filters.providers = [selectedAccountRow.provider];
    }
    if (selectedModel !== "all") filters.models = [selectedModel];
    if (selectedChannel !== "all") filters.source_hashes = [selectedChannel];
    if (selectedAuthFile) filters.auth_files = [selectedAuthFile];
    if (selectedProjectId) filters.project_ids = [selectedProjectId];
    if (selectedRequestType) filters.request_types = [selectedRequestType];

    if (apiKeySearchScope.apiKeyIds.length > 0) {
      filters.api_key_ids = apiKeySearchScope.apiKeyIds;
    }

    if (selectedStatus === "success") {
      filters.include_failed = false;
    } else if (selectedStatus === "failed") {
      filters.failed_only = true;
    }
    return filters;
  }, [
    apiKeySearchScope.apiKeyIds,
    selectedAccount,
    selectedAccountRow,
    selectedAuthFile,
    selectedChannel,
    selectedModel,
    selectedProvider,
    selectedProjectId,
    selectedRequestType,
    selectedStatus,
  ]);

  const requestSearchQuery = apiKeySearchScope.searchQuery;
  const requestKey = useMemo(
    () =>
      JSON.stringify({
        analyticsFilters,
        analyticsRange,
        customRange,
        requestSearchQuery,
      }),
    [analyticsFilters, analyticsRange, customRange, requestSearchQuery],
  );
  const statisticsRequestFactory = useCallback(
    () =>
      buildAnalyticsRequest({
        range: analyticsRange,
        customRange,
        granularity: "auto",
        searchQuery: requestSearchQuery,
        filters: analyticsFilters,
        include: {
          summary: true,
          summary_profile: "compact",
          timeline: true,
          hourly_distribution: true,
          model_share: true,
          channel_share: true,
          model_stats: true,
          failure_sources: true,
          account_stats: true,
          api_key_stats: true,
          filter_options: true,
          filter_selectors: true,
          recent_failures: 8,
        },
      }),
    [analyticsFilters, analyticsRange, customRange, requestSearchQuery],
  );
  const realtimeRequestFactory = useCallback(
    () =>
      buildAnalyticsRequest({
        range: analyticsRange,
        customRange,
        granularity: "auto",
        searchQuery: requestSearchQuery,
        filters: analyticsFilters,
        include: {
          events_page: { limit: MONITORING_EVENTS_PAGE_LIMIT },
        },
      }),
    [analyticsFilters, analyticsRange, customRange, requestSearchQuery],
  );
  const statisticsAnalytics = useViewerAnalytics(statisticsRequestFactory, {
    autoRefreshMs: Number(autoRefreshMs),
    registerGlobalRefresh: false,
    requestKey: `${requestKey}:statistics`,
  });
  const realtimeAnalytics = useViewerAnalytics(realtimeRequestFactory, {
    autoRefreshMs: Number(autoRefreshMs),
    registerGlobalRefresh: false,
    requestKey: `${requestKey}:realtime`,
  });
  const data = statisticsAnalytics.data;
  const realtimeData = realtimeAnalytics.data;
  const refreshStatistics = statisticsAnalytics.refresh;
  const refreshRealtime = realtimeAnalytics.refresh;
  const refreshFilterSelectors = filterSelectorsAnalytics.refresh;
  const lastRefreshedAt =
    realtimeAnalytics.lastRefreshedAt ?? statisticsAnalytics.lastRefreshedAt;

  const refreshAll = useCallback(async () => {
    await Promise.all([
      refreshStatistics(false),
      refreshRealtime(false),
      refreshFilterSelectors(false),
      refreshAliases(),
      refreshModelPrices(),
      refreshQuota(),
    ]);
  }, [
    refreshRealtime,
    refreshFilterSelectors,
    refreshAliases,
    refreshModelPrices,
    refreshQuota,
    refreshStatistics,
  ]);
  useViewerRefresh(refreshAll);

  const handleViewerExport = useCallback(async () => {
    setUsageExporting(true);
    try {
      const exportedAt = new Date().toISOString();
      downloadBlob({
        filename: `viewer-monitoring-${exportedAt.replace(/[:.]/g, "-")}.json`,
        blob: new Blob(
          [
            JSON.stringify(
              {
                exported_at: exportedAt,
                read_only: true,
                analytics: statisticsAnalytics.data,
                events: realtimeAnalytics.data?.events ?? null,
                aliases,
              },
              null,
              2,
            ),
          ],
          { type: "application/json;charset=utf-8" },
        ),
      });
    } finally {
      setUsageExporting(false);
    }
  }, [aliases, realtimeAnalytics.data?.events, statisticsAnalytics.data]);

  const baseEventItems = useMemo(
    () => realtimeData?.events?.items ?? [],
    [realtimeData?.events?.items],
  );
  const baseEventSignature = useMemo(
    () =>
      [
        realtimeData?.generated_at_ms ?? 0,
        realtimeData?.events?.next_cursor ?? "",
        ...baseEventItems.map((item) => item.event_hash),
      ].join(":"),
    [
      baseEventItems,
      realtimeData?.events?.next_cursor,
      realtimeData?.generated_at_ms,
    ],
  );
  const hasLoadedEventPages =
    loadedEventPages?.scopeKey === requestKey &&
    loadedEventPages.baseSignature === baseEventSignature;
  const activeEventItems = hasLoadedEventPages
    ? loadedEventPages.items
    : baseEventItems;
  const activeEventsHasMore = hasLoadedEventPages
    ? loadedEventPages.hasMore
    : Boolean(realtimeData?.events?.has_more);
  const activeEventsNextCursor = hasLoadedEventPages
    ? loadedEventPages.nextCursor
    : realtimeData?.events?.next_cursor || "";
  const activeEventsTotalCount = hasLoadedEventPages
    ? loadedEventPages.totalCount
    : Number(realtimeData?.events?.total_count ?? baseEventItems.length);
  const activeEventsLoadError =
    eventsLoadError?.scopeKey === requestKey ? eventsLoadError.message : "";

  const loadMoreEvents = useCallback(async () => {
    if (
      eventsLoadingMore ||
      !activeEventsHasMore ||
      !activeEventsNextCursor ||
      activeEventItems.length >= MONITORING_EVENTS_RETENTION_LIMIT
    ) {
      return;
    }
    setEventsLoadingMore(true);
    setEventsLoadError(null);
    try {
      const payload = realtimeRequestFactory();
      payload.include = {
        ...payload.include,
        events_page: {
          limit: MONITORING_EVENTS_PAGE_LIMIT,
          cursor: activeEventsNextCursor,
        },
      };
      const response = (await viewerApi.analytics(
        payload,
      )) as ViewerAnalyticsResponse;
      const warning = viewerEventPageFeedback(response);
      if (warning)
        setEventsLoadError({ scopeKey: requestKey, message: warning });
      const page = response.events;
      const mergedItems = mergeViewerMonitoringEvents(
        activeEventItems,
        page?.items ?? [],
      ).slice(0, MONITORING_EVENTS_RETENTION_LIMIT);
      setLoadedEventPages({
        scopeKey: requestKey,
        baseSignature: baseEventSignature,
        items: mergedItems,
        hasMore:
          Boolean(page?.has_more) &&
          mergedItems.length < MONITORING_EVENTS_RETENTION_LIMIT,
        nextCursor: page?.next_cursor || "",
        totalCount: Number(page?.total_count ?? activeEventsTotalCount),
      });
    } catch (reason) {
      setEventsLoadError({
        scopeKey: requestKey,
        message:
          reason instanceof Error && "status" in reason && reason.status === 409
            ? "分页状态已过期，请刷新请求监控后重新加载。"
            : reason instanceof Error
              ? reason.message
              : String(reason),
      });
    } finally {
      setEventsLoadingMore(false);
    }
  }, [
    activeEventItems,
    activeEventsHasMore,
    activeEventsNextCursor,
    activeEventsTotalCount,
    baseEventSignature,
    eventsLoadingMore,
    requestKey,
    realtimeRequestFactory,
  ]);

  const monitoringSummary = useMemo(
    () => buildViewerMonitoringSummary(data),
    [data],
  );
  const accountRows = useMemo(
    () =>
      buildViewerMonitoringAccountRows(data?.account_stats).map((row) => ({
        ...row,
        planTypes: uniqueValues([
          ...(row.planTypes ?? []),
          ...findViewerQuotaAccounts(row, quotaData?.accounts ?? []).map(
            (account) => account.plan,
          ),
        ]),
      })),
    [data?.account_stats, quotaData?.accounts],
  );
  const apiKeyRows = useMemo(
    () => buildViewerMonitoringApiKeyRows(data?.api_key_stats, aliases),
    [aliases, data?.api_key_stats],
  );
  const eventRows = useMemo(
    () =>
      buildViewerMonitoringEventRows(
        activeEventItems,
        aliases,
        modelPrices,
        instancePrices,
      ),
    [activeEventItems, aliases, modelPrices, instancePrices],
  );
  const realtimeLogRows = useMemo(
    () => buildViewerRealtimeLogRows(eventRows),
    [eventRows],
  );

  const accountOptions = useMemo(
    () =>
      buildViewerAccountOptions(
        filterAccountRows.length > 0 ? filterAccountRows : accountRows,
        selectedAccount,
        t,
        accountDisplayMode,
      ),
    [accountDisplayMode, accountRows, filterAccountRows, selectedAccount, t],
  );
  const providerOptions = useMemo(
    () =>
      buildViewerProviderOptions(
        uniqueValues([
          ...(data?.filter_options?.providers ?? []),
          ...eventRows.map((row) => row.provider),
        ]),
        selectedProvider,
        t,
      ),
    [data?.filter_options?.providers, eventRows, selectedProvider, t],
  );
  const modelOptions = useMemo(
    () =>
      buildViewerModelOptions(
        uniqueValues([
          ...(data?.filter_options?.models ?? []),
          ...eventRows.map((row) => row.model),
        ]),
        selectedModel,
        t,
      ),
    [data?.filter_options?.models, eventRows, selectedModel, t],
  );
  const channelOptions = useMemo(
    () =>
      buildSafeOptionList(
        shortLabel(
          t,
          "monitoring.filter_all_channels_short",
          "monitoring.filter_all_channels",
        ),
        eventRows.map(
          (row) =>
            [
              row.sourceKey,
              withViewerInstance(row.channel || row.source, row),
            ] as const,
        ),
        selectedChannel,
      ),
    [eventRows, selectedChannel, t],
  );
  const apiKeyOptions = useMemo(
    () =>
      buildSafeOptionList(
        shortLabel(
          t,
          "monitoring.filter_all_api_keys_short",
          "monitoring.filter_all_api_keys",
        ),
        [
          ...buildViewerApiKeyFilterRows(
            aliases,
            filterSelectorsAnalytics.data,
            data,
            realtimeData,
          ).map(
            (row) => [row.id, row.apiKeyLabel || row.apiKeyMasked] as const,
          ),
        ],
        selectedApiKeyId,
      ),
    [
      aliases,
      filterSelectorsAnalytics.data,
      data,
      realtimeData,
      selectedApiKeyId,
      t,
    ],
  );
  const statusOptions = useMemo(() => buildViewerStatusOptions(t), [t]);

  const accountOverviewColumns = useMemo(
    () => buildViewerAccountColumns(t),
    [t],
  );
  const apiKeyOverviewColumns = useMemo(() => buildViewerApiKeyColumns(t), [t]);
  const accountSortOptions = useMemo(
    () => buildViewerAccountSortOptions(accountOverviewColumns, t),
    [accountOverviewColumns, t],
  );
  const sortedAccountRows = useMemo(
    () => sortAccountRows(accountRows, accountSort),
    [accountRows, accountSort],
  );
  const accountPagination = useMemo(
    () =>
      buildViewerPaginationState(
        sortedAccountRows,
        accountPage,
        accountPageSize,
      ),
    [accountPage, accountPageSize, sortedAccountRows],
  );
  const apiKeyPagination = useMemo(
    () => buildViewerPaginationState(apiKeyRows, apiKeyPage, apiKeyPageSize),
    [apiKeyPage, apiKeyPageSize, apiKeyRows],
  );
  const realtimePagination = useMemo(
    () =>
      buildViewerPaginationState(
        realtimeLogRows,
        realtimePage,
        realtimePageSize,
      ),
    [realtimeLogRows, realtimePage, realtimePageSize],
  );

  const rangeBounds = useMemo(() => {
    const bounds = getRangeBounds(
      analyticsRange,
      lastRefreshedAt ?? data?.generated_at_ms ?? initialNowMs,
      customRange,
    );
    return { startMs: bounds.fromMs, endMs: bounds.toMs };
  }, [
    analyticsRange,
    customRange,
    data?.generated_at_ms,
    initialNowMs,
    lastRefreshedAt,
  ]);
  const accountStatusDataByRowId = useMemo(
    () => buildMonitoringAccountStatusDataMap(eventRows, rangeBounds),
    [eventRows, rangeBounds],
  );
  const emptyAccountStatusData = useMemo(
    () => buildEmptyMonitoringStatusData(rangeBounds),
    [rangeBounds],
  );
  const accountAuthStateByRowId = useMemo(
    () =>
      new Map<string, MonitoringAccountAuthState>(
        accountRows.map((row) => [
          row.id,
          {
            files: [],
            enabledState: row.lastSeenAt > 0 ? "enabled" : "unavailable",
          },
        ]),
      ),
    [accountRows],
  );
  const accountQuotaStates = useMemo(() => {
    const quotaAccounts = quotaData?.accounts ?? [];
    const states: Record<string, AccountQuotaState> = {};
    accountRows.forEach((row) => {
      const matches = findViewerQuotaAccounts(row, quotaAccounts);
      if (matches.length === 0) return;
      const entries = matches.map((account) =>
        buildViewerMonitoringQuotaEntry(account, i18n.language),
      );
      states[row.id] = {
        status: quotaLoading ? "loading" : quotaError ? "error" : "success",
        targetKey: matches.map((account) => account.id).join("|"),
        entries,
        error: quotaError || undefined,
        lastRefreshedAt: quotaData?.generated_at_ms,
      };
    });
    return states;
  }, [
    accountRows,
    i18n.language,
    quotaData?.accounts,
    quotaData?.generated_at_ms,
    quotaError,
    quotaLoading,
  ]);
  const accountOverviewScopeText = useMemo(
    () => formatViewerAccountScopeText(rangeBounds, i18n.language, t),
    [i18n.language, rangeBounds, t],
  );

  const failedGroupCount = useMemo(
    () =>
      new Set(
        eventRows.filter((row) => row.failed).map(buildViewerEventStreamKey),
      ).size,
    [eventRows],
  );
  const hasPrices =
    Object.keys(modelPrices).length > 0 ||
    data?.summary?.total_cost !== undefined;
  const primarySummaryCards = useMemo(
    () =>
      buildViewerPrimarySummaryCards({
        summary: monitoringSummary,
        accountCount: accountRows.length,
        failedGroupCount,
        hasPrices,
        locale: i18n.language,
        t,
      }),
    [
      accountRows.length,
      failedGroupCount,
      hasPrices,
      i18n.language,
      monitoringSummary,
      t,
    ],
  );
  const secondarySummaryCards = useMemo(
    () => buildViewerSecondarySummaryCards(monitoringSummary, i18n.language, t),
    [i18n.language, monitoringSummary, t],
  );

  const scopedFailureCount = monitoringSummary.failureCalls;
  const failedOnlyActive = selectedStatus === "failed";
  const dataTabs = useMemo<MonitoringTab<MonitoringDataTab>[]>(
    () => [
      {
        id: "accounts",
        label: shortLabel(
          t,
          "monitoring.data_tab_accounts_short",
          "monitoring.data_tab_accounts",
        ),
        fullLabel: t("monitoring.data_tab_accounts"),
        icon: "accounts",
        badge: accountRows.length,
      },
      {
        id: "apiKeys",
        label: shortLabel(
          t,
          "monitoring.data_tab_api_keys_short",
          "monitoring.data_tab_api_keys",
        ),
        fullLabel: t("monitoring.data_tab_api_keys"),
        icon: "apiKeys",
        badge: apiKeyRows.length,
      },
      {
        id: "realtime",
        label: shortLabel(
          t,
          "monitoring.data_tab_realtime_short",
          "monitoring.data_tab_realtime",
        ),
        fullLabel: t("monitoring.data_tab_realtime"),
        icon: "realtime",
        badge:
          scopedFailureCount > 0
            ? scopedFailureCount
            : monitoringSummary.totalCalls,
        badgeTone: scopedFailureCount > 0 ? "failure" : "default",
      },
    ],
    [
      accountRows.length,
      apiKeyRows.length,
      monitoringSummary.totalCalls,
      scopedFailureCount,
      t,
    ],
  );

  const resetPages = useCallback(() => {
    setAccountPage(1);
    setApiKeyPage(1);
    setRealtimePage(1);
  }, []);
  const clearFilters = useCallback(() => {
    setSearchInput("");
    setSelectedAccount("all");
    setSelectedProvider("all");
    setSelectedModel("all");
    setSelectedChannel("all");
    setSelectedApiKeyId("all");
    setSelectedStatus("all");
    setSelectedAuthFile("");
    setSelectedProjectId("");
    setSelectedRequestType("");
    setFocusedAccountId(null);
    resetPages();
  }, [resetPages]);
  const handleSearchChange = useCallback(
    (value: string) => {
      setSearchInput(value);
      resetPages();
    },
    [resetPages],
  );
  const handleTimeRangeChange = useCallback(
    (range: MonitoringTimeRange) => {
      if (range === "custom") {
        setCustomDraftStartInput(
          customStartInput || getViewerTodayStartInputValue(),
        );
        setCustomDraftEndInput(customEndInput || getViewerCurrentInputValue());
        setIsCustomRangeModalOpen(true);
        return;
      }
      setTimeRange(range);
      resetPages();
    },
    [customEndInput, customStartInput, resetPages],
  );
  const applyCustomTimeRange = useCallback(() => {
    if (customDraftTimeRangeError) return;
    setCustomStartInput(customDraftStartInput);
    setCustomEndInput(customDraftEndInput);
    setTimeRange("custom");
    setIsCustomRangeModalOpen(false);
    resetPages();
  }, [
    customDraftEndInput,
    customDraftStartInput,
    customDraftTimeRangeError,
    resetPages,
  ]);
  const handleCustomDraftStartChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      setCustomDraftStartInput(event.target.value);
    },
    [],
  );
  const handleCustomDraftEndChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      setCustomDraftEndInput(event.target.value);
    },
    [],
  );
  const toggleFailedOnly = useCallback(() => {
    setSelectedStatus((previous) => (previous === "failed" ? "all" : "failed"));
    setRealtimePage(1);
  }, []);
  const toggleAccountExpanded = useCallback(
    (rowId: string) => {
      if (!expandedAccounts[rowId] && !accountQuotaStates[rowId]) {
        void refreshQuota();
      }
      setExpandedAccounts((previous) => ({
        ...previous,
        [rowId]: !previous[rowId],
      }));
    },
    [accountQuotaStates, expandedAccounts, refreshQuota],
  );
  const toggleApiKeyExpanded = useCallback((apiKeyId: string) => {
    setExpandedApiKeys((previous) => ({
      ...previous,
      [apiKeyId]: !previous[apiKeyId],
    }));
  }, []);
  const focusAccount = useCallback(
    (row: MonitoringAccountRow) => {
      const nextFocused = focusedAccountId === row.id ? null : row.id;
      setFocusedAccountId(nextFocused);
      setSelectedAccount(nextFocused ? row.id : "all");
      resetPages();
    },
    [focusedAccountId, resetPages],
  );
  const handleAccountSortKeyChange = useCallback((key: AccountSortKey) => {
    setAccountSort((previous) =>
      previous.key === key ? previous : { key, direction: "desc" },
    );
    setAccountPage(1);
  }, []);
  const handleAccountSort = useCallback((key: AccountSortKey) => {
    setAccountSort((previous) =>
      previous.key === key
        ? {
            key,
            direction: previous.direction === "desc" ? "asc" : "desc",
          }
        : { key, direction: "desc" },
    );
    setAccountPage(1);
  }, []);
  const handleAccountModeChange = useCallback(
    (mode: MonitoringAccountOverviewMode) => {
      setAccountOverviewMode(mode);
      const options =
        mode === "card"
          ? ACCOUNT_OVERVIEW_CARD_PAGE_SIZE_OPTIONS
          : ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS;
      setAccountPageSize(options[0]);
      setAccountPage(1);
    },
    [],
  );

  const analyticsError = [statisticsAnalytics.error, realtimeAnalytics.error]
    .filter(Boolean)
    .join("；");
  const analyticsLoading =
    statisticsAnalytics.loading || realtimeAnalytics.loading;
  const overallLoading = analyticsLoading || eventsLoadingMore;
  const combinedError = [
    analyticsError,
    filterSelectorsAnalytics.error,
    ...(aliasWarnings ?? []),
    ...viewerSourceWarnings(
      data,
      realtimeData,
      filterSelectorsAnalytics.data,
      quotaData,
    ),
    activeEventsLoadError,
    modelPricesError ? `Model prices: ${modelPricesError}` : "",
    quotaError ? `Quota: ${quotaError}` : "",
  ]
    .filter(Boolean)
    .join("；");
  const connectionTone: MonitoringStatusTone = analyticsError
    ? "bad"
    : analyticsLoading
      ? "warn"
      : "good";
  const connectionLabel = analyticsError
    ? t("common.error")
    : analyticsLoading
      ? t("common.connecting_status")
      : t("common.connected_status");
  const hasActiveDataFilter =
    Boolean(deferredSearch.trim()) ||
    selectedAccount !== "all" ||
    selectedProvider !== "all" ||
    selectedModel !== "all" ||
    selectedChannel !== "all" ||
    selectedApiKeyId !== "all" ||
    selectedStatus !== "all" ||
    Boolean(selectedAuthFile) ||
    Boolean(selectedProjectId) ||
    Boolean(selectedRequestType);
  const emptyState = (
    <div className={styles.emptyState}>
      <IconInbox
        size={48}
        className={styles.emptyStateIcon}
        aria-hidden="true"
      />
      <strong className={styles.emptyStateTitle}>
        {hasActiveDataFilter
          ? t("monitoring.no_filtered_data")
          : t("monitoring.no_data")}
      </strong>
      {!hasActiveDataFilter ? (
        <details className={styles.emptyStateDetails}>
          <summary className={styles.emptyStateSummary}>
            {t("monitoring.empty_diagnostics_link")}
          </summary>
          <span className={styles.emptyStateBody}>
            {t("monitoring.empty_diagnostics_body")}
          </span>
        </details>
      ) : null}
    </div>
  );

  const dataPanelActions = useMemo(() => {
    if (activeDataTab === "accounts") {
      return (
        <AccountOverviewPanelActions
          mode={accountOverviewMode}
          accountDisplayMode={accountDisplayMode}
          searchInput={searchInput}
          accountSort={accountSort}
          accountSortOptions={accountSortOptions}
          overallLoading={overallLoading}
          t={t}
          onSearchChange={handleSearchChange}
          onRefreshAll={refreshAll}
          onAccountSortKeyChange={handleAccountSortKeyChange}
          onModeChange={handleAccountModeChange}
          onAccountDisplayModeChange={setAccountDisplayMode}
        />
      );
    }
    if (activeDataTab === "apiKeys") {
      return <ApiKeySummaryPanelActions rowCount={apiKeyRows.length} t={t} />;
    }
    return (
      <RealtimeEventsPanelActions
        rowCount={realtimeLogRows.length}
        scopedFailureCount={scopedFailureCount}
        failedOnlyActive={failedOnlyActive}
        accountDisplayMode={accountDisplayMode}
        t={t}
        onToggleFailedOnly={toggleFailedOnly}
        onAccountDisplayModeChange={setAccountDisplayMode}
      />
    );
  }, [
    accountDisplayMode,
    accountOverviewMode,
    accountSort,
    accountSortOptions,
    activeDataTab,
    apiKeyRows.length,
    failedOnlyActive,
    handleAccountModeChange,
    handleAccountSortKeyChange,
    handleSearchChange,
    overallLoading,
    realtimeLogRows.length,
    refreshAll,
    scopedFailureCount,
    searchInput,
    t,
    toggleFailedOnly,
  ]);

  return (
    <div className={styles.page}>
      <MonitoringStatusHeader
        showLoadingOverlay={
          analyticsLoading &&
          !statisticsAnalytics.data &&
          !realtimeAnalytics.data
        }
        monitoringUnavailable={false}
        monitoringUnavailableTitle=""
        monitoringUnavailableBody=""
        t={t}
      />

      <MonitoringDatabaseMaintenanceHint
        performanceDegraded={monitoringMaintenanceWarning}
        longRange={monitoringMaintenanceLongRange}
      />

      <MonitoringActionBar
        usageTransferAvailable
        usageExporting={usageExporting}
        usageImporting={false}
        loggingToFile
        modelPricesAvailable
        usageImportInputRef={usageImportInputRef}
        t={t}
        onUsageExport={handleViewerExport}
        onUsageImportClick={() => undefined}
        onUsageImportChange={() => undefined}
        readOnly
        readOnlyMessage={t("dashboard.viewer_read_only")}
        allowReadOnlyExport
        statusSummary={
          <MonitoringStatusSummary
            connectionTone={connectionTone}
            connectionLabel={connectionLabel}
            lastRefreshedAt={lastRefreshedAt ? new Date(lastRefreshedAt) : null}
            locale={i18n.language}
            scopedFailureCount={scopedFailureCount}
            totalCalls={monitoringSummary.totalCalls}
            t={t}
          />
        }
      />

      <MonitoringFiltersPanel
        timeRange={timeRange}
        autoRefreshMs={autoRefreshMs}
        selectedAccount={selectedAccount}
        selectedProvider={selectedProvider}
        selectedModel={selectedModel}
        selectedChannel={selectedChannel}
        selectedApiKeyHash={selectedApiKeyId}
        selectedStatus={selectedStatus}
        searchInput={searchInput}
        accountOptions={accountOptions}
        providerOptions={providerOptions}
        modelOptions={modelOptions}
        channelOptions={channelOptions}
        apiKeyOptions={apiKeyOptions}
        statusOptions={statusOptions}
        combinedError={combinedError || null}
        usageStatisticsEnabled
        overallLoading={overallLoading}
        t={t}
        onTimeRangeChange={handleTimeRangeChange}
        onAutoRefreshChange={setAutoRefreshMs}
        onRefreshAll={refreshAll}
        onAccountFilterChange={(value) => {
          setSelectedAccount(value);
          setFocusedAccountId(null);
          resetPages();
        }}
        onProviderChange={(value) => {
          setSelectedProvider(value);
          resetPages();
        }}
        onModelChange={(value) => {
          setSelectedModel(value);
          resetPages();
        }}
        onChannelChange={(value) => {
          setSelectedChannel(value);
          resetPages();
        }}
        onApiKeyChange={(value) => {
          setSelectedApiKeyId(value);
          resetPages();
        }}
        onStatusChange={(value) => {
          setSelectedStatus(value as StatusFilter);
          resetPages();
        }}
        onSearchChange={handleSearchChange}
        onClearFilters={clearFilters}
      />

      <UsageCoverageWarning coverage={statisticsAnalytics.coverage} t={t} />

      <MonitoringSummarySection
        primaryCards={primarySummaryCards}
        secondaryCards={secondarySummaryCards}
      />

      <MonitoringDataPanel
        tabs={dataTabs}
        activeTab={activeDataTab}
        onTabChange={(tab) => {
          setActiveDataTab(tab);
          resetPages();
        }}
        ariaLabel={t("monitoring.data_tabs_aria_label")}
        actions={dataPanelActions}
        renderContent={(tab) => {
          if (tab === "accounts") {
            return (
              <AccountOverviewPanel
                embedded
                mode={accountOverviewMode}
                accountDisplayMode={accountDisplayMode}
                searchInput={searchInput}
                columns={accountOverviewColumns}
                rows={sortedAccountRows}
                pagination={accountPagination}
                accountSort={accountSort}
                accountSortOptions={accountSortOptions}
                expandedAccounts={expandedAccounts}
                focusedAccountId={focusedAccountId}
                accountAuthStateByRowId={accountAuthStateByRowId}
                accountStatusDataByRowId={accountStatusDataByRowId}
                emptyAccountStatusData={emptyAccountStatusData}
                accountQuotaStatesByRowId={accountQuotaStates}
                accountPageSize={accountPageSize}
                accountPageSizeOptions={
                  accountOverviewMode === "card"
                    ? ACCOUNT_OVERVIEW_CARD_PAGE_SIZE_OPTIONS
                    : ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS
                }
                accountOverviewScopeText={accountOverviewScopeText}
                hasPrices={hasPrices}
                overallLoading={overallLoading}
                locale={i18n.language}
                emptyState={emptyState}
                t={t}
                onSearchChange={handleSearchChange}
                onRefreshAll={refreshAll}
                onAccountSortKeyChange={handleAccountSortKeyChange}
                onModeChange={handleAccountModeChange}
                onAccountDisplayModeChange={setAccountDisplayMode}
                onAccountSort={handleAccountSort}
                onLoadAccountQuota={() => refreshQuota()}
                onToggleExpanded={toggleAccountExpanded}
                onFocusAccount={focusAccount}
                onPageChange={setAccountPage}
                onPageSizeChange={(pageSize) => {
                  setAccountPageSize(pageSize);
                  setAccountPage(1);
                }}
              />
            );
          }
          if (tab === "apiKeys") {
            return (
              <ApiKeySummaryPanel
                embedded
                rows={apiKeyRows}
                columns={apiKeyOverviewColumns}
                pagination={apiKeyPagination}
                expandedApiKeys={expandedApiKeys}
                hasPrices={hasPrices}
                locale={i18n.language}
                pageSize={apiKeyPageSize}
                pageSizeOptions={ACCOUNT_OVERVIEW_TABLE_PAGE_SIZE_OPTIONS}
                emptyState={emptyState}
                t={t}
                onToggleApiKey={toggleApiKeyExpanded}
                onPageChange={setApiKeyPage}
                onPageSizeChange={(pageSize) => {
                  setApiKeyPageSize(pageSize);
                  setApiKeyPage(1);
                }}
              />
            );
          }
          return (
            <RealtimeEventsPanel
              embedded
              rows={realtimeLogRows}
              pagination={realtimePagination}
              pageSize={realtimePageSize}
              scopedFailureCount={scopedFailureCount}
              failedOnlyActive={failedOnlyActive}
              eventsHasMore={
                activeEventsHasMore &&
                activeEventItems.length < MONITORING_EVENTS_RETENTION_LIMIT
              }
              eventsLoadingMore={eventsLoadingMore}
              eventsRetentionLimited={
                activeEventItems.length >= MONITORING_EVENTS_RETENTION_LIMIT &&
                (activeEventsHasMore ||
                  activeEventsTotalCount > MONITORING_EVENTS_RETENTION_LIMIT)
              }
              eventsTotalCount={activeEventsTotalCount}
              eventsLoadedCount={activeEventItems.length}
              overallLoading={overallLoading}
              hasPrices={hasPrices}
              accountDisplayMode={accountDisplayMode}
              locale={i18n.language}
              emptyState={emptyState}
              t={t}
              onToggleFailedOnly={toggleFailedOnly}
              onAccountDisplayModeChange={setAccountDisplayMode}
              onPageChange={setRealtimePage}
              onPageSizeChange={(pageSize) => {
                setRealtimePageSize(pageSize);
                setRealtimePage(1);
              }}
              onLoadMoreEvents={() => void loadMoreEvents()}
            />
          );
        }}
      />

      <MonitoringCustomRangeModal
        open={isCustomRangeModalOpen}
        startInput={customDraftStartInput}
        endInput={customDraftEndInput}
        error={customDraftTimeRangeError || null}
        t={t}
        onClose={() => setIsCustomRangeModalOpen(false)}
        onApply={applyCustomTimeRange}
        onStartChange={handleCustomDraftStartChange}
        onEndChange={handleCustomDraftEndChange}
      />
    </div>
  );
}

export default ViewerMonitoringPage;
