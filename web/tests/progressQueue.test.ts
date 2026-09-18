import { describe, expect, it, vi } from "vitest";
import { ProgressQueue, type ProgressPatch } from "../src/progressQueue";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

describe("ProgressQueue", () => {
  it("merges queued patches instead of replacing them", async () => {
    const sent: ProgressPatch[] = [];
    const gate = deferred();
    const q = new ProgressQueue({
      send: async (patch) => {
        await gate.promise;
        sent.push(patch);
      },
    });
    q.enqueue({ page: 12 });
    const first = q.flush();
    q.enqueue({ finished: true });
    gate.resolve();
    await first;
    await flushMicrotasks();
    expect(sent).toEqual([{ page: 12 }, { finished: true }]);
    q.stop();
  });

  it("keeps a failed patch and re-delivers it under newer patches", async () => {
    const sent: ProgressPatch[] = [];
    let fail = true;
    const q = new ProgressQueue({
      send: async (patch) => {
        if (fail) throw new Error("offline");
        sent.push(patch);
      },
      retryBaseMs: 1,
    });
    const errors: unknown[] = [];
    q.enqueue({ page: 3 });
    const first = q.flush();
    q.enqueue({ percent: 0.5 });
    await first;
    await flushMicrotasks();
    expect(errors).toEqual([]);
    expect(sent).toEqual([]);
    fail = false;
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(sent).toEqual([{ page: 3, percent: 0.5 }]);
    q.stop();
  });

  it("never runs two deliveries at once", async () => {
    let inFlight = 0;
    let maxInFlight = 0;
    const q = new ProgressQueue({
      send: async () => {
        inFlight += 1;
        maxInFlight = Math.max(maxInFlight, inFlight);
        await new Promise((resolve) => setTimeout(resolve, 5));
        inFlight -= 1;
      },
    });
    q.enqueue({ page: 1 });
    const a = q.flush();
    q.enqueue({ page: 2 });
    const b = q.flush();
    await Promise.all([a, b]);
    expect(maxInFlight).toBe(1);
    q.stop();
  });

  it("applies explicit false and reports it as present", async () => {
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({ send: async (patch) => { sent.push(patch); } });
    q.enqueue({ finished: false });
    await q.flush();
    expect(sent).toEqual([{ finished: false }]);
    q.stop();
  });

  it("drops undefined fields from enqueued patches", async () => {
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({ send: async (patch) => { sent.push(patch); } });
    q.enqueue({ page: undefined, locator: "ch1" });
    await q.flush();
    expect(sent).toEqual([{ locator: "ch1" }]);
    q.stop();
  });

  it("does not retry after stop", async () => {
    const send = vi.fn(async () => {
      throw new Error("offline");
    });
    const q = new ProgressQueue({ send, retryBaseMs: 1 });
    q.enqueue({ page: 9 });
    const done = q.flush();
    await done;
    q.stop();
    const calls = send.mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(send.mock.calls.length).toBe(calls);
  });
});
