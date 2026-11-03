import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Language } from "@/types/common";
import type { ModelPriceStatusResponse } from "@/viewer/api/modelPriceStatus";
import { ViewerModelPriceNotice } from "./ViewerModelPriceNotice";

const mocks = vi.hoisted(() => ({
  language: "zh-CN" as Language,
  data: null as ModelPriceStatusResponse | null,
}));

vi.mock("@/stores/useLanguageStore", () => ({
  useLanguageStore: (selector: (state: { language: Language }) => unknown) =>
    selector({ language: mocks.language }),
}));

vi.mock("@/viewer/hooks/useViewerModelPriceStatus", () => ({
  useViewerModelPriceStatus: () => ({ data: mocks.data }),
}));

const snapshot = (): ModelPriceStatusResponse => ({
  available: true,
  stale: false,
  checked_at_ms: Date.UTC(2026, 8, 20, 10, 0, 0),
  unpriced_models: ["model-a", "model-b"],
  unpriced_count: 2,
  model_count: 10,
});

beforeEach(() => {
  mocks.language = "zh-CN";
  mocks.data = snapshot();
});

describe("ViewerModelPriceNotice", () => {
  it("stays silent when disabled, unavailable, pending, or fully priced", () => {
    expect(
      renderToStaticMarkup(<ViewerModelPriceNotice enabled={false} />),
    ).toBe("");
    mocks.data = null;
    expect(renderToStaticMarkup(<ViewerModelPriceNotice enabled />)).toBe("");
    mocks.data = { ...snapshot(), available: false, stale: true };
    expect(renderToStaticMarkup(<ViewerModelPriceNotice enabled />)).toBe("");
    mocks.data = { ...snapshot(), unpriced_count: 0 };
    expect(renderToStaticMarkup(<ViewerModelPriceNotice enabled />)).toBe("");
  });

  it("shows the explicit unpriced count with a collapsed read-only model list", () => {
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("2 个模型未配置价格，费用统计可能不完整。");
    expect(markup).toContain("查看模型");
    expect(markup).toContain("model-a");
    expect(markup).toContain("model-b");
    expect(markup).toMatch(/datetime="2026-09-20T10:00:00\.000Z"/i);
    expect(markup).not.toContain(" open=");
    expect(markup).not.toMatch(/<(?:a|button|input)\b/);
    expect(markup).not.toContain("显示上次检查结果");
    expect(markup).not.toContain("仅展示前");
  });

  it("marks cached results as potentially outdated", () => {
    mocks.data = { ...snapshot(), stale: true };
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("显示上次检查结果，当前价格状态可能已变化。");
    expect(markup).toContain("model-a");
  });

  it("explains a truncated list using the total count without rendering beyond 200 names", () => {
    mocks.data = {
      ...snapshot(),
      unpriced_models: Array.from(
        { length: 201 },
        (_, index) => `model-${index}`,
      ),
      unpriced_count: 205,
      model_count: 300,
    };
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("205 个模型未配置价格");
    expect(markup).toContain("仅展示前 200 个模型（共 205 个）。");
    expect(markup.match(/<li\b/g)).toHaveLength(200);
    expect(markup).toContain(">model-199</li>");
    expect(markup).not.toContain(">model-200</li>");
  });

  it("shows unknown combined pricing and failed instances instead of silently treating them as priced", () => {
    mocks.data = {
      ...snapshot(),
      unpriced_count: null,
      model_count: null,
      unpriced_models: [],
      partial_failed: true,
      warnings: ["CPA 二号 模型价格状态暂不可用"],
      instances: [
        {
          instance_id: "one",
          instance_name: "CPA 一号",
          available: true,
          stale: false,
          unpriced_count: 0,
          model_count: 12,
          unpriced_models: [],
        },
        {
          instance_id: "two",
          instance_name: "CPA 二号",
          available: false,
          stale: false,
          unpriced_count: 0,
          model_count: 0,
          unpriced_models: [],
        },
      ],
    };
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("模型价格状态未完整获取，费用统计可能不完整");
    expect(markup).toContain("部分实例暂不可用，汇总数据不完整");
    expect(markup).toContain("CPA 二号 模型价格状态暂不可用");
    expect(markup).toContain("各实例价格状态");
    expect(markup).toContain("CPA 一号");
    expect(markup).toContain("模型总数: 12");
    expect(markup).not.toContain("模型总数: 0");
    expect(markup).not.toContain("已读取的模型均已配置价格");
  });

  it("keeps an all-failed federation visible", () => {
    mocks.data = {
      ...snapshot(),
      available: false,
      unpriced_count: null,
      model_count: null,
      partial_failed: true,
      instances: [
        {
          instance_id: "one",
          instance_name: "暂离实例",
          available: false,
          stale: false,
          unpriced_count: 0,
          model_count: 0,
          unpriced_models: [],
        },
      ],
    };
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("暂离实例");
    expect(markup).toContain("暂不可用");
    expect(markup).not.toMatch(/<(?:a|button|input)\b/);
  });

  it("escapes untrusted model names as text", () => {
    mocks.data = {
      ...snapshot(),
      unpriced_models: [
        '<img src=x onerror="alert(1)">',
        "<script>alert(1)</script>",
      ],
    };
    const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
    expect(markup).toContain("&lt;img src=x onerror=&quot;alert(1)&quot;&gt;");
    expect(markup).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(markup).not.toMatch(/<(?:img|script)\b/);
  });

  it.each([
    ["zh-CN", "2 个模型未配置价格", "查看模型", "显示上次检查结果", "仅展示前"],
    [
      "zh-TW",
      "2 個模型尚未設定價格",
      "查看模型",
      "顯示上次檢查結果",
      "僅顯示前",
    ],
    [
      "en",
      "2 models have no configured price",
      "View models",
      "Showing the last check",
      "Showing only the first",
    ],
    [
      "ru",
      "Моделей без настроенной цены: 2",
      "Показать модели",
      "Показан результат последней проверки",
      "Показаны только первые",
    ],
  ] as const)(
    "provides all notice states in %s",
    (language, summary, details, stale, truncated) => {
      mocks.language = language;
      mocks.data = { ...snapshot(), stale: true, unpriced_models: ["model-a"] };
      const markup = renderToStaticMarkup(<ViewerModelPriceNotice enabled />);
      for (const text of [summary, details, stale, truncated]) {
        expect(markup).toContain(text);
      }
    },
  );

  it("uses singular English for one unpriced model", () => {
    mocks.language = "en";
    mocks.data = {
      ...snapshot(),
      unpriced_count: 1,
      unpriced_models: ["model-a"],
    };
    expect(renderToStaticMarkup(<ViewerModelPriceNotice enabled />)).toContain(
      "1 model has no configured price.",
    );
  });
});
