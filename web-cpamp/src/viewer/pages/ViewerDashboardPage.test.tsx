import { act } from "react";
import { create, type ReactTestRenderer } from "react-test-renderer";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { HealthAlertsSurface } from "@/features/dashboard/components/HealthAlertsSurface";
import { VersionCardSurface } from "@/features/dashboard/components/VersionCardSurface";
import { ViewerDashboardPage } from "./ViewerDashboardPage";

(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

vi.stubGlobal("window", {
  setInterval: globalThis.setInterval.bind(globalThis),
  clearInterval: globalThis.clearInterval.bind(globalThis),
});

const { mocks } = vi.hoisted(() => ({
  mocks: {
    dashboardRefresh: vi.fn<() => Promise<void>>(),
    headerRefresh: null as (() => Promise<void>) | null,
    warnings: [] as string[],
    instanceName: "东站",
  },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: "zh-CN" },
  }),
}));

vi.mock("@/hooks/useHeaderRefresh", () => ({
  useHeaderRefresh: (handler: () => Promise<void>) => {
    mocks.headerRefresh = handler;
  },
}));

vi.mock("@/viewer/hooks/useViewerData", () => ({
  useViewerDashboard: () => ({
    data: {
      warnings: mocks.warnings,
      generated_at_ms: Date.UTC(2026, 6, 14, 8, 0, 0),
      connection: { connected: true },
      system: { management_version: "2.0.0", server_version: "v1" },
      health: {
        usage_monitor: "ok",
        request_log: "ok",
        data_source: "usage",
        error_logs: 0,
      },
      stats: {
        management_keys: 3,
        auth_files: 9,
        available_models: 27,
        providers: { gemini: 1, codex: 2, claude: 3, openai: 4, total: 10 },
      },
      config: {
        debug: false,
        logging_to_file: true,
        request_retry: 2,
        ws_auth: true,
        routing_strategy: "round-robin",
      },
      collector: { events: 10, deadLetters: 0, collector: {} },
      today: {},
      rolling_30m: {},
      channel_health: [
        {
          id: "view_channel_1",
          account_display: "安全账号",
          instance_id: "instance_channel_hidden",
          instance_name: mocks.instanceName,
          account_snapshot: "raw-channel-secret",
          calls: 10,
          failures: 1,
          success_rate: 0.9,
          tone: "warn",
        },
      ],
      recent_failures: [
        {
          timestamp_ms: Date.UTC(2026, 6, 14, 7, 0, 0),
          model: "gpt-safe",
          api_key_id: "view_key_1",
          api_key_alias: "马哥",
          instance_id: "instance_failure_hidden",
          instance_name: "西站",
          account_display: "安全失败账号",
          account_snapshot: "raw-failure-secret",
          header_trace_id: "raw-trace-secret",
          duration_ms: 120,
          fail_status_code: 429,
        },
      ],
    },
    loading: false,
    error: "",
    refresh: mocks.dashboardRefresh,
  }),
}));

vi.mock("@/features/dashboard/components/UsageMetricsCard", () => ({
  UsageMetricsCard: () => <div data-card="usage" />,
}));

vi.mock("@/features/dashboard/components/TrafficOverviewCard", () => ({
  TrafficOverviewCard: () => <div data-card="traffic" />,
}));

describe("ViewerDashboardPage refresh", () => {
  beforeEach(() => {
    mocks.dashboardRefresh.mockReset();
    mocks.headerRefresh = null;
    mocks.warnings = [];
    mocks.instanceName = "东站";
  });

  it("keeps both page and header refresh pending until Viewer data is done", async () => {
    let resolveDashboard!: () => void;
    const dashboardRequest = new Promise<void>((resolve) => {
      resolveDashboard = resolve;
    });
    mocks.dashboardRefresh.mockReturnValue(dashboardRequest);

    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <MemoryRouter>
          <ViewerDashboardPage />
        </MemoryRouter>,
      );
    });

    const refreshButton = renderer.root
      .findAllByType("button")
      .find((button) => button.props["aria-label"] === "common.refresh");
    expect(refreshButton).toBeDefined();

    let pageRefresh!: Promise<void>;
    act(() => {
      pageRefresh = refreshButton?.props.onClick();
    });

    expect(mocks.dashboardRefresh).toHaveBeenCalledTimes(1);
    expect(refreshButton?.props.disabled).toBe(true);

    const headerRefresh = mocks.headerRefresh?.();
    expect(headerRefresh).toBe(pageRefresh);
    expect(mocks.dashboardRefresh).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveDashboard();
      await pageRefresh;
    });

    expect(refreshButton?.props.disabled).toBe(false);
    act(() => renderer.unmount());
  });

  it("reuses the upstream dashboard surfaces with only safe display rows", () => {
    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <MemoryRouter>
          <ViewerDashboardPage />
        </MemoryRouter>,
      );
    });

    expect(renderer.root.findAllByType(VersionCardSurface)).toHaveLength(1);
    expect(renderer.root.findAllByType(HealthAlertsSurface)).toHaveLength(1);

    const output = JSON.stringify(renderer.toJSON());
    expect(output).toContain("dashboard.system_overview");
    expect(output).toContain("dashboard.health_status");
    expect(output).toContain("dashboard.channel_health_status");
    expect(output).toContain("dashboard.recent_failed_requests");
    expect(output).toContain("dashboard.collector_status_title");
    expect(output).toContain("nav.config_management");
    expect(output).toContain("dashboard.manage");
    expect(output).toContain("dashboard.view_full_config");
    expect(output).toContain("安全账号");
    expect(output).toContain("马哥");
    const health = renderer.root.findByType(HealthAlertsSurface);
    expect(health.props.channelRows[0].label).toBe("东站 · 安全账号");
    expect(health.props.channelRows[0].title).toBe("东站 · 安全账号");
    expect(health.props.failureRows[0].source).toBe("西站 · 马哥");
    expect(health.props.failureRows[0].sourceTitle).toBe("西站 · 马哥");
    expect(output).not.toContain("instance_channel_hidden");
    expect(output).not.toContain("instance_failure_hidden");
    expect(output).not.toContain("raw-channel-secret");
    expect(output).not.toContain("raw-failure-secret");
    expect(output).not.toContain("raw-trace-secret");
    expect(output).not.toContain("dashboard.view_details");
    expect(output).not.toContain("dashboard.viewer_open_monitoring");

    act(() => renderer.unmount());
  });

  it("displays partial source failures while preserving available dashboard cards", () => {
    mocks.warnings = ["西站：统计暂不可用", "西站：统计暂不可用", "备用站 authorization: Bearer test-secret-value"];
    mocks.instanceName = "东站 authorization: Bearer instance-secret-value";
    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(<MemoryRouter><ViewerDashboardPage /></MemoryRouter>);
    });
    const output = JSON.stringify(renderer.toJSON());
    expect(output.match(/西站：统计暂不可用/g)).toHaveLength(1);
    expect(output).not.toContain("test-secret-value");
    expect(output).not.toContain("instance-secret-value");
    expect(renderer.root.findByType(HealthAlertsSurface).props.channelRows[0].title).toContain("[redacted]");
    expect(renderer.root.findAllByProps({ role: "status" })).toHaveLength(2);
    expect(renderer.root.findAllByType(VersionCardSurface)).toHaveLength(1);
    expect(renderer.root.findAllByType(HealthAlertsSurface)).toHaveLength(1);
    act(() => renderer.unmount());
  });
});
