import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UsageStatusResponse } from "@/viewer/api/usageStatus";
import { ViewerUsageStatusPage } from "./ViewerUsageStatusPage";

(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;
const mock = vi.hoisted(() => ({
  language: "zh-CN",
  headerRefresh: vi.fn(),
  result: {
    data: null as UsageStatusResponse | null,
    loading: false,
    refresh: vi.fn<() => Promise<void>>(),
  },
}));
vi.mock("@/stores/useLanguageStore", () => ({
  useLanguageStore: (select: (state: { language: string }) => unknown) =>
    select({ language: mock.language }),
}));
vi.mock("@/viewer/hooks/useViewerUsageStatus", () => ({
  useViewerUsageStatus: () => mock.result,
}));
vi.mock("@/hooks/useHeaderRefresh", () => ({
  useHeaderRefresh: (handler: () => Promise<void>) =>
    mock.headerRefresh(handler),
}));

const snapshot = (): UsageStatusResponse => ({
  available: true,
  stale: false,
  checked_at_ms: 1_790_414_400_000,
  status: {
    raw_event_count: 1200,
    raw_archived_event_count: 800,
    raw_deleted_event_count: 600,
    raw_min_timestamp_ms: 1_790_000_000_000,
    raw_max_timestamp_ms: 1_790_400_000_000,
    migration_ready: true,
    hourly_aggregate_ready: false,
    storage: {
      database_bytes: 1048576,
      wal_bytes: 1024,
      shm_bytes: 0,
      total_bytes: 1049600,
      reclaimable_bytes: 524288,
    },
    active_operation: "archive",
    active_status: "archiving",
  },
});

describe("readonly usage status page", () => {
  let renderer: ReactTestRenderer | undefined;
  async function render() {
    await act(async () => {
      renderer = create(<ViewerUsageStatusPage />);
    });
    return JSON.stringify(renderer?.toJSON());
  }
  beforeEach(() => {
    mock.language = "zh-CN";
    mock.result = {
      data: snapshot(),
      loading: false,
      refresh: vi.fn().mockResolvedValue(undefined),
    };
    mock.headerRefresh.mockReset();
  });
  afterEach(async () => {
    if (renderer)
      await act(async () => {
        renderer?.unmount();
      });
    renderer = undefined;
  });

  it("shows projected counts, range, storage and readiness with only a refresh control", async () => {
    Object.assign(mock.result.data!.status!, {
      manifest_file: "/private/file",
      access_token: "private-token",
      id: "private-id",
    });
    const output = await render();
    for (const expected of [
      "1,200",
      "800",
      "600",
      "1 MiB",
      "512 KiB",
      "小时聚合",
      "尚未就绪",
      "正在归档",
      "已归档的在线明细",
      "已包含在在线明细总数中，不含已清理的记录",
      "不能直接相加",
      "不会自动缩小",
    ])
      expect(output).toContain(expected);
    for (const hidden of [
      "private",
      "导入",
      "导出",
      "设置",
      "确认清理",
      "管理密钥",
    ])
      expect(output).not.toContain(hidden);
    expect(renderer?.root.findAllByType("button")).toHaveLength(1);
    expect(renderer?.root.findAllByType("a")).toHaveLength(0);
    expect(renderer?.root.findAllByType("time")).toHaveLength(3);
    expect(mock.headerRefresh).toHaveBeenLastCalledWith(mock.result.refresh);
    await act(async () => {
      renderer?.root.findByType("button").props.onClick();
    });
    expect(mock.result.refresh).toHaveBeenCalledTimes(1);
  });

  it.each([
    null,
    { available: false, stale: false },
    { available: true, stale: false },
  ])(
    "shows unavailable rather than fabricated zero cards for %j",
    async (data) => {
      mock.result.data = data;
      const output = await render();
      expect(output).toContain("不支持用量状态，或暂时无法读取");
      expect(output).not.toContain("0 B");
      expect(renderer?.root.findAllByType("article")).toHaveLength(0);
    },
  );

  it("distinguishes initial loading and disables the refresh button", async () => {
    mock.result.data = null;
    mock.result.loading = true;
    expect(await render()).toContain("正在读取用量状态");
    expect(renderer?.root.findByType("button").props.disabled).toBe(true);
  });

  it("marks retained data stale and keeps the actual check timestamp", async () => {
    mock.result.data!.stale = true;
    const output = await render();
    expect(output).toContain("上次检查结果");
    expect(output).toContain("1,200");
    const times = renderer?.root.findAllByType("time") ?? [];
    expect(times[times.length - 1]?.props.dateTime).toBe(
      new Date(mock.result.data!.checked_at_ms!).toISOString(),
    );
  });

  it("keeps unknown optional values separate from an explicitly empty online history", async () => {
    const status = mock.result.data!.status!;
    delete status.raw_archived_event_count;
    delete status.raw_min_timestamp_ms;
    delete status.raw_max_timestamp_ms;
    delete status.active_operation;
    delete status.active_status;
    const output = await render();
    expect(output).toContain("未知");
    expect(output).toContain("当前没有维护任务");
    expect(output).not.toContain("当前没有在线明细");
    status.raw_event_count = 0;
    await act(async () => {
      renderer?.update(<ViewerUsageStatusPage />);
    });
    expect(JSON.stringify(renderer?.toJSON())).toContain("当前没有在线明细");
  });

  it("keeps incomplete combined counts unknown and shows independent instance status", async () => {
    const local = snapshot().status!;
    mock.result.data = {
      ...snapshot(),
      partial_failed: true,
      warnings: ["CPA 二号 用量状态暂不可用"],
      status: {
        raw_event_count: null,
        raw_archived_event_count: null,
        raw_deleted_event_count: null,
        raw_min_timestamp_ms: null,
        raw_max_timestamp_ms: null,
        migration_ready: null,
        hourly_aggregate_ready: null,
        storage: {
          database_bytes: null,
          wal_bytes: null,
          shm_bytes: null,
          total_bytes: null,
          reclaimable_bytes: null,
        },
      },
      instances: [
        {
          instance_id: "one",
          instance_name: "CPA 一号",
          available: true,
          stale: false,
          status: local,
        },
        {
          instance_id: "two",
          instance_name: "CPA 二号",
          available: false,
          stale: false,
        },
      ],
    };
    const output = await render();
    expect(output).toContain("部分实例暂不可用，汇总数据不完整");
    expect(output).toContain("CPA 二号 用量状态暂不可用");
    expect(output).toContain("各实例状态");
    expect(output).toContain("CPA 一号");
    expect(output).toContain("CPA 二号");
    expect(output).toContain("1,200");
    expect(output).toContain("正在归档");
    expect(output).not.toContain("当前没有在线明细");
    const metrics = renderer!.root.findAll(
      (node) =>
        node.type === "p" && node.props.className?.includes("metricValue"),
    );
    expect(metrics.map((node) => node.children.join(""))).toEqual([
      "未知",
      "未知",
      "未知",
    ]);
    const failed = renderer!.root
      .findAllByType("article")
      .find((node) =>
        node
          .findAllByType("h3")
          .some((heading) => heading.children.includes("CPA 二号")),
      );
    expect(failed).toBeDefined();
    expect(failed?.findAllByType("dd")).toHaveLength(0);
    expect(renderer!.root.findAllByType("button")).toHaveLength(1);
  });

  it("does not render unexpected internal state text", async () => {
    mock.result.data!.status!.active_status = "/private/path Bearer secret";
    const output = await render();
    expect(output).not.toContain("private");
    expect(output).not.toContain("secret");
    expect(output).toContain("未知");
  });

  it.each([
    ["previewed", "已预览"],
    ["failed", "失败"],
    ["archived", "已归档"],
    ["verified", "已校验"],
  ])(
    "keeps an unfinished %s task visible when no operation is currently running",
    async (status, label) => {
      delete mock.result.data!.status!.active_operation;
      mock.result.data!.status!.active_status = status;
      const output = await render();
      expect(output).toContain("有尚未完成的维护任务");
      expect(output).toContain(label);
      expect(output).not.toContain("当前没有维护任务");
    },
  );

  it.each([
    [
      "zh-CN",
      "已归档的在线明细",
      "已包含在在线明细总数中，不含已清理的记录",
      "有尚未完成的维护任务",
      "当前没有维护任务",
    ],
    [
      "zh-TW",
      "已封存的線上明細",
      "已包含在線上明細總數中，不含已清理的記錄",
      "有尚未完成的維護工作",
      "目前沒有維護工作",
    ],
    [
      "en",
      "Archived online records",
      "included in the online total and exclude removed records",
      "An unfinished maintenance task remains",
      "No active maintenance task",
    ],
    [
      "ru",
      "Архивированные записи в базе",
      "входят в число доступных записей и не включают удалённые записи",
      "Есть незавершённая задача обслуживания",
      "Нет активных задач обслуживания",
    ],
  ])(
    "explains archived online counts and paused tasks consistently in %s",
    async (language, archived, recordsNote, pendingTask, idle) => {
      mock.language = language;
      delete mock.result.data!.status!.active_operation;
      mock.result.data!.status!.active_status = "previewed";
      const output = await render();
      expect(output).toContain(archived);
      expect(output).toContain(recordsNote);
      expect(output).toContain(pendingTask);
      expect(output).not.toContain(idle);
    },
  );

  it.each([
    ["zh-CN", "用量状态"],
    ["zh-TW", "用量狀態"],
    ["en", "Usage status"],
    ["ru", "Состояние данных"],
  ])("localizes the page for %s", async (language, title) => {
    mock.language = language;
    expect(await render()).toContain(title);
  });
});
