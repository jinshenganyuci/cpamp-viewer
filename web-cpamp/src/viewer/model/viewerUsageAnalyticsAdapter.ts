import { withViewerInstance } from "./instance";
import type {
  MonitoringAnalyticsFilters,
  MonitoringAnalyticsInclude,
  MonitoringAnalyticsResponse,
} from "@/services/api/usageService";
import type { ApiKeyDisplayInfo } from "@/features/monitoring/model/apiKeys";
import type {
  ViewerAlias,
  ViewerAnalyticsRequest,
  ViewerAnalyticsResponse,
  ViewerInstanceIdentity,
} from "@/viewer/model/viewerTypes";

type JsonRecord = Record<string, unknown>;

const isRecord = (value: unknown): value is JsonRecord =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const readString = (value: unknown) =>
  typeof value === "string" ? value.trim() : "";

const isViewerApiKeyId = (value: string) => /^view_[a-f0-9]{12}$/i.test(value);

const copyViewerIdentityFields = (record: JsonRecord) => {
  const apiKeyId = readString(record.api_key_id);
  if (apiKeyId) {
    record.api_key_hash =
      record.api_key_selectable === false
        ? `unknown-client-api-key:${apiKeyId}`
        : apiKeyId;
    if (!readString(record.id)) record.id = apiKeyId;
  }

  const fieldAliases: Array<[string, string]> = [
    ["auth_id", "auth_index"],
    ["source_id", "source_hash"],
    ["account_id", "account_snapshot"],
    ["auth_label_id", "auth_label_snapshot"],
    ["auth_file_id", "auth_file_snapshot"],
    ["project_id", "auth_project_id_snapshot"],
    ["account_subject_id", "auth_account_id_snapshot"],
    ["auth_ids", "auth_indices"],
    ["source_ids", "source_hashes"],
    ["api_key_ids", "api_key_hashes"],
    ["auth_file_ids", "auth_files"],
  ];

  fieldAliases.forEach(([source, target]) => {
    if (record[source] !== undefined && record[target] === undefined) {
      record[target] = record[source];
    }
  });

  const displayLabel =
    readString(record.auth_label_display) ||
    readString(record.account_display) ||
    readString(record.auth_file_display);
  if (displayLabel && !readString(record.label)) record.label = displayLabel;
  // Preserve IDs/snapshots used for filtering; decorate only visible labels.
  if (readString(record.instance_name)) {
    for (const key of [
      "label",
      "display",
      "api_key_alias",
      "account_display",
      "auth_label_display",
      "auth_file_display",
      "project_display",
      "source_display",
    ]) {
      const label = readString(record[key]);
      if (label)
        record[key] = withViewerInstance(
          label,
          record as ViewerInstanceIdentity,
        );
    }
  }
};

const normalizeViewerValue = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(normalizeViewerValue);
  if (!isRecord(value)) return value;

  const normalized: JsonRecord = {};
  Object.entries(value).forEach(([key, entry]) => {
    normalized[key] = normalizeViewerValue(entry);
  });
  copyViewerIdentityFields(normalized);
  return normalized;
};

const normalizeHeatmapApiKeyContributors = (response: JsonRecord) => {
  const points = response.heatmap;
  if (!Array.isArray(points)) return;
  points.forEach((point) => {
    if (!isRecord(point) || !Array.isArray(point.api_key_contributors)) return;
    point.api_key_contributors.forEach((contributor) => {
      if (!isRecord(contributor)) return;
      const apiKeyId = readString(contributor.api_key_id);
      if (apiKeyId && !readString(contributor.key)) {
        contributor.key =
          contributor.api_key_selectable === false
            ? `unknown-client-api-key:${apiKeyId}`
            : apiKeyId;
      }
    });
  });
};

/**
 * The public Viewer endpoint deliberately renames sensitive upstream identities.
 * This restores only the field shape expected by the original read-only UI. All
 * restored values remain Viewer pseudonyms such as `view_0123456789ab`.
 */
export const adaptViewerAnalyticsResponse = (
  response: ViewerAnalyticsResponse | null | undefined,
): MonitoringAnalyticsResponse | null => {
  if (!response) return null;
  const normalized = normalizeViewerValue(response) as JsonRecord;
  if (!normalized.filter_options && isRecord(normalized.filter_selectors)) {
    normalized.filter_options = normalized.filter_selectors;
  }
  normalizeHeatmapApiKeyContributors(normalized);
  return normalized as unknown as MonitoringAnalyticsResponse;
};

export const buildViewerApiKeyDisplayMap = (
  aliases: ViewerAlias[],
  ...responses: Array<ViewerAnalyticsResponse | null | undefined>
): Map<string, ApiKeyDisplayInfo> => {
  const displayMap = new Map<string, ApiKeyDisplayInfo>();
  const add = (
    rawId: unknown,
    rawAlias: unknown,
    instance: ViewerInstanceIdentity = {},
  ) => {
    const id = readString(rawId).toLowerCase();
    const alias = withViewerInstance(readString(rawAlias), instance);
    if (!isViewerApiKeyId(id) || !readString(rawAlias)) return;
    displayMap.set(id, {
      label: alias,
      masked: withViewerInstance(`Key ${id.slice(-6)}`, instance),
    });
  };

  aliases.forEach((entry) => add(entry.id, entry.alias, entry));

  const collectInlineAliases = (value: unknown) => {
    if (Array.isArray(value)) {
      value.forEach(collectInlineAliases);
      return;
    }
    if (!isRecord(value)) return;
    add(value.api_key_id, value.api_key_alias, value as ViewerInstanceIdentity);
    Object.values(value).forEach(collectInlineAliases);
  };
  responses.forEach(collectInlineAliases);

  return displayMap;
};

export const buildViewerAuthFileDisplayMap = (
  response: ViewerAnalyticsResponse | null | undefined,
): ReadonlyMap<string, string> => {
  if (!response) return new Map();
  const root = response as unknown as JsonRecord;
  const optionsRoot = isRecord(root.filter_options)
    ? root.filter_options
    : isRecord(root.filter_selectors)
      ? root.filter_selectors
      : null;
  const options = optionsRoot?.auth_file_options;
  if (!Array.isArray(options)) return new Map();
  return new Map(
    options
      .filter(isRecord)
      .map(
        (option) =>
          [
            readString(option.id),
            withViewerInstance(
              readString(option.display),
              option as ViewerInstanceIdentity,
            ),
          ] as const,
      )
      .filter(([id, display]) => isViewerApiKeyId(id) && Boolean(display)),
  );
};

const buildPublicFilters = (
  filters: MonitoringAnalyticsFilters,
): Record<string, unknown> => {
  const publicFilters: Record<string, unknown> = {};
  if (filters.models?.length) publicFilters.models = filters.models;
  if (filters.providers?.length) publicFilters.providers = filters.providers;
  if (filters.api_key_hashes?.length) {
    const apiKeyIds = filters.api_key_hashes.filter(isViewerApiKeyId);
    if (apiKeyIds.length) publicFilters.api_key_ids = apiKeyIds;
  }
  if (filters.credential_ids?.length) {
    const credentialIds = filters.credential_ids.filter(isViewerApiKeyId);
    if (credentialIds.length) publicFilters.credential_ids = credentialIds;
  }
  if (filters.auth_files?.length) {
    const authFileIds = filters.auth_files.filter(isViewerApiKeyId);
    if (authFileIds.length) publicFilters.auth_files = authFileIds;
  }
  if (filters.include_failed !== undefined) {
    publicFilters.include_failed = filters.include_failed;
  }
  if (filters.failed_only !== undefined) {
    publicFilters.failed_only = filters.failed_only;
  }
  if (filters.min_latency_ms !== undefined) {
    publicFilters.min_latency_ms = filters.min_latency_ms;
  }
  if (filters.cache_status) publicFilters.cache_status = filters.cache_status;
  return publicFilters;
};

export const buildViewerUsageAnalyticsRequest = ({
  fromMs,
  toMs,
  nowMs,
  searchQuery,
  filters = {},
  include,
  timeZone,
}: {
  fromMs: number;
  toMs: number;
  nowMs: number;
  searchQuery?: string;
  filters?: MonitoringAnalyticsFilters;
  include: MonitoringAnalyticsInclude;
  timeZone: string;
}): ViewerAnalyticsRequest => {
  const request: ViewerAnalyticsRequest = {
    from_ms: fromMs,
    to_ms: toMs,
    now_ms: nowMs,
    time_zone: timeZone,
    include: { ...include },
  };
  const normalizedQuery = searchQuery?.trim();
  if (normalizedQuery) request.search_query = normalizedQuery;
  const publicFilters = buildPublicFilters(filters);
  if (Object.keys(publicFilters).length > 0) request.filters = publicFilters;
  return request;
};
