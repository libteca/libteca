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
    const a = new VideoProgressSaver(600, sendA);
    const finished = a.save(600, true);
    await Promise.resolve();
    const b = new VideoProgressSaver(800, sendB);
    pending.resolve();
    await finished;
    await b.save(30);
    expect(sendA).toHaveBeenCalledWith({ position: 600, duration: 600, finished: true });
    expect(sendB).toHaveBeenCalledWith({ position: 30, duration: 800, finished: false });
  });

  it("serializes heartbeat and completion and ignores later ordinary saves", async () => {
    const pending = deferred();
    const send = vi.fn().mockImplementationOnce(() => pending.promise).mockResolvedValue(undefined);
    const saver = new VideoProgressSaver(600, send);
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
    const saver = new VideoProgressSaver(600, send);
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
    const saver = new VideoProgressSaver(600, send);
    await saver.save(30);
    await saver.save(30);
    await saver.save(30);
    expect(send).toHaveBeenCalledTimes(2);
  });
});
