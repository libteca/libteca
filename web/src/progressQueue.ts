export type ProgressPatch = {
  page?: number; percent?: number; locator?: string; finished?: boolean;
};

export type ServerProgress = {
  revision?: number; page?: number; percent?: number;
  locator?: string; isFinished?: boolean;
};

export type StoredProgress = { baseRevision: number; patch: ProgressPatch };

export type QueueStorage = {
  load: () => StoredProgress | null;
  save: (patch: ProgressPatch, baseRevision: number) => void;
};

type QueueOptions = {
  send: (patch: ProgressPatch, baseRevision: number) => Promise<number | void>;
  prepare?: (patch: ProgressPatch) => ProgressPatch;
  onError?: (error: unknown, terminal: boolean) => void;
  shouldRetry?: (error: unknown) => boolean;
  retryBaseMs?: number;
  maxRetryMs?: number;
  storage?: QueueStorage;
  initialBaseRevision?: number;
};

// Strict validation for patches restored from storage: property names AND
// types/ranges are checked so a malformed record is quarantined instead of
// replayed forever as a doomed request.
export function parseProgressPatch(value: unknown): ProgressPatch {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("invalid saved progress");
  }
  const v = value as Record<string, unknown>;
  const out: ProgressPatch = {};
  if (v.page !== undefined) {
    if (!Number.isSafeInteger(v.page) || (v.page as number) < 0) throw new Error("invalid saved page");
    out.page = v.page as number;
  }
  if (v.percent !== undefined) {
    if (typeof v.percent !== "number" || !Number.isFinite(v.percent) ||
      v.percent < 0 || v.percent > 1) throw new Error("invalid saved percent");
    out.percent = v.percent;
  }
  if (v.locator !== undefined) {
    if (typeof v.locator !== "string" || new TextEncoder().encode(v.locator).length > 8192) {
      throw new Error("invalid saved locator");
    }
    out.locator = v.locator;
  }
  if (v.finished !== undefined) {
    if (typeof v.finished !== "boolean") throw new Error("invalid saved completion");
    out.finished = v.finished;
  }
  return out;
}

export function parseStoredProgress(value: unknown): StoredProgress {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("invalid saved progress record");
  }
  const v = value as Record<string, unknown>;
  if (typeof v.baseRevision !== "number" || !Number.isSafeInteger(v.baseRevision) || v.baseRevision < 0) {
    throw new Error("invalid saved progress revision");
  }
  return { baseRevision: v.baseRevision, patch: parseProgressPatch(v.patch) };
}

// Serial, merge-preserving delivery queue for reader progress patches. The
// queue owns the base revision for its (user, edition) scope: a persisted
// record replays with the revision it was BASED on (never one minted later
// by a fresh page load - that would relabel a stale patch as current and
// the server would accept it without the conflict the merge logic depends
// on), and the base only advances when the server acknowledges a write or
// answers a conflict with its current state. Enqueued patches merge
// field-by-field (a completion update never wipes the page state queued
// before it), exactly one delivery runs at a time, and a retryable failure
// is reinserted UNDER newer patches and retried with backoff. A failure the
// shouldRetry policy declares permanent drops its patch (a malformed or
// 404-gone operation must not wedge every later save behind it) and is
// reported as terminal. When a storage adapter is supplied the union of the
// in-flight batch and the pending patch survives reloads with its base
// revision; the entry is only cleared after a delivery succeeds, so a
// crash between take and ack replays the batch on the next open against
// the SAME base - safe under the server's conditional write.
export class ProgressQueue {
  private pending: ProgressPatch = {};
  private inFlight: ProgressPatch | undefined;
  private baseRevision: number;
  private sending = false;
  private stopped = false;
  private failures = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly options: QueueOptions) {
    this.baseRevision = Math.max(0, Math.trunc(options.initialBaseRevision ?? 0));
    if (options.storage) {
      try {
        const stored = options.storage.load();
        if (stored) {
          this.pending = parseProgressPatch(stored.patch);
          this.baseRevision = stored.baseRevision;
        }
      } catch {
        this.pending = {};
      }
    }
  }

  base(): number {
    return this.baseRevision;
  }

  snapshot(): ProgressPatch {
    return { ...this.pending };
  }

  operation(): StoredProgress {
    const patch = this.clean({ ...this.inFlight, ...this.pending });
    return {
      baseRevision: this.baseRevision,
      patch: this.options.prepare ? this.options.prepare(patch) : patch,
    };
  }

  private clean(patch: ProgressPatch): ProgressPatch {
    const out: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(patch)) {
      if (value !== undefined) out[key] = value;
    }
    return out as ProgressPatch;
  }

  private persist(): void {
    try {
      this.options.storage?.save(this.clean({ ...this.inFlight, ...this.pending }), this.baseRevision);
    } catch { /* storage unavailable */ }
  }

  enqueue(patch: ProgressPatch): void {
    if (this.stopped) return;
    this.pending = { ...this.pending, ...this.clean(patch) };
    this.persist();
  }

  async flush(): Promise<void> {
    if (this.sending || this.stopped) return;
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    this.sending = true;
    try {
      while (!this.stopped && Object.keys(this.pending).length > 0) {
        const batch = this.pending;
        this.pending = {};
        this.inFlight = batch;
        this.persist();
        try {
          const prepared = this.options.prepare ? this.options.prepare({ ...batch }) : batch;
          const nextBase = await this.options.send(prepared, this.baseRevision);
          this.failures = 0;
          this.inFlight = undefined;
          if (typeof nextBase === "number" && Number.isSafeInteger(nextBase) && nextBase >= 0) {
            this.baseRevision = nextBase;
          }
          this.persist();
        } catch (error) {
          this.inFlight = undefined;
          const retryable = this.options.shouldRetry ? this.options.shouldRetry(error) : true;
          if (!retryable) {
            this.persist();
            this.notifyError(error, true);
            continue;
          }
          this.pending = { ...batch, ...this.pending };
          this.persist();
          this.notifyError(error, false);
          this.failures += 1;
          if (!this.stopped) this.scheduleRetry();
          return;
        }
      }
    } finally {
      this.sending = false;
    }
  }

  private notifyError(error: unknown, terminal: boolean): void {
    try {
      this.options.onError?.(error, terminal);
    } catch { /* observer must not break queue housekeeping */ }
  }

  private scheduleRetry(): void {
    if (this.timer !== undefined) return;
    const base = Math.max(1, this.options.retryBaseMs ?? 1000);
    const cap = Math.max(base, this.options.maxRetryMs ?? 30000);
    const delay = Math.min(cap, base * 2 ** Math.min(this.failures - 1, 8));
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.flush();
    }, delay);
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }
}

// Rebases a patch onto the server state returned by a stale-revision
// rejection. Numeric reading position is monotonic: the farther-along side
// wins and takes its percent/locator with it (the losing side's values
// describe a position the server already passed). Explicit finished intent
// survives either way so a deliberate reopen is never buried by a stale
// isFinished=true. A locator-only patch has no comparable numeric position:
// against a server state that already holds any position it never wins on
// its own (an unordered locator cannot be ranked against a page/percent,
// and letting it through produced rows reading "90%" while pointing at an
// early CFI) - only the explicit completion intent survives. The next
// coherent save (locator plus its computed percent, once the EPUB location
// index exists) carries the position forward.
export function mergeServerProgress(server: ServerProgress, patch: ProgressPatch): ProgressPatch {
  if (patch.locator !== undefined && patch.page === undefined && patch.percent === undefined) {
    const serverHasPosition = server.page !== undefined || server.percent !== undefined || server.locator !== undefined;
    if (serverHasPosition) {
      return patch.finished === undefined ? {} : { finished: patch.finished };
    }
  }
  const merged: ProgressPatch = {};
  let localWins = true;
  if (patch.page !== undefined) {
    if (server.page !== undefined && server.page >= patch.page) localWins = false;
  } else if (patch.percent !== undefined) {
    if (server.percent !== undefined && server.percent >= patch.percent) localWins = false;
  }
  if (localWins) {
    if (patch.page !== undefined) merged.page = patch.page;
    if (patch.percent !== undefined) merged.percent = patch.percent;
  }
  if (patch.locator !== undefined && (localWins || (patch.page === undefined && patch.percent === undefined))) {
    merged.locator = patch.locator;
  }
  if (patch.finished !== undefined) {
    merged.finished = patch.finished;
  }
  return merged;
}

export function mergeStoredProgress(records: readonly StoredProgress[]): StoredProgress | null {
  if (records.length === 0) return null;
  let patch: ProgressPatch = {};
  let baseRevision = Number.MAX_SAFE_INTEGER;
  for (const record of records) {
    const delta = mergeServerProgress(patch, record.patch);
    if (delta.page !== undefined || delta.percent !== undefined || delta.locator !== undefined) {
      const finished = patch.finished;
      patch = { ...delta };
      if (finished !== undefined && patch.finished === undefined) patch.finished = finished;
    } else if (delta.finished !== undefined) {
      patch.finished = delta.finished;
    }
    baseRevision = Math.min(baseRevision, record.baseRevision);
  }
  return Object.keys(patch).length > 0 ? { baseRevision, patch } : null;
}

export function createProgressStorage(storage: Storage, prefix: string, ownKey: string): QueueStorage {
  return {
    load: () => {
      const keys = Array.from({ length: storage.length }, (_, i) => storage.key(i));
      const records: StoredProgress[] = [];
      const sources: { key: string; raw: string }[] = [];
      for (const key of keys) {
        if (!key || !key.startsWith(prefix) || key === ownKey) continue;
        const raw = storage.getItem(key);
        if (raw === null) continue;
        try {
          records.push(parseStoredProgress(JSON.parse(raw)));
          sources.push({ key, raw });
        } catch {
          try { storage.removeItem(key); } catch {}
        }
      }
      const merged = mergeStoredProgress(records);
      if (!merged) return null;
      try {
        storage.setItem(ownKey, JSON.stringify(merged));
        for (const source of sources) {
          if (storage.getItem(source.key) === source.raw) storage.removeItem(source.key);
        }
      } catch {}
      return merged;
    },
    save: (patch, baseRevision) => {
      if (Object.keys(patch).length === 0) storage.removeItem(ownKey);
      else storage.setItem(ownKey, JSON.stringify({ baseRevision, patch }));
    },
  };
}
