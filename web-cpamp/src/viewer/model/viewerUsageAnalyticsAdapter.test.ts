import { describe, expect, it } from "vitest";
import {
  adaptUsageAnalyticsData,
  computeCacheHitRate,
} from "@/features/usage-analytics/usageAnalyticsModel";
import type { ViewerAnalyticsResponse } from "@/viewer/model/viewerTypes";
import {
  adaptViewerAnalyticsResponse,
  buildViewerAuthFileDisplayMap,
  buildViewerApiKeyDisplayMap,
  buildViewerUsageAnalyticsRequest,
} from "./viewerUsageAnalyticsAdapter";

describe("viewer usage analytics adapter", () => {
  it("restores the original read-only shape with Viewer pseudonyms", () => {
    const response = {
      generated_at_ms: 1_700_000_000_000,
      api_key_stats: [
        {
          id: "view_row00000001",
          api_key_id: "view_0123456789ab",
          api_key_alias: "马哥",
          auth_ids: ["view_auth0000001"],
          source_ids: ["view_source00001"],
          calls: 3,
        },
      ],
      filter_selectors: {
        api_key_ids: ["view_0123456789ab"],
        account_count: 7,
        api_key_count: 3,
        auth_file_ids: ["view_f11e00000001"],
        auth_file_options: [
          { id: "view_f11e00000001", display: "codex-account.json" },
        ],
      },
      heatmap: [
        {
          weekday: 1,
          hour: 8,
          api_key_contributors: [{ api_key_id: "view_0123456789ab", calls: 3 }],
        },
      ],
      credential_stats: [
        {
          id: "view_c0ffee000001",
          auth_file_id: "view_f11e00000001",
          auth_file_display: "codex-account.json",
          calls: 3,
        },
      ],
      credential_timeline: [
        {
          id: "view_c0ffee000001",
          auth_file_id: "view_f11e00000001",
          auth_file_display: "codex-account.json",
          bucket_ms: 1_700_000_000_000,
          calls: 3,
        },
      ],
      api_key_timeline: [
        {
          api_key_id: "view_0123456789ab",
          api_key_alias: "马哥",
          bucket_ms: 1_700_000_000_000,
          calls: 3,
        },
      ],
      drilldown_preview: {
        items: [
          {
            event_hash: "view_event000001",
            api_key_id: "view_0123456789ab",
            auth_id: "view_auth0000001",
            endpoint: "/v1/responses",
          },
        ],
      },
    } as unknown as ViewerAnalyticsResponse;

    const adapted = adaptViewerAnalyticsResponse(response);

    expect(adapted?.api_key_stats?.[0]).toMatchObject({
      api_key_hash: "view_0123456789ab",
      auth_indices: ["view_auth0000001"],
      source_hashes: ["view_source00001"],
    });
    expect(adapted?.filter_options).toMatchObject({
      api_key_hashes: ["view_0123456789ab"],
      auth_files: ["view_f11e00000001"],
    });
    expect(adapted?.heatmap?.[0].api_key_contributors?.[0].key).toBe(
      "view_0123456789ab",
    );
    expect(adapted?.drilldown_preview?.items[0]).toMatchObject({
      api_key_hash: "view_0123456789ab",
      auth_index: "view_auth0000001",
      endpoint: "/v1/responses",
    });
    expect(adapted?.credential_stats?.[0]).toMatchObject({
      auth_file_snapshot: "view_f11e00000001",
      label: "codex-account.json",
    });
    expect(adapted?.credential_timeline?.[0]).toMatchObject({
      id: "view_c0ffee000001",
      auth_file_snapshot: "view_f11e00000001",
    });
    expect(adapted?.api_key_timeline?.[0]).toMatchObject({
      api_key_hash: "view_0123456789ab",
      api_key_alias: "马哥",
    });
    expect(adapted?.filter_options).toMatchObject({
      account_count: 7,
      api_key_count: 3,
    });
    expect(
      buildViewerAuthFileDisplayMap(response).get("view_f11e00000001"),
    ).toBe("codex-account.json");
    const presentation = adaptUsageAnalyticsData(
      adapted,
      "hour",
      "",
      buildViewerApiKeyDisplayMap([{ id: "view_0123456789ab", alias: "马哥" }]),
    );
    expect(presentation.apiKeyRows[0]?.label).toBe("马哥");
    expect(presentation.credentialRows[0]?.label).toBe("codex-account.json");
  });

  it("sends only the public API key id filter contract", () => {
    const request = buildViewerUsageAnalyticsRequest({
      fromMs: 1_700_000_000_000,
      toMs: 1_700_003_600_000,
      nowMs: 1_700_003_600_000,
      searchQuery: " gpt ",
      timeZone: "Asia/Shanghai",
      filters: {
        models: ["gpt-5"],
        providers: ["codex"],
        api_key_hashes: ["view_0123456789ab"],
        auth_files: ["view_f11e00000001"],
        credential_ids: ["view_c0ffee000001"],
        failed_only: true,
      },
      include: {
        summary: true,
        summary_profile: "compact",
        summary_percentiles: true,
        api_key_timeline: true,
        credential_timeline: true,
      },
    });

    expect(request).toMatchObject({
      search_query: "gpt",
      filters: {
        models: ["gpt-5"],
        providers: ["codex"],
        api_key_ids: ["view_0123456789ab"],
        auth_files: ["view_f11e00000001"],
        credential_ids: ["view_c0ffee000001"],
        failed_only: true,
      },
    });
    expect(request.filters).not.toHaveProperty("api_key_hashes");
    expect(request.include).toMatchObject({
      summary_profile: "compact",
      summary_percentiles: true,
      api_key_timeline: true,
      credential_timeline: true,
    });
  });

  it("preserves synthetic API key totals while keeping them unselectable", () => {
    const adapted = adaptViewerAnalyticsResponse({
      api_key_stats: [
        {
          id: "view_unknown00001",
          api_key_id: "view_unknown00001",
          api_key_selectable: false,
          calls: 3,
        },
      ],
      filter_options: { api_key_count: 1 },
    } as unknown as ViewerAnalyticsResponse);

    expect(adapted?.api_key_stats?.[0]).toMatchObject({
      id: "view_unknown00001",
      api_key_hash: "unknown-client-api-key:view_unknown00001",
      calls: 3,
    });
  });

  it("uses alias labels without exposing key material", () => {
    const displayMap = buildViewerApiKeyDisplayMap([
      { id: "view_0123456789ab", alias: "马哥" },
      { id: "not-public", alias: "忽略" },
    ]);

    expect(displayMap.get("view_0123456789ab")).toEqual({
      label: "马哥",
      masked: "Key 6789ab",
    });
    expect(displayMap.has("not-public")).toBe(false);
  });

  it("learns historical aliases from safe inline analytics rows", () => {
    const response = {
      api_key_stats: [
        {
          api_key_id: "view_437145000000",
          api_key_alias: "牛哥",
          calls: 1,
        },
      ],
      events: {
        items: [
          {
            api_key_id: "view_437145000000",
            api_key_alias: "牛哥",
          },
        ],
      },
    } as unknown as ViewerAnalyticsResponse;

    const displayMap = buildViewerApiKeyDisplayMap([], response);
    expect(displayMap.get("view_437145000000")).toEqual({
      label: "牛哥",
      masked: "Key 000000",
    });

    const unsafe = buildViewerApiKeyDisplayMap([], {
      api_key_stats: [
        {
          api_key_id: "a".repeat(64),
          api_key_alias: "禁止进入映射",
        },
      ],
    } as unknown as ViewerAnalyticsResponse);
    expect(unsafe.size).toBe(0);
  });
});

describe("federated analytics labels", () => {
  it("keeps same-name credentials and keys separate without changing filter identities", () => {
    const accounts = ["111111111111", "222222222222"].map((suffix, index) => ({
      id: `view_${suffix}`,
      account_id: `view_${suffix}`,
      auth_file_id: `view_${suffix}`,
      account_display: "reader@example.com",
      auth_file_display: "same.json",
      instance_id: `instance-${index}`,
      instance_name: `CPA ${index + 1}`,
      calls: index + 1,
    }));
    const data = {
      credential_stats: accounts,
      api_key_stats: accounts.map((account) => ({
        ...account,
        api_key_id: account.id,
        api_key_alias: "Shared",
      })),
    };
    const normalized = adaptViewerAnalyticsResponse(data);
    expect(normalized?.credential_stats).toMatchObject([
      { label: "reader@example.com · CPA 1" },
      { label: "reader@example.com · CPA 2" },
    ]);
    expect(
      normalized?.credential_stats?.map((row) => row.auth_file_snapshot),
    ).toEqual(accounts.map((row) => row.id));
    const map = buildViewerApiKeyDisplayMap([], data);
    expect([...map.values()].map((row) => row.label)).toEqual([
      "Shared · CPA 1",
      "Shared · CPA 2",
    ]);
    expect([...map.values()].map((row) => row.masked)).toEqual([
      "Key 111111 · CPA 1",
      "Key 222222 · CPA 2",
    ]);
    const adapted = adaptUsageAnalyticsData(normalized, "hour", "", map);
    expect(adapted.credentialRows).toHaveLength(2);
    expect(adapted.apiKeyRows).toHaveLength(2);
  });
});

it("preserves explicit unknown percentiles and cache ratios in aggregated responses", () => {
  const normalized = adaptViewerAnalyticsResponse({
    summary: {
      total_calls: 20,
      cache_hit_rate: null,
      p95_latency_ms: null,
      average_latency_ms: null,
      input_tokens: 100,
      cached_tokens: 90,
    },
  });
  const adapted = adaptUsageAnalyticsData(normalized, "hour", "", new Map());
  expect(adapted.summary.p95LatencyMs).toBeNull();
  expect(adapted.summary.averageLatencyMs).toBeNull();
  expect(adapted.summary.cacheHitRate).toBeNull();
  expect(Number.isNaN(computeCacheHitRate(adapted.summary))).toBe(true);
});
