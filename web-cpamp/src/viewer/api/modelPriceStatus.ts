export interface ModelPriceStatusResponse {
  available: boolean;
  stale: boolean;
  checked_at_ms?: number;
  unpriced_models: string[];
  unpriced_count: number | null;
  model_count: number | null;
  partial_failed?: boolean;
  warnings?: string[];
  instances?: Array<{
    instance_id: string;
    instance_name: string;
    available: boolean;
    stale: boolean;
    checked_at_ms?: number;
    unpriced_models: string[];
    unpriced_count: number;
    model_count: number;
  }>;
}
