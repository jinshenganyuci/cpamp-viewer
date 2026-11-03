import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { KeyQuotaItem, KeyQuotaResponse } from "@/viewer/api/types";
import { ViewerKeyQuotaPage } from "./ViewerKeyQuotaPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { mock } = vi.hoisted(() => ({
  mock: {
    language: "zh-CN",
    result: {
      data: null as KeyQuotaResponse | null,
      loading: false,
      failed: false,
      stale: false,
      refresh: vi.fn(),
    },
  },
}));

vi.mock("@/stores/useLanguageStore", () => ({
  useLanguageStore: (selector: (state: { language: string }) => unknown) => selector({ language: mock.language }),
}));
vi.mock("@/viewer/hooks/useViewerKeyQuotas", () => ({
  useViewerKeyQuotas: () => mock.result,
}));

describe("public Key quota page", () => {
  let renderer: ReactTestRenderer | undefined;
  async function render(items: KeyQuotaItem[] = []) {
    mock.result.data ??= { configured: true, stale: false, items };
    await act(async () => { renderer = create(<ViewerKeyQuotaPage />); });
    return JSON.stringify(renderer?.toJSON());
  }
  beforeEach(() => {
    mock.language = "zh-CN";
    mock.result = { data: null, loading: false, failed: false, stale: false, refresh: vi.fn() };
  });
  afterEach(async () => { if (renderer) await act(async () => { renderer?.unmount(); }); });

  it("shows only public names, amounts and reset status without internal identifiers or write controls", async () => {
    const text = await render([
      { id: "view_never_visible", name: "公开用户", state: "active", weekly_limit_usd: 800, remaining_usd: 0.001, remaining_percent: 0.000125, window_started: false },
      { id: "view_reset", name: "已使用用户", state: "active", weekly_limit_usd: 800, remaining_usd: 400, remaining_percent: 50, window_started: true, reset_at: "2026-09-14T06:00:00Z" },
    ]);
    expect(text).toContain("公开用户");
    expect(text).toContain("<$0.01");
    expect(text).toContain("<1%");
    expect(text).toContain("使用后开始计时");
    expect(text).toContain("重置时间");
    for (const hidden of ["view_never_visible", "view_reset", "删除", "编辑", "重置额度", "原生 Key", "凭证限制", "模型权限"]) expect(text).not.toContain(hidden);
    expect(renderer?.root.findAllByType("button")).toHaveLength(1);
    expect(renderer?.root.findAllByProps({ role: "progressbar" })).toHaveLength(2);
  });

  it("distinguishes unlimited, inactive and unavailable from exhausted", async () => {
    const text = await render([
      { id: "view_a", name: "A", state: "unlimited" },
      { id: "view_b", name: "B", state: "inactive" },
      { id: "view_c", name: "C", state: "unavailable" },
      { id: "view_d", name: "D", state: "active", weekly_limit_usd: 800, remaining_usd: 0, remaining_percent: 0 },
    ]);
    expect(text).toContain("未设置周额度");
    expect(text).toContain("额度策略未启用");
    expect(text).toContain("额度暂不可用");
    expect(text).toContain("$0.00");
    expect(text).not.toContain("Key 已禁用");
    expect(renderer?.root.findAllByProps({ role: "progressbar" })).toHaveLength(1);
  });

  it("distinguishes matching public names by instance without exposing instance identifiers", async () => {
    const text = await render([
      { id: "view_east", name: "公开用户", state: "unlimited", instance_id: "instance_east_hidden", instance_name: "东站" },
      { id: "view_west", name: "公开用户", state: "unlimited", instance_id: "instance_west_hidden", instance_name: "西站 authorization: Bearer instance-secret-value" },
    ]);
    expect(renderer?.root.findAllByType("article")).toHaveLength(2);
    expect(text).toContain("所属实例");
    expect(text).toContain("东站");
    expect(text).toContain("西站");
    expect(text).not.toContain("instance_east_hidden");
    expect(text).not.toContain("instance_west_hidden");
    expect(text).not.toContain("instance-secret-value");
  });

  it("keeps successful quotas visible with safe partial failure warnings", async () => {
    mock.result.data = {
      configured: true,
      stale: false,
      partial_failed: true,
      warnings: ["西站：额度暂不可用", "西站：额度暂不可用", "备用站 authorization: Bearer test-secret-value"],
      items: [{ id: "view_east", name: "可用用户", state: "unlimited", instance_name: "东站" }],
    };
    const text = await render();
    expect(text).toContain("可用用户");
    expect(text).toContain("部分实例额度暂未更新，当前结果可能不完整。");
    expect(text.match(/西站：额度暂不可用/g)).toHaveLength(1);
    expect(text).not.toContain("test-secret-value");
    expect(text).not.toContain("额度暂未更新，以下为上次数据。");
  });

  it("does not describe an empty partial failure as quotas being unpublished", async () => {
    mock.result.data = { configured: false, stale: false, partial_failed: true, warnings: [], items: [] };
    const text = await render();
    expect(text).toContain("暂时无法获取额度，请稍后再试。");
    expect(text).not.toContain("额度暂未开放");
    expect(text).not.toContain("暂无公开额度");
  });

  it("identifies cached quotas in a mixed fresh and stale response", async () => {
    mock.result.data = {
      configured: true,
      stale: true,
      partial_failed: true,
      items: [{ id: "view_cached", name: "缓存用户", state: "unlimited", instance_name: "西站" }],
    };
    mock.result.stale = true;
    const text = await render();
    expect(text).toContain("部分额度暂未更新，以下包含上次数据。");
    expect(text).toContain("缓存用户");
  });

  it("shows that quota viewing is not open for an unconfigured integration", async () => {
    mock.result.data = { configured: false, stale: false, items: [] };
    const text = await render();
    expect(text).toContain("额度暂未开放");
    expect(text).not.toContain("暂无公开额度");
    expect(text).not.toContain("ACCESS_GUARD");
    expect(text).not.toContain("配置");
  });

  it("marks retained data stale while preserving its actual update time", async () => {
    mock.result.data = { configured: true, stale: true, updated_at: "2026-09-07T06:00:00Z", items: [{ id: "view_a", name: "保留用户", state: "unlimited" }] };
    mock.result.stale = true;
    const text = await render();
    expect(text).toContain("额度暂未更新，以下为上次数据。");
    expect(text).toContain("保留用户");
    expect(renderer?.root.findByType("time").props.dateTime).toBe("2026-09-07T06:00:00Z");
  });

  it("falls back to English for other existing locales", async () => {
    mock.language = "ru";
    expect(await render()).toContain("No public quotas available");
  });

  it("keeps an enabled empty publication separate from quota viewing not being open", async () => {
    const text = await render();
    expect(text).toContain("暂无公开额度");
    expect(text).not.toContain("额度暂未开放");
  });

  it("explains unavailable quota viewing in English without setup instructions", async () => {
    mock.language = "en";
    mock.result.data = {
      configured: false,
      stale: false,
      items: [{ id: "view_closed", name: "must-stay-hidden", state: "unlimited" }],
    };
    const text = await render();
    expect(text).toContain("Quota viewing is not available yet");
    expect(text).not.toContain("No public quotas available");
    expect(text).not.toContain("ACCESS_GUARD");
    expect(text).not.toContain("must-stay-hidden");
  });
});
