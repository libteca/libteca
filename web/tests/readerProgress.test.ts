import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useProgressSaver } from "../src/reader/shared";
import { fetchWithDeadline, setToken } from "../src/api";
import { setCurrentUser } from "../src/user";

const hooks = vi.hoisted(() => ({ effects: [] as (() => void | (() => void))[], refs: [] as { current: unknown }[], cursor: 0 }));
vi.mock("preact/hooks", () => ({
  useState: (value: unknown) => [value, () => {}],
  useRef: (value: unknown) => hooks.refs[hooks.cursor++] ?? (hooks.refs[hooks.cursor - 1] = { current: value }),
  useCallback: (callback: unknown) => callback,
  useEffect: (effect: () => void | (() => void)) => { hooks.effects.push(effect); },
}));
vi.mock("../src/api", async (original) => ({ ...await original<object>(), fetchWithDeadline: vi.fn() }));

let data: Map<string, string>;
let beacon: ReturnType<typeof vi.fn>;
let cleanups: (() => void)[];

async function settle() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

function mount(edition = 9, revision = 10, remote = { page: 90, percent: 0.9 }) {
  hooks.cursor = 0;
  const saver = useProgressSaver(edition, revision, remote);
  for (const effect of hooks.effects.splice(0)) {
    const cleanup = effect();
    if (cleanup) cleanups.push(cleanup);
  }
  return saver;
}

beforeEach(() => {
  vi.useFakeTimers();
  hooks.refs = [];
  hooks.effects = [];
  hooks.cursor = 0;
  data = new Map();
  cleanups = [];
  beacon = vi.fn(() => Promise.resolve(new Response()));
  setCurrentUser(1);
  setToken("reader-token");
  vi.stubGlobal("window", { setTimeout, clearTimeout });
  vi.stubGlobal("document", { visibilityState: "visible", addEventListener: vi.fn(), removeEventListener: vi.fn() });
  vi.stubGlobal("addEventListener", vi.fn());
  vi.stubGlobal("removeEventListener", vi.fn());
  vi.stubGlobal("fetch", beacon);
  vi.stubGlobal("localStorage", {
    get length() { return data.size; },
    key: (i: number) => [...data.keys()][i] ?? null,
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value); },
    removeItem: (key: string) => { data.delete(key); },
  });
  vi.mocked(fetchWithDeadline).mockReset().mockResolvedValue({ response: new Response(), text: '{"revision":11}' });
});

afterEach(() => {
  for (const cleanup of cleanups) cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setToken("");
  setCurrentUser(null);
});

describe("reader progress transport integration", () => {
  it("recovers the full pending winner through the real hook and queue", async () => {
    data.set("libteca-progress-v2-u1-e9-a", JSON.stringify({ baseRevision: 1, patch: { page: 80, percent: 0.8, locator: "80" } }));
    data.set("libteca-progress-v2-u1-e9-b", JSON.stringify({ baseRevision: 1, patch: { page: 20, percent: 0.2, locator: "20" } }));
    mount(9, 1, { page: 10, percent: 0.1 });
    await settle();
    expect(fetchWithDeadline).toHaveBeenCalledTimes(1);
    expect(JSON.parse(vi.mocked(fetchWithDeadline).mock.calls[0][1]!.body as string))
      .toEqual({ page: 80, percent: 0.8, locator: "80", revision: 1 });
    expect(data.size).toBe(0);
  });

  it("applies the same high-water reconciliation to normal saves and lifecycle flushes", async () => {
    const saver = mount();
    saver.save({ page: 20, percent: 0.2, locator: "20" });
    await settle();
    expect(fetchWithDeadline).not.toHaveBeenCalled();
    saver.save({ page: 21, percent: 0.21, locator: "21" });
    saver.flush();
    expect(beacon).not.toHaveBeenCalled();
  });

  it("retains explicit completion intent when lifecycle position loses", async () => {
    const saver = mount();
    saver.save({ page: 20 });
    await settle();
    saver.save({ page: 21, finished: false });
    saver.flush();
    expect(beacon).toHaveBeenCalledTimes(1);
    expect(beacon.mock.calls[0][1].headers.Authorization).toBe("Bearer reader-token");
    expect(JSON.parse(beacon.mock.calls[0][1].body as string)).toEqual({ finished: false, revision: 10 });
  });

  it("updates the high-water mark after successful forward delivery", async () => {
    const saver = mount();
    saver.save({ page: 95, percent: 0.95, locator: "95" });
    await settle();
    saver.save({ page: 91, percent: 0.91, locator: "91" });
    saver.flush();
    expect(fetchWithDeadline).toHaveBeenCalledTimes(1);
    expect(beacon).not.toHaveBeenCalled();
  });

  it("retains a conflict revision even when rebasing discards its position", async () => {
    vi.mocked(fetchWithDeadline).mockResolvedValueOnce({
      response: new Response("", { status: 409 }),
      text: JSON.stringify({ error: "conflict", current: { revision: 20, page: 95, percent: 0.95 } }),
    });
    const saver = mount();
    saver.save({ page: 91 });
    await settle();
    saver.save({ page: 92, finished: false });
    saver.flush();
    expect(JSON.parse(beacon.mock.calls[0][1].body as string)).toEqual({ finished: false, revision: 20 });
  });

  it("includes an unresolved ordinary save in lifecycle delivery when storage is unavailable", async () => {
    vi.mocked(fetchWithDeadline).mockImplementation(() => new Promise(() => {}));
    localStorage.setItem = () => { throw new Error("quota"); };
    const saver = mount();
    saver.save({ page: 95, percent: 0.95, locator: "95" });
    await settle();
    expect(fetchWithDeadline).toHaveBeenCalledTimes(1);
    saver.flush();
    expect(JSON.parse(beacon.mock.calls[0][1].body as string))
      .toEqual({ page: 95, percent: 0.95, locator: "95", revision: 10 });
  });

  it("combines in-flight position with newer completion intent on close", async () => {
    vi.mocked(fetchWithDeadline).mockImplementation(() => new Promise(() => {}));
    const saver = mount();
    saver.save({ page: 95, percent: 0.95, locator: "95" });
    await settle();
    saver.save({ finished: true });
    saver.flush();
    expect(JSON.parse(beacon.mock.calls[0][1].body as string))
      .toEqual({ page: 95, percent: 0.95, locator: "95", finished: true, revision: 10 });
  });

  it("does not flush one user's pending state with another user's session", async () => {
    const saver = mount();
    saver.save({ page: 95 });
    await settle();
    saver.save({ page: 96 });
    setCurrentUser(2);
    saver.flush();
    expect(beacon).not.toHaveBeenCalled();
  });
});
