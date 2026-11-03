import { useEffect } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { triggerHeaderRefresh } from "@/hooks/useHeaderRefresh";
import type { KeyQuotaResponse } from "@/viewer/api/types";
import { useViewerKeyQuotas } from "./useViewerKeyQuotas";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { keyQuotas } = vi.hoisted(() => ({
  keyQuotas: vi.fn<(signal?: AbortSignal) => Promise<KeyQuotaResponse>>(),
}));
vi.mock("@/viewer/viewerApi", () => ({ viewerApi: { keyQuotas } }));

const snapshot: KeyQuotaResponse = {
  configured: true,
  stale: false,
  updated_at: "2026-09-07T06:00:00Z",
  items: [{ id: "view_public", name: "Public", state: "unlimited" }],
};

describe("Key quota refresh lifecycle", () => {
  let renderer: ReactTestRenderer | undefined;
  let latest: ReturnType<typeof useViewerKeyQuotas>;
  let visibility: "visible" | "hidden";
  let fakeDocument: EventTarget;

  function Harness() {
    const result = useViewerKeyQuotas();
    useEffect(() => { latest = result; }, [result]);
    return null;
  }

  async function mount() {
    await act(async () => { renderer = create(<Harness />); });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  }

  async function setVisibility(value: "visible" | "hidden") {
    visibility = value;
    await act(async () => { fakeDocument.dispatchEvent(new Event("visibilitychange")); });
  }

  beforeEach(() => {
    vi.useFakeTimers();
    visibility = "visible";
    fakeDocument = new EventTarget();
    Object.defineProperty(fakeDocument, "visibilityState", { get: () => visibility });
    vi.stubGlobal("document", fakeDocument);
    keyQuotas.mockReset();
    keyQuotas.mockResolvedValue(snapshot);
  });

  afterEach(async () => {
    if (renderer) await act(async () => { renderer?.unmount(); });
    renderer = undefined;
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("refreshes every 30 seconds only while visible and mounted", async () => {
    await mount();
    expect(keyQuotas).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(keyQuotas).toHaveBeenCalledTimes(2);
    await setVisibility("hidden");
    await act(async () => { await vi.advanceTimersByTimeAsync(90_000); });
    expect(keyQuotas).toHaveBeenCalledTimes(2);
    await setVisibility("visible");
    expect(keyQuotas).toHaveBeenCalledTimes(3);
    await act(async () => { await triggerHeaderRefresh(); });
    expect(keyQuotas).toHaveBeenCalledTimes(4);
    await act(async () => { renderer?.unmount(); });
    renderer = undefined;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90_000);
      await triggerHeaderRefresh();
      fakeDocument.dispatchEvent(new Event("visibilitychange"));
    });
    expect(keyQuotas).toHaveBeenCalledTimes(4);
  });

  it("does not fetch when mounted in a hidden tab", async () => {
    visibility = "hidden";
    await mount();
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(keyQuotas).not.toHaveBeenCalled();
    await setVisibility("visible");
    expect(keyQuotas).toHaveBeenCalledTimes(1);
  });

  it("joins manual refresh to an in-flight poll and aborts on unmount", async () => {
    let resolve!: (value: KeyQuotaResponse) => void;
    keyQuotas.mockImplementation(() => new Promise((done) => { resolve = done; }));
    await mount();
    let first: Promise<void> | undefined;
    let second: Promise<void> | undefined;
    await act(async () => {
      first = latest.refresh();
      second = latest.refresh();
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(first).toBe(second);
    expect(keyQuotas).toHaveBeenCalledTimes(1);
    const signal = keyQuotas.mock.calls[0][0];
    await act(async () => { renderer?.unmount(); });
    renderer = undefined;
    expect(signal?.aborted).toBe(true);
    await act(async () => { resolve(snapshot); await first; });
    expect(keyQuotas).toHaveBeenCalledTimes(1);
  });

  it("preserves the last snapshot and its timestamp after a failed refresh", async () => {
    await mount();
    keyQuotas.mockRejectedValueOnce(new Error("upstream unavailable"));
    await act(async () => { await latest.refresh(); });
    expect(latest.data).toBe(snapshot);
    expect(latest.data?.updated_at).toBe("2026-09-07T06:00:00Z");
    expect(latest.failed).toBe(true);
    expect(latest.stale).toBe(true);
    await act(async () => { await latest.refresh(); });
    expect(latest.stale).toBe(false);
  });

  it("accepts partial successes as fresh data and clears warnings after recovery", async () => {
    const partial: KeyQuotaResponse = {
      ...snapshot,
      partial_failed: true,
      warnings: ["西站：额度暂不可用"],
      items: [{ ...snapshot.items[0], instance_id: "view_east", instance_name: "东站" }],
    };
    keyQuotas.mockResolvedValueOnce(partial);
    await mount();
    expect(latest.data).toBe(partial);
    expect(latest.failed).toBe(false);
    expect(latest.stale).toBe(false);
    await act(async () => { await latest.refresh(); });
    expect(latest.data).toBe(snapshot);
    expect(latest.data?.partial_failed).not.toBe(true);
    expect(latest.data?.warnings).toBeUndefined();
  });

  it("ignores an aborted hidden-tab request after a newer request completes", async () => {
    let resolveOld!: (value: KeyQuotaResponse) => void;
    keyQuotas.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
    await mount();
    const oldSignal = keyQuotas.mock.calls[0][0];
    await setVisibility("hidden");
    expect(oldSignal?.aborted).toBe(true);
    await setVisibility("visible");
    expect(latest.data).toBe(snapshot);
    await act(async () => { resolveOld({ ...snapshot, updated_at: "old" }); });
    expect(latest.data).toBe(snapshot);
  });
});
