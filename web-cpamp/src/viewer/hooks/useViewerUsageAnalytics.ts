import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useHeaderRefresh } from "@/hooks/useHeaderRefresh";
import {
  adaptUsageAnalyticsData,
  analyzeUsageBucket,
  buildApiKeyTrendSeries,
  buildCredentialQuotaRows,
  buildEntityTrendSeries,
  buildKeyAnomalies,
  buildSelectedApiKeyTrendSeries,
  buildSelectedCredentialTrendSeries,
  buildUsageAnalyticsFilters,
  buildUsageAnalyticsFilterSelectorsInclude,
  buildUsageAnalyticsInclude,
  buildUsageHeatmap,
  buildUsageHeatmapCellDateOptions,
  buildUsageHeatmapCellDetail,
  buildUsageHeatmapHighlights,
  buildUsageHeatmapRangeContext,
  buildUsageInsights,
  buildUsageMatrix,
  buildUsageSummaryDelta,
  buildUsageApiKeyTimeline,
  buildUsageCredentialTimeline,
  buildUsageTimeline,
  getSelectableApiKeyHash,
  getUsageRangeBounds,
  resolveUsageGranularity,
  USAGE_ANALYTICS_DEFAULT_FILTERS,
  type UsageAnalyticsFiltersState,
  type UsageAnalyticsTab,
  type UsageAnomalyAnalysis,
  type UsageHeatmapCellSelection,
  type UsageHeatmapDateOption,
  type UsageHeatmapMetricKey,
  type UsageHeatmapScaleMode,
  type UsageMatrixDimension,
  type UsageMatrixMetricKey,
  type UsageSelectedFilterKey,
  type UsageTimelinePoint,
  type UsageTrendMetricKey,
} from "@/features/usage-analytics/usageAnalyticsModel";
import {
  buildUsageAnalyticsSearchParams,
  buildUsageAnalyticsUiStateFromSearchParams,
  readUsageAnalyticsUiState,
  writeUsageAnalyticsUiState,
  type UsageAnalyticsUiState,
} from "@/features/usage-analytics/usageAnalyticsUiState";
import type {
  MonitoringAnalyticsFilters,
  MonitoringAnalyticsInclude,
} from "@/services/api/usageService";
import {
  useViewerAliases,
  useViewerAnalytics,
} from "@/viewer/hooks/useViewerData";
import {
  adaptViewerAnalyticsResponse,
  buildViewerAuthFileDisplayMap,
  buildViewerApiKeyDisplayMap,
  buildViewerUsageAnalyticsRequest,
} from "@/viewer/model/viewerUsageAnalyticsAdapter";

import { viewerSourceWarnings } from "@/viewer/model/instance";

const USAGE_SEARCH_DEBOUNCE_MS = 350;
const USAGE_HEATMAP_ALL_DATES_KEY = "all";
const API_KEY_TREND_SERIES_LIMIT = 4;
const DAY_MS = 24 * 60 * 60 * 1000;

const getBrowserTimeZone = () => {
  if (typeof Intl === "undefined") return "UTC";
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
};

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debouncedValue, setDebouncedValue] = useState(value);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedValue(value), delayMs);
    return () => window.clearTimeout(timer);
  }, [delayMs, value]);

  return debouncedValue;
}

const validBounds = (
  bounds: { fromMs: number; toMs: number } | null,
  nowMs: number,
) =>
  bounds && bounds.fromMs > 0 && bounds.toMs > bounds.fromMs
    ? bounds
    : { fromMs: nowMs - DAY_MS, toMs: nowMs };

const buildRequest = ({
  bounds,
  nowMs,
  browserTimeZone,
  searchQuery,
  filters,
  include,
}: {
  bounds: { fromMs: number; toMs: number };
  nowMs: number;
  browserTimeZone: string;
  searchQuery?: string;
  filters?: MonitoringAnalyticsFilters;
  include: MonitoringAnalyticsInclude;
}) =>
  buildViewerUsageAnalyticsRequest({
    fromMs: bounds.fromMs,
    toMs: bounds.toMs,
    nowMs,
    searchQuery,
    filters,
    include,
    timeZone: browserTimeZone,
  });

export function useViewerUsageAnalytics() {
  const refreshPromiseRef = useRef<Promise<void> | null>(null);
  const {
    aliases,
    warnings: aliasWarnings,
    refresh: refreshAliases,
  } = useViewerAliases();
  const [searchParams, setSearchParams] = useSearchParams();
  const [initialUiState] = useState<UsageAnalyticsUiState>(() =>
    buildUsageAnalyticsUiStateFromSearchParams(
      searchParams,
      readUsageAnalyticsUiState(),
    ),
  );
  const [filters, setFiltersState] = useState<UsageAnalyticsFiltersState>(
    () => ({
      ...initialUiState.filters,
      apiKeyHash:
        getSelectableApiKeyHash(initialUiState.filters.apiKeyHash) || "all",
    }),
  );
  const [activeTabState, setActiveTabState] = useState<UsageAnalyticsTab>(
    () => initialUiState.activeTab,
  );
  const [refreshing, setRefreshing] = useState(false);
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [selectedBucketMs, setSelectedBucketMs] = useState<number | null>(null);
  const [selectedModelId, setSelectedModelId] = useState("");
  const [selectedApiKeyHash, setSelectedApiKeyHash] = useState("");
  const [selectedCredentialId, setSelectedCredentialId] = useState("");
  const [trendMetric, setTrendMetric] =
    useState<UsageTrendMetricKey>("requestCount");
  const [matrixDimension, setMatrixDimension] =
    useState<UsageMatrixDimension>("apiKeyModel");
  const [matrixMetric, setMatrixMetric] =
    useState<UsageMatrixMetricKey>("requestCount");
  const [heatmapMetric, setHeatmapMetric] =
    useState<UsageHeatmapMetricKey>("requestCount");
  const [heatmapScaleMode, setHeatmapScaleMode] =
    useState<UsageHeatmapScaleMode>("absolute");
  const [selectedHeatmapDateKey, setSelectedHeatmapDateKey] = useState(
    USAGE_HEATMAP_ALL_DATES_KEY,
  );
  const [selectedHeatmapCell, setSelectedHeatmapCell] =
    useState<UsageHeatmapCellSelection | null>(null);
  const browserTimeZone = useMemo(() => getBrowserTimeZone(), []);
  const setActiveTab = useCallback((tab: UsageAnalyticsTab) => {
    setActiveTabState(tab);
    writeUsageAnalyticsUiState({ activeTab: tab });
  }, []);
  const debouncedSearchQuery = useDebouncedValue(
    filters.searchQuery.trim(),
    USAGE_SEARCH_DEBOUNCE_MS,
  );
  const rawBounds = useMemo(
    () => getUsageRangeBounds(filters, nowMs),
    [filters, nowMs],
  );
  const bounds = useMemo(
    () => validBounds(rawBounds, nowMs),
    [nowMs, rawBounds],
  );
  const heatmapRangeContext = useMemo(
    () => buildUsageHeatmapRangeContext(bounds, "en-US", browserTimeZone),
    [bounds, browserTimeZone],
  );
  const heatmapDateOptions = useMemo(
    () =>
      buildUsageHeatmapCellDateOptions(
        heatmapRangeContext,
        selectedHeatmapCell,
      ),
    [heatmapRangeContext, selectedHeatmapCell],
  );
  const selectedHeatmapDate = useMemo<UsageHeatmapDateOption | null>(
    () =>
      selectedHeatmapDateKey === USAGE_HEATMAP_ALL_DATES_KEY
        ? null
        : (heatmapDateOptions.find(
            (option) => option.key === selectedHeatmapDateKey,
          ) ?? null),
    [heatmapDateOptions, selectedHeatmapDateKey],
  );
  const activeHeatmapDateKey = selectedHeatmapDate
    ? selectedHeatmapDateKey
    : USAGE_HEATMAP_ALL_DATES_KEY;
  const resolvedGranularity = useMemo(
    () => resolveUsageGranularity(filters, nowMs),
    [filters, nowMs],
  );
  const analyticsFilters = useMemo(
    () =>
      buildUsageAnalyticsFilters({
        ...filters,
        apiKeyHash: getSelectableApiKeyHash(filters.apiKeyHash) || "all",
      }),
    [filters],
  );

  useEffect(() => {
    const nextState = { activeTab: activeTabState, filters };
    writeUsageAnalyticsUiState(nextState);
    const nextParams = buildUsageAnalyticsSearchParams(nextState);
    if (nextParams.toString() !== searchParams.toString()) {
      setSearchParams(nextParams, { replace: true });
    }
  }, [activeTabState, filters, searchParams, setSearchParams]);

  const drilldownPreviewRequest = useMemo(() => {
    if (selectedBucketMs === null) return null;
    return {
      fromMs: selectedBucketMs,
      toMs:
        selectedBucketMs +
        (resolvedGranularity === "day" ? DAY_MS : 60 * 60 * 1000),
      limit: 12,
    };
  }, [resolvedGranularity, selectedBucketMs]);
  const include = useMemo(
    () =>
      buildUsageAnalyticsInclude(
        activeTabState,
        resolvedGranularity,
        drilldownPreviewRequest,
      ),
    [activeTabState, drilldownPreviewRequest, resolvedGranularity],
  );
  const requestKey = useMemo(
    () =>
      JSON.stringify({
        activeTabState,
        debouncedSearchQuery,
        filters,
        selectedBucketMs,
      }),
    [activeTabState, debouncedSearchQuery, filters, selectedBucketMs],
  );
  const requestFactory = useCallback(() => {
    const requestNowMs = Date.now();
    const requestBounds = validBounds(
      getUsageRangeBounds(filters, requestNowMs),
      requestNowMs,
    );
    return buildRequest({
      bounds: requestBounds,
      nowMs: requestNowMs,
      browserTimeZone,
      searchQuery: debouncedSearchQuery,
      filters: analyticsFilters,
      include,
    });
  }, [
    analyticsFilters,
    browserTimeZone,
    debouncedSearchQuery,
    filters,
    include,
  ]);
  const analytics = useViewerAnalytics(requestFactory, {
    requestKey,
    registerGlobalRefresh: false,
  });

  const filterSelectorsInclude = useMemo(
    () => buildUsageAnalyticsFilterSelectorsInclude(),
    [],
  );
  const filterSelectorsRequestFactory = useCallback(() => {
    const requestNowMs = Date.now();
    const requestBounds = validBounds(
      getUsageRangeBounds(filters, requestNowMs),
      requestNowMs,
    );
    return buildRequest({
      bounds: requestBounds,
      nowMs: requestNowMs,
      browserTimeZone,
      searchQuery: debouncedSearchQuery,
      include: filterSelectorsInclude,
    });
  }, [browserTimeZone, debouncedSearchQuery, filterSelectorsInclude, filters]);
  const filterSelectorsAnalytics = useViewerAnalytics(
    filterSelectorsRequestFactory,
    {
      requestKey: JSON.stringify({ debouncedSearchQuery, filters }),
      registerGlobalRefresh: false,
    },
  );

  const heatmapDateBounds = useMemo(
    () =>
      selectedHeatmapDate
        ? {
            fromMs: selectedHeatmapDate.fromMs,
            toMs: selectedHeatmapDate.toMs,
          }
        : bounds,
    [bounds, selectedHeatmapDate],
  );
  const heatmapDateRequestFactory = useCallback(
    () =>
      buildRequest({
        bounds: heatmapDateBounds,
        nowMs: Date.now(),
        browserTimeZone,
        searchQuery: debouncedSearchQuery,
        filters: analyticsFilters,
        include: { granularity: "hour", heatmap: true },
      }),
    [
      analyticsFilters,
      browserTimeZone,
      debouncedSearchQuery,
      heatmapDateBounds,
    ],
  );
  const heatmapDateAnalytics = useViewerAnalytics(heatmapDateRequestFactory, {
    enabled: Boolean(selectedHeatmapDate),
    requestKey: JSON.stringify({
      debouncedSearchQuery,
      filters: analyticsFilters,
      selectedHeatmapDate,
    }),
    registerGlobalRefresh: false,
  });

  const analyticsData = useMemo(
    () => adaptViewerAnalyticsResponse(analytics.data),
    [analytics.data],
  );
  const filterSelectorsData = useMemo(
    () => adaptViewerAnalyticsResponse(filterSelectorsAnalytics.data),
    [filterSelectorsAnalytics.data],
  );
  const authFileDisplayMap = useMemo(
    () => buildViewerAuthFileDisplayMap(filterSelectorsAnalytics.data),
    [filterSelectorsAnalytics.data],
  );
  const apiKeyDisplayMap = useMemo(
    () =>
      buildViewerApiKeyDisplayMap(
        aliases,
        analytics.data,
        filterSelectorsAnalytics.data,
        heatmapDateAnalytics.data,
      ),
    [
      aliases,
      analytics.data,
      filterSelectorsAnalytics.data,
      heatmapDateAnalytics.data,
    ],
  );
  const adapted = useMemo(
    () =>
      adaptUsageAnalyticsData(
        analyticsData,
        resolvedGranularity,
        filters.apiKeyKeyword,
        apiKeyDisplayMap,
        undefined,
        include.timeline ? bounds : null,
      ),
    [
      analyticsData,
      apiKeyDisplayMap,
      bounds,
      filters.apiKeyKeyword,
      include.timeline,
      resolvedGranularity,
    ],
  );
  const apiKeyTrendRows = useMemo(
    () =>
      adapted.apiKeyRows.filter((row) =>
        Boolean(getSelectableApiKeyHash(row.apiKeyHash)),
      ),
    [adapted.apiKeyRows],
  );
  const apiKeyTrendHashes = useMemo(() => {
    if (activeTabState !== "overview" && activeTabState !== "trends") return [];
    return Array.from(
      new Set(
        apiKeyTrendRows
          .map((row) => getSelectableApiKeyHash(row.apiKeyHash))
          .filter(Boolean),
      ),
    ).slice(0, API_KEY_TREND_SERIES_LIMIT);
  }, [activeTabState, apiKeyTrendRows]);
  const apiKeyTrendFilters = useMemo(
    () =>
      apiKeyTrendHashes.length > 0
        ? { ...analyticsFilters, api_key_hashes: apiKeyTrendHashes }
        : analyticsFilters,
    [analyticsFilters, apiKeyTrendHashes],
  );
  const apiKeyTrendRequestFactory = useCallback(
    () =>
      buildRequest({
        bounds,
        nowMs: Date.now(),
        browserTimeZone,
        searchQuery: debouncedSearchQuery,
        filters: apiKeyTrendFilters,
        include: {
          granularity: resolvedGranularity,
          api_key_timeline: true,
        },
      }),
    [
      apiKeyTrendFilters,
      bounds,
      browserTimeZone,
      debouncedSearchQuery,
      resolvedGranularity,
    ],
  );
  const apiKeyTrendEnabled =
    (activeTabState === "overview" || activeTabState === "trends") &&
    apiKeyTrendHashes.length > 0;
  const apiKeyTrendAnalytics = useViewerAnalytics(apiKeyTrendRequestFactory, {
    enabled: apiKeyTrendEnabled,
    requestKey: JSON.stringify({
      activeTabState,
      apiKeyTrendFilters,
      apiKeyTrendHashes,
      bounds,
      debouncedSearchQuery,
      resolvedGranularity,
    }),
    registerGlobalRefresh: false,
  });
  const apiKeyTrendData = useMemo(
    () => adaptViewerAnalyticsResponse(apiKeyTrendAnalytics.data),
    [apiKeyTrendAnalytics.data],
  );
  const apiKeyTimeline = useMemo(
    () =>
      buildUsageApiKeyTimeline(
        apiKeyTrendData?.api_key_timeline ?? [],
        resolvedGranularity,
      ),
    [apiKeyTrendData, resolvedGranularity],
  );
  const hasExactAPIKeyTimeline = Array.isArray(
    apiKeyTrendData?.api_key_timeline,
  );
  const heatmapDateData = useMemo(
    () => adaptViewerAnalyticsResponse(heatmapDateAnalytics.data),
    [heatmapDateAnalytics.data],
  );
  const heatmapDateRows = useMemo(
    () => buildUsageHeatmap(heatmapDateData?.heatmap ?? [], apiKeyDisplayMap),
    [apiKeyDisplayMap, heatmapDateData],
  );
  const heatmapDetailSource = selectedHeatmapDate
    ? heatmapDateRows
    : adapted.heatmap;
  const heatmapDateRefreshing = Boolean(
    selectedHeatmapDate && heatmapDateAnalytics.loading,
  );
  const summaryDelta = useMemo(
    () => buildUsageSummaryDelta(adapted.summary, adapted.summaryComparison),
    [adapted.summary, adapted.summaryComparison],
  );
  const selectedBucket = useMemo(
    () =>
      selectedBucketMs === null
        ? null
        : (adapted.timeline.find(
            (point) => point.bucketMs === selectedBucketMs,
          ) ?? null),
    [adapted.timeline, selectedBucketMs],
  );
  const anomalyAnalysis = useMemo<UsageAnomalyAnalysis | null>(
    () =>
      selectedBucketMs === null
        ? null
        : analyzeUsageBucket(adapted.timeline, selectedBucketMs),
    [adapted.timeline, selectedBucketMs],
  );
  const selectedModel =
    adapted.modelRows.find((row) => row.id === selectedModelId) ??
    adapted.modelRows[0] ??
    null;
  const selectedApiKey =
    adapted.apiKeyRows.find((row) => {
      const apiKeyHash = getSelectableApiKeyHash(row.apiKeyHash);
      return Boolean(apiKeyHash) && apiKeyHash === selectedApiKeyHash;
    }) ??
    adapted.apiKeyRows.find((row) =>
      Boolean(getSelectableApiKeyHash(row.apiKeyHash)),
    ) ??
    adapted.apiKeyRows[0] ??
    null;
  const selectedCredential =
    adapted.credentialRows.find((row) => row.id === selectedCredentialId) ??
    adapted.credentialRows[0] ??
    null;
  const modelTrendSeries = useMemo(
    () =>
      buildEntityTrendSeries(
        adapted.modelRows,
        adapted.timeline,
        trendMetric,
        4,
      ),
    [adapted.modelRows, adapted.timeline, trendMetric],
  );
  const apiKeyTrendSeries = useMemo(
    () =>
      hasExactAPIKeyTimeline
        ? buildApiKeyTrendSeries(
            apiKeyTrendRows,
            adapted.timeline,
            apiKeyTimeline,
            trendMetric,
            API_KEY_TREND_SERIES_LIMIT,
          )
        : buildEntityTrendSeries(
            apiKeyTrendRows,
            adapted.timeline,
            trendMetric,
            API_KEY_TREND_SERIES_LIMIT,
            adapted.apiKeyRows,
          ),
    [
      adapted.apiKeyRows,
      apiKeyTrendRows,
      adapted.timeline,
      apiKeyTimeline,
      hasExactAPIKeyTimeline,
      trendMetric,
    ],
  );
  const selectedApiKeyFilterHash = getSelectableApiKeyHash(
    selectedApiKey?.apiKeyHash,
  );
  const selectedApiKeyTimelineFilters = useMemo(
    () =>
      selectedApiKeyFilterHash
        ? buildUsageAnalyticsFilters({
            ...filters,
            apiKeyHash: selectedApiKeyFilterHash,
          })
        : {},
    [filters, selectedApiKeyFilterHash],
  );
  const selectedApiKeyTimelineRequestFactory = useCallback(() => {
    const requestNowMs = Date.now();
    const requestBounds = validBounds(
      getUsageRangeBounds(filters, requestNowMs),
      requestNowMs,
    );
    return buildRequest({
      bounds: requestBounds,
      nowMs: requestNowMs,
      browserTimeZone,
      searchQuery: debouncedSearchQuery,
      filters: selectedApiKeyTimelineFilters,
      include: { granularity: resolvedGranularity, timeline: true },
    });
  }, [
    browserTimeZone,
    debouncedSearchQuery,
    filters,
    resolvedGranularity,
    selectedApiKeyTimelineFilters,
  ]);
  const selectedApiKeyTimelineEnabled =
    activeTabState === "apiKeys" && Boolean(selectedApiKeyFilterHash);
  const selectedApiKeyTimelineAnalytics = useViewerAnalytics(
    selectedApiKeyTimelineRequestFactory,
    {
      enabled: selectedApiKeyTimelineEnabled,
      requestKey: JSON.stringify({
        activeTabState,
        debouncedSearchQuery,
        filters,
        selectedApiKeyFilterHash,
      }),
      registerGlobalRefresh: false,
    },
  );
  const selectedApiKeyTimelineData = useMemo(
    () => adaptViewerAnalyticsResponse(selectedApiKeyTimelineAnalytics.data),
    [selectedApiKeyTimelineAnalytics.data],
  );
  const selectedApiKeyTimeline = useMemo(
    () =>
      buildUsageTimeline(
        selectedApiKeyTimelineData?.timeline ?? [],
        resolvedGranularity,
      ),
    [resolvedGranularity, selectedApiKeyTimelineData],
  );
  const selectedApiKeyTrendSeries = useMemo(
    () =>
      buildSelectedApiKeyTrendSeries(
        selectedApiKey,
        selectedApiKeyTimeline,
        trendMetric,
      ),
    [selectedApiKey, selectedApiKeyTimeline, trendMetric],
  );
  const selectedCredentialFilterId = selectedCredential?.id || "";
  const selectedCredentialTimelineFilters = useMemo(
    () =>
      selectedCredentialFilterId
        ? {
            ...analyticsFilters,
            credential_ids: [selectedCredentialFilterId],
          }
        : analyticsFilters,
    [analyticsFilters, selectedCredentialFilterId],
  );
  const selectedCredentialTimelineRequestFactory = useCallback(
    () =>
      buildRequest({
        bounds,
        nowMs: Date.now(),
        browserTimeZone,
        searchQuery: debouncedSearchQuery,
        filters: selectedCredentialTimelineFilters,
        include: {
          granularity: resolvedGranularity,
          credential_timeline: true,
        },
      }),
    [
      bounds,
      browserTimeZone,
      debouncedSearchQuery,
      resolvedGranularity,
      selectedCredentialTimelineFilters,
    ],
  );
  const selectedCredentialTimelineEnabled =
    activeTabState === "credentials" && Boolean(selectedCredentialFilterId);
  const selectedCredentialTimelineAnalytics = useViewerAnalytics(
    selectedCredentialTimelineRequestFactory,
    {
      enabled: selectedCredentialTimelineEnabled,
      requestKey: JSON.stringify({
        activeTabState,
        bounds,
        debouncedSearchQuery,
        resolvedGranularity,
        selectedCredentialFilterId,
        selectedCredentialTimelineFilters,
      }),
      registerGlobalRefresh: false,
    },
  );
  const selectedCredentialTimelineData = useMemo(
    () =>
      adaptViewerAnalyticsResponse(selectedCredentialTimelineAnalytics.data),
    [selectedCredentialTimelineAnalytics.data],
  );
  const credentialTrendLoading = Boolean(
    selectedCredentialTimelineEnabled &&
    selectedCredentialTimelineAnalytics.loading,
  );
  const credentialTrendError = selectedCredentialTimelineEnabled
    ? selectedCredentialTimelineAnalytics.error
    : "";
  const selectedCredentialTimeline = useMemo(
    () =>
      buildUsageCredentialTimeline(
        selectedCredentialTimelineData?.credential_timeline ?? [],
        resolvedGranularity,
      ),
    [resolvedGranularity, selectedCredentialTimelineData],
  );
  const credentialTrendSeries = useMemo(
    () =>
      buildSelectedCredentialTrendSeries(
        selectedCredential,
        selectedCredentialTimeline,
        trendMetric,
      ),
    [selectedCredential, selectedCredentialTimeline, trendMetric],
  );
  const heatmapDetail = useMemo(
    () =>
      buildUsageHeatmapCellDetail(
        heatmapDetailSource,
        selectedHeatmapCell,
        heatmapMetric,
      ),
    [heatmapDetailSource, heatmapMetric, selectedHeatmapCell],
  );
  const heatmapHighlights = useMemo(
    () => buildUsageHeatmapHighlights(adapted.heatmap),
    [adapted.heatmap],
  );
  const matrix = useMemo(
    () =>
      buildUsageMatrix({
        apiKeyRows: adapted.apiKeyRows,
        credentialRows: adapted.credentialRows,
        dimension: matrixDimension,
        metric: matrixMetric,
      }),
    [adapted.apiKeyRows, adapted.credentialRows, matrixDimension, matrixMetric],
  );
  const keyAnomalies = useMemo(
    () => buildKeyAnomalies(adapted.apiKeyRows),
    [adapted.apiKeyRows],
  );
  const credentialAnomalies = useMemo(
    () => buildKeyAnomalies(adapted.credentialRows),
    [adapted.credentialRows],
  );
  const credentialQuotaRows = useMemo(
    () => buildCredentialQuotaRows(adapted.credentialRows, nowMs),
    [adapted.credentialRows, nowMs],
  );
  const insights = useMemo(
    () =>
      buildUsageInsights({
        apiKeyRows: adapted.apiKeyRows,
        credentialRows: adapted.credentialRows,
        modelRows: adapted.modelRows,
        providerRows: adapted.providerRows,
        summary: adapted.summary,
      }),
    [
      adapted.apiKeyRows,
      adapted.credentialRows,
      adapted.modelRows,
      adapted.providerRows,
      adapted.summary,
    ],
  );

  const setFilters = useCallback(
    (patch: Partial<UsageAnalyticsFiltersState>) => {
      setFiltersState((current) => {
        const next = {
          ...current,
          ...patch,
          ...(patch.apiKeyHash === undefined
            ? {}
            : {
                apiKeyHash: getSelectableApiKeyHash(patch.apiKeyHash) || "all",
              }),
        };
        writeUsageAnalyticsUiState({ filters: next });
        return next;
      });
      setSelectedBucketMs(null);
      setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
      setSelectedHeatmapCell(null);
    },
    [],
  );
  const resetFilters = useCallback(() => {
    setFiltersState(USAGE_ANALYTICS_DEFAULT_FILTERS);
    writeUsageAnalyticsUiState({ filters: USAGE_ANALYTICS_DEFAULT_FILTERS });
    setSelectedBucketMs(null);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    setSelectedHeatmapCell(null);
  }, []);
  const clearFilter = useCallback((key: UsageSelectedFilterKey) => {
    setFiltersState((current) => {
      const next = { ...current, [key]: "all" };
      writeUsageAnalyticsUiState({ filters: next });
      return next;
    });
    setSelectedBucketMs(null);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    setSelectedHeatmapCell(null);
  }, []);
  const selectBucket = useCallback((point: UsageTimelinePoint | null) => {
    setSelectedBucketMs(point?.bucketMs ?? null);
  }, []);
  const selectHeatmapCell = useCallback(
    (cell: UsageHeatmapCellSelection | null) => {
      setSelectedHeatmapCell(cell);
      setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    },
    [],
  );
  const selectHeatmapDate = useCallback((key: string) => {
    setSelectedHeatmapDateKey(key || USAGE_HEATMAP_ALL_DATES_KEY);
  }, []);
  const selectApiKeyHash = useCallback((hash: string) => {
    setSelectedApiKeyHash(getSelectableApiKeyHash(hash));
  }, []);

  const refresh = useCallback(() => {
    if (refreshPromiseRef.current) return refreshPromiseRef.current;
    setRefreshing(true);
    const pending = (async () => {
      setNowMs(Date.now());
      const requests: Array<Promise<void>> = [
        refreshAliases(),
        analytics.refresh(false),
        filterSelectorsAnalytics.refresh(false),
      ];
      if (selectedApiKeyTimelineEnabled) {
        requests.push(selectedApiKeyTimelineAnalytics.refresh(false));
      }
      if (apiKeyTrendEnabled) {
        requests.push(apiKeyTrendAnalytics.refresh(false));
      }
      if (selectedCredentialTimelineEnabled) {
        requests.push(selectedCredentialTimelineAnalytics.refresh(false));
      }
      if (selectedHeatmapDate) {
        requests.push(heatmapDateAnalytics.refresh(false));
      }
      await Promise.all(requests);
    })().finally(() => {
      setRefreshing(false);
      if (refreshPromiseRef.current === pending) {
        refreshPromiseRef.current = null;
      }
    });
    refreshPromiseRef.current = pending;
    return pending;
  }, [
    analytics,
    apiKeyTrendAnalytics,
    apiKeyTrendEnabled,
    filterSelectorsAnalytics,
    heatmapDateAnalytics,
    refreshAliases,
    selectedApiKeyTimelineAnalytics,
    selectedApiKeyTimelineEnabled,
    selectedCredentialTimelineAnalytics,
    selectedCredentialTimelineEnabled,
    selectedHeatmapDate,
  ]);
  useHeaderRefresh(refresh);

  return {
    filters,
    setFilters,
    resetFilters,
    clearFilter,
    activeTab: activeTabState,
    setActiveTab,
    bounds,
    resolvedGranularity,
    loading: analytics.loading || refreshing,
    error: [
      analytics.error,
      filterSelectorsAnalytics.error,
      ...(aliasWarnings ?? []),
      ...viewerSourceWarnings(
        analytics.data,
        filterSelectorsAnalytics.data,
        heatmapDateAnalytics.data,
        apiKeyTrendAnalytics.data,
        selectedApiKeyTimelineAnalytics.data,
        selectedCredentialTimelineAnalytics.data,
      ),
    ]
      .filter(Boolean)
      .join("；"),
    enabled: true,
    apiKeyDisplayMap,
    authFileDisplayMap,
    unavailableReason: "" as const,
    lastRefreshedAt: analytics.lastRefreshedAt
      ? new Date(analytics.lastRefreshedAt)
      : null,
    refresh,
    coverage: analytics.coverage,
    summary: adapted.summary,
    summaryDelta,
    timeline: adapted.timeline,
    modelRows: adapted.modelRows,
    apiKeyRows: adapted.apiKeyRows,
    credentialRows: adapted.credentialRows,
    allCredentialRows: adapted.credentialRows,
    providerRows: adapted.providerRows,
    heatmap: adapted.heatmap,
    heatmapMetric,
    setHeatmapMetric,
    heatmapScaleMode,
    setHeatmapScaleMode,
    heatmapDateOptions,
    selectedHeatmapDateKey: activeHeatmapDateKey,
    selectHeatmapDate,
    heatmapDateLoading: heatmapDateRefreshing,
    heatmapDateError: selectedHeatmapDate ? heatmapDateAnalytics.error : "",
    selectedHeatmapCell,
    selectHeatmapCell,
    heatmapDetail,
    heatmapHighlights,
    browserTimeZone,
    matrix,
    matrixDimension,
    setMatrixDimension,
    matrixMetric,
    setMatrixMetric,
    trendMetric,
    setTrendMetric,
    modelTrendSeries,
    apiKeyTrendSeries,
    selectedApiKeyTrendSeries,
    credentialTrendSeries,
    credentialTrendLoading,
    credentialTrendError,
    keyAnomalies,
    credentialAnomalies,
    credentialQuotaRows,
    insights,
    anomalyPoints: adapted.anomalyPoints,
    drilldownPreview: adapted.drilldownPreview,
    filterOptions: filterSelectorsData?.filter_options ?? adapted.filterOptions,
    selectedBucket,
    selectBucket,
    anomalyAnalysis,
    selectedModel,
    setSelectedModelId,
    selectedApiKey,
    setSelectedApiKeyHash: selectApiKeyHash,
    selectedCredential,
    setSelectedCredentialId,
  };
}
