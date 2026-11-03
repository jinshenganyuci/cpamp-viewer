import type { ReactNode } from "react";
import { Button } from "@/components/ui/Button";
import { IconRefreshCw } from "@/components/ui/icons";
import { useHeaderRefresh } from "@/hooks/useHeaderRefresh";
import { useLanguageStore } from "@/stores/useLanguageStore";
import { useViewerUsageStatus } from "@/viewer/hooks/useViewerUsageStatus";
import {
  formatUsageBytes,
  formatUsageCount,
  usageStatusCopy,
  usageStatusTime,
} from "@/viewer/model/usageStatus";
import styles from "./ViewerUsageStatusPage.module.scss";

function ValueRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className={styles.row}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

export function ViewerUsageStatusPage() {
  const language = useLanguageStore((state) => state.language);
  const copy = usageStatusCopy(language);
  const { data, loading, refresh } = useViewerUsageStatus();
  useHeaderRefresh(refresh);
  const status = data?.available ? data.status : undefined;
  const metrics: Array<[string, number | null | undefined]> = [
    [copy.online, status?.raw_event_count],
    [copy.archived, status?.raw_archived_event_count],
    [copy.deleted, status?.raw_deleted_event_count],
  ];
  const checked = usageStatusTime(data?.checked_at_ms, language);
  const dateValue = (value: number | null | undefined) => {
    const time = usageStatusTime(value, language);
    return time ? <time dateTime={time.iso}>{time.label}</time> : copy.unknown;
  };
  const readiness = (ready: boolean | null | undefined) => (
    <span
      className={`${styles.badge} ${ready ? styles.ready : styles.pending}`}
    >
      {ready === true
        ? copy.ready
        : ready === false
          ? copy.pending
          : copy.unknown}
    </span>
  );

  return (
    <section className={styles.page} aria-busy={loading}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{copy.title}</h1>
          <p className={styles.description}>{copy.description}</p>
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

      {data?.partial_failed ? (
        <p role="status" className={styles.warning}>
          {copy.partial}
        </p>
      ) : null}
      {data?.warnings?.map((warning, index) => (
        <p key={`${index}:${warning}`} role="status" className={styles.warning}>
          {warning}
        </p>
      ))}
      {status && data?.stale ? (
        <p role="status" className={styles.warning}>
          {copy.stale}
        </p>
      ) : null}

      {status ? (
        <>
          <div className={styles.metrics}>
            {metrics.map(([label, value]) => (
              <article className={styles.card} key={label}>
                <h2 className={styles.metricLabel}>{label}</h2>
                <p className={styles.metricValue}>
                  {formatUsageCount(value, language)}
                </p>
              </article>
            ))}
          </div>
          <p className={styles.note}>{copy.recordsNote}</p>
          <div className={styles.grid}>
            <article className={styles.card}>
              <h2 className={styles.cardTitle}>{copy.range}</h2>
              {status.raw_event_count === 0 ? (
                <p className={styles.description}>{copy.emptyRange}</p>
              ) : (
                <dl className={styles.values}>
                  <ValueRow label={copy.earliest}>
                    {dateValue(status.raw_min_timestamp_ms)}
                  </ValueRow>
                  <ValueRow label={copy.latest}>
                    {dateValue(status.raw_max_timestamp_ms)}
                  </ValueRow>
                </dl>
              )}
            </article>
            <article className={styles.card}>
              <h2 className={styles.cardTitle}>{copy.readiness}</h2>
              <dl className={styles.values}>
                <ValueRow label={copy.migration}>
                  {readiness(status.migration_ready)}
                </ValueRow>
                <ValueRow label={copy.aggregate}>
                  {readiness(status.hourly_aggregate_ready)}
                </ValueRow>
              </dl>
            </article>
            <article className={styles.card}>
              <h2 className={styles.cardTitle}>{copy.storage}</h2>
              <dl className={styles.values}>
                <ValueRow label={copy.total}>
                  <strong>
                    {formatUsageBytes(status.storage.total_bytes, language)}
                  </strong>
                </ValueRow>
                <ValueRow label={copy.database}>
                  {formatUsageBytes(status.storage.database_bytes, language)}
                </ValueRow>
                <ValueRow label={copy.wal}>
                  {formatUsageBytes(status.storage.wal_bytes, language)}
                </ValueRow>
                <ValueRow label={copy.shm}>
                  {formatUsageBytes(status.storage.shm_bytes, language)}
                </ValueRow>
                <ValueRow label={copy.reclaimable}>
                  {formatUsageBytes(status.storage.reclaimable_bytes, language)}
                </ValueRow>
              </dl>
              <p className={styles.note}>{copy.storageNote}</p>
            </article>
            <article className={styles.card}>
              <h2 className={styles.cardTitle}>{copy.activity}</h2>
              <p className={styles.activity}>
                {data?.instances?.length
                  ? copy.instanceActivity
                  : status.active_operation
                    ? Object.prototype.hasOwnProperty.call(
                        copy.operations,
                        status.active_operation,
                      )
                      ? copy.operations[status.active_operation]
                      : copy.operations.maintenance
                    : status.active_status
                      ? copy.pendingTask
                      : copy.idle}
              </p>
              {status.active_status ? (
                <dl className={styles.values}>
                  <ValueRow label={copy.state}>
                    {Object.prototype.hasOwnProperty.call(
                      copy.states,
                      status.active_status,
                    )
                      ? copy.states[status.active_status]
                      : copy.unknown}
                  </ValueRow>
                </dl>
              ) : null}
            </article>
          </div>
        </>
      ) : (
        <p role="status" className={styles.empty}>
          {loading && !data ? copy.loading : copy.unavailable}
        </p>
      )}

      {data?.instances?.length ? (
        <section aria-label={copy.instances} className={styles.instances}>
          <h2 className={styles.cardTitle}>{copy.instances}</h2>
          <div className={styles.grid}>
            {data.instances.map((instance) => {
              const local = instance.available ? instance.status : undefined;
              const localChecked = usageStatusTime(
                instance.checked_at_ms,
                language,
              );
              return (
                <article key={instance.instance_id} className={styles.card}>
                  <h3 className={styles.cardTitle}>{instance.instance_name}</h3>
                  {instance.stale ? (
                    <p className={styles.warning}>{copy.stale}</p>
                  ) : null}
                  {local ? (
                    <dl className={styles.values}>
                      <ValueRow label={copy.online}>
                        {formatUsageCount(local.raw_event_count, language)}
                      </ValueRow>
                      <ValueRow label={copy.archived}>
                        {formatUsageCount(
                          local.raw_archived_event_count,
                          language,
                        )}
                      </ValueRow>
                      <ValueRow label={copy.deleted}>
                        {formatUsageCount(
                          local.raw_deleted_event_count,
                          language,
                        )}
                      </ValueRow>
                      <ValueRow label={copy.total}>
                        {formatUsageBytes(local.storage.total_bytes, language)}
                      </ValueRow>
                      <ValueRow label={copy.reclaimable}>
                        {formatUsageBytes(
                          local.storage.reclaimable_bytes,
                          language,
                        )}
                      </ValueRow>
                      <ValueRow label={copy.earliest}>
                        {dateValue(local.raw_min_timestamp_ms)}
                      </ValueRow>
                      <ValueRow label={copy.latest}>
                        {dateValue(local.raw_max_timestamp_ms)}
                      </ValueRow>
                      <ValueRow label={copy.migration}>
                        {readiness(local.migration_ready)}
                      </ValueRow>
                      <ValueRow label={copy.aggregate}>
                        {readiness(local.hourly_aggregate_ready)}
                      </ValueRow>
                      <ValueRow label={copy.activity}>
                        {local.active_operation
                          ? Object.prototype.hasOwnProperty.call(
                              copy.operations,
                              local.active_operation,
                            )
                            ? copy.operations[local.active_operation]
                            : copy.operations.maintenance
                          : local.active_status
                            ? copy.pendingTask
                            : copy.idle}
                      </ValueRow>
                      {local.active_status ? (
                        <ValueRow label={copy.state}>
                          {Object.prototype.hasOwnProperty.call(
                            copy.states,
                            local.active_status,
                          )
                            ? copy.states[local.active_status]
                            : copy.unknown}
                        </ValueRow>
                      ) : null}
                    </dl>
                  ) : (
                    <p className={styles.description}>{copy.unavailable}</p>
                  )}
                  {localChecked ? (
                    <p className={styles.checked}>
                      {copy.checked}{" "}
                      <time dateTime={localChecked.iso}>
                        {localChecked.label}
                      </time>
                    </p>
                  ) : null}
                </article>
              );
            })}
          </div>
        </section>
      ) : null}

      {checked ? (
        <p className={styles.checked}>
          {copy.checked} <time dateTime={checked.iso}>{checked.label}</time>
        </p>
      ) : null}
    </section>
  );
}
