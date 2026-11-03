import { useLanguageStore } from "@/stores/useLanguageStore";
import type { Language } from "@/types/common";
import { useViewerModelPriceStatus } from "@/viewer/hooks/useViewerModelPriceStatus";
import styles from "./ViewerModelPriceNotice.module.scss";

type NoticeCopy = {
  summary: (count: number, formatted: string) => string;
  details: string;
  stale: string;
  truncated: (shown: string, total: string) => string;
  checked: string;
  unknown: string;
  complete: string;
  partial: string;
  unavailable: string;
  instances: string;
  unpriced: string;
  total: string;
  unknownCount: string;
};

const copy: Record<Language, NoticeCopy> = {
  "zh-CN": {
    summary: (_count, formatted) =>
      `${formatted} 个模型未配置价格，费用统计可能不完整。`,
    details: "查看模型",
    stale: "显示上次检查结果，当前价格状态可能已变化。",
    truncated: (shown, total) => `仅展示前 ${shown} 个模型（共 ${total} 个）。`,
    checked: "上次检查",
    unknown: "模型价格状态未完整获取，费用统计可能不完整。",
    complete: "已读取的模型均已配置价格。",
    partial: "部分实例暂不可用，汇总数据不完整。",
    unavailable: "暂不可用",
    instances: "各实例价格状态",
    unpriced: "未定价模型",
    total: "模型总数",
    unknownCount: "未知",
  },
  "zh-TW": {
    summary: (_count, formatted) =>
      `${formatted} 個模型尚未設定價格，費用統計可能不完整。`,
    details: "查看模型",
    stale: "顯示上次檢查結果，目前價格狀態可能已變更。",
    truncated: (shown, total) => `僅顯示前 ${shown} 個模型（共 ${total} 個）。`,
    checked: "上次檢查",
    unknown: "模型價格狀態尚未完整取得，費用統計可能不完整。",
    complete: "已讀取的模型均已設定價格。",
    partial: "部分實例暫時無法使用，彙總資料不完整。",
    unavailable: "暫時無法使用",
    instances: "各實例價格狀態",
    unpriced: "未定價模型",
    total: "模型總數",
    unknownCount: "未知",
  },
  en: {
    summary: (count, formatted) =>
      `${formatted} ${count === 1 ? "model has" : "models have"} no configured price. Cost statistics may be incomplete.`,
    details: "View models",
    stale: "Showing the last check. Current pricing status may have changed.",
    truncated: (shown, total) =>
      `Showing only the first ${shown} of ${total} models.`,
    checked: "Last checked",
    unknown:
      "Model pricing status is incomplete. Cost statistics may be incomplete.",
    complete: "All retrieved models have configured prices.",
    partial: "Some instances are unavailable. Combined data is incomplete.",
    unavailable: "Unavailable",
    instances: "Pricing by instance",
    unpriced: "Unpriced models",
    total: "Total models",
    unknownCount: "Unknown",
  },
  ru: {
    summary: (_count, formatted) =>
      `Моделей без настроенной цены: ${formatted}. Статистика расходов может быть неполной.`,
    details: "Показать модели",
    stale:
      "Показан результат последней проверки. Текущее состояние цен могло измениться.",
    truncated: (shown, total) =>
      `Показаны только первые ${shown} из ${total} моделей.`,
    checked: "Последняя проверка",
    unknown:
      "Состояние цен получено не полностью. Статистика расходов может быть неполной.",
    complete: "Цены настроены для всех полученных моделей.",
    partial: "Некоторые экземпляры недоступны. Сводные данные неполны.",
    unavailable: "Недоступно",
    instances: "Цены по экземплярам",
    unpriced: "Модели без цены",
    total: "Всего моделей",
    unknownCount: "Неизвестно",
  },
};

export function ViewerModelPriceNotice({
  enabled,
  refreshKey = 0,
}: {
  enabled: boolean;
  refreshKey?: number;
}) {
  const language = useLanguageStore((state) => state.language);
  const { data } = useViewerModelPriceStatus(enabled, refreshKey);

  const hasInstances = Boolean(data?.instances?.length);
  const knownCount =
    typeof data?.unpriced_count === "number" &&
    Number.isSafeInteger(data.unpriced_count) &&
    data.unpriced_count >= 0
      ? data.unpriced_count
      : null;
  if (
    !enabled ||
    !data ||
    (!hasInstances && !data.available) ||
    (!hasInstances &&
      knownCount === 0 &&
      !data.partial_failed &&
      !data.warnings?.length)
  ) {
    return null;
  }

  const text = copy[language];
  const models = Array.isArray(data.unpriced_models)
    ? data.unpriced_models
        .filter((model) => typeof model === "string" && model.trim())
        .slice(0, 200)
    : [];
  const count =
    knownCount === null
      ? text.unknownCount
      : knownCount.toLocaleString(language);
  const checked = data.checked_at_ms ? new Date(data.checked_at_ms) : null;
  const checkedIsValid = checked && Number.isFinite(checked.getTime());

  return (
    <aside className={styles.notice}>
      <span className={styles.icon} aria-hidden="true">
        !
      </span>
      <div className={styles.content}>
        <p className={styles.summary} role="status" aria-live="polite">
          {knownCount === null || !data.available
            ? text.unknown
            : knownCount > 0
              ? text.summary(knownCount, count)
              : text.complete}
        </p>
        {data.partial_failed ? (
          <p className={styles.note}>{text.partial}</p>
        ) : null}
        {data.warnings?.map((warning, index) => (
          <p className={styles.note} key={`${index}:${warning}`}>
            {warning}
          </p>
        ))}
        {hasInstances ? (
          <details className={styles.details}>
            <summary>{text.instances}</summary>
            <ul className={styles.models}>
              {data.instances!.map((instance) => (
                <li key={instance.instance_id}>
                  <strong>{instance.instance_name}</strong>
                  {" · "}
                  {instance.available ? (
                    <span>
                      {text.unpriced}:{" "}
                      {Number.isSafeInteger(instance.unpriced_count) &&
                      instance.unpriced_count >= 0
                        ? instance.unpriced_count.toLocaleString(language)
                        : text.unknownCount}
                      {" / "}
                      {text.total}:{" "}
                      {Number.isSafeInteger(instance.model_count) &&
                      instance.model_count >= 0
                        ? instance.model_count.toLocaleString(language)
                        : text.unknownCount}
                    </span>
                  ) : (
                    <span>{text.unavailable}</span>
                  )}
                  {instance.stale ? (
                    <span>
                      {" · "}
                      {text.stale}
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {data.stale && <p className={styles.note}>{text.stale}</p>}
        {models.length > 0 && (
          <details className={styles.details}>
            <summary>{text.details}</summary>
            <ul className={styles.models}>
              {models.map((model, index) => (
                <li key={`${index}:${model}`}>{model}</li>
              ))}
            </ul>
            {knownCount !== null && knownCount > models.length && (
              <p className={styles.note}>
                {text.truncated(models.length.toLocaleString(language), count)}
              </p>
            )}
            {checkedIsValid && (
              <p className={styles.note}>
                {text.checked}:{" "}
                <time dateTime={checked.toISOString()}>
                  {checked.toLocaleString(language)}
                </time>
              </p>
            )}
          </details>
        )}
      </div>
    </aside>
  );
}
