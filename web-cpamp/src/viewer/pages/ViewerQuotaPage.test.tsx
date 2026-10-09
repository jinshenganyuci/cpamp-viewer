import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ViewerQuotaAccount,
  ViewerQuotaResponse,
  ViewerQuotaWindow,
} from "@/viewer/model/viewerTypes";
import { formatViewerCodexWindowLabel } from "@/viewer/model/viewerQuota";
import { ViewerQuotaPage } from "./ViewerQuotaPage";

(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

const { mock } = vi.hoisted(() => ({
  mock: {
    data: { accounts: [] } as ViewerQuotaResponse,
    refresh: vi.fn(),
  },
}));

vi.mock("@/viewer/hooks/useViewerData", () => ({
  useViewerQuota: () => ({
    data: mock.data,
    loading: false,
    error: "",
    refresh: mock.refresh,
  }),
}));
vi.mock("@/components/ui/Select", () => ({
  Select: ({ ariaLabel, value }: { ariaLabel?: string; value: string }) => (
    <select aria-label={ariaLabel} value={value} onChange={() => undefined}>
      <option value={value}>{value}</option>
    </select>
  ),
}));

const older = Date.UTC(2026, 8, 8, 1);
const newer = Date.UTC(2026, 8, 9, 1);

function windowFixture(
  overrides: Partial<ViewerQuotaWindow>,
): ViewerQuotaWindow {
  return {
    id: "codex-main-primary",
    label: "5 小时额度",
    pool: "codex_main",
    remaining_percent: 75,
    used_percent: 25,
    window_minutes: 300,
    observed_at_ms: older,
    reset_at_ms: newer + 5 * 60 * 60 * 1000,
    ...overrides,
  };
}

describe("Viewer quota pool and duration display", () => {
  let renderer: ReactTestRenderer | undefined;

  async function render(
    windows: ViewerQuotaWindow[],
    account: Partial<ViewerQuotaAccount> = {},
  ) {
    mock.data = {
      accounts: [
        {
          id: "view_account",
          provider: "codex",
          display_name: "测试账号",
          plan: "Pro",
          status: "active",
          updated_at_ms: newer,
          windows,
          ...account,
        },
      ],
    };
    await act(async () => {
      renderer = create(<ViewerQuotaPage />);
    });
    return JSON.stringify(renderer?.toJSON());
  }

  beforeEach(() => {
    mock.refresh.mockClear();
  });
  afterEach(async () => {
    if (renderer)
      await act(async () => {
        renderer?.unmount();
      });
    renderer = undefined;
  });

  it("keeps main 7D and Spark 5H/7D in separate groups within one account", async () => {
    await render([
      windowFixture({
        id: "main-weekly",
        window_minutes: 10080,
        label: "周额度",
        remaining_percent: 64,
      }),
      windowFixture({
        id: "spark-five-hour",
        pool: "codex_spark",
        remaining_percent: 12,
        observed_at_ms: newer,
      }),
      windowFixture({
        id: "spark-weekly",
        pool: "codex_spark",
        window_minutes: 10080,
        remaining_percent: 5,
        observed_at_ms: newer,
      }),
    ]);
    expect(renderer?.root.findAllByType("article")).toHaveLength(1);
    const main = renderer!.root.findByProps({ "aria-label": "普通 Codex" });
    const spark = renderer!.root.findByProps({ "aria-label": "Spark" });
    expect(
      main
        .findAllByProps({ role: "progressbar" })
        .map((bar) => [bar.props["aria-label"], bar.props["aria-valuenow"]]),
    ).toEqual([["7D 额度", 64]]);
    expect(
      spark
        .findAllByProps({ role: "progressbar" })
        .map((bar) => [bar.props["aria-label"], bar.props["aria-valuenow"]]),
    ).toEqual([
      ["5H 额度", 12],
      ["7D 额度", 5],
    ]);
    expect(main.findByType("time").props.dateTime).toBe(
      new Date(older).toISOString(),
    );
    expect(
      spark.findAllByType("time").map((time) => time.props.dateTime),
    ).toEqual([new Date(newer).toISOString(), new Date(newer).toISOString()]);
  });

  it("uses duration even when primary/secondary IDs and labels claim the opposite", async () => {
    await render([
      windowFixture({
        id: "primary",
        label: "5 小时额度",
        window_minutes: 10080,
        remaining_percent: 90,
      }),
      windowFixture({
        id: "secondary",
        label: "周额度",
        window_minutes: 300,
        remaining_percent: 30,
      }),
    ]);
    expect(
      renderer!.root
        .findAllByProps({ role: "progressbar" })
        .map((bar) => [bar.props["aria-label"], bar.props["aria-valuenow"]]),
    ).toEqual([
      ["7D 额度", 90],
      ["5H 额度", 30],
    ]);
  });

  it("does not invent a 5H quota or another pool for a Pro account with only 7D evidence", async () => {
    const text = await render([
      windowFixture({
        id: "main-weekly",
        label: "周额度",
        window_minutes: 10080,
      }),
    ]);
    expect(text).toContain("7D 额度");
    expect(text).not.toContain("5H 额度");
    expect(text).not.toContain("Spark");
    expect(renderer!.root.findAllByProps({ role: "progressbar" })).toHaveLength(
      1,
    );
  });

  it("shows missing snapshot evidence without inventing zero or full quota", async () => {
    const text = await render([]);
    expect(text).toContain("暂无已记录的额度快照");
    expect(text).not.toContain("额度池未确认");
    expect(renderer!.root.findAllByProps({ role: "progressbar" })).toHaveLength(
      0,
    );
  });

  it("does not guess a legacy pool or unknown duration from a name or internal ID", async () => {
    const text = await render([
      windowFixture({
        id: "spark-secondary",
        label: "Spark 周额度",
        pool: undefined,
        window_minutes: undefined,
        observed_at_ms: undefined,
      }),
      windowFixture({
        id: "private-primary",
        label: "Bearer must-not-display",
        pool: "unknown",
        window_minutes: Number.NaN,
        observed_at_ms: undefined,
      }),
      windowFixture({
        id: "review",
        pool: "codex_review",
        window_minutes: 10080,
      }),
    ]);
    const unknown = renderer!.root.findByProps({
      "aria-label": "额度池未确认",
    });
    expect(
      unknown
        .findAllByProps({ role: "progressbar" })
        .map((bar) => bar.props["aria-label"]),
    ).toEqual(["额度窗口（时长未确认）", "额度窗口（时长未确认）"]);
    expect(unknown.findAllByType("time")).toHaveLength(0);
    expect(text).toContain("观测时间未记录");
    expect(text).toContain("代码审查");
    for (const raw of [
      "spark-secondary",
      "private-primary",
      "Bearer must-not-display",
      "Spark 周额度",
    ])
      expect(text).not.toContain(raw);
  });

  it("retains positive small percentages and does not round consumed quota to full", async () => {
    await render([
      windowFixture({ id: "small", remaining_percent: 0.2 }),
      windowFixture({ id: "empty", remaining_percent: 0 }),
      windowFixture({ id: "almost-full", remaining_percent: 99.99 }),
      windowFixture({ id: "full", remaining_percent: 100 }),
    ]);
    expect(
      renderer!.root
        .findAllByProps({ role: "progressbar" })
        .map((bar) => bar.props["aria-valuetext"]),
    ).toEqual(["<1%", "0%", "99%", "100%"]);
  });

  it.each([
    [40320, "28D 月额度"],
    [43200, "30D 月额度"],
    [44640, "31D 月额度"],
    [2880, "2 天额度"],
    [120, "2 小时额度"],
    [90, "90 分钟额度"],
    [0.5, "0.5 分钟额度"],
  ] as Array<[number, string]>)(
    "labels a real %s-minute window without guessing its primary/secondary role",
    (minutes, expected) => {
      expect(
        formatViewerCodexWindowLabel(
          windowFixture({ window_minutes: minutes }),
        ),
      ).toBe(expected);
    },
  );

  it("preserves non-Codex window presentation", async () => {
    const text = await render(
      [windowFixture({ label: "Claude 专用额度", pool: undefined })],
      { provider: "claude", plan: "Max" },
    );
    expect(text).toContain("Claude 专用额度");
    expect(text).not.toContain("额度池未确认");
    expect(text).not.toContain("普通 Codex");
    expect(text).not.toContain("观测时间未记录");
  });

  it.each(["meta", "muse", "devin", "xai"])(
    "keeps known and unknown windows for %s without inventing a percentage",
    async (provider) => {
      const text = await render(
        [
          windowFixture({
            id: "daily",
            pool: undefined,
            label: "日额度",
            window_minutes: 1440,
            remaining_percent: 0,
            used_percent: 100,
          }),
          windowFixture({
            id: "weekly",
            pool: undefined,
            label: "周额度",
            window_minutes: undefined,
            remaining_percent: null,
            used_percent: null,
            stale: true,
          }),
          windowFixture({
            id: "model-specific",
            pool: undefined,
            label: "模型额度",
            window_minutes: undefined,
            remaining_percent: 99.99,
            used_percent: null,
            model_scope: "model-family-a",
          }),
        ],
        { provider },
      );
      const card = renderer!.root.findByType("article");
      expect(
        card
          .findAllByProps({ role: "progressbar" })
          .map((bar) => bar.props["aria-valuetext"]),
      ).toEqual(["0%", "99%"]);
      const unknown = card.findByProps({ "aria-label": "剩余额度未知" });
      expect(unknown.children).toEqual(["--"]);
      expect(text).toContain("周额度");
      expect(text).toContain("上次快照，等待更新");
      expect(text).toContain("model-family-a");
      expect(card.findAllByType("time")).toHaveLength(3);
      expect(text).not.toContain("100%");
      expect(text).not.toContain("普通 Codex");
      expect(card.findAllByType("button")).toHaveLength(0);
      expect(card.findAllByType("input")).toHaveLength(0);
    },
  );

  it.each(["meta", "devin", "xai"])(
    "does not synthesize daily, weekly, or monthly windows when %s has no observations",
    async (provider) => {
      const text = await render([], { provider, plan: undefined });
      expect(text).toContain("暂无已记录的额度快照");
      expect(text).not.toContain("月额度");
      expect(text).not.toContain("周额度");
      expect(text).not.toContain("日额度");
      expect(
        renderer!.root.findAllByProps({ role: "progressbar" }),
      ).toHaveLength(0);
    },
  );

  it("groups legacy Muse and Meta accounts together and keeps Devin separate", async () => {
    mock.data = {
      accounts: ["muse", "META", "devin"].map((provider, index) => ({
        id: `view_account_${index}`,
        provider,
        display_name: `账号${index}`,
        status: "active",
        windows: [],
      })),
    };
    await act(async () => {
      renderer = create(<ViewerQuotaPage />);
    });
    const text = JSON.stringify(renderer?.toJSON());
    expect(text.match(/Meta \/ Muse 额度/g)).toHaveLength(1);
    expect(text.match(/Devin 额度/g)).toHaveLength(1);
    expect(text).not.toContain("muse 额度");
    expect(renderer!.root.findAllByType("article")).toHaveLength(3);
    expect(text.match(/刷新额度/g)).toHaveLength(2);
  });

  it("shows both account sources in the existing quota cards", async () => {
    mock.data = {
      warnings: ["Sub2API 额度暂不可用"],
      accounts: [
        {
          id: "view_cpamp",
          source: "cpamp",
          provider: "claude",
          display_name: "CPA 账号",
          status: "active",
          windows: [],
        },
        {
          id: "view_sub2api",
          source: "sub2api",
          provider: "claude",
          display_name: "Sub2API 账号",
          status: "active",
          windows: [],
        },
      ],
    };
    await act(async () => {
      renderer = create(<ViewerQuotaPage />);
    });
    const text = JSON.stringify(renderer?.toJSON());
    expect(renderer?.root.findAllByType("article")).toHaveLength(2);
    expect(text).toContain("CPA Manager Plus");
    expect(text).toContain("Sub2API");
    expect(text).toContain("Sub2API 额度暂不可用");
  });

  it("shows the original plan and masks both account emails", async () => {
    mock.data = {
      accounts: [
        {
          id: "view_cpamp",
          source: "cpamp",
          provider: "codex",
          display_name: "cpa@example.test",
          plan: "pro",
          status: "active",
          windows: [],
        },
        {
          id: "view_sub2api",
          source: "sub2api",
          provider: "codex",
          display_name: "sub@example.test",
          plan: "pro",
          status: "active",
          windows: [],
        },
      ],
    };
    await act(async () => {
      renderer = create(<ViewerQuotaPage />);
    });
    const text = JSON.stringify(renderer?.toJSON());
    const plans = renderer?.root.findAll(
      (node) => node.type === "strong" && node.children.includes("pro"),
    );
    expect(plans).toHaveLength(2);
    expect(text).toContain("c***@example.test");
    expect(text).toContain("s***@example.test");
    expect(text).not.toContain("cpa@example.test");
    expect(text).not.toContain("sub@example.test");
  });

  it("escapes model scope labels instead of interpreting upstream text as markup", async () => {
    await render(
      [
        windowFixture({
          pool: undefined,
          model_scope: '<img src=x onerror="alert(1)">',
          label: "模型额度",
        }),
      ],
      { provider: "meta" },
    );
    const markup = renderToStaticMarkup(<ViewerQuotaPage />);
    expect(markup).toContain("&lt;img src=x onerror=&quot;alert(1)&quot;&gt;");
    expect(markup).not.toContain("<img");
  });

  it("adds no account management controls or active provider calls", async () => {
    const text = await render([windowFixture({})]);
    const card = renderer!.root.findByType("article");
    expect(card.findAllByType("button")).toHaveLength(0);
    expect(card.findAllByType("input")).toHaveLength(0);
    for (const forbidden of ["重置额度", "删除", "编辑", "/api-call"])
      expect(text).not.toContain(forbidden);
    expect(mock.refresh).not.toHaveBeenCalled();
  });
});
