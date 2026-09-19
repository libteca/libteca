import { describe, expect, it, vi } from "vitest";
import { ProgressQueue, mergeServerProgress, type ProgressPatch } from "../src/progressQueue";

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

  it("persists the pending patch and reloads it into a new queue", async () => {
    const sent: ProgressPatch[] = [];
    let store: ProgressPatch = {};
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch) => { store = patch; },
    };
    const q = new ProgressQueue({ send: async (patch) => { sent.push(patch); }, storage });
    q.enqueue({ page: 40, percent: 0.4 });
    expect(store).toEqual({ page: 40, percent: 0.4 });
    const reloaded = new ProgressQueue({ send: async (patch) => { sent.push(patch); }, storage });
    expect(reloaded.snapshot()).toEqual({ page: 40, percent: 0.4 });
    await reloaded.flush();
    expect(sent).toEqual([{ page: 40, percent: 0.4 }]);
    expect(store).toEqual({});
    q.stop();
    reloaded.stop();
  });

  it("keeps the in-flight batch in storage until delivery succeeds", async () => {
    const gate = deferred();
    let store: ProgressPatch = {};
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch) => { store = patch; },
    };
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({
      send: async (patch) => {
        sent.push(patch);
        if (sent.length === 1) {
          await gate.promise;
          throw new Error("offline");
        }
      },
      storage,
      retryBaseMs: 100,
    });
    q.enqueue({ page: 3 });
    const first = q.flush();
    q.enqueue({ locator: "cfi(/6)" });
    expect(store).toEqual({ page: 3, locator: "cfi(/6)" });
    gate.resolve();
    await first;
    await flushMicrotasks();
    expect(store).toEqual({ page: 3, locator: "cfi(/6)" });
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(store).toEqual({});
    expect(sent).toEqual([{ page: 3 }, { page: 3, locator: "cfi(/6)" }]);
    q.stop();
  });

  it("ignores storage that throws or returns junk", async () => {
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({
      send: async (patch) => { sent.push(patch); },
      storage: {
        load: (): ProgressPatch => { throw new Error("unavailable"); },
        save: () => { throw new Error("unavailable"); },
      },
    });
    q.enqueue({ page: 1 });
    await q.flush();
    expect(sent).toEqual([{ page: 1 }]);
    q.stop();
  });
});

describe("mergeServerProgress", () => {
  it("keeps the server side when it is farther along", () => {
    expect(mergeServerProgress({ page: 100, percent: 0.9, revision: 4 }, { page: 40, percent: 0.4, locator: "40" })).toEqual({});
  });

  it("keeps the local side when it is farther along, with its locator", () => {
    expect(mergeServerProgress({ page: 10, revision: 4 }, { page: 40, percent: 0.4, locator: "cfi(/8)" })).toEqual({ page: 40, percent: 0.4, locator: "cfi(/8)" });
  });

  it("merges by percent when the patch carries no page", () => {
    expect(mergeServerProgress({ percent: 0.9, revision: 2 }, { percent: 0.5, locator: "a" })).toEqual({});
    expect(mergeServerProgress({ percent: 0.2, revision: 2 }, { percent: 0.5, locator: "a" })).toEqual({ percent: 0.5, locator: "a" });
  });

  it("keeps an explicit local reopen against a finished server state", () => {
    expect(mergeServerProgress({ page: 100, isFinished: true, revision: 6 }, { finished: false })).toEqual({ finished: false });
  });

  it("keeps local completion on top of any server state", () => {
    expect(mergeServerProgress({ page: 10, revision: 1 }, { finished: true })).toEqual({ finished: true });
  });

  it("keeps a locator-only patch", () => {
    expect(mergeServerProgress({ locator: "old", revision: 3 }, { locator: "new" })).toEqual({ locator: "new" });
  });

  it("drops the stale locator when the server page wins", () => {
    expect(mergeServerProgress({ page: 50, revision: 5 }, { page: 20, locator: "20" })).toEqual({});
  });
});
