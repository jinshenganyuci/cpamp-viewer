import { useCallback, useEffect, useRef, useState } from "react";
import { useHeaderRefresh } from "@/hooks/useHeaderRefresh";
import { viewerApi } from "@/viewer/viewerApi";
import type {
  ViewerAlias,
  ViewerAliasResponse,
  ViewerAnalyticsRequest,
  ViewerAnalyticsResponse,
  ViewerDashboardResponse,
  ViewerMaintenanceResponse,
  ViewerModelPricesResponse,
  ViewerQuotaResponse,
} from "@/viewer/model/viewerTypes";

type ViewerApiShape = {
  aliases: () => Promise<ViewerAliasResponse>;
  analytics: (
    payload: ViewerAnalyticsRequest,
    signal?: AbortSignal,
  ) => Promise<ViewerAnalyticsResponse>;
  dashboard: () => Promise<ViewerDashboardResponse>;
  maintenance: () => Promise<ViewerMaintenanceResponse>;
  modelPrices: () => Promise<ViewerModelPricesResponse>;
  quota: () => Promise<ViewerQuotaResponse>;
};

const api = viewerApi as unknown as ViewerApiShape;

export function useViewerRefresh(
  refresh: () => void | Promise<void>,
  enabled = true,
) {
  const refreshRef = useRef(refresh);

  useEffect(() => {
    refreshRef.current = refresh;
  }, [refresh]);

  const handler = useCallback(() => refreshRef.current(), []);
  useHeaderRefresh(handler, enabled);
}

export function useViewerAliases() {
  const [aliases, setAliases] = useState<ViewerAlias[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);

  const refresh = useCallback(async () => {
    try {
      const response = await api.aliases();
      setAliases(Array.isArray(response.items) ? response.items : []);
      setWarnings(response.warnings ?? []);
    } catch {
      setAliases([]);
      setWarnings(["API Key 名称暂不可用"]);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(timer);
  }, [refresh]);

  return { aliases, warnings, refresh };
}

export function useViewerModelPrices() {
  const [prices, setPrices] = useState<
    NonNullable<ViewerModelPricesResponse["prices"]>
  >({});
  const [instancePrices, setInstancePrices] = useState<
    Record<string, NonNullable<ViewerModelPricesResponse["prices"]>>
  >({});
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setError("");
    try {
      const response = await api.modelPrices();
      setPrices(response.prices ?? {});
      setInstancePrices(
        Object.fromEntries(
          (response.instances ?? [])
            .filter(
              (instance) =>
                instance.instance_id && instance.available !== false,
            )
            .map((instance) => [instance.instance_id!, instance.prices ?? {}]),
        ),
      );
      setError((response.warnings ?? []).join("；"));
    } catch (reason) {
      setPrices({});
      setInstancePrices({});
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(timer);
  }, [refresh]);

  return { prices, instancePrices, error, refresh };
}

export function useViewerAnalytics(
  request: ViewerAnalyticsRequest | (() => ViewerAnalyticsRequest),
  options: {
    autoRefreshMs?: number;
    enabled?: boolean;
    registerGlobalRefresh?: boolean;
    requestKey?: string;
  } = {},
) {
  const enabled = options.enabled !== false;
  const autoRefreshMs = options.autoRefreshMs;
  const registerGlobalRefresh = options.registerGlobalRefresh !== false;
  const [data, setData] = useState<ViewerAnalyticsResponse | null>(null);
  const [responseRequestKey, setResponseRequestKey] = useState<string | null>(
    null,
  );
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [lastRefreshedAt, setLastRefreshedAt] = useState<number | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const requestRef = useRef(request);
  const requestKey =
    options.requestKey ??
    (typeof request === "function"
      ? "viewer-request-factory"
      : JSON.stringify(request));

  useEffect(() => {
    requestRef.current = request;
  }, [request]);

  const refresh = useCallback(
    async (quiet = false) => {
      if (!enabled) {
        setLoading(false);
        return;
      }
      const activeRequestKey = requestKey;
      controllerRef.current?.abort();
      const controller = new AbortController();
      controllerRef.current = controller;
      if (!quiet) setLoading(true);
      setError("");
      try {
        const currentRequest = requestRef.current;
        const payload =
          typeof currentRequest === "function"
            ? currentRequest()
            : currentRequest;
        const response = await api.analytics(payload, controller.signal);
        if (!controller.signal.aborted && activeRequestKey === requestKey) {
          setData(response);
          setResponseRequestKey(activeRequestKey);
          setLastRefreshedAt(Date.now());
        }
      } catch (reason) {
        if (controller.signal.aborted) return;
        setError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    },
    [enabled, requestKey],
  );

  useEffect(() => {
    if (!enabled) {
      controllerRef.current?.abort();
      return;
    }
    const timer = window.setTimeout(() => void refresh(false), 120);
    return () => {
      window.clearTimeout(timer);
      controllerRef.current?.abort();
    };
  }, [enabled, refresh]);

  useEffect(() => {
    const interval = autoRefreshMs;
    if (!interval || interval < 1000) return;
    const timer = window.setInterval(() => void refresh(true), interval);
    return () => window.clearInterval(timer);
  }, [autoRefreshMs, refresh]);

  useViewerRefresh(() => refresh(false), enabled && registerGlobalRefresh);

  return {
    data,
    // Statistics can retain their last snapshot, but a coverage notice must
    // describe the currently selected request scope.
    coverage:
      enabled && responseRequestKey === requestKey ? data?.coverage : undefined,
    loading: enabled ? loading : false,
    error,
    lastRefreshedAt,
    refresh,
  };
}

export function useViewerDashboard() {
  const [data, setData] = useState<ViewerDashboardResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setData(await api.dashboard());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const kickoff = window.setTimeout(() => void refresh(), 0);
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      window.clearTimeout(kickoff);
      window.clearInterval(timer);
    };
  }, [refresh]);
  useViewerRefresh(refresh);

  return { data, loading, error, refresh };
}

export function useViewerMaintenance() {
  const [data, setData] = useState<ViewerMaintenanceResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setError("");
    try {
      setData(await api.maintenance());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const kickoff = window.setTimeout(() => void refresh(), 0);
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      window.clearTimeout(kickoff);
      window.clearInterval(timer);
    };
  }, [refresh]);
  useViewerRefresh(refresh);

  return { data, loading, error, refresh };
}

export function useViewerQuota(
  options: { registerGlobalRefresh?: boolean } = {},
) {
  const [data, setData] = useState<ViewerQuotaResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setData(await api.quota());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(timer);
  }, [refresh]);
  useViewerRefresh(refresh, options.registerGlobalRefresh !== false);

  return { data, loading, error, refresh };
}
