import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  IconBot,
  IconCheck,
  IconFileText,
  IconInfo,
  IconKey,
  IconRefreshCw,
  IconSatellite,
  IconSettings,
} from "@/components/ui/icons";
import { TrafficOverviewCard } from "@/features/dashboard/components/TrafficOverviewCard";
import { UsageMetricsCard } from "@/features/dashboard/components/UsageMetricsCard";
import { CollectorStatusCard } from "@/features/dashboard/components/CollectorStatusCard";
import {
  HealthAlertsSurface,
  type HealthAlertsChannelRow,
  type HealthAlertsFailureRow,
} from "@/features/dashboard/components/HealthAlertsSurface";
import {
  VersionCardSurface,
  type VersionHealthItem,
  type VersionHealthTone,
} from "@/features/dashboard/components/VersionCardSurface";
import dashboardStyles from "@/features/dashboard/DashboardPage.module.scss";
import { useHeaderRefresh } from "@/hooks/useHeaderRefresh";
import { maskSensitiveText, truncateText } from "@/utils/format";
import { getPlanPresentation } from "@/utils/plans";
import { normalizeRoutingStrategy } from "@/utils/routingStrategy";
import { useViewerDashboard } from "@/viewer/hooks/useViewerData";
import { formatDuration } from "@/viewer/model/analytics";
import type {
  ViewerChannelStat,
  ViewerFailure,
} from "@/viewer/model/viewerTypes";
import {
  getViewerDashboardCounts,
  normalizeViewerDashboard,
  type ViewerDashboardReadOnlyProjection,
} from "./ViewerDashboardPage.model";
import viewerStyles from "./ViewerDashboardPage.module.scss";

const healthTone = (value?: string): VersionHealthTone => {
  const normalized = String(value || "").toLowerCase();
  if (
    normalized.includes("正常") ||
    normalized.includes("healthy") ||
    normalized === "ok" ||
    normalized === "connected"
  ) {
    return "ok";
  }
  if (
    normalized.includes("不可用") ||
    normalized.includes("异常") ||
    normalized.includes("错误") ||
    normalized === "error" ||
    normalized === "disconnected"
  ) {
    return "error";
  }
  if (
    normalized.includes("警告") ||
    normalized.includes("warning") ||
    normalized === "warn"
  ) {
    return "warn";
  }
  return "muted";
};

const healthStatusTranslationKey = (value?: string) => {
  const tone = healthTone(value);
  if (tone === "ok") return "dashboard.health_status_normal";
  if (tone === "error" || tone === "warn") {
    return "dashboard.health_status_problem";
  }
  return "common.not_set";
};

const normalizeChannelTone = (
  tone: string | undefined,
  successRate: number,
) => {
  if (tone === "success") return "good";
  if (tone === "warning") return "warn";
  if (tone === "failure") return "bad";
  if (tone === "good" || tone === "warn" || tone === "bad") return tone;
  return successRate >= 0.98 ? "good" : successRate >= 0.9 ? "warn" : "bad";
};

function ViewerBottomSummary({
  data,
  counts,
  loading,
}: {
  data: ViewerDashboardReadOnlyProjection | null;
  counts: ReturnType<typeof getViewerDashboardCounts>;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const config = data?.config;
  const routingStrategyRaw = config?.routing_strategy?.trim() || "";
  const routingStrategy = normalizeRoutingStrategy(routingStrategyRaw);
  const routingStrategyDisplay = !routingStrategyRaw
    ? "-"
    : routingStrategy === "round-robin"
      ? t("basic_settings.routing_strategy_round_robin")
      : routingStrategy === "weighted-round-robin"
        ? t("basic_settings.routing_strategy_weighted_round_robin")
        : routingStrategy === "fill-first"
          ? t("basic_settings.routing_strategy_fill_first")
          : routingStrategyRaw;
  const routingStrategyBadgeClass = !routingStrategyRaw
    ? dashboardStyles.configBadgeUnknown
    : routingStrategy === "round-robin"
      ? dashboardStyles.configBadgeRoundRobin
      : routingStrategy === "weighted-round-robin"
        ? dashboardStyles.configBadgeWeightedRoundRobin
        : routingStrategy === "fill-first"
          ? dashboardStyles.configBadgeFillFirst
          : dashboardStyles.configBadgeUnknown;
  const quickStats = [
    {
      key: "management-keys",
      label: t("dashboard.management_keys"),
      value: counts.accessKeyCount,
      icon: <IconKey size={24} />,
      path: "/usage-analytics",
      sublabel: t("nav.config_management"),
    },
    {
      key: "providers",
      label: t("nav.ai_providers"),
      value: counts.providerTotal,
      icon: <IconBot size={24} />,
      path: "/monitoring",
      sublabel: t("dashboard.provider_keys_detail", counts.providers),
    },
    {
      key: "auth-files",
      label: t("nav.accounts"),
      value: counts.authFiles,
      icon: <IconFileText size={24} />,
      path: "/quota",
      sublabel: t("dashboard.oauth_credentials"),
    },
    {
      key: "models",
      label: t("dashboard.available_models"),
      value: counts.availableModels ?? "-",
      icon: <IconSatellite size={24} />,
      path: "/usage-analytics",
      sublabel: t("dashboard.available_models_desc"),
    },
  ];

  return (
    <section className={dashboardStyles.bottomSummaryRow}>
      <div className={dashboardStyles.quickStatsPanel}>
        <div className={dashboardStyles.bentoGrid}>
          {quickStats.map((stat, index) => (
            <Link
              key={stat.key}
              to={stat.path}
              className={dashboardStyles.bentoCard}
              style={{ animationDelay: `${index * 80}ms` }}
            >
              <div className={dashboardStyles.bentoIcon}>{stat.icon}</div>
              <div className={dashboardStyles.bentoContent}>
                <span className={dashboardStyles.bentoLabel}>{stat.label}</span>
                <span className={dashboardStyles.bentoValue}>
                  {loading ? "..." : stat.value}
                </span>
                {!loading ? (
                  <span className={dashboardStyles.bentoSublabel}>
                    {stat.sublabel}
                  </span>
                ) : null}
              </div>
              <div className={dashboardStyles.bentoArrow}>
                {t("dashboard.manage")} →
              </div>
            </Link>
          ))}
        </div>
      </div>

      {config ? (
        <div className={dashboardStyles.configCard}>
          <div className={dashboardStyles.configHeader}>
            <h3>{t("dashboard.current_config_summary")}</h3>
          </div>
          <div className={dashboardStyles.configGrid}>
            <div className={dashboardStyles.configItem}>
              <span className={dashboardStyles.configLabel}>Debug</span>
              <span
                className={`${dashboardStyles.configValue} ${config.debug ? dashboardStyles.on : dashboardStyles.off}`}
              >
                {config.debug ? t("common.enabled") : t("common.disabled")}
              </span>
            </div>
            <div className={dashboardStyles.configItem}>
              <span className={dashboardStyles.configLabel}>
                {t("basic_settings.logging_to_file_enable")}
              </span>
              <span
                className={`${dashboardStyles.configValue} ${config.logging_to_file ? dashboardStyles.on : dashboardStyles.off}`}
              >
                {config.logging_to_file
                  ? t("common.enabled")
                  : t("common.disabled")}
              </span>
            </div>
            <div className={dashboardStyles.configItem}>
              <span className={dashboardStyles.configLabel}>
                {t("basic_settings.retry_count_label")}
              </span>
              <span className={dashboardStyles.configValue}>
                {config.request_retry ?? 0}
              </span>
            </div>
            <div className={dashboardStyles.configItem}>
              <span className={dashboardStyles.configLabel}>
                {t("basic_settings.ws_auth_enable")}
              </span>
              <span
                className={`${dashboardStyles.configValue} ${config.ws_auth ? dashboardStyles.on : dashboardStyles.off}`}
              >
                {config.ws_auth ? t("common.enabled") : t("common.disabled")}
              </span>
            </div>
            <div className={dashboardStyles.configItem}>
              <span className={dashboardStyles.configLabel}>
                {t("dashboard.routing_strategy")}
              </span>
              <span
                className={`${dashboardStyles.configBadge} ${routingStrategyBadgeClass}`}
              >
                {routingStrategyDisplay}
              </span>
            </div>
            {config.proxy_url ? (
              <div
                className={`${dashboardStyles.configItem} ${dashboardStyles.fullWidth}`}
              >
                <span className={dashboardStyles.configLabel}>Proxy URL</span>
                <span className={dashboardStyles.configValueMono}>
                  {config.proxy_url}
                </span>
              </div>
            ) : null}
          </div>
          <Link to="/monitoring" className={dashboardStyles.configLink}>
            {t("dashboard.view_full_config")} →
          </Link>
        </div>
      ) : null}
    </section>
  );
}

export function ViewerDashboardPage() {
  const { t, i18n } = useTranslation();
  const { data, loading, error, refresh } = useViewerDashboard();
  const dashboard = data as ViewerDashboardReadOnlyProjection | null;
  const warnings = [
    ...new Set((dashboard?.warnings || []).map(maskSensitiveText).filter(Boolean)),
  ];
  const panelBase =
    typeof window !== "undefined" && window.location?.origin
      ? window.location.origin
      : "";
  const [currentTime, setCurrentTime] = useState(() => new Date());
  const [refreshing, setRefreshing] = useState(false);
  const refreshInFlight = useRef<Promise<void> | null>(null);
  const summary = useMemo(
    () => normalizeViewerDashboard(dashboard),
    [dashboard],
  );
  const counts = useMemo(
    () => getViewerDashboardCounts(dashboard),
    [dashboard],
  );
  const versionHealthItems = useMemo<VersionHealthItem[]>(() => {
    const health = dashboard?.health || {};
    const errorLogValue = health.error_logs;
    const rows: Array<Omit<VersionHealthItem, "icon">> = [
      {
        label: t("dashboard.health_usage_monitor"),
        value: loading
          ? "..."
          : t(healthStatusTranslationKey(health.usage_monitor)),
        tone: healthTone(health.usage_monitor),
      },
      {
        label: t("dashboard.collector_status_title"),
        value: loading
          ? "..."
          : t(healthStatusTranslationKey(health.request_log)),
        tone: healthTone(health.request_log),
      },
      {
        label: t("dashboard.health_queue_status"),
        value: loading
          ? "..."
          : health.data_source
            ? t("dashboard.health_status_normal")
            : t("common.not_set"),
        tone: health.data_source ? "ok" : "muted",
      },
      {
        label: t("dashboard.health_error_logs"),
        value:
          errorLogValue === undefined
            ? loading
              ? "..."
              : t("common.not_set")
            : typeof errorLogValue === "number"
              ? errorLogValue > 0
                ? t("dashboard.health_error_log_count", {
                    count: errorLogValue,
                  })
                : t("dashboard.health_status_normal")
              : String(errorLogValue),
        tone:
          typeof errorLogValue === "number"
            ? errorLogValue > 0
              ? "warn"
              : "ok"
            : healthTone(String(errorLogValue || "")),
        to: "/monitoring",
      },
    ];
    return rows.map((row) => ({
      ...row,
      icon:
        row.tone === "ok" ? <IconCheck size={16} /> : <IconInfo size={16} />,
    }));
  }, [dashboard?.health, loading, t]);
  const channelRows = useMemo<HealthAlertsChannelRow[]>(() => {
    const rows: ViewerChannelStat[] = dashboard?.channel_health || [];
    return rows.slice(0, 5).map((row, index) => {
      const accountLabel =
        row.account_display ||
        row.auth_label_display ||
        row.source_display ||
        row.channel ||
        row.provider ||
        t("dashboard.viewer_channel_fallback", { index: index + 1 });
      const label = [row.instance_name, accountLabel]
        .filter(Boolean)
        .map((value) => maskSensitiveText(value || ""))
        .join(" · ");
      const failures = Number(row.failure ?? row.failures ?? 0);
      const calls = Number(row.calls || 0);
      const successRate = Number(
        row.success_rate ?? (calls > 0 ? (calls - failures) / calls : 0),
      );
      const title = [
        row.instance_name,
        row.account_display,
        row.auth_label_display,
        row.source_display,
        row.provider,
      ]
        .filter(
          (value, position, values) =>
            Boolean(value) && values.indexOf(value) === position,
        )
        .map((value) => maskSensitiveText(value || ""))
        .join(" · ");
      return {
        key: row.id || `${label}-${index}`,
        label,
        title: title || label,
        successRate,
        tone: normalizeChannelTone(row.tone, successRate),
      };
    });
  }, [dashboard?.channel_health, t]);
  const failureRows = useMemo<HealthAlertsFailureRow[]>(() => {
    const rows: ViewerFailure[] = dashboard?.recent_failures || [];
    return rows.slice(0, 3).map((row, index) => {
      const callerLabel =
        row.api_key_alias ||
        row.auth_label_display ||
        row.account_display ||
        row.source_display ||
        row.auth_provider_snapshot ||
        t("dashboard.viewer_unknown_caller");
      const caller = [row.instance_name, callerLabel]
        .filter(Boolean)
        .map((value) => maskSensitiveText(value || ""))
        .join(" · ");
      const statusText = row.fail_status_code
        ? `${t("monitoring.fail_status_code_short")} ${row.fail_status_code}`
        : "";
      const summary = row.fail_summary
        ? truncateText(maskSensitiveText(row.fail_summary), 160)
        : "";
      const quotaPlanLabel = getPlanPresentation({
        provider: row.auth_provider_snapshot || row.source_display || "",
        planType: row.header_quota_plan_type,
        t,
      })?.fullLabel;
      const diagnostics = [
        row.header_error_kind || row.header_error_code
          ? `${t("monitoring.header_error")}: ${[
              row.header_error_kind,
              row.header_error_code,
            ]
              .filter(Boolean)
              .join(" / ")}`
          : "",
        row.header_quota_plan_type ||
        typeof row.header_quota_used_percent === "number"
          ? `${t("monitoring.header_quota")}: ${[
              quotaPlanLabel,
              typeof row.header_quota_used_percent === "number"
                ? `${row.header_quota_used_percent.toFixed(0)}%`
                : "",
            ]
              .filter(Boolean)
              .join(" · ")}`
          : "",
      ].filter(Boolean);
      const hasTooltip = Boolean(
        statusText || summary || diagnostics.length > 0,
      );
      return {
        key: `${row.timestamp_ms}-${row.api_key_id || row.source_id || index}-${row.model || "unknown"}`,
        timestampMs: row.timestamp_ms,
        model: row.model || t("dashboard.version_unknown"),
        source: caller,
        sourceTitle: caller,
        durationText: formatDuration(row.duration_ms ?? row.latency_ms),
        tooltip: hasTooltip ? { statusText, summary, diagnostics } : null,
      };
    });
  }, [dashboard?.recent_failures, t]);

  useEffect(() => {
    const timer = window.setInterval(() => setCurrentTime(new Date()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const handleRefresh = useCallback(() => {
    if (refreshInFlight.current) return refreshInFlight.current;

    setRefreshing(true);
    const request = refresh()
      .then(() => {
        setCurrentTime(new Date());
      })
      .finally(() => {
        if (refreshInFlight.current === request) {
          refreshInFlight.current = null;
          setRefreshing(false);
        }
      });
    refreshInFlight.current = request;
    return request;
  }, [refresh]);

  useHeaderRefresh(handleRefresh);

  const connectionStatus =
    loading && !data
      ? "connecting"
      : dashboard?.connection?.connected === false || (error && !dashboard)
        ? "disconnected"
        : "connected";
  const formattedDate = currentTime.toLocaleDateString(i18n.language, {
    weekday: "long",
  });
  const formattedDateTime = currentTime.toLocaleString(i18n.language, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
  const lastRefreshedAt = dashboard?.generated_at_ms
    ? new Date(dashboard.generated_at_ms)
    : null;

  return (
    <>
      <header className={dashboardStyles.header}>
        <div className={dashboardStyles.headerLeft}>
          <div className={dashboardStyles.connectionStatus}>
            <span
              className={`${dashboardStyles.statusDot} ${dashboardStyles[connectionStatus]}`}
            />
            <div className={dashboardStyles.statusInfo}>
              <span className={dashboardStyles.statusLabel}>
                {t("common.connection_status")}
              </span>
              <span className={dashboardStyles.statusValue}>
                {t(`common.${connectionStatus}`)}
              </span>
            </div>
          </div>
          <div className={dashboardStyles.apiBaseBlock}>
            <span className={dashboardStyles.apiLabel}>
              {t("dashboard.api_base")}
            </span>
            <span className={dashboardStyles.apiValue}>
              {panelBase || t("dashboard.viewer_api_base_value")}
            </span>
          </div>
        </div>

        <div className={dashboardStyles.headerRight}>
          <div className={dashboardStyles.timeDisplay}>
            <span className={dashboardStyles.time}>{formattedDateTime}</span>
            <span className={dashboardStyles.date}>{formattedDate}</span>
          </div>
          <div className={dashboardStyles.headerActions}>
            <button
              type="button"
              className={dashboardStyles.actionBtn}
              onClick={handleRefresh}
              disabled={refreshing}
              aria-busy={refreshing}
              aria-label={t("common.refresh")}
            >
              <IconRefreshCw size={16} />
              <span>{t("common.refresh")}</span>
            </button>
            <Link
              to="/quota"
              className={dashboardStyles.actionBtn}
              title={t("nav.system_config")}
              aria-label={t("nav.system_config")}
            >
              <IconSettings size={16} />
            </Link>
          </div>
        </div>
      </header>

      {warnings.map((warning) => (
        <p key={warning} role="status" className={viewerStyles.warning}>
          {warning}
        </p>
      ))}

      <section className={dashboardStyles.overviewRow}>
        <VersionCardSurface
          appVersion={
            dashboard?.system?.management_version || __APP_VERSION__ || ""
          }
          apiVersion={dashboard?.system?.server_version || ""}
          cpaBase={dashboard?.system?.cpa_base || ""}
          serverBuildDate={dashboard?.system?.build_time}
          checkingAppVersion={refreshing}
          checkingApiVersion={refreshing}
          healthItems={versionHealthItems}
          onAppVersionCheck={handleRefresh}
          onApiVersionCheck={handleRefresh}
        />
      </section>

      <section className={dashboardStyles.metricsRow}>
        <h2 className={dashboardStyles.sectionTitle}>
          {t("dashboard.today_overview_usage_service")}
        </h2>
        <UsageMetricsCard
          summary={summary}
          topModels={summary.top_models_today}
          modelCostRank={summary.model_cost_rank || []}
          loading={loading}
          error={error}
          lastRefreshedAt={lastRefreshedAt}
          mode="metrics-only"
        />
      </section>

      <section className={dashboardStyles.chartsRow}>
        <TrafficOverviewCard
          timeline={summary.traffic_timeline || []}
          trafficNowMs={summary.window.now_ms}
          todayRequestHealthTimeline={
            summary.today_request_health_timeline || null
          }
          tokenMix={summary.token_mix || []}
          loading={loading}
        />
      </section>

      <section className={dashboardStyles.dataRow}>
        <UsageMetricsCard
          summary={summary}
          topModels={summary.top_models_today}
          modelCostRank={summary.model_cost_rank || []}
          loading={loading}
          error={error}
          lastRefreshedAt={lastRefreshedAt}
          mode="rank-only"
        />
        <HealthAlertsSurface
          loading={loading}
          channelRows={channelRows}
          failureRows={failureRows}
        />
        <CollectorStatusCard
          enabled
          serviceBase={dashboard?.system?.cpa_base || ""}
          status={dashboard?.collector || null}
          loading={loading}
          error={error}
        />
      </section>

      <ViewerBottomSummary data={dashboard} counts={counts} loading={loading} />
    </>
  );
}

export default ViewerDashboardPage;
