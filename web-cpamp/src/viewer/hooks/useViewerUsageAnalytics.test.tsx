import { useEffect } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useViewerUsageAnalytics } from "./useViewerUsageAnalytics";

(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

vi.stubGlobal("window", {
  setTimeout: globalThis.setTimeout.bind(globalThis),
  clearTimeout: globalThis.clearTimeout.bind(globalThis),
});

const { mocks } = vi.hoisted(() => ({
  mocks: {
    aliasesRefresh: vi.fn<() => Promise<void>>(),
    analyticsRefresh: vi.fn<() => Promise<void>>(),
    analyticsRequests: [] as Array<{
      payload: Record<string, unknown>;
      options: Record<string, unknown>;
    }>,
    warnings: [] as string[],
    provideV112Data: false,
    coverage: undefined as Record<string, unknown> | undefined,
    previousScopeCoverage: undefined as Record<string, unknown> | undefined,
    headerRefresh: null as (() => Promise<void>) | null,
  },
}));

vi.mock("@/hooks/useHeaderRefresh", () => ({
  useHeaderRefresh: (handler: () => Promise<void>) => {
    mocks.headerRefresh = handler;
  },
}));

vi.mock("@/viewer/hooks/useViewerData", () => ({
  useViewerAliases: () => ({ aliases: [], refresh: mocks.aliasesRefresh }),
  useViewerAnalytics: (
    request: Record<string, unknown> | (() => Record<string, unknown>),
    options: Record<string, unknown> = {},
  ) => {
    const payload = typeof request === "function" ? request() : request;
    mocks.analyticsRequests.push({ payload, options });
    const include = (payload.include ?? {}) as Record<string, unknown>;
    let data: Record<string, unknown> | null = null;
    if (mocks.provideV112Data) {
      if (include.api_key_timeline) {
        data = {
          api_key_timeline: [
            {
              api_key_id: "view_0123456789ab",
              bucket_ms: 1_700_000_000_000,
              calls: 3,
            },
          ],
        };
      } else if (include.credential_timeline) {
        data = {
          credential_timeline: [
            {
              id: "view_c0ffee000001",
              bucket_ms: 1_700_000_000_000,
              calls: 3,
            },
          ],
        };
      } else if (include.credential_stats) {
        data = {
          credential_stats: [
            {
              id: "view_c0ffee000001",
              auth_file_id: "view_f11e00000001",
              auth_file_display: "credential-a.json",
              calls: 3,
            },
          ],
        };
      } else if (include.api_key_stats) {
        data = {
          api_key_stats: [
            {
              id: "view_0123456789ab",
              api_key_id: "view_0123456789ab",
              calls: 3,
            },
          ],
          timeline: [],
        };
      }
    }
    if (mocks.warnings.length) data = { ...data, warnings: mocks.warnings };
    const snapshotCoverage = mocks.previousScopeCoverage ?? mocks.coverage;
    if (snapshotCoverage) data = { ...data, coverage: snapshotCoverage };
    return {
      data,
      coverage: mocks.coverage,
      loading: false,
      error: "",
      lastRefreshedAt: null,
      refresh: mocks.analyticsRefresh,
    };
  },
}));

describe("useViewerUsageAnalytics refresh", () => {
  let renderer: ReactTestRenderer | null = null;
  let latestResult: ReturnType<typeof useViewerUsageAnalytics> | null = null;

  function Harness() {
    const result = useViewerUsageAnalytics();
    useEffect(() => {
      latestResult = result;
    }, [result]);
    return null;
  }

  beforeEach(() => {
    latestResult = null;
    mocks.aliasesRefresh.mockReset();
    mocks.analyticsRefresh.mockReset();
    mocks.analyticsRequests = [];
    mocks.provideV112Data = false;
    mocks.warnings = [];
    mocks.coverage = undefined;
    mocks.previousScopeCoverage = undefined;
    mocks.headerRefresh = null;
  });

  afterEach(() => {
    act(() => renderer?.unmount());
    renderer = null;
  });

  it("keeps successful source analytics visible while reporting source failure once", async () => {
    mocks.provideV112Data = true;
    mocks.warnings = ["CPA 二号用量暂不可用"];
    await act(async () => {
      renderer = create(
        <MemoryRouter>
          <Harness />
        </MemoryRouter>,
      );
    });
    expect(latestResult!.error).toBe("CPA 二号用量暂不可用");
    expect(latestResult!.enabled).toBe(true);
    expect(latestResult!.loading).toBe(false);
  });

  it("keeps page and header refresh pending until both Viewer requests finish", async () => {
    let resolveAliases!: () => void;
    let resolveAnalytics!: () => void;
    const aliasesRequest = new Promise<void>((resolve) => {
      resolveAliases = resolve;
    });
    const analyticsRequest = new Promise<void>((resolve) => {
      resolveAnalytics = resolve;
    });
    mocks.aliasesRefresh.mockReturnValue(aliasesRequest);
    mocks.analyticsRefresh.mockReturnValue(analyticsRequest);

    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={["/usage-analytics"]}>
          <Harness />
        </MemoryRouter>,
      );
      await Promise.resolve();
    });

    expect(latestResult).not.toBeNull();
    expect(latestResult!.coverage).toBeUndefined();
    let pageRefresh!: Promise<void>;
    act(() => {
      pageRefresh = latestResult!.refresh();
    });

    expect(mocks.aliasesRefresh).toHaveBeenCalledTimes(1);
    expect(mocks.analyticsRefresh).toHaveBeenCalledTimes(2);
    expect(mocks.headerRefresh?.()).toBe(pageRefresh);
    expect(mocks.aliasesRefresh).toHaveBeenCalledTimes(1);
    expect(mocks.analyticsRefresh).toHaveBeenCalledTimes(2);

    let settled = false;
    void pageRefresh.then(() => {
      settled = true;
    });
    await act(async () => {
      resolveAnalytics();
      await Promise.resolve();
    });
    expect(settled).toBe(false);

    await act(async () => {
      resolveAliases();
      await pageRefresh;
    });
    expect(settled).toBe(true);
  });

  it("uses current-request archive coverage supplied by the Viewer data hook", async () => {
    mocks.coverage = {
      scope: "time_range",
      mode: "mixed",
      raw_complete: false,
      core_aggregate_used: true,
      raw_deleted_event_count: 2,
      min_deleted_timestamp_ms: 1,
      max_deleted_timestamp_ms: 2,
      comparison_raw_deleted_event_count: 3,
      auxiliary_ranges: [
        {
          scope: "drilldown_preview",
          from_ms: 1,
          to_ms: 2,
          raw_deleted_event_count: 1,
        },
      ],
      fidelity_limitations: ["event_details_require_raw_events"],
    };

    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={["/usage-analytics"]}>
          <Harness />
        </MemoryRouter>,
      );
      await Promise.resolve();
    });

    expect(latestResult!.coverage).toEqual(mocks.coverage);
  });

  it("does not expose previous-scope coverage retained in the statistics snapshot", async () => {
    mocks.previousScopeCoverage = {
      scope: "time_range",
      mode: "mixed",
      raw_complete: false,
      core_aggregate_used: true,
      raw_deleted_event_count: 9,
      min_deleted_timestamp_ms: 1,
      max_deleted_timestamp_ms: 2,
      fidelity_limitations: [],
    };
    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={["/usage-analytics"]}>
          <Harness />
        </MemoryRouter>,
      );
      await Promise.resolve();
    });

    expect(latestResult!.coverage).toBeUndefined();
  });

  it("uses the v1.12 per-key and per-credential timeline contracts with Viewer IDs", async () => {
    mocks.provideV112Data = true;
    mocks.aliasesRefresh.mockResolvedValue();
    mocks.analyticsRefresh.mockResolvedValue();

    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={["/usage-analytics"]}>
          <Harness />
        </MemoryRouter>,
      );
      await Promise.resolve();
    });

    const apiKeyTimelineRequest = mocks.analyticsRequests.find(({ payload }) =>
      Boolean((payload.include as Record<string, unknown>)?.api_key_timeline),
    );
    expect(apiKeyTimelineRequest?.options.enabled).toBe(true);
    expect(apiKeyTimelineRequest?.payload.filters).toMatchObject({
      api_key_ids: ["view_0123456789ab"],
    });

    await act(async () => {
      latestResult!.setActiveTab("credentials");
      await Promise.resolve();
    });

    const credentialTimelineRequest = [...mocks.analyticsRequests]
      .reverse()
      .find(({ payload }) =>
        Boolean(
          (payload.include as Record<string, unknown>)?.credential_timeline,
        ),
      );
    expect(credentialTimelineRequest?.options.enabled).toBe(true);
    expect(credentialTimelineRequest?.payload.filters).toMatchObject({
      credential_ids: ["view_c0ffee000001"],
    });
    expect(credentialTimelineRequest?.payload.filters).not.toHaveProperty(
      "auth_file_snapshot",
    );
  });
});
