import { describe, expect, it, vi } from "vitest";
import {
  mergeServerProgress, parseProgressPatch, parseStoredProgress, ProgressQueue, ResetConflictError,
  type ProgressPatch, type StoredProgress,
} from "../src/progressQueue";

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

  it("persists the pending patch with its base revision and replays against the stored base", async () => {
    const sent: { patch: ProgressPatch; base: number }[] = [];
    let store: StoredProgress | null = null;
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => {
        store = { baseRevision, resetGeneration, patch };
      },
      clear: () => { store = null; },
    };
    const q = new ProgressQueue({
      send: async (patch, base) => { sent.push({ patch, base }); },
      storage,
      initialBaseRevision: 3,
    });
    q.enqueue({ page: 40, percent: 0.4 });
    expect(store).toEqual({ baseRevision: 3, resetGeneration: 0, patch: { page: 40, percent: 0.4 } });
    const reloaded = new ProgressQueue({
      send: async (patch, base) => { sent.push({ patch, base }); return { revision: 7 }; },
      storage,
      initialBaseRevision: 9,
    });
    expect(reloaded.base()).toBe(3);
    expect(reloaded.snapshot()).toEqual({ page: 40, percent: 0.4 });
    await reloaded.flush();
    expect(sent).toEqual([{ patch: { page: 40, percent: 0.4 }, base: 3 }]);
    expect(reloaded.base()).toBe(7);
    expect(store).toBeNull();
    q.stop();
    reloaded.stop();
  });

  it("keeps the in-flight batch in storage until delivery succeeds", async () => {
    const gate = deferred();
    let store: StoredProgress | null = null;
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => {
        store = { baseRevision, resetGeneration, patch };
      },
      clear: () => { store = null; },
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
    expect(store?.patch).toEqual({ page: 3, locator: "cfi(/6)" });
    gate.resolve();
    await first;
    await flushMicrotasks();
    expect(store?.patch).toEqual({ page: 3, locator: "cfi(/6)" });
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(store).toBeNull();
    expect(sent).toEqual([{ page: 3 }, { page: 3, locator: "cfi(/6)" }]);
    q.stop();
  });

  it("ignores storage that throws or returns junk", async () => {
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({
      send: async (patch) => { sent.push(patch); },
      storage: {
        load: () => { throw new Error("unavailable"); },
        save: () => { throw new Error("unavailable"); },
        clear: () => { throw new Error("unavailable"); },
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

  it("keeps a locator-only patch against an empty server state", () => {
    expect(mergeServerProgress({}, { locator: "new" })).toEqual({ locator: "new" });
  });

  it("never lets an unordered locator defeat a positioned server state", () => {
    expect(mergeServerProgress({ percent: 0.9, locator: "far", revision: 7 }, { locator: "early" })).toEqual({});
    expect(mergeServerProgress({ page: 12, revision: 3 }, { locator: "early" })).toEqual({});
    expect(mergeServerProgress({ locator: "old", revision: 3 }, { locator: "new" })).toEqual({});
    expect(mergeServerProgress({ page: 12, revision: 3 }, { locator: "early", finished: true })).toEqual({ finished: true });
  });

  it("drops the stale locator when the server page wins", () => {
    expect(mergeServerProgress({ page: 50, revision: 5 }, { page: 20, locator: "20" })).toEqual({});
  });
});

describe("terminal error policy", () => {
  class GoneError extends Error {}

  it("drops a patch the policy declares permanent and keeps delivering later work", async () => {
    const sent: ProgressPatch[] = [];
    const q = new ProgressQueue({
      send: async (patch) => {
        if (patch.page === 3) throw new GoneError();
        sent.push(patch);
      },
      shouldRetry: (error) => !(error instanceof GoneError),
      retryBaseMs: 1,
    });
    q.enqueue({ page: 3 });
    q.enqueue({ page: 4 });
    await q.flush();
    await flushMicrotasks();
    expect(sent).toEqual([{ page: 4 }]);
    q.stop();
  });

  it("retries when the policy allows", async () => {
    const sent: ProgressPatch[] = [];
    let fail = true;
    const q = new ProgressQueue({
      send: async (patch) => {
        if (fail) throw new Error("offline");
        sent.push(patch);
      },
      shouldRetry: () => true,
      retryBaseMs: 1,
    });
    q.enqueue({ page: 5 });
    await q.flush();
    fail = false;
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(sent).toEqual([{ page: 5 }]);
    q.stop();
  });
});

describe("reset-wins delivery policy", () => {
  it("quarantines a reset-conflicted operation and rebases onto the server lineage", async () => {
    let store: StoredProgress | null = null;
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => {
        store = { baseRevision, resetGeneration, patch };
      },
      clear: () => { store = null; },
    };
    const delivered: { patch: ProgressPatch; base: number; generation: number }[] = [];
    let conflict = true;
    const q = new ProgressQueue({
      send: async (patch, base, generation) => {
        if (conflict) throw new ResetConflictError({ revision: 6, resetGeneration: 2, deleted: true });
        delivered.push({ patch, base, generation });
        return { revision: 7, resetGeneration: 2 };
      },
      storage,
      initialBaseRevision: 5,
      initialResetGeneration: 0,
      shouldRetry: () => true,
      retryBaseMs: 1,
    });
    q.enqueue({ page: 80 });
    await q.flush();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(q.snapshot()).toEqual({});
    expect(q.base()).toBe(6);
    expect(q.generation()).toBe(2);
    expect(store).toEqual({ baseRevision: 5, resetGeneration: 0, patch: { page: 80 } });

    conflict = false;
    q.enqueue({ page: 3 });
    expect(store).toEqual({ baseRevision: 6, resetGeneration: 2, patch: { page: 3 } });
    await q.flush();
    expect(delivered).toEqual([{ patch: { page: 3 }, base: 6, generation: 2 }]);
    expect(q.base()).toBe(7);
    expect(store).toBeNull();
    q.stop();
  });

  it("delivers input enqueued during a conflicting delivery on the post-reset lineage", async () => {
    let store: StoredProgress | null = null;
    const storage = {
      load: () => store,
      save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => {
        store = { baseRevision, resetGeneration, patch };
      },
      clear: () => { store = null; },
    };
    const gate = deferred();
    const delivered: { patch: ProgressPatch; base: number; generation: number }[] = [];
    let conflict = true;
    const q = new ProgressQueue({
      send: async (patch, base, generation) => {
        if (conflict) {
          await gate.promise;
          conflict = false;
          throw new ResetConflictError({ revision: 6, resetGeneration: 2, deleted: true });
        }
        delivered.push({ patch, base, generation });
        return { revision: 7, resetGeneration: 2 };
      },
      storage,
      initialBaseRevision: 5,
      initialResetGeneration: 0,
    });
    q.enqueue({ page: 80 });
    const first = q.flush();
    q.enqueue({ page: 3 });
    gate.resolve();
    await first;
    await flushMicrotasks();
    expect(delivered).toEqual([{ patch: { page: 3 }, base: 6, generation: 2 }]);
    expect(store).toBeNull();
    q.stop();
  });

  it("does not replay durable recovery from an older generation than the server", () => {
    let store: StoredProgress | null = { baseRevision: 4, resetGeneration: 0, patch: { page: 80 } };
    const q = new ProgressQueue({
      send: async () => { throw new Error("must not deliver quarantined work"); },
      storage: {
        load: () => store,
        save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => {
          store = { baseRevision, resetGeneration, patch };
        },
        clear: () => { store = null; },
      },
      initialBaseRevision: 6,
      initialResetGeneration: 1,
    });
    expect(q.snapshot()).toEqual({});
    expect(q.base()).toBe(6);
    expect(q.generation()).toBe(1);
    expect(store).toEqual({ baseRevision: 4, resetGeneration: 0, patch: { page: 80 } });
    q.stop();
  });
});

describe("parseProgressPatch", () => {
  it("accepts a well-formed record", () => {
    expect(parseProgressPatch({ page: 3, percent: 0.5, locator: "cfi(/6)", finished: true }))
      .toEqual({ page: 3, percent: 0.5, locator: "cfi(/6)", finished: true });
  });

  it("rejects malformed values", () => {
    for (const bad of [
      { page: "three" }, { page: -1 }, { page: 1.5 },
      { percent: 2 }, { percent: "half" },
      { locator: 42 }, { finished: "yes" }, [], "x", null,
    ]) {
      expect(() => parseProgressPatch(bad)).toThrow();
    }
    expect(() => parseProgressPatch({ locator: "x".repeat(8193) })).toThrow();
  });

  it("rejects stored records without a valid base revision", () => {
    expect(() => parseStoredProgress({ patch: {} })).toThrow();
    expect(() => parseStoredProgress({ baseRevision: -1, patch: {} })).toThrow();
    expect(() => parseStoredProgress({ baseRevision: 4, resetGeneration: -1, patch: {} })).toThrow();
    expect(parseStoredProgress({ baseRevision: 4, patch: { page: 1 } })).toEqual({ baseRevision: 4, resetGeneration: 0, patch: { page: 1 } });
    expect(parseStoredProgress({ baseRevision: 4, resetGeneration: 2, patch: { page: 1 } })).toEqual({ baseRevision: 4, resetGeneration: 2, patch: { page: 1 } });
  });
});
