import type { KeyQuotaItem } from "@/viewer/api/types";

const english = {
  title: "Key quota",
  refresh: "Refresh",
  refreshing: "Refreshing…",
  updated: "Updated",
  instance: "Instance",
  remaining: "Weekly quota remaining",
  total: "Weekly limit",
  resets: "Resets",
  startsOnUse: "Starts after first use",
  resetUnknown: "Reset time unavailable",
  unlimited: "No weekly quota set",
  inactive: "Quota policy is not enabled",
  unavailable: "Quota temporarily unavailable",
  notOpen: "Quota viewing is not available yet",
  empty: "No public quotas available",
  loading: "Loading quotas…",
  failed: "Quotas are temporarily unavailable. Please try again later.",
  stale: "Quotas have not updated. Showing the last available data.",
  partialFailed:
    "Some instances could not refresh their quotas. Results may be incomplete.",
  partialStale:
    "Some quotas have not updated. Results include previously available data.",
};

export function keyQuotaCopy(language: string): typeof english {
  if (!language.toLowerCase().startsWith("zh")) return english;
  return {
    title: "Key 额度",
    refresh: "刷新",
    refreshing: "刷新中…",
    updated: "更新时间",
    instance: "所属实例",
    remaining: "周额度剩余",
    total: "周总额度",
    resets: "重置时间",
    startsOnUse: "使用后开始计时",
    resetUnknown: "重置时间暂不可用",
    unlimited: "未设置周额度",
    inactive: "额度策略未启用",
    unavailable: "额度暂不可用",
    notOpen: "额度暂未开放",
    empty: "暂无公开额度",
    loading: "正在加载额度…",
    failed: "暂时无法获取额度，请稍后再试。",
    stale: "额度暂未更新，以下为上次数据。",
    partialFailed: "部分实例额度暂未更新，当前结果可能不完整。",
    partialStale: "部分额度暂未更新，以下包含上次数据。",
  };
}

export function quotaPercent(value: number) {
  return Math.max(0, Math.min(100, Number(value.toPrecision(12))));
}

export function formatQuotaPercent(value: number, consumed = false) {
  const percent = quotaPercent(value);
  if (percent > 0 && percent < 1) return "<1%";
  if (value >= 100 && percent >= 100 && !consumed) return "100%";
  return `${Math.min(99, Math.floor(percent))}%`;
}

export function quotaTone(value: number) {
  const percent = quotaPercent(value);
  return percent < 5 ? "danger" : percent <= 20 ? "warning" : "success";
}

export function formatQuotaUSD(value: number) {
  if (value > 0 && value < 0.01) return "<$0.01";
  return `$${value.toLocaleString("en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
}

export function hasQuotaAmounts(item: KeyQuotaItem): item is KeyQuotaItem & {
  weekly_limit_usd: number;
  remaining_usd: number;
  remaining_percent: number;
} {
  return (
    item.state === "active" &&
    typeof item.weekly_limit_usd === "number" &&
    Number.isFinite(item.weekly_limit_usd) &&
    item.weekly_limit_usd > 0 &&
    typeof item.remaining_usd === "number" &&
    Number.isFinite(item.remaining_usd) &&
    item.remaining_usd >= 0 &&
    typeof item.remaining_percent === "number" &&
    Number.isFinite(item.remaining_percent)
  );
}

export function formatQuotaTime(value: string | undefined, language: string) {
  if (!value) return "";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toLocaleString(language, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}
