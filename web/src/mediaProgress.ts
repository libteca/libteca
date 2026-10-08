import { mediaOperationId, withMediaReplayLock } from "./mediaProgressLock";
import { apiTransport } from "./api";
import { getMediaIdentity } from "./mediaProgressIdentity";

export type MediaSnapshot = {
  ownerId: string; kind: string; targetId: number; generation: string;
  revision: number; resetGeneration: number; deleted: boolean; position: number; finished: boolean;
  resumeConflict?: boolean;
  files: { id: number; duration: number; missing: boolean }[];
};
export type MediaEnvelope = {
  version: 1; operationId: string; ownerId: string; kind: string; targetId: number;
  installationId: string; sessionId: string; sequence: number; intentEpoch: number;
  generation: string; baseRevision: number; resetGeneration: number; predecessorId?: string;
  intent: "heartbeat" | "seek" | "restart" | "finish" | "reset";
  fileId: number; fileOffset: number; position: number; duration: number; finished: boolean;
};
type Receipt = { operationId: string; revision: number; resetGeneration: number };
type Identity = { ownerId: string | null; token: string; fence: number };
type Stored = { operation: MediaEnvelope; createdAt: number };
type Patch = { position: number; duration: number; finished: boolean; mediaIntent?: MediaEnvelope["intent"]; expectedGeneration?: string };
type Dependencies = {
  storage: () => Storage;
  identity: () => Identity;
  request: (path: string, opts?: RequestInit) => Promise<any>;
  lock: <T>(name: string, action: (owns?: () => boolean) => Promise<T>) => Promise<T>;
  warn: (message: string) => void;
};
const prefix = "libteca-media-v1:";
const validNumber = (n: unknown): n is number => typeof n === "number" && Number.isFinite(n) && n >= 0;
const targetOf = (path: string) => {
  const edition = /^\/progress\/(\d+)$/.exec(path);
  const podcast = /^\/podcasts\/episodes\/(\d+)\/progress$/.exec(path);
  return edition ? { kind: "edition", id: Number(edition[1]) } : podcast ? { kind: "podcast-episode", id: Number(podcast[1]) } : null;
};
export function isMediaProgressPath(path: string) { return targetOf(path) !== null; }

export class DurableMediaQueue {
  private volatile = new Map<string, { stored: Stored; identity: Identity }>();
  private bases = new Map<string, MediaSnapshot>();
  private preparations = new Map<string, Promise<void>>();
  private chains = new Map<string, { sessionId: string; sequence: number; epoch: number; predecessorId?: string; position?: number }>();
  private tail: Promise<unknown> = Promise.resolve();
  private retry: ReturnType<typeof setTimeout> | undefined;
  private retryDelay = 1000;
  private installationId: string | undefined;
  constructor(private readonly deps: Dependencies) {}
  private key(identity: Identity, path: string) { return `${identity.ownerId}:${identity.fence}:${path}`; }
  private same(identity: Identity) { const now = this.deps.identity(); return now.ownerId === identity.ownerId && now.token === identity.token && now.fence === identity.fence; }
  private async request(identity: Identity, path: string, opts: RequestInit = {}, owns = () => true) {
    if (!this.same(identity) || !owns()) throw new Error("Playback login or replay owner changed");
    const headers = new Headers(opts.headers);
    headers.set("Authorization", `Bearer ${identity.token}`);
    const result = await this.deps.request(path, { ...opts, headers });
    if (!this.same(identity) || !owns()) throw new Error("Playback login or replay owner changed");
    if (result?.error) throw Object.assign(new Error(result.error), { status: result.status });
    return result;
  }
  private install() {
    if (this.installationId) return this.installationId;
    const id = mediaOperationId();
    this.installationId = id;
    try {
      const storage = this.deps.storage();
      this.installationId = storage.getItem(`${prefix}installation`) || id;
      storage.setItem(`${prefix}installation`, this.installationId);
    } catch {}
    return this.installationId;
  }
  private async snapshot(identity: Identity, path: string) {
    const target = targetOf(path)!;
    const s = await this.request(identity, `/media-progress/${target.kind}/${target.id}`) as MediaSnapshot;
    if (s.ownerId !== identity.ownerId || s.kind !== target.kind || s.targetId !== target.id ||
      !s.generation || !Number.isSafeInteger(s.revision) || s.revision < 0 ||
      !Number.isSafeInteger(s.resetGeneration) || s.resetGeneration < 0 || !Array.isArray(s.files)) throw new Error("Invalid media progress snapshot");
    if (s.resumeConflict) this.deps.warn("Saved progress refers to changed media. Make a deliberate seek to choose a new starting point.");
    this.bases.set(this.key(identity, path), s);
    try { this.deps.storage().setItem(`${prefix}base:${s.ownerId}:${s.kind}:${s.targetId}`, JSON.stringify(s)); } catch {}
    return s;
  }
  rememberEdition(edition: { resumeConflict?: boolean; id: number; generation?: string; revision?: number; resetGeneration?: number; deleted?: boolean; position?: number; isFinished?: boolean; format: string; files: {id:number;duration:number}[] }) {
    const identity = this.deps.identity();
    if (!identity.ownerId || !edition.generation || !Number.isSafeInteger(edition.revision) || !Number.isSafeInteger(edition.resetGeneration) || !["audio","mp3","m4b","m4a","flac","ogg","wav","aac","opus","video"].includes(edition.format)) return;
    const snapshot: MediaSnapshot = { ownerId: identity.ownerId, kind: "edition", targetId: edition.id,
      generation: edition.generation, revision: edition.revision!, resetGeneration: edition.resetGeneration!,
      resumeConflict: edition.resumeConflict, position: edition.position ?? 0, finished: !!edition.isFinished, deleted: !!edition.deleted,
      files: edition.files.map(f => ({id:f.id,duration:f.duration,missing:false})) };
    if (snapshot.resumeConflict) this.deps.warn("Saved progress refers to changed media. Make a deliberate seek to choose a new starting point.");
    this.bases.set(this.key(identity, `/progress/${edition.id}`), snapshot);
    try { this.deps.storage().setItem(`${prefix}base:${identity.ownerId}:edition:${edition.id}`, JSON.stringify(snapshot)); } catch {}
  }
  generationForFile(id: number) {
    const identity = this.deps.identity();
    for (const [key, base] of this.bases) if (key.startsWith(`${identity.ownerId}:${identity.fence}:`) && base.files.some(file => file.id === id)) return base.generation;
    return undefined;
  }
  generation(path: string) { return this.bases.get(this.key(this.deps.identity(), path))?.generation; }

  prepare(path: string): Promise<void> {
    const identity = this.deps.identity();
    if (!identity.ownerId || !targetOf(path)) return Promise.resolve();
    const key = this.key(identity, path);
    if (!this.bases.has(key)) {
      try {
        const target = targetOf(path)!;
        const cached = JSON.parse(this.deps.storage().getItem(`${prefix}base:${identity.ownerId}:${target.kind}:${target.id}`) ?? "null") as MediaSnapshot;
        if (cached?.ownerId === identity.ownerId && cached.kind === target.kind && cached.targetId === target.id && cached.generation && Number.isSafeInteger(cached.revision) && Number.isSafeInteger(cached.resetGeneration) && Array.isArray(cached.files)) this.bases.set(key, cached);
      } catch {}
    }
    const existing = this.preparations.get(key);
    if (existing) return existing;
    if (this.bases.has(key)) return Promise.resolve();
    const preparation = this.snapshot(identity, path).then(() => {}).catch(() => {}).finally(() => this.preparations.delete(key));
    this.preparations.set(key, preparation);
    return preparation;
  }
  enqueue(path: string, patch: Patch): Promise<unknown> {
    const identity = this.deps.identity();
    const run = this.tail.catch(() => {}).then(async () => {
      if (!identity.ownerId || !this.same(identity)) throw new Error("Playback login changed");
      if (!validNumber(patch.position) || !validNumber(patch.duration)) throw new Error("Invalid media position");
      const key = this.key(identity, path);
      if (!this.bases.has(key)) await this.prepare(path);
      let base = this.bases.get(key);
      if (!base) {
        try { base = await this.snapshot(identity, path); }
        catch { this.deps.warn("Progress could not be saved: no verified resume state. Reconnect before playing."); throw new Error("Media progress base unavailable"); }
      }
      if (!this.same(identity)) throw new Error("Playback login changed");
      let chain = this.chains.get(key);
      if (!chain) { chain = { sessionId: mediaOperationId(), sequence: 0, epoch: 0 }; this.chains.set(key, chain); }
      let predecessorQuarantined = false;
      try { predecessorQuarantined = !!chain.predecessorId && !!this.deps.storage().getItem(`${prefix}quarantine:${prefix}op:${identity.ownerId}:${chain.predecessorId}`); } catch {}
      if (predecessorQuarantined) {
        if (patch.mediaIntent !== "seek" && patch.mediaIntent !== "restart" && patch.mediaIntent !== "reset") {
          this.deps.warn("Progress has a conflict. Make a deliberate seek to save a new position.");
          return { quarantined: true };
        }
        base = await this.snapshot(identity, path);
        chain = { sessionId: mediaOperationId(), sequence: 0, epoch: 0 };
        this.chains.set(key, chain);
      }
      for (const pending of this.volatile.values()) {
        if (pending.identity.ownerId === identity.ownerId && pending.identity.fence === identity.fence && pending.stored.operation.sessionId === chain.sessionId && pending.stored.operation.finished) {
          this.deps.warn("Completion is retained in this tab. Allow storage and use Retry saved progress.");
          throw new Error("Completion awaits persistence");
        }
      }
      const intent = patch.finished ? "finish" : patch.mediaIntent ?? "heartbeat";
      if (intent !== "heartbeat") chain.epoch++;
      const generation = patch.expectedGeneration ?? base.generation;
      let before = 0;
      let file = base.files[0];
      for (let i = 0; i < base.files.length; i++) {
        file = base.files[i];
        if (file.missing) throw new Error("Media file unavailable");
        if (file.duration <= 0 || before + file.duration > patch.position || i === base.files.length - 1) break;
        before += file.duration;
      }
      if (!file && intent !== "reset") throw new Error("Media file unavailable");
      const operation: MediaEnvelope = {
        version: 1, operationId: mediaOperationId(), ownerId: identity.ownerId,
        kind: base.kind, targetId: base.targetId, installationId: this.install(),
        sessionId: chain.sessionId, sequence: chain.sequence + 1, intentEpoch: chain.epoch,
        generation, baseRevision: base.revision, resetGeneration: base.resetGeneration,
        ...(chain.predecessorId ? { predecessorId: chain.predecessorId } : {}),
        intent, fileId: file?.id ?? 0, fileOffset: patch.position - before,
        position: patch.position, duration: patch.duration, finished: patch.finished,
      };
      const storageKey = `${prefix}op:${operation.ownerId}:${operation.operationId}`;
      const stored: Stored = { operation, createdAt: Date.now() };
      chain.sequence = operation.sequence;
      chain.predecessorId = operation.operationId;
      chain.position = operation.position;
      this.volatile.set(storageKey, { stored, identity });
      try {
        this.deps.storage().setItem(storageKey, JSON.stringify(stored));
        this.volatile.delete(storageKey);
      } catch {
        this.deps.warn("Progress is retained in this tab but could not be stored. Free storage or allow site storage, then use Retry saved progress.");
        this.scheduleRetry(identity);
        throw new Error("Media progress storage unavailable");
      }
      if (base.resumeConflict && !["seek", "restart", "reset"].includes(intent)) {
        this.deps.storage().setItem(`${prefix}quarantine:${prefix}op:${operation.ownerId}:${operation.operationId}`, "Saved progress refers to changed media");
        this.deps.warn("Saved progress refers to changed media. Make a deliberate seek to save a new position.");
      } else if (base.resumeConflict) { base.resumeConflict = false; }
      chain.sequence = operation.sequence;
      chain.predecessorId = operation.operationId;
      chain.position = operation.position;
      return { queued: true, operationId: operation.operationId };
    });
    this.tail = run;
    return run.then(async (result) => { await this.replay(identity); return result; });
  }
  private records(owner: string) {
    const storage = this.deps.storage();
    const keys: string[] = [];
    for (let i = 0; i < storage.length; i++) { const key = storage.key(i); if (key?.startsWith(`${prefix}op:${owner}:`)) keys.push(key); }
    const records: { key: string; record: Stored }[] = [];
    for (const key of keys) {
      try {
        const record = JSON.parse(storage.getItem(key) ?? "null") as Stored;
        const o = record?.operation;
        if (!o || o.version !== 1 || o.ownerId !== owner || key !== `${prefix}op:${owner}:${o.operationId}` ||
          !validNumber(record.createdAt) || !Number.isSafeInteger(o.sequence) || o.sequence < 1 || !o.generation ||
          !validNumber(o.position) || !validNumber(o.fileOffset) || !validNumber(o.duration) ||
          !Number.isSafeInteger(o.baseRevision) || o.baseRevision < 0 || !Number.isSafeInteger(o.resetGeneration) || o.resetGeneration < 0) throw new Error();
        records.push({ key, record });
      } catch {
        storage.setItem(`${prefix}quarantine:${key}`, "Unreadable or unsupported progress record");
        this.deps.warn("Saved progress needs review. An unreadable record was retained on this device.");
      }
    }
    return records.sort((a, b) => a.record.createdAt - b.record.createdAt || (a.record.operation.sessionId === b.record.operation.sessionId ? a.record.operation.sequence - b.record.operation.sequence : a.key.localeCompare(b.key)));
  }
  private quarantine(key: string, message: string) {
    this.deps.storage().setItem(`${prefix}quarantine:${key}`, message);
    this.deps.warn(message);
  }
  private scheduleRetry(identity: Identity) {
    clearTimeout(this.retry);
    this.retry = setTimeout(() => { this.retry = undefined; void this.replay(identity); }, this.retryDelay);
    this.retryDelay = Math.min(30000, this.retryDelay * 2);
  }
  async replay(identity = this.deps.identity()): Promise<void> {
    if (!identity.ownerId || !this.same(identity)) return;
    try {
      for (const [key, pending] of this.volatile) {
        if (pending.identity.ownerId !== identity.ownerId || pending.identity.fence !== identity.fence || !this.same(pending.identity)) continue;
        this.deps.storage().setItem(key, JSON.stringify(pending.stored));
        this.volatile.delete(key);
      }
      await this.deps.lock(`libteca-media:${identity.ownerId}`, async (owns = () => true) => {
        for (const { key, record } of this.records(identity.ownerId!)) {
          if (!this.same(identity) || !owns()) return;
          const storage = this.deps.storage();
          if (storage.getItem(`${prefix}quarantine:${key}`)) continue;
          const o = record.operation;
          if (o.predecessorId && storage.getItem(`${prefix}quarantine:${prefix}op:${o.ownerId}:${o.predecessorId}`)) {
            this.quarantine(key, "Progress depends on a conflicting save. Pending progress was retained for review."); continue;
          }
          let receipt: Receipt | undefined;
          try { receipt = await this.request(identity, `/media-operations/${o.operationId}`, {}, owns); }
          catch (error) { if ((error as { status?: number }).status !== 404) throw error; }
          if (!receipt) {
            try { receipt = await this.request(identity, "/media-operations", { method: "POST", keepalive: true, body: JSON.stringify(o) }, owns); }
            catch (error) {
              const status = (error as { status?: number }).status;
              if (status && status >= 400 && status < 500 && status !== 408 && status !== 429) {
                this.quarantine(key, status === 409 ? "Progress changed on another session, was reset, or its files changed. Pending progress was retained. Resume the current saved position or make a new deliberate seek." : "This saved progress was rejected. It was retained for review.");
                continue;
              }
              throw error;
            }
          }
          if (!receipt || receipt.operationId !== o.operationId || !Number.isSafeInteger(receipt.revision) || receipt.revision < 1) throw new Error("Invalid media receipt");
          if (!this.same(identity) || !owns()) return;
          for (const [key, base] of this.bases) {
            if (base.ownerId === o.ownerId && base.kind === o.kind && base.targetId === o.targetId) {
              const updated = { ...base, revision: receipt.revision, resetGeneration: receipt.resetGeneration, position: o.position, finished: o.finished, deleted: o.intent === "reset" };
              this.bases.set(key, updated);
              try { storage.setItem(`${prefix}base:${o.ownerId}:${o.kind}:${o.targetId}`, JSON.stringify(updated)); } catch {}
            }
          }
          storage.removeItem(key);
        }
      });
      clearTimeout(this.retry);
      this.retry = undefined;
      this.retryDelay = 1000;
    } catch {
      if (!this.same(identity)) return;
      this.scheduleRetry(identity);
      this.deps.warn("Progress is pending on this device and will retry when connected.");
    }
  }

}

function showProblem(message: string) {
  if (typeof document === "undefined") return;
  let panel = document.getElementById("libteca-media-progress-status");
  if (!panel) {
    panel = document.createElement("div"); panel.id = "libteca-media-progress-status"; panel.setAttribute("role", "alert");
    Object.assign(panel.style, { position: "fixed", bottom: "8rem", right: "1rem", maxWidth: "26rem", padding: "1rem", background: "#18191c", color: "#f5f5f7", border: "1px solid #444", borderRadius: "12px", zIndex: "100" });
    document.body.append(panel);
  }
  panel.textContent = message;
  const retry = document.createElement("button"); retry.textContent = "Retry saved progress"; retry.onclick = () => { void mediaProgress.replay(); }; panel.append(retry);
  const close = document.createElement("button"); close.textContent = "Dismiss"; close.onclick = () => panel?.remove(); panel.append(close);
}
async function transport(path: string, opts: RequestInit = {}) {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      apiTransport(path, { ...opts, signal: controller.signal }),
      new Promise<never>((_, reject) => { timer = setTimeout(() => { controller.abort(); reject(new Error("Progress request timed out")); }, 15000); }),
    ]);
  } finally { clearTimeout(timer); }
}
export const mediaProgress = new DurableMediaQueue({
  storage: () => localStorage, identity: getMediaIdentity, request: transport,
  lock: withMediaReplayLock, warn: showProblem,
});
if (typeof addEventListener !== "undefined") {
  addEventListener("online", () => { void mediaProgress.replay(); });
  addEventListener("storage", (event) => { if ((event as StorageEvent).key?.startsWith(prefix)) void mediaProgress.replay(); });
}
