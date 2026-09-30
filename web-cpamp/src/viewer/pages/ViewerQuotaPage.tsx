import { useMemo, useState } from "react";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import {
  IconEye,
  IconEyeOff,
  IconRefreshCw,
  IconSearch,
} from "@/components/ui/icons";
import styles from "@/features/quota/QuotaPage.module.scss";
import { useViewerQuota } from "@/viewer/hooks/useViewerData";
import { formatDateTime } from "@/viewer/model/analytics";
import {
  formatViewerCodexWindowLabel,
  formatViewerQuotaPercent,
  formatViewerQuotaWindowLabel,
  groupViewerCodexWindows,
  normalizeViewerQuotaProvider,
  viewerQuotaObservedTime,
  viewerQuotaRemainingPercent,
} from "@/viewer/model/viewerQuota";
import type {
  ViewerQuotaAccount,
  ViewerQuotaWindow,
} from "@/viewer/model/viewerTypes";
import viewerStyles from "./ViewerQuotaPage.module.scss";

type SortMode = "default" | "name-asc" | "plan-desc" | "plan-asc";
type ViewMode = "paged" | "all";

const providerLabels: Record<string, string> = {
  codex: "Codex 额度",
  claude: "Claude 额度",
  antigravity: "Antigravity 额度",
  kimi: "Kimi 额度",
  xai: "xAI 额度",
  meta: "Meta / Muse 额度",
  devin: "Devin 额度",
  openai: "OpenAI 额度",
  gemini: "Gemini 额度",
  zhipu: "智谱额度",
  deepseek: "DeepSeek 额度",
  minimax: "MiniMax 额度",
};

const providerBadge: Record<
  string,
  { backgroundColor: string; color: string; border?: string }
> = {
  codex: {
    backgroundColor: "#eae7ff",
    color: "#5f46d8",
    border: "1px solid #d8d1ff",
  },
  claude: {
    backgroundColor: "#fbece4",
    color: "#9a4f2d",
    border: "1px solid #f0d6c8",
  },
  antigravity: {
    backgroundColor: "#e0f7fa",
    color: "#087f8c",
    border: "1px solid #bdebf0",
  },
  kimi: {
    backgroundColor: "#dce8ff",
    color: "#315eb3",
    border: "1px solid #c2d6fb",
  },
  xai: {
    backgroundColor: "#eceff3",
    color: "#29313d",
    border: "1px solid #d9dee5",
  },
  meta: {
    backgroundColor: "var(--info-bg)",
    color: "var(--info-text)",
    border: "1px solid var(--info-border)",
  },
  devin: {
    backgroundColor: "var(--data-badge-success-bg)",
    color: "var(--data-badge-success-text)",
    border: "1px solid var(--data-badge-success-border)",
  },
};

const providerGridClass = (provider: string) => {
  if (provider === "claude") return styles.claudeGrid;
  if (provider === "antigravity") return styles.antigravityGrid;
  if (provider === "kimi") return styles.kimiGrid;
  return styles.codexGrid;
};

const providerCardClass = (provider: string) => {
  if (provider === "claude") return styles.claudeCard;
  if (provider === "antigravity") return styles.antigravityCard;
  if (provider === "kimi") return styles.kimiCard;
  return styles.codexCard;
};

const maskDisplayName = (value: string) => {
  const at = value.indexOf("@");
  if (at > 1) return `${value.slice(0, 1)}***${value.slice(at)}`;
  if (value.length > 6) return `${value.slice(0, 3)}***${value.slice(-2)}`;
  return value;
};

function QuotaWindowRow({
  window,
  codex = false,
}: {
  window: ViewerQuotaWindow;
  codex?: boolean;
}) {
  const remaining = viewerQuotaRemainingPercent(window.remaining_percent);
  const observedAt = viewerQuotaObservedTime(window.observed_at_ms);
  const resetAt = viewerQuotaObservedTime(window.reset_at_ms);
  const label = codex
    ? formatViewerCodexWindowLabel(window)
    : formatViewerQuotaWindowLabel(window);
  const percent = formatViewerQuotaPercent(remaining);
  const fillClass =
    remaining === null
      ? ""
      : remaining >= 60
        ? styles.quotaBarFillHigh
        : remaining >= 20
          ? styles.quotaBarFillMedium
          : styles.quotaBarFillLow;
  return (
    <div className={`${styles.quotaRow} ${viewerStyles.windowRow}`}>
      <div className={styles.quotaRowHeader}>
        <div className={viewerStyles.windowHeading}>
          <span className={`${styles.quotaModel} ${viewerStyles.windowLabel}`}>
            {label}
          </span>
          {window.model_scope ? (
            <span className={viewerStyles.modelScope}>
              {window.model_scope}
            </span>
          ) : null}
        </div>
        <div className={`${styles.quotaMeta} ${viewerStyles.windowMeta}`}>
          <span
            className={styles.quotaPercent}
            aria-label={remaining === null ? "剩余额度未知" : undefined}
          >
            {percent}
          </span>
          <span className={styles.quotaReset}>
            {resetAt
              ? `重置 ${formatDateTime(window.reset_at_ms)}`
              : "重置时间未知"}
          </span>
        </div>
      </div>
      {remaining === null ? (
        <>
          <div
            className={`${styles.quotaBar} ${viewerStyles.unknownBar}`}
            aria-hidden="true"
          />
          <span className={viewerStyles.observation}>剩余额度未知</span>
        </>
      ) : (
        <div
          className={styles.quotaBar}
          role="progressbar"
          aria-label={
            window.model_scope ? `${label} · ${window.model_scope}` : label
          }
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={remaining}
          aria-valuetext={percent}
        >
          <div
            className={`${styles.quotaBarFill} ${fillClass}`}
            style={{ width: `${remaining}%` }}
          />
        </div>
      )}
      {window.stale ? (
        <div className={viewerStyles.stale}>上次快照，等待更新</div>
      ) : null}
      {codex || observedAt ? (
        <div className={viewerStyles.observation}>
          {observedAt ? (
            <>
              观测{" "}
              <time dateTime={observedAt}>
                {formatDateTime(window.observed_at_ms)}
              </time>
            </>
          ) : (
            "观测时间未记录"
          )}
        </div>
      ) : null}
    </div>
  );
}

function ViewerQuotaCard({
  account,
  showFull,
}: {
  account: ViewerQuotaAccount;
  showFull: boolean;
}) {
  const provider = normalizeViewerQuotaProvider(account.provider);
  const badge = providerBadge[provider] || providerBadge.xai;
  const windows = account.windows || [];
  const codexGroups =
    provider === "codex" ? groupViewerCodexWindows(windows) : [];
  return (
    <article className={`${styles.fileCard} ${providerCardClass(provider)}`}>
      <div className={styles.cardHeader}>
        <span className={styles.typeBadge} style={badge}>
          {provider === "xai"
            ? "xAI"
            : provider.charAt(0).toUpperCase() + provider.slice(1)}
        </span>
        {account.source ? (
          <span className={viewerStyles.sourceBadge}>
            {account.source === "sub2api" ? "Sub2API" : "CPA Manager Plus"}
          </span>
        ) : null}
        <span className={styles.fileName} title={account.display_name}>
          {showFull
            ? account.display_name
            : maskDisplayName(account.display_name)}
        </span>
      </div>
      <div className={styles.quotaSection}>
        {account.plan ? (
          <div className={styles.codexPlan}>
            <span className={styles.codexPlanLabel}>套餐</span>
            <strong className={styles.codexPlanValue}>{account.plan}</strong>
          </div>
        ) : null}
        {provider === "codex"
          ? codexGroups.map((group) => (
              <section
                key={group.id}
                className={viewerStyles.pool}
                aria-label={group.label}
              >
                <h3 className={viewerStyles.poolTitle}>{group.label}</h3>
                {group.windows.map((window, index) => (
                  <QuotaWindowRow
                    key={`${window.id}:${window.model_scope ?? ""}:${index}`}
                    window={window}
                    codex
                  />
                ))}
              </section>
            ))
          : windows.map((window, index) => (
              <QuotaWindowRow
                key={`${window.id}:${window.model_scope ?? ""}:${index}`}
                window={window}
              />
            ))}
        {!windows.length ? (
          <div className={styles.quotaMessage}>暂无已记录的额度快照</div>
        ) : null}
        {account.status_message ? (
          <div className={styles.quotaWarning}>{account.status_message}</div>
        ) : null}
        <div className={styles.quotaMeta}>
          <span>
            {account.disabled ? "已停用" : account.status || "unknown"}
          </span>
          <span>
            {account.updated_at_ms
              ? `${provider === "codex" ? "最新观测" : "最后观测"} ${formatDateTime(account.updated_at_ms)}`
              : "最后观测 —"}
          </span>
        </div>
      </div>
    </article>
  );
}

function ViewerQuotaGroup({
  provider,
  accounts,
  loading,
  refresh,
}: {
  provider: string;
  accounts: ViewerQuotaAccount[];
  loading: boolean;
  refresh: () => Promise<void>;
}) {
  const [showFull, setShowFull] = useState(false);
  const [viewMode, setViewMode] = useState<ViewMode>("paged");
  const [page, setPage] = useState(1);
  const pageSize = 6;
  const totalPages = Math.max(1, Math.ceil(accounts.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const visible =
    viewMode === "all"
      ? accounts
      : accounts.slice((currentPage - 1) * pageSize, currentPage * pageSize);
  const title = (
    <span className={styles.titleWrapper}>
      <span>{providerLabels[provider] || `${provider} 额度`}</span>
      <span className={styles.countBadge}>{accounts.length}</span>
    </span>
  );
  return (
    <Card
      title={title}
      extra={
        <div className={styles.headerActions}>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className={`${styles.accountDisplayModeButton} ${showFull ? styles.accountDisplayModeButtonActive : ""}`}
            onClick={() => setShowFull((value) => !value)}
          >
            {showFull ? <IconEye size={15} /> : <IconEyeOff size={15} />}
            {showFull ? "完整账号" : "脱敏账号"}
          </Button>
          <div className={styles.viewModeToggle}>
            <Button
              variant="secondary"
              size="sm"
              className={`${styles.viewModeButton} ${viewMode === "paged" ? styles.viewModeButtonActive : ""}`}
              onClick={() => setViewMode("paged")}
            >
              分页
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className={`${styles.viewModeButton} ${viewMode === "all" ? styles.viewModeButtonActive : ""}`}
              onClick={() => setViewMode("all")}
            >
              全部
            </Button>
          </div>
          <Button
            variant="secondary"
            size="sm"
            className={styles.refreshAllButton}
            onClick={() => void refresh()}
            disabled={loading}
            loading={loading}
          >
            {!loading ? <IconRefreshCw size={16} /> : null}刷新额度
          </Button>
        </div>
      }
    >
      <div className={providerGridClass(provider)}>
        {visible.map((account) => (
          <ViewerQuotaCard
            key={account.id}
            account={account}
            showFull={showFull}
          />
        ))}
      </div>
      {viewMode === "paged" && accounts.length > pageSize ? (
        <div className={styles.pagination}>
          <Button
            variant="secondary"
            size="sm"
            disabled={currentPage <= 1}
            onClick={() => setPage((value) => Math.max(1, value - 1))}
          >
            上一页
          </Button>
          <div className={styles.pageInfo}>
            第 {currentPage} / {totalPages} 页，共 {accounts.length} 个账号
          </div>
          <Button
            variant="secondary"
            size="sm"
            disabled={currentPage >= totalPages}
            onClick={() => setPage((value) => Math.min(totalPages, value + 1))}
          >
            下一页
          </Button>
        </div>
      ) : null}
    </Card>
  );
}

export function ViewerQuotaPage() {
  const { data, loading, error, refresh } = useViewerQuota();
  const [searchQuery, setSearchQuery] = useState("");
  const [sortMode, setSortMode] = useState<SortMode>("default");
  const accounts = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    const rows = (data?.accounts || []).filter(
      (account) =>
        !query ||
        [
          account.display_name,
          account.provider,
          normalizeViewerQuotaProvider(account.provider),
          providerLabels[normalizeViewerQuotaProvider(account.provider)],
          account.plan,
          account.status,
          account.status_message,
        ]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(query)),
    );
    if (sortMode === "name-asc")
      return [...rows].sort((a, b) =>
        a.display_name.localeCompare(b.display_name, "zh-CN"),
      );
    if (sortMode === "plan-asc")
      return [...rows].sort((a, b) =>
        String(a.plan || "").localeCompare(String(b.plan || ""), "zh-CN"),
      );
    if (sortMode === "plan-desc")
      return [...rows].sort((a, b) =>
        String(b.plan || "").localeCompare(String(a.plan || ""), "zh-CN"),
      );
    return rows;
  }, [data, searchQuery, sortMode]);
  const groups = useMemo(() => {
    const map = new Map<string, ViewerQuotaAccount[]>();
    accounts.forEach((account) => {
      const provider = normalizeViewerQuotaProvider(account.provider);
      const current = map.get(provider) || [];
      current.push(account);
      map.set(provider, current);
    });
    const order = [
      "codex",
      "claude",
      "antigravity",
      "gemini",
      "openai",
      "kimi",
      "xai",
      "meta",
      "devin",
      "zhipu",
      "deepseek",
      "minimax",
    ];
    const rank = (provider: string) => {
      const index = order.indexOf(provider);
      return index < 0 ? order.length : index;
    };
    return [...map.entries()].sort(
      (left, right) => rank(left[0]) - rank(right[0]),
    );
  }, [accounts]);

  return (
    <div className={styles.container}>
      {error ? <div className={styles.errorBox}>{error}</div> : null}
      {data?.warnings?.map((warning) => (
        <div key={warning} className={styles.errorBox}>
          {warning}
        </div>
      ))}
      <div className={styles.toolbar}>
        <div className={styles.toolbarField}>
          <Input
            label="搜索凭证"
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder="搜索名称、类型、账号 ID、套餐或错误状态"
            rightElement={<IconSearch size={16} />}
            aria-label="搜索凭证"
          />
        </div>
        <div className={`${styles.toolbarField} ${styles.sortField}`}>
          <label className={styles.toolbarLabel}>排序</label>
          <Select
            value={sortMode}
            options={[
              { value: "default", label: "默认排序" },
              { value: "name-asc", label: "名称升序" },
              { value: "plan-desc", label: "套餐高到低" },
              { value: "plan-asc", label: "套餐低到高" },
            ]}
            onChange={(value) => setSortMode(value as SortMode)}
            ariaLabel="排序"
          />
        </div>
      </div>
      {groups.map(([provider, rows]) => (
        <ViewerQuotaGroup
          key={provider}
          provider={provider}
          accounts={rows}
          loading={loading}
          refresh={refresh}
        />
      ))}
      {!loading && groups.length === 0 ? (
        <Card>
          <div className={styles.quotaMessage}>
            {searchQuery ? "没有符合搜索条件的凭证" : "暂无可展示的配额账号"}
          </div>
        </Card>
      ) : null}
    </div>
  );
}

export default ViewerQuotaPage;
