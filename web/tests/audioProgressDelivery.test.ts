import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AudioProgressSaver } from "../src/players/audioProgress";
import { setToken } from "../src/api";
let request: ReturnType<typeof vi.fn>;
beforeEach(() => {
  vi.useFakeTimers();
  setToken(`audio-${Math.random()}`);
  request = vi.fn().mockImplementation(async () => new Response("{}"));
  vi.stubGlobal("fetch", request);
});
afterEach(async () => {
  setToken("logged-out");
  await vi.advanceTimersByTimeAsync(100000);
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
async function settle() { for (let i = 0; i < 30; i++) await Promise.resolve(); }
function patches() { return request.mock.calls.map(([, opts]) => JSON.parse(opts.body)); }

describe("audio progress transport", () => {
  it("releases a stalled body by deadline and then saves the newer position", async () => {
    request.mockResolvedValueOnce({ status: 200, ok: true, text: () => new Promise(() => {}) });
    const saver = new AudioProgressSaver("/progress/1", 100);
    const first = saver.save(30), second = saver.save(10);
    await settle();
    expect(request).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(15000);
    await Promise.all([first, second]);
    expect(request.mock.calls[0][1].signal.aborted).toBe(true);
    expect(patches().map((p) => p.position)).toEqual([30, 10]);
    await vi.advanceTimersByTimeAsync(30000);
    expect(request).toHaveBeenCalledTimes(2);
  });
  it("checks HTTP failures and retains failed completion ahead of teardown positions", async () => {
    request.mockResolvedValueOnce(new Response('{"error":"unavailable"}', { status: 503 }));
    const saver = new AudioProgressSaver("/progress/2", 100);
    await saver.save(100, true);
    await saver.save(40);
    expect(patches()).toEqual([{ position: 100, duration: 100, finished: true }, { position: 100, duration: 100, finished: true }]);
  });
  it("serializes separate sessions sharing the same endpoint", async () => {
    let resolve!: (value: Response) => void;
    request.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const a = new AudioProgressSaver("/progress/3", 100);
    const first = a.save(80);
    await settle();
    const b = new AudioProgressSaver("/progress/3", 100);
    const second = b.start(0);
    expect(request).toHaveBeenCalledTimes(1);
    resolve(new Response("{}"));
    await Promise.all([first, second]);
    expect(patches().map((p) => p.position)).toEqual([80, 0]);
  });
  it("does not let an older failed completion undo an explicit restart", async () => {
    request.mockResolvedValueOnce(new Response("{}", { status: 503 }));
    const old = new AudioProgressSaver("/progress/4", 100);
    await old.save(100, true);
    const restarted = new AudioProgressSaver("/progress/4", 100);
    await restarted.start(0);
    await restarted.save(5);
    await vi.advanceTimersByTimeAsync(30000);
    expect(patches().map((p) => [p.position, p.finished])).toEqual([[100, true], [0, false], [5, false]]);
  });
  it("discards retries and queued writes when the login changes", async () => {
    request.mockResolvedValueOnce(new Response("{}", { status: 503 }));
    const saver = new AudioProgressSaver("/progress/5", 100);
    await saver.save(100, true);
    setToken("different-user");
    await saver.save(50);
    await vi.advanceTimersByTimeAsync(30000);
    expect(request).toHaveBeenCalledTimes(1);
  });
});
