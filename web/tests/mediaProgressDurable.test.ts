import { afterEach, describe, expect, it, vi } from "vitest";
import { DurableMediaQueue, type MediaEnvelope, type MediaSnapshot } from "../src/mediaProgress";
class MemoryStorage implements Storage {
  entries = new Map<string, string>(); deny = false;
  get length() { return this.entries.size; }
  clear() { this.entries.clear(); }
  getItem(k: string) { return this.entries.get(k) ?? null; }
  key(i: number) { return [...this.entries.keys()][i] ?? null; }
  removeItem(k: string) { this.entries.delete(k); }
  setItem(k: string, v: string) { if (this.deny) throw new DOMException("full", "QuotaExceededError"); this.entries.set(k, v); }
}
function harness() {
  const storage = new MemoryStorage(), applied: MediaEnvelope[] = [], warnings: string[] = [];
  let identity = { ownerId: "1" as string | null, token: "secret", fence: 1 };
  const remote: MediaSnapshot = { ownerId: "1", kind: "edition", targetId: 7, generation: "7:1", revision: 0, resetGeneration: 0, deleted: false, position: 0, finished: false, files: [{ id: 3, duration: 100, missing: false }] };
  const receipts = new Map<string, any>(); let offline = false, lost = false, hold: Promise<void> | undefined;
  let active = 0, maxActive = 0, tail = Promise.resolve();
  const request = vi.fn(async (path: string, opts?: RequestInit) => {
    if (path.startsWith("/media-progress/")) return structuredClone(remote);
    if (path.startsWith("/media-operations/")) return receipts.get(path.split("/").at(-1)!) ?? { error: "missing", status: 404 };
    active++; maxActive = Math.max(maxActive, active);
    try {
      if (hold) await hold;
      if (offline) throw new Error("offline");
      const op: MediaEnvelope = JSON.parse(String(opts?.body));
      expect([...storage.entries.values()].some(v => v.includes(op.operationId))).toBe(true);
      const base = op.predecessorId ? receipts.get(op.predecessorId)?.revision : op.baseRevision;
      if (base !== remote.revision || op.resetGeneration !== remote.resetGeneration || op.generation !== remote.generation) return { error: "conflict", status: 409 };
      remote.revision++; remote.position = op.position; applied.push(op);
      const receipt = { operationId: op.operationId, revision: remote.revision, resetGeneration: remote.resetGeneration };
      receipts.set(op.operationId, receipt); if (lost) throw new Error("lost ack"); return receipt;
    } finally { active--; }
  });
  const lock = async <T>(_name: string, action: () => Promise<T>) => {
    const previous = tail; let release!: () => void; tail = new Promise(resolve => { release = resolve; });
    await previous; try { return await action(); } finally { release(); }
  };
  const create = () => new DurableMediaQueue({ storage: () => storage, identity: () => identity, request, lock, warn: s => warnings.push(s) });
  return { storage, create, applied, warnings, remote, request, maxActive: () => maxActive, offline: (v: boolean) => { offline = v; }, loseAck: (v: boolean) => { lost = v; }, hold: (v: Promise<void>) => { hold = v; }, changeOwner: () => { identity = { ownerId: "2", token: "other", fence: 2 }; } };
}
const path = "/progress/7", patch = (position: number, finished = false) => ({ position, duration: 100, finished });
const ops = (s: MemoryStorage) => [...s.entries.entries()].filter(([k]) => k.startsWith("libteca-media-v1:op:"));
async function settle() { for (let i = 0; i < 50; i++) await Promise.resolve(); }
afterEach(() => vi.useRealTimers());
describe("durable media transport", () => {
  it("persists every operation before blocked network completion", async () => {
    const h = harness(), q = h.create(); await q.prepare(path); let release!: () => void; h.hold(new Promise(r => { release = r; }));
    const a = q.enqueue(path, patch(40)); await settle(); const b = q.enqueue(path, { ...patch(0), mediaIntent: "seek" }); await settle();
    expect(ops(h.storage)).toHaveLength(2); const envelopes = ops(h.storage).map(([, v]) => JSON.parse(v).operation);
    expect(envelopes.map(o => o.baseRevision)).toEqual([0, 0]); expect(envelopes[1].predecessorId).toBe(envelopes[0].operationId);
    release(); await Promise.all([a, b]); expect(h.applied.map(o => o.position)).toEqual([40, 0]); expect(h.maxActive()).toBe(1); expect(ops(h.storage)).toHaveLength(0);
  });
  it("settles lost acknowledgement by receipt after reload", async () => {
    vi.useFakeTimers(); const h = harness(), q = h.create(); await q.prepare(path); h.loseAck(true); await q.enqueue(path, patch(50));
    expect(ops(h.storage)).toHaveLength(1); const original = ops(h.storage)[0][1]; h.loseAck(false); await h.create().replay();
    expect(h.applied).toHaveLength(1); expect(ops(h.storage)).toHaveLength(0); expect(JSON.parse(original).operation.baseRevision).toBe(0);
  });
  it("replays offline rewind and finish with original bases", async () => {
    vi.useFakeTimers(); const h = harness(), q = h.create(); await q.prepare(path); h.offline(true);
    await q.enqueue(path, patch(60)); await q.enqueue(path, { ...patch(10), mediaIntent: "seek" }); await q.enqueue(path, patch(100, true));
    expect(ops(h.storage)).toHaveLength(3); h.offline(false); await h.create().replay();
    expect(h.applied.map(o => [o.position, o.intent, o.baseRevision])).toEqual([[60, "heartbeat", 0], [10, "seek", 0], [100, "finish", 0]]);
  });
  it("refuses quota failures before mutation transport", async () => {
    vi.useFakeTimers(); const h = harness(), q = h.create(); await q.prepare(path); h.storage.deny = true;
    await expect(q.enqueue(path, patch(20))).rejects.toThrow(); expect(h.request.mock.calls.filter(([p]) => p === "/media-operations")).toHaveLength(0);
  });
  it("quarantines reset and dependencies without rebasing", async () => {
    vi.useFakeTimers(); const h = harness(), q = h.create(); await q.prepare(path); h.offline(true); await q.enqueue(path, patch(60)); await q.enqueue(path, patch(20));
    const original = ops(h.storage).map(([, v]) => v); h.remote.resetGeneration++; h.remote.revision++; h.offline(false); await q.replay();
    expect(h.applied).toHaveLength(0); expect(ops(h.storage).map(([, v]) => v)).toEqual(original);
    expect([...h.storage.entries.keys()].filter(k => k.includes("quarantine"))).toHaveLength(2);
    await q.enqueue(path, { ...patch(0), mediaIntent: "restart" }); expect(h.applied.map(o => o.position)).toEqual([0]); expect(ops(h.storage)).toHaveLength(2);
  });
  it("requires a new deliberate seek after a competing device save", async () => {
    const h = harness(), q = h.create(); await q.prepare(path); h.remote.revision = 10; h.remote.position = 90;
    await q.enqueue(path, { ...patch(5), mediaIntent: "seek" }); expect(h.remote.position).toBe(90); expect(ops(h.storage)).toHaveLength(1);
    await q.enqueue(path, patch(6)); expect(h.applied).toHaveLength(0); await q.enqueue(path, { ...patch(2), mediaIntent: "seek" }); expect(h.remote.position).toBe(2);
  });
  it("fences late acknowledgement after account replacement", async () => {
    const h = harness(), q = h.create(); await q.prepare(path); let release!: () => void; h.hold(new Promise(r => { release = r; }));
    const pending = q.enqueue(path, patch(30)); await settle(); h.changeOwner(); release(); await pending;
    expect(ops(h.storage)).toHaveLength(1); await q.replay(); expect(ops(h.storage)).toHaveLength(1); expect(h.applied).toHaveLength(1);
  });
  it("uses one cooperating lock across two tabs", async () => {
    const h = harness(), a = h.create(), b = h.create(); await Promise.all([a.prepare(path), b.prepare(path)]);
    await Promise.all([a.enqueue(path, patch(20)), b.enqueue(path, { ...patch(0), mediaIntent: "seek" })]);
    expect(h.maxActive()).toBe(1); expect(h.applied).toHaveLength(1); expect(ops(h.storage)).toHaveLength(1);
  });
  it("retains unsupported records without sending them", async () => {
    const h = harness(); h.storage.setItem("libteca-media-v1:op:1:bad", '{"operation":{"version":2}}'); await h.create().replay();
    expect(h.storage.getItem("libteca-media-v1:op:1:bad")).not.toBeNull(); expect(h.applied).toHaveLength(0); expect(h.warnings).toHaveLength(1);
  });
});

it("retains changed-content heartbeat evidence until a deliberate new seek", async () => {
 const h=harness(); h.remote.resumeConflict=true; const q=h.create(); await q.prepare(path);
 await q.enqueue(path,patch(1)); expect(h.applied).toHaveLength(0); const original=ops(h.storage)[0][1]; expect(h.warnings.some(w=>w.includes("changed media"))).toBe(true);
 await q.enqueue(path,{...patch(0),mediaIntent:"restart"}); expect(h.applied.map(o=>o.position)).toEqual([0]); expect(ops(h.storage)[0][1]).toBe(original);
});

it("retains the exact completion across teardown and recovers storage before one send", async () => {
  vi.useFakeTimers();
  const h = harness(), queue = h.create(); await queue.prepare(path); h.storage.deny = true;
  let player: { ended: () => Promise<unknown> } | null = { ended: () => queue.enqueue(path, patch(100, true)) };
  await expect(player.ended()).rejects.toThrow("storage");
  player = null;
  expect(h.applied).toHaveLength(0); expect(ops(h.storage)).toHaveLength(0);
  await expect(queue.enqueue(path, patch(0))).rejects.toThrow("Completion");
  h.storage.deny = false; h.loseAck(true); await queue.replay();
  expect(h.applied).toHaveLength(1);
  const saved = ops(h.storage)[0][1]; const operation = JSON.parse(saved).operation;
  expect([operation.intent, operation.fileId, operation.fileOffset, operation.position, operation.baseRevision]).toEqual(["finish", 3, 100, 100, 0]);
  h.loseAck(false); await queue.replay(); await queue.replay();
  expect(h.applied).toHaveLength(1); expect(ops(h.storage)).toHaveLength(0);
});
it("preserves caller heartbeat intent for a lower automatic report", async () => {
  vi.useFakeTimers(); const h=harness(), queue=h.create(); await queue.prepare(path); h.offline(true);
  await queue.enqueue(path,patch(50)); await queue.enqueue(path,patch(10));
  expect(ops(h.storage).map(([,raw])=>JSON.parse(raw).operation.intent)).toEqual(["heartbeat","heartbeat"]);
});
it("does not recover another owner's volatile completion", async () => {
  vi.useFakeTimers(); const h=harness(), queue=h.create(); await queue.prepare(path); h.storage.deny=true;
  await expect(queue.enqueue(path,patch(100,true))).rejects.toThrow(); h.changeOwner(); h.storage.deny=false; await queue.replay();
  expect(h.applied).toHaveLength(0); expect(ops(h.storage)).toHaveLength(0);
});
