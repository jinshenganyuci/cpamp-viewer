import { describe, expect, it } from "vitest";
import type { ViewerQuotaWindow } from "./viewerTypes";
import {
  formatViewerQuotaPercent,
  formatViewerResetCreditRemaining,
  viewerResetCreditCount,
  formatViewerQuotaWindowLabel,
  normalizeViewerQuotaProvider,
  viewerQuotaRemainingPercent,
} from "./viewerQuota";

const windowFixture = (
  overrides: Partial<ViewerQuotaWindow>,
): ViewerQuotaWindow => ({
  id: "window-internal",
  label: "",
  remaining_percent: null,
  used_percent: null,
  ...overrides,
});

describe("Viewer quota evidence", () => {
  it.each([
    null,
    undefined,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    -1,
    101,
    "75",
    false,
  ])(
    "does not turn unknown or invalid percentage %s into zero or full quota",
    (value) => {
      expect(viewerQuotaRemainingPercent(value)).toBeNull();
      expect(formatViewerQuotaPercent(value)).toBe("--");
    },
  );

  it.each([
    [0, "0%"],
    [0.1, "<1%"],
    [99.999, "99%"],
    [100, "100%"],
  ] as const)("retains an explicit %s percentage", (value, label) => {
    expect(viewerQuotaRemainingPercent(value)).toBe(value);
    expect(formatViewerQuotaPercent(value)).toBe(label);
  });

  it.each([
    ["Muse", "meta"],
    [" META ", "meta"],
    ["Devin", "devin"],
    ["x-ai", "xai"],
    ["grok", "xai"],
  ])("normalizes provider %s to %s", (value, expected) => {
    expect(normalizeViewerQuotaProvider(value)).toBe(expected);
  });

  it("uses a recorded weekly kind without claiming a measured duration", () => {
    expect(
      formatViewerQuotaWindowLabel(windowFixture({ window_kind: "weekly" })),
    ).toBe("周额度");
    expect(
      formatViewerQuotaWindowLabel(
        windowFixture({ window_kind: "weekly", window_minutes: 11520 }),
      ),
    ).toBe("8 天额度");
  });

  it("keeps a supplied safe label and never presents internal IDs as a fallback", () => {
    expect(
      formatViewerQuotaWindowLabel(
        windowFixture({ label: "每日额度", window_kind: "daily" }),
      ),
    ).toBe("每日额度");
    expect(
      formatViewerQuotaWindowLabel(
        windowFixture({ id: "secret-or-private-id" }),
      ),
    ).toBe("额度窗口（时长未确认）");
  });
});

describe("reset credit evidence", () => {
  it("keeps zero distinct from missing and invalid counts", () => {
    expect(viewerResetCreditCount(0)).toBe(0);
    for (const value of [undefined, null, -1, 1.5, Infinity, "2"])
      expect(viewerResetCreditCount(value)).toBeNull();
  });

  it("formats remaining days, hours, minutes and expiry", () => {
    const now = Date.UTC(2030, 8, 10, 12);
    expect(formatViewerResetCreditRemaining(now + 3 * 86_400_000, now)).toBe(
      "剩余 3 天",
    );
    expect(formatViewerResetCreditRemaining(now + 27 * 86_400_000, now)).toBe(
      "剩余 27 天",
    );
    expect(formatViewerResetCreditRemaining(now + 2 * 3_600_000, now)).toBe(
      "剩余 2 小时",
    );
    expect(formatViewerResetCreditRemaining(now + 30_000, now)).toBe(
      "剩余 1 分钟",
    );
    expect(formatViewerResetCreditRemaining(now, now)).toBe("已过期");
  });
});
