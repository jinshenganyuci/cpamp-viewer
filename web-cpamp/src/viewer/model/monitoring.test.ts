import { describe, expect, it } from "vitest";
import {
  buildViewerMonitoringAccountRows,
  buildViewerApiKeyFilterRows,
  findViewerQuotaAccounts,
  viewerEventPageFeedback,
  buildViewerMonitoringSummary,
  buildViewerMonitoringApiKeyRows,
  buildViewerMonitoringEventRows,
  findViewerApiKeyIdsByAlias,
  mergeViewerMonitoringEvents,
  resolveViewerApiKeySearchScope,
} from "./monitoring";
import type { ViewerMonitoringEvent } from "./viewerTypes";
import type { TFunction } from "i18next";
import { resolveAccountDisplayText } from "@/features/monitoring/accountOverviewState";
import {
  buildViewerRealtimeLogRows,
  buildViewerPrimarySummaryCards,
  buildViewerSecondarySummaryCards,
} from "./monitoringPage";
import { buildRealtimeSourceDisplay } from "@/features/monitoring/realtimeSourceDisplay";

describe("Viewer monitoring adapters", () => {
  it.each(["敏感错误详情已隐藏", "内部错误详情已隐藏"])(
    "keeps readable account and alias ahead of legacy DTO redaction placeholders: %s",
    (placeholder) => {
      const event = {
        event_hash: "view_event000001",
        timestamp_ms: 1_720_000_000_000,
        source: placeholder,
        source_display: placeholder,
        source_hash: `h:${"a".repeat(64)}`,
        auth_label_display: placeholder,
        account_display: "reader@example.com",
        auth_provider_snapshot: "codex",
        api_key_id: "view_0123456789ab",
        api_key_alias: "Public team",
      };
      const [row] = buildViewerMonitoringEventRows([event], []);
      const t = ((key: string) => key) as TFunction;
      const masked = buildRealtimeSourceDisplay(row, t, "masked");
      const full = buildRealtimeSourceDisplay(row, t, "full");
      expect(masked.primary).toBe("rea***@example.com");
      expect(full.primary).toBe("reader@example.com");
      expect(row.apiKeyLabel).toBe("Public team");
      expect(JSON.stringify([masked, full])).not.toContain(placeholder);
      expect(JSON.stringify([row, masked, full])).not.toMatch(
        /h:[a-f0-9]{64}/i,
      );
    },
  );

  it("projects model diagnostics and explicit request flags without sensitive upstream metadata", () => {
    const event = {
      event_hash: "view_event000001",
      timestamp_ms: 1_720_000_000_000,
      model: "analytics-model",
      requested_model: "public-alias(high)",
      resolved_model: "routed-model",
      response_model: "observed-response-model",
      reasoning_effort: "high",
      service_tier: "priority",
      request_service_tier: "priority",
      response_service_tier: "default",
      generate: true,
      stream: false,
      session_id: "private-session-marker",
      parent_session_id: "private-parent-marker",
      access_token_sha256: "private-token-hash-marker",
      response_metadata: {
        trace: { primary_trace_id: "private-trace-marker" },
      },
      client_ip: "private-ip-marker",
      auth_provider_snapshot: "devin",
    };
    const [row] = buildViewerMonitoringEventRows([event], []);
    expect(row).toMatchObject({
      model: "analytics-model",
      requestedModel: "public-alias(high)",
      resolvedModel: "routed-model",
      responseModel: "observed-response-model",
      reasoningEffort: "high",
      serviceTier: "priority",
      requestServiceTier: "priority",
      responseServiceTier: "default",
      generate: true,
      stream: false,
      provider: "devin",
    });
    expect(row.searchText).toContain("observed-response-model");
    expect(row.searchText).toContain("default");
    const serialized = JSON.stringify(row);
    expect(serialized).not.toContain("private-");
    expect(serialized).not.toContain("sessionId");
    expect(serialized).not.toContain("accessToken");
    expect(row.responseMetadata).toBeUndefined();
  });

  it.each([
    { generate: undefined, stream: undefined },
    { generate: null, stream: null },
    { generate: "true", stream: "false" },
    { generate: 1, stream: 0 },
  ])("keeps missing or invalid request flags unknown: %j", (flags) => {
    const event = {
      event_hash: "view_event000001",
      timestamp_ms: 1_720_000_000_000,
      service_tier: "priority",
      ...flags,
    } as unknown as ViewerMonitoringEvent;
    const [row] = buildViewerMonitoringEventRows([event], []);
    expect(row.generate).toBeUndefined();
    expect(row.stream).toBeUndefined();
    expect(row.responseModel).toBeUndefined();
    expect(row.requestServiceTier).toBeUndefined();
    expect(row.responseServiceTier).toBeUndefined();
    expect(row.serviceTier).toBe("priority");
  });

  it("uses explicit tier evidence for fallback costs without replacing server costs", () => {
    const base = {
      event_hash: "view_event000001",
      timestamp_ms: 1_720_000_000_000,
      model: "priced-model",
      input_tokens: 1_000_000,
      request_service_tier: "priority",
      response_service_tier: "default",
    };
    const prices = {
      "priced-model": {
        prompt: 1,
        completion: 1,
        cache: 0,
        serviceTiers: [
          {
            mode: "priority",
            serviceTier: "priority",
            prompt: 2,
            completion: 2,
            cache: 0,
            promptConfigured: true,
          },
        ],
      },
    };
    const [codex] = buildViewerMonitoringEventRows(
      [{ ...base, auth_provider_snapshot: "codex" }],
      [],
      prices,
    );
    const [api] = buildViewerMonitoringEventRows(
      [{ ...base, auth_provider_snapshot: "openai" }],
      [],
      prices,
    );
    const [priced] = buildViewerMonitoringEventRows(
      [{ ...base, auth_provider_snapshot: "codex", cost: 0.25 }],
      [],
      prices,
    );
    expect(codex.totalCost).toBe(2);
    expect(api.totalCost).toBe(1);
    expect(priced.totalCost).toBe(0.25);
  });

  it("keeps Viewer aliases while restoring realtime performance columns", () => {
    const rows = buildViewerMonitoringEventRows(
      [
        {
          event_hash: "view_event000001",
          timestamp_ms: 1_720_000_000_000,
          model: "legacy-model",
          analytics_model: "gpt-test",
          requested_model: "gpt-test(high)",
          resolved_model: "gpt-test-resolved",
          api_key_id: "view_0123456789ab",
          account_display: "lao***@example.com",
          source_display: "Codex Account",
          auth_provider_snapshot: "codex",
          reasoning_effort: "high",
          service_tier: "priority",
          method: "POST",
          path: "/v1/responses",
          output_tokens: 20,
          total_tokens: 30,
          latency_ms: 2_000,
          ttft_ms: 300,
          cost: 0.12,
          failed: false,
        },
      ],
      [{ id: "view_0123456789ab", alias: "马哥" }],
    );

    expect(rows[0]).toMatchObject({
      account: "lao***@example.com",
      apiKeyLabel: "马哥",
      reasoningEffort: "high",
      serviceTier: "priority",
      model: "gpt-test",
      requestedModel: "gpt-test(high)",
      tokensPerSecond: 10,
      ttftMs: 300,
      totalCost: 0.12,
    });
    expect(rows[0].apiKeyHash).toBe("view_0123456789ab");
    expect(rows[0].headerTraceId).toBe("");
    expect(rows[0].clientIp).toBeUndefined();
    expect(rows[0].xForwardedFor).toBeUndefined();
    expect(rows[0].userAgent).toBeUndefined();
  });

  it("uses aliases in the original API key summary row shape", () => {
    const rows = buildViewerMonitoringApiKeyRows(
      [
        {
          id: "view_0123456789ab",
          api_key_id: "view_0123456789ab",
          calls: 2,
          success_calls: 2,
          failure_calls: 0,
          success_rate: 1,
          total_tokens: 10,
          cost: 0.01,
        },
      ],
      [{ id: "view_0123456789ab", alias: "牛哥" }],
    );

    expect(rows[0].apiKeyLabel).toBe("牛哥");
    expect(rows[0].apiKeyHash).toBe("view_0123456789ab");
  });

  it("keeps provider-scoped account rows distinct", () => {
    const rows = buildViewerMonitoringAccountRows([
      {
        id: "view_codex0000001",
        account_id: "view_shared000001",
        account_display: "same@example.com",
        auth_provider_snapshot: "codex",
        auth_ids: ["view_auth0000001"],
      },
      {
        id: "view_xai000000001",
        account_id: "view_shared000001",
        account_display: "same@example.com",
        auth_provider_snapshot: "xai",
        auth_ids: ["view_auth0000002"],
      },
    ]);

    expect(rows.map((row) => row.id)).toEqual([
      "view_codex0000001",
      "view_xai000000001",
    ]);
    expect(rows.map((row) => row.provider)).toEqual(["codex", "xai"]);
    expect(rows.map((row) => row.authIndices)).toEqual([
      ["view_auth0000001"],
      ["view_auth0000002"],
    ]);
  });

  it("keeps synthetic API key statistics visible but unselectable", () => {
    const rows = buildViewerMonitoringApiKeyRows(
      [
        {
          id: "view_unknown00001",
          api_key_id: "view_unknown00001",
          api_key_selectable: false,
          calls: 4,
        },
      ],
      [],
    );
    expect(rows[0]).toMatchObject({
      id: "view_unknown00001",
      apiKeyHash: "",
      apiKeyLabel: "未识别 Key",
      isUnknown: true,
      totalCalls: 4,
    });
  });

  it("keeps a historical inline alias when it is absent from the active alias list", () => {
    const summaryRows = buildViewerMonitoringApiKeyRows(
      [
        {
          id: "view_437145000000",
          api_key_id: "view_437145000000",
          api_key_alias: "牛哥",
          calls: 1,
        },
      ],
      [],
    );
    const eventRows = buildViewerMonitoringEventRows(
      [
        {
          event_hash: "view_historical001",
          timestamp_ms: 1_720_000_000_000,
          api_key_id: "view_437145000000",
          api_key_alias: "牛哥",
          model: "gpt-test",
          failed: false,
        },
      ],
      [],
    );

    expect(summaryRows[0].apiKeyLabel).toBe("牛哥");
    expect(eventRows[0].apiKeyLabel).toBe("牛哥");
    expect(findViewerApiKeyIdsByAlias(summaryRows, "牛哥")).toEqual([
      "view_437145000000",
    ]);
    expect(
      resolveViewerApiKeySearchScope(summaryRows, "  牛哥  ", "all"),
    ).toEqual({
      apiKeyIds: ["view_437145000000"],
      searchQuery: "",
    });
    expect(
      resolveViewerApiKeySearchScope(summaryRows, "牛哥", "view_aaaaaaaaaaaa"),
    ).toEqual({
      apiKeyIds: ["view_aaaaaaaaaaaa"],
      searchQuery: "牛哥",
    });
  });

  it("calculates realtime cost from the read-only model price contract", () => {
    const rows = buildViewerMonitoringEventRows(
      [
        {
          event_hash: "view_event000002",
          timestamp_ms: 1_720_000_000_000,
          model: "gpt-priced",
          input_tokens: 10,
          output_tokens: 20,
          total_tokens: 30,
          failed: false,
        },
      ],
      [],
      {
        "gpt-priced": {
          prompt: 2,
          completion: 4,
          cache: 1,
          promptConfigured: true,
          completionConfigured: true,
        },
      },
    );

    expect(rows[0].totalCost).toBeCloseTo(0.0001, 8);
  });

  it("deduplicates cursor pages by safe event id", () => {
    const first = { event_hash: "event-a", timestamp_ms: 2, model: "a" };
    const second = { event_hash: "event-b", timestamp_ms: 1, model: "b" };
    expect(mergeViewerMonitoringEvents([first], [first, second])).toEqual([
      first,
      second,
    ]);
  });
});

describe("Viewer instance separation", () => {
  const instances = [
    { instance_id: "view_111111111111", instance_name: "CPA 一号" },
    { instance_id: "view_222222222222", instance_name: "CPA 二号" },
  ];

  it("keeps identical account names distinct and masks their visible and hover labels", () => {
    const rows = buildViewerMonitoringAccountRows(
      instances.map((instance, index) => ({
        ...instance,
        id: `view_00000000000${index}`,
        account_display: "reader@example.com",
        auth_provider_snapshot: "codex",
        source_ids: [`view_33333333333${index}`],
      })),
    );
    expect(new Set(rows.map((row) => row.id)).size).toBe(2);
    expect(rows.map((row) => row.accountMasked)).toEqual([
      "rea***@example.com · CPA 一号",
      "rea***@example.com · CPA 二号",
    ]);
    rows.forEach((row) => {
      const display = resolveAccountDisplayText(row, "masked");
      expect(display.title).not.toContain("reader@example.com");
      expect(display.title).toContain(row.instance_name);
    });
    const events = buildViewerMonitoringEventRows(
      instances.map((instance, index) => ({
        ...instance,
        event_hash: `view_00000000000${index}`,
        timestamp_ms: 1720000000000,
        account_display: "reader@example.com",
        source_id: `view_33333333333${index}`,
        auth_provider_snapshot: "codex",
      })),
      [],
    );
    const t = ((key: string) => key) as TFunction;
    events.forEach((row, index) => {
      const display = buildRealtimeSourceDisplay(row, t, "masked");
      expect(JSON.stringify(display)).toContain(instances[index].instance_name);
      expect(JSON.stringify(display)).not.toContain("reader@example.com");
      expect(row.sourceIdentity).toBe(`view_33333333333${index}`);
    });
  });
});

describe("Viewer API Key dropdown fallback", () => {
  it("lists aliases even when selector statistics are empty and retains both instances", () => {
    const rows = buildViewerApiKeyFilterRows(
      [
        { id: "view_111111111111", alias: "Shared", instance_name: "CPA 一号" },
        { id: "view_222222222222", alias: "Shared", instance_name: "CPA 二号" },
        { id: "a".repeat(64), alias: "raw hash must stay hidden" },
      ],
      { filter_options: { api_key_stats: [] } },
    );
    expect(rows.map((row) => row.apiKeyLabel)).toEqual([
      "Shared · CPA 一号",
      "Shared · CPA 二号",
    ]);
    expect(resolveViewerApiKeySearchScope(rows, "Shared", "all")).toEqual({
      apiKeyIds: ["view_111111111111", "view_222222222222"],
      searchQuery: "",
    });
    expect(
      resolveViewerApiKeySearchScope(rows, "Shared · CPA 二号", "all")
        .apiKeyIds,
    ).toEqual(["view_222222222222"]);
  });

  it("uses legacy selectors and events but never makes an unknown client selectable", () => {
    const rows = buildViewerApiKeyFilterRows([], {
      filter_selectors: {
        api_key_stats: [
          { id: "view_111111111111", api_key_alias: "Selector key" },
        ],
      },
      events: {
        items: [
          {
            event_hash: "event-a",
            timestamp_ms: 1,
            api_key_id: "view_222222222222",
            api_key_alias: "Event key",
          },
          {
            event_hash: "event-b",
            timestamp_ms: 2,
            api_key_id: "view_333333333333",
            api_key_selectable: false,
          },
        ],
      },
    });
    expect(rows.map((row) => row.id)).toEqual([
      "view_111111111111",
      "view_222222222222",
    ]);
  });

  it("matches quota only inside the account's instance even when the email is identical", () => {
    const [row] = buildViewerMonitoringAccountRows([
      {
        id: "view_111111111111",
        instance_id: "instance-a",
        instance_name: "CPA A",
        account_display: "reader@example.com",
        auth_provider_snapshot: "codex",
      },
    ]);
    const quotas = ["instance-a", "instance-b"].map((instance_id) => ({
      id: instance_id,
      instance_id,
      provider: "codex",
      display_name: "reader@example.com",
      status: "active",
      windows: [],
    }));
    expect(findViewerQuotaAccounts(row, quotas)).toEqual([quotas[0]]);
    expect(findViewerQuotaAccounts(row, [quotas[1]])).toEqual([]);
  });
});

it("does not price a different instance with the primary instance's model prices", () => {
  const event = {
    event_hash: "event-cost",
    timestamp_ms: 1720000000000,
    instance_id: "instance-b",
    model: "priced",
    input_tokens: 1000000,
  };
  const prices = { priced: { prompt: 99, completion: 99, cache: 99 } };
  const [unknown] = buildViewerMonitoringEventRows([event], [], prices);
  const [known] = buildViewerMonitoringEventRows(
    [{ ...event, cost: 3 }],
    [],
    prices,
  );
  expect(unknown.costKnown).toBe(false);
  expect(unknown.totalCost).not.toBe(99);
  expect(known.costKnown).toBe(true);
  expect(known.totalCost).toBe(3);
});

it("keeps status histories separate even when two instances have the same public name", () => {
  const rows = buildViewerMonitoringEventRows(
    [1, 2].map((index) => ({
      event_hash: `event-${index}`,
      timestamp_ms: 1720000000000 + index,
      account_id: `view_00000000000${index}`,
      account_display: "reader@example.com",
      instance_name: "Same CPA",
      instance_id: `instance-${index}`,
      model: "same-model",
      auth_provider_snapshot: "codex",
      total_tokens: 10,
      failed: index === 1,
    })),
    [],
  );
  const history = buildViewerRealtimeLogRows(rows);
  expect(new Set(history.map((row) => row.streamKey)).size).toBe(2);
  expect(history.map((row) => row.requestCount)).toEqual([1, 1]);
  expect(history.map((row) => row.successRate)).toEqual([1, 0]);
});

it("prices missing event cost from its own instance only", () => {
  const event = {
    event_hash: "event-priced",
    timestamp_ms: 1720000000000,
    instance_id: "instance-b",
    model: "priced",
    input_tokens: 1000000,
  };
  const [row] = buildViewerMonitoringEventRows(
    [event],
    [],
    { priced: { prompt: 99, completion: 99, cache: 0 } },
    { "instance-b": { priced: { prompt: 2, completion: 2, cache: 0 } } },
  );
  expect(row.totalCost).toBe(2);
  expect(row.costKnown).toBe(true);
});

it("preserves unavailable federated averages and rates instead of manufacturing zeros", () => {
  const summary = buildViewerMonitoringSummary({
    summary: {
      success_rate: null,
      avg_daily_requests: null,
      avg_daily_tokens: null,
      approx_task_success_rate: null,
      cache_hit_rate: null,
    },
  });
  expect(summary).toMatchObject({
    successRate: null,
    avgDailyRequests: null,
    avgDailyTokens: null,
    approxTaskSuccessRate: null,
    cacheHitRate: null,
  });
  const t = ((key: string) => key) as TFunction;
  const primary = buildViewerPrimarySummaryCards({
    summary,
    t,
    locale: "en",
    accountCount: 2,
    failedGroupCount: 0,
    hasPrices: true,
  });
  expect(primary.find((card) => card.icon === "success")?.value).toBe("--");
  expect(
    buildViewerSecondarySummaryCards(summary, "en", t).find(
      (card) => card.icon === "cache",
    )?.meta,
  ).toContain("--");
});

it("reports partial event page failure while retaining the current events and allows recovery", () => {
  const current = [
    { event_hash: "view_111111111111", timestamp_ms: 1720000000000 },
  ];
  const response = {
    partial_failed: true,
    warnings: ["CPA 二号暂不可用"],
    events: {
      items: [],
      has_more: true,
      next_cursor: "retry-cursor",
    },
  };
  expect(mergeViewerMonitoringEvents(current, response.events.items)).toEqual(
    current,
  );
  expect(viewerEventPageFeedback(response)).toBe("CPA 二号暂不可用");
  expect(viewerEventPageFeedback({ events: { items: current } })).toBe("");
});
