import { describe, expect, it, vi } from "vitest";
import { VideoProgressSaver } from "../src/players/videoProgress";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("edition-scoped video progress", () => {
  it("does not let an old episode acknowledgement disable the next episode", async () => {
    const pending = deferred();
    const sendA = vi.fn(() => pending.promise);
    const sendB = vi.fn(async () => {});
    const a = new VideoProgressSaver("/progress/1", 600, sendA, "");
    const finished = a.save(600, true);
    await Promise.resolve();
    const b = new VideoProgressSaver("/progress/2", 800, sendB, "");
    pending.resolve();
    await finished;
    await b.save(30);
    expect(sendA).toHaveBeenCalledWith({ position: 600, duration: 600, finished: true });
    expect(sendB).toHaveBeenCalledWith({ position: 30, duration: 800, finished: false });
  });

  it("serializes heartbeat and completion and ignores later ordinary saves", async () => {
    const pending = deferred();
    const send = vi.fn().mockImplementationOnce(() => pending.promise).mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    const heartbeat = saver.save(590);
    const finished = saver.save(600, true);
    const paused = saver.save(600);
    await Promise.resolve();
    expect(send).toHaveBeenCalledTimes(1);
    pending.resolve();
    await Promise.all([heartbeat, finished, paused]);
    expect(send.mock.calls.map(([patch]) => patch.finished)).toEqual([false, true]);
  });

  it("retries failed completion instead of downgrading it through teardown", async () => {
    const send = vi.fn().mockRejectedValueOnce(new Error("ambiguous response")).mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(600, true);
    await saver.save(590);
    await saver.save(600);
    expect(send.mock.calls.map(([patch]) => patch)).toEqual([
      { position: 600, duration: 600, finished: true },
      { position: 600, duration: 600, finished: true },
    ]);
  });

  it("keeps a failed position eligible for retry", async () => {
    const send = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(30);
    await saver.save(30);
    await saver.save(30);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("persists an intentional zero rewind instead of dropping it", async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(60);
    await saver.save(0, false, true);
    expect(send.mock.calls.map(([patch]) => patch.position)).toEqual([60, 0]);
  });

  it("saves a first sub-second position when nothing is acknowledged", async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(0.25);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0].position).toBe(0.25);
  });

  it("rejects negative and non-finite positions without sending", async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(-1);
    await saver.save(NaN);
    await saver.save(Infinity);
    expect(send).not.toHaveBeenCalled();
  });

  it("bounds same-endpoint ordering across component replacement", async () => {
    const pending = deferred();
    const send = vi.fn().mockImplementationOnce(() => pending.promise).mockResolvedValue(undefined);
    const old = new VideoProgressSaver("/progress/1", 600, send, "");
    const oldWrite = old.save(65);
    await Promise.resolve();
    const fresh = new VideoProgressSaver("/progress/1", 600, send, "");
    const freshWrite = fresh.save(10);
    pending.resolve();
    await Promise.all([oldWrite, freshWrite]);
    expect(send.mock.calls.map(([patch]) => patch.position)).toEqual([65, 10]);
  });

  it("supersedes a failed old completion when the same endpoint restarts", async () => {
    vi.useFakeTimers();
    const send = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    const old = new VideoProgressSaver("/progress/1", 600, send, "");
    await old.save(600, true);
    const fresh = new VideoProgressSaver("/progress/1", 600, send, "");
    await fresh.save(10);
    await vi.advanceTimersByTimeAsync(60000);
    expect(send.mock.calls.map(([patch]) => patch)).toEqual([
      { position: 600, duration: 600, finished: true },
      { position: 10, duration: 600, finished: false },
    ]);
    vi.useRealTimers();
  });

  it("keeps retrying a failed completion after its component is gone", async () => {
    vi.useFakeTimers();
    const send = vi.fn().mockRejectedValueOnce(new Error("offline")).mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(600, true);
    await vi.advanceTimersByTimeAsync(60000);
    expect(send.mock.calls.every(([patch]) => patch.finished)).toBe(true);
    expect(send).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });

  it("drops a terminally rejected patch instead of retrying forever", async () => {
    vi.useFakeTimers();
    const { APIError } = await import("../src/api");
    const send = vi.fn().mockRejectedValue(new APIError("gone", 410));
    const saver = new VideoProgressSaver("/progress/1", 600, send, "");
    await saver.save(600, true);
    await vi.advanceTimersByTimeAsync(120000);
    expect(send).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
});
