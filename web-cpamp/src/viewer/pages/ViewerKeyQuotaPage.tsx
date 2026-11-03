import { Button } from "@/components/ui/Button";
import { IconRefreshCw } from "@/components/ui/icons";
import { useLanguageStore } from "@/stores/useLanguageStore";
import { maskSensitiveText } from "@/utils/format";
import type { KeyQuotaItem } from "@/viewer/api/types";
import { useViewerKeyQuotas } from "@/viewer/hooks/useViewerKeyQuotas";
import {
  formatQuotaPercent,
  formatQuotaTime,
  formatQuotaUSD,
  hasQuotaAmounts,
  keyQuotaCopy,
  quotaPercent,
  quotaTone,
} from "@/viewer/model/keyQuota";
import styles from "./ViewerKeyQuotaPage.module.scss";

function KeyQuotaCard({
  item,
  language,
}: {
  item: KeyQuotaItem;
  language: string;
}) {
  const copy = keyQuotaCopy(language);
  const resetAt = formatQuotaTime(item.reset_at, language);
  const amounts = hasQuotaAmounts(item);
  const name = maskSensitiveText(item.name);
  const instanceName = maskSensitiveText(item.instance_name || "");
  return (
    <article className={styles.card}>
      <div className={styles.cardHeading}>
        <h2 className={styles.name}>{name}</h2>
        {instanceName ? (
          <p className={styles.instance}>
            {copy.instance}: {instanceName}
          </p>
        ) : null}
      </div>
      {amounts ? (
        <>
          <p className={styles.label}>{copy.remaining}</p>
          <div className={styles.amounts}>
            <strong className={styles.remaining}>
              {formatQuotaUSD(item.remaining_usd)}
            </strong>
            <span
              className={styles.total}
              aria-label={`${copy.total} ${formatQuotaUSD(item.weekly_limit_usd)}`}
            >
              / {formatQuotaUSD(item.weekly_limit_usd)}
            </span>
          </div>
          <div className={styles.progressRow}>
            <span className={styles.period}>7D</span>
            <div
              role="progressbar"
              aria-label={[name, instanceName, copy.remaining]
                .filter(Boolean)
                .join(" · ")}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={quotaPercent(item.remaining_percent)}
              aria-valuetext={formatQuotaPercent(
                item.remaining_percent,
                item.remaining_usd < item.weekly_limit_usd,
              )}
              className={styles.track}
            >
              <div
                className={`${styles.fill} ${styles[quotaTone(item.remaining_percent)]}`}
                style={{ width: `${quotaPercent(item.remaining_percent)}%` }}
              />
            </div>
            <span className={styles.percent}>
              {formatQuotaPercent(
                item.remaining_percent,
                item.remaining_usd < item.weekly_limit_usd,
              )}
            </span>
          </div>
          <p className={styles.reset}>
            {item.window_started === false
              ? copy.startsOnUse
              : resetAt
                ? `${copy.resets} ${resetAt}`
                : copy.resetUnknown}
          </p>
        </>
      ) : (
        <p className={styles.state}>
          {item.state === "unlimited"
            ? copy.unlimited
            : item.state === "inactive"
              ? copy.inactive
              : copy.unavailable}
        </p>
      )}
    </article>
  );
}

export function ViewerKeyQuotaPage() {
  const language = useLanguageStore((state) => state.language);
  const copy = keyQuotaCopy(language);
  const { data, loading, failed, stale, refresh } = useViewerKeyQuotas();
  const updatedAt = formatQuotaTime(data?.updated_at, language);
  const items = data?.configured ? data.items : [];
  const warnings = [
    ...new Set((data?.warnings || []).map(maskSensitiveText).filter(Boolean)),
  ];

  return (
    <section className={styles.page} aria-busy={loading}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{copy.title}</h1>
          {updatedAt ? (
            <p className={styles.updated}>
              {copy.updated} <time dateTime={data?.updated_at}>{updatedAt}</time>
            </p>
          ) : null}
        </div>
        <Button
          variant="secondary"
          disabled={loading}
          onClick={() => void refresh()}
        >
          <span className={styles.refreshLabel}>
            <IconRefreshCw size={16} />
            {loading ? copy.refreshing : copy.refresh}
          </span>
        </Button>
      </header>

      {stale && data ? (
        <p role="status" className={styles.warning}>
          {data.partial_failed ? copy.partialStale : copy.stale}
        </p>
      ) : null}

      {data?.partial_failed || warnings.length ? (
        <div role="status" className={styles.warning}>
          {data?.partial_failed ? <p>{copy.partialFailed}</p> : null}
          {warnings.map((warning) => (
            <p key={warning}>{warning}</p>
          ))}
        </div>
      ) : null}

      {items.length ? (
        <div className={styles.grid}>
          {items.map((item) => (
            <KeyQuotaCard key={item.id} item={item} language={language} />
          ))}
        </div>
      ) : (
        <p role="status" className={styles.empty}>
          {!data && loading
            ? copy.loading
            : !data && failed
              ? copy.failed
              : data?.partial_failed
                ? copy.failed
                : data?.configured === false
                  ? copy.notOpen
                  : copy.empty}
        </p>
      )}
    </section>
  );
}
