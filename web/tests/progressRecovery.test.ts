import { describe, expect, it } from "vitest";
import { createProgressStorage, mergeStoredProgress, type StoredProgress } from "../src/progressQueue";

function memoryStorage(entries: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(entries));
  return {
    get length() { return data.size; },
    key: (index) => [...data.keys()][index] ?? null,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => { data.set(key, value); },
    removeItem: (key) => { data.delete(key); },
    clear: () => data.clear(),
  };
}

const high: StoredProgress = { baseRevision: 3, resetGeneration: 0, patch: { page: 80, percent: 0.8, locator: "high" } };
const low: StoredProgress = { baseRevision: 1, resetGeneration: 0, patch: { page: 20, percent: 0.2, locator: "low" } };

describe("durable progress recovery", () => {
  it.each([[high, low], [low, high], [high, high]])("keeps the full winning position for %j then %j", (a, b) => {
    expect(mergeStoredProgress([a, b]))
      .toEqual({ baseRevision: Math.min(a.baseRevision, b.baseRevision), resetGeneration: 0, patch: high.patch });
  });

  it("keeps completion-only intent without losing the position", () => {
    expect(mergeStoredProgress([high, { baseRevision: 4, resetGeneration: 0, patch: { finished: false } }]))
      .toEqual({ baseRevision: 3, resetGeneration: 0, patch: { ...high.patch, finished: false } });
    expect(mergeStoredProgress([{ baseRevision: 4, resetGeneration: 0, patch: { finished: true } }, high]))
      .toEqual({ baseRevision: 3, resetGeneration: 0, patch: { ...high.patch, finished: true } });
  });

  it("never borrows a losing locator or percent", () => {
    expect(mergeStoredProgress([low, { baseRevision: 3, resetGeneration: 0, patch: { page: 80 } }]))
      .toEqual({ baseRevision: 1, resetGeneration: 0, patch: { page: 80 } });
    expect(mergeStoredProgress([high, { baseRevision: 3, resetGeneration: 0, patch: { locator: "unknown" } }]))
      .toEqual(high);
  });

  it("never merges recovery across reset generations", () => {
    const stale: StoredProgress = { baseRevision: 1, resetGeneration: 0, patch: { page: 80, percent: 0.8, locator: "old" } };
    const fresh: StoredProgress = { baseRevision: 4, resetGeneration: 1, patch: { page: 5, percent: 0.05, locator: "new" } };
    expect(mergeStoredProgress([stale, fresh])).toEqual(fresh);
    expect(mergeStoredProgress([fresh, stale])).toEqual(fresh);
  });

  it("publishes recovered state before removing original records", () => {
    const storage = memoryStorage({ "scope-a": JSON.stringify(high), "scope-b": JSON.stringify(low), "other-user": "keep" });
    const remove = storage.removeItem;
    storage.removeItem = (key) => {
      expect(storage.getItem("scope-own-1"))
        .toBe(JSON.stringify({ baseRevision: 1, resetGeneration: 0, patch: high.patch }));
      remove(key);
    };
    expect(createProgressStorage(storage, "scope-", "own").load())
      .toEqual({ baseRevision: 1, resetGeneration: 0, patch: high.patch });
    expect(storage.length).toBe(2);
    expect(storage.getItem("other-user")).toBe("keep");
  });

  it("preserves originals when replacement storage fails", () => {
    const storage = memoryStorage({ "scope-a": JSON.stringify(high), "scope-b": JSON.stringify(low) });
    storage.setItem = () => { throw new Error("quota"); };
    expect(createProgressStorage(storage, "scope-", "own").load())
      .toEqual({ baseRevision: 1, resetGeneration: 0, patch: high.patch });
    expect(storage.getItem("scope-a")).toBe(JSON.stringify(high));
    expect(storage.getItem("scope-b")).toBe(JSON.stringify(low));
  });

  it("does not remove a record updated after it was read", () => {
    const storage = memoryStorage({ "scope-a": JSON.stringify(low) });
    const set = storage.setItem;
    storage.setItem = (key, value) => {
      set(key, value);
      if (key === "scope-own-1") set("scope-a", JSON.stringify(high));
    };
    createProgressStorage(storage, "scope-", "own").load();
    expect(storage.getItem("scope-a")).toBe(JSON.stringify(high));
  });

  it("does not skip a valid record after quarantining malformed storage", () => {
    const storage = memoryStorage({ "scope-bad": "broken", "scope-good": JSON.stringify(high) });
    expect(createProgressStorage(storage, "scope-", "own").load())
      .toEqual({ baseRevision: 3, resetGeneration: 0, patch: high.patch });
  });

  it("keeps a live writer's newer record when recovery retires its older one", () => {
    const storage = memoryStorage();
    const writer = createProgressStorage(storage, "scope-", "a");
    writer.save(low.patch, low.baseRevision, 0);
    const remove = storage.removeItem;
    storage.removeItem = (key) => {
      if (key === "scope-a-1") writer.save(high.patch, high.baseRevision, 0);
      remove(key);
    };
    const recovered = createProgressStorage(storage, "scope-", "b").load();
    expect(recovered).toEqual({ baseRevision: 1, resetGeneration: 0, patch: low.patch });
    const survivors = Array.from({ length: storage.length }, (_, i) => storage.key(i)!).sort()
      .map((key) => JSON.parse(storage.getItem(key)!));
    expect(survivors.some((r) => r.patch.page === 80)).toBe(true);
  });

  it("leaves older-generation evidence in place instead of merging or dropping it", () => {
    const storage = memoryStorage({
      "scope-old": JSON.stringify({ baseRevision: 1, resetGeneration: 0, patch: { page: 80, percent: 0.8 } }),
      "scope-new": JSON.stringify({ baseRevision: 4, resetGeneration: 1, patch: { page: 5, percent: 0.05 } }),
    });
    expect(createProgressStorage(storage, "scope-", "own").load())
      .toEqual({ baseRevision: 4, resetGeneration: 1, patch: { page: 5, percent: 0.05 } });
    expect(JSON.parse(storage.getItem("scope-old")!)).toEqual({ baseRevision: 1, resetGeneration: 0, patch: { page: 80, percent: 0.8 } });
  });

  it("acknowledges only the exact immutable version it published", () => {
    const storage = memoryStorage();
    const writer = createProgressStorage(storage, "scope-", "a");
    writer.save(low.patch, low.baseRevision, 0);
    expect(storage.getItem("scope-a-1")).not.toBeNull();
    writer.save(high.patch, high.baseRevision, 0);
    expect(storage.getItem("scope-a-1")).toBeNull();
    expect(JSON.parse(storage.getItem("scope-a-2")!)).toEqual({ baseRevision: 3, resetGeneration: 0, patch: high.patch });
    writer.clear();
    expect(storage.getItem("scope-a-2")).toBeNull();
  });

  it("keeps a durable full-winner copy across two successive recovering tabs", () => {
    const storage = memoryStorage();
    createProgressStorage(storage, "scope-", "a").save(high.patch, high.baseRevision, 0);
    const first = createProgressStorage(storage, "scope-", "b").load();
    expect(first).toEqual({ baseRevision: 3, resetGeneration: 0, patch: high.patch });
    expect(storage.getItem("scope-a-1")).toBeNull();
    const second = createProgressStorage(storage, "scope-", "c").load();
    expect(second).toEqual({ baseRevision: 3, resetGeneration: 0, patch: high.patch });
    const survivors = Array.from({ length: storage.length }, (_, i) => storage.key(i)!);
    expect(survivors.some((k) => k.startsWith("scope-"))).toBe(true);
  });

  it("migrates legacy records by assigning the never-reset generation", () => {
    const storage = memoryStorage({ "scope-a": JSON.stringify({ baseRevision: 2, patch: { page: 40, percent: 0.4 } }) });
    expect(createProgressStorage(storage, "scope-", "own").load())
      .toEqual({ baseRevision: 2, resetGeneration: 0, patch: { page: 40, percent: 0.4 } });
  });
});
