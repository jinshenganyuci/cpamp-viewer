export type UsageStatusOperation =
  "archive" | "verify" | "delete" | "compact" | "maintenance";

export interface ViewerUsageStatus {
  raw_event_count: number | null;
  raw_archived_event_count?: number | null;
  raw_deleted_event_count: number | null;
  raw_min_timestamp_ms?: number | null;
  raw_max_timestamp_ms?: number | null;
  migration_ready: boolean | null;
  hourly_aggregate_ready: boolean | null;
  storage: {
    database_bytes: number | null;
    wal_bytes: number | null;
    shm_bytes: number | null;
    total_bytes: number | null;
    reclaimable_bytes: number | null;
  };
  active_operation?: UsageStatusOperation;
  active_status?: string;
}

export interface UsageStatusResponse {
  available: boolean;
  stale: boolean;
  checked_at_ms?: number | null;
  status?: ViewerUsageStatus;
  partial_failed?: boolean;
  warnings?: string[];
  instances?: Array<{
    instance_id: string;
    instance_name: string;
    available: boolean;
    stale: boolean;
    checked_at_ms?: number;
    status?: ViewerUsageStatus;
  }>;
}
