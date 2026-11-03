import type { ViewerQuotaWindow } from "./viewerTypes";

type QuotaPool = NonNullable<ViewerQuotaWindow["pool"]>;

const codexPools: Array<{ id: QuotaPool; label: string }> = [
  { id: "codex_main", label: "普通 Codex" },
  { id: "codex_spark", label: "Spark" },
  { id: "codex_review", label: "代码审查" },
  { id: "unknown", label: "额度池未确认" },
];

export function normalizeViewerQuotaProvider(value: string) {
  const provider = value.trim().toLowerCase();
  if (provider === "muse") return "meta";
  if (provider === "x-ai" || provider === "grok") return "xai";
  return provider;
}

export function groupViewerCodexWindows(windows: ViewerQuotaWindow[]) {
  const groups = new Map<QuotaPool, ViewerQuotaWindow[]>();
  for (const window of windows) {
    const pool = codexPools.some(({ id }) => id === window.pool)
      ? window.pool!
      : "unknown";
    const current = groups.get(pool) ?? [];
    current.push(window);
    groups.set(pool, current);
  }
  return codexPools.flatMap((pool) => {
    const current = groups.get(pool.id);
    return current?.length ? [{ ...pool, windows: current }] : [];
  });
}

export function formatViewerCodexWindowLabel(window: ViewerQuotaWindow) {
  const minutes = window.window_minutes;
  if (typeof minutes === "number" && Number.isFinite(minutes) && minutes > 0) {
    if (minutes === 300) return "5H 额度";
    if (minutes === 10080) return "7D 额度";
    if (minutes >= 27 * 1440 && minutes <= 32 * 1440) {
      return `${Number((minutes / 1440).toPrecision(12))}D 月额度`;
    }
    if (minutes % 1440 === 0) return `${minutes / 1440} 天额度`;
    if (minutes % 60 === 0) return `${minutes / 60} 小时额度`;
    return `${Number(minutes.toPrecision(12))} 分钟额度`;
  }

  // Legacy labels and primary/secondary IDs are not evidence of duration.
  const label = window.label?.trim();
  return label === "已观测额度" || label === "额度窗口"
    ? `${label}（时长未确认）`
    : "额度窗口（时长未确认）";
}

export function formatViewerQuotaWindowLabel(window: ViewerQuotaWindow) {
  const label = window.label?.trim();
  if (label) return label;
  if (
    typeof window.window_minutes === "number" &&
    Number.isFinite(window.window_minutes) &&
    window.window_minutes > 0
  ) {
    return formatViewerCodexWindowLabel(window);
  }
  const labels: Record<string, string> = {
    five_hour: "5 小时额度",
    daily: "日额度",
    weekly: "周额度",
    monthly: "月额度",
    rolling_24h: "滚动 24 小时额度",
  };
  return labels[window.window_kind ?? ""] ?? "额度窗口（时长未确认）";
}

export function viewerQuotaRemainingPercent(value: unknown): number | null {
  return typeof value === "number" &&
    Number.isFinite(value) &&
    value >= 0 &&
    value <= 100
    ? value
    : null;
}

export function formatViewerQuotaPercent(value: unknown) {
  const remaining = viewerQuotaRemainingPercent(value);
  if (remaining === null) return "--";
  if (remaining > 0 && remaining < 1) return "<1%";
  return `${Math.floor(remaining)}%`;
}

export function viewerQuotaObservedTime(value: number | undefined) {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return undefined;
  }
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined;
}

export function formatViewerResetCreditRemaining(
  expiresAt: number,
  now = Date.now(),
) {
  if (!Number.isFinite(expiresAt) || expiresAt <= 0) return "到期时间未知";
  const remaining = expiresAt - now;
  if (remaining <= 0) return "已过期";
  if (remaining >= 86_400_000)
    return `剩余 ${Math.ceil(remaining / 86_400_000)} 天`;
  if (remaining >= 3_600_000)
    return `剩余 ${Math.ceil(remaining / 3_600_000)} 小时`;
  return `剩余 ${Math.max(1, Math.ceil(remaining / 60_000))} 分钟`;
}

export function viewerResetCreditCount(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value
    : null;
}
