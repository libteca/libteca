import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiWithDeadline, RequestTimeoutError, setToken } from "../src/api";

beforeEach(() => { vi.useFakeTimers(); setToken("deadline-test"); });
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("background API deadlines", () => {
  it("bounds stalled response headers and aborts the fetch", async () => {
    const fetch = vi.fn(() => new Promise<Response>(() => {}));
    vi.stubGlobal("fetch", fetch);
    const pending = apiWithDeadline("/status", {}, 1000);
    const check = expect(pending).rejects.toBeInstanceOf(RequestTimeoutError);
    await vi.advanceTimersByTimeAsync(1000);
    await check;
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  });

  it("bounds a stalled response body", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ status: 200, text: () => new Promise<string>(() => {}) })));
    const pending = apiWithDeadline("/status", {}, 1000);
    const check = expect(pending).rejects.toBeInstanceOf(RequestTimeoutError);
    await vi.advanceTimersByTimeAsync(1000);
    await check;
  });

  it("keeps authenticated API parsing and clears its timer on success", async () => {
    const fetch = vi.fn(async () => new Response('{"status":"done"}', { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await expect(apiWithDeadline("/status")).resolves.toEqual({ status: "done" });
    expect(fetch.mock.calls[0][1].headers.get("Authorization")).toBe("Bearer deadline-test");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("honors parent cancellation while a request is pending", async () => {
    const fetch = vi.fn(() => new Promise<Response>(() => {}));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    const pending = apiWithDeadline("/status", { signal: controller.signal });
    const check = expect(pending).rejects.toMatchObject({ name: "AbortError" });
    controller.abort();
    await check;
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });
});
