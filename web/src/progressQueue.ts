export type ProgressPatch = {
  page?: number; percent?: number; locator?: string; finished?: boolean;
};

export type ServerProgress = {
  revision?: number; resetGeneration?: number; deleted?: boolean;
  page?: number; percent?: number;
  locator?: string; isFinished?: boolean;
};

export type StoredProgress = { baseRevision: number; resetGeneration: number; patch: ProgressPatch };

export type QueueStorage = {
  load: () => StoredProgress | null;
  save: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => void;
  clear: () => void;
};

export class ResetConflictError extends Error {
  constructor(readonly server: ServerProgress) {
    super("progress was reset");
  }
}

type SendResult = { revision?: number; resetGeneration?: number };

type QueueOptions = {
  send: (patch: ProgressPatch, baseRevision: number, resetGeneration: number) => Promise<SendResult | void>;
  prepare?: (patch: ProgressPatch) => ProgressPatch;
  onError?: (error: unknown, terminal: boolean) => void;
  shouldRetry?: (error: unknown) => boolean;
  retryBaseMs?: number;
  maxRetryMs?: number;
  storage?: QueueStorage;
  initialBaseRevision?: number;
  initialResetGeneration?: number;
};

// Strict validation for patches restored from storage: property names AND
// types/ranges are checked so a malformed record is quarantined instead of
// replayed forever as a doomed request. resetGeneration is absent on
// records written before the reset-wins protocol: the floor value 0 is the
// only lineage such a record can ever match (generations only rise), so it
// replays unchanged against never-reset servers and is rejected once any
// reset has happened - never relabeled from a fresh read.
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
  let resetGeneration = 0;
  if (v.resetGeneration !== undefined) {
    if (typeof v.resetGeneration !== "number" || !Number.isSafeInteger(v.resetGeneration) || v.resetGeneration < 0) {
      throw new Error("invalid saved progress generation");
    }
    resetGeneration = v.resetGeneration;
  }
  return { baseRevision: v.baseRevision, resetGeneration, patch: parseProgressPatch(v.patch) };
}

// Serial, merge-preserving delivery queue for reader progress patches. The
// queue owns the base revision AND reset generation for its (user, edition)
// scope: a persisted record replays with the revision and generation it was
// BASED on (never ones minted later by a fresh page load - that would
// relabel a stale patch as current and the server would accept it without
// the conflict the merge logic depends on), and the base only advances when
// the server acknowledges a write or answers a conflict with its current
// state. Enqueued patches merge field-by-field (a completion update never
// wipes the page state queued before it), exactly one delivery runs at a
// time, and a retryable failure is reinserted UNDER newer patches and
// retried with backoff. A failure the shouldRetry policy declares permanent
// drops its patch (a malformed or 404-gone operation must not wedge every
// later save behind it) and is reported as terminal. A ResetConflictError
// (the server lineage was reset after this operation was captured) drops
// the conflicted batch without retrying and without publishing a
// replacement over it: the durable record keeps the discarded operation as
// recoverable evidence until a deliberate new save supersedes it, while
// the queue rebases onto the server state the conflict carried - patches
// enqueued DURING the conflicting delivery are fresh local input and are
// delivered on the post-reset lineage next. When a storage adapter is
// supplied the union of the in-flight batch and the pending patch
// survives reloads with its base; the
// record is only acknowledged after a delivery succeeds, so a crash between
// take and ack replays the batch on the next open against the SAME base -
// safe under the server's conditional write.
export class ProgressQueue {
  private pending: ProgressPatch = {};
  private inFlight: ProgressPatch | undefined;
  private baseRevision: number;
  private resetGeneration: number;
  private sending = false;
  private stopped = false;
  private failures = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly options: QueueOptions) {
    this.baseRevision = Math.max(0, Math.trunc(options.initialBaseRevision ?? 0));
    this.resetGeneration = Math.max(0, Math.trunc(options.initialResetGeneration ?? 0));
    if (options.storage) {
      try {
        const stored = options.storage.load();
        if (stored && stored.resetGeneration >= this.resetGeneration) {
          this.pending = parseProgressPatch(stored.patch);
          this.baseRevision = stored.baseRevision;
          this.resetGeneration = stored.resetGeneration;
        }
      } catch {
        this.pending = {};
      }
    }
  }

  base(): number {
    return this.baseRevision;
  }

  generation(): number {
    return this.resetGeneration;
  }

  snapshot(): ProgressPatch {
    return { ...this.pending };
  }

  operation(): StoredProgress {
    const patch = this.clean({ ...this.inFlight, ...this.pending });
    return {
      baseRevision: this.baseRevision,
      resetGeneration: this.resetGeneration,
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
      const patch = this.clean({ ...this.inFlight, ...this.pending });
      if (Object.keys(patch).length === 0) this.options.storage?.clear();
      else this.options.storage?.save(patch, this.baseRevision, this.resetGeneration);
    } catch { /* storage unavailable */ }
  }

  enqueue(patch: ProgressPatch): void {
    if (this.stopped) return;
    this.pending = { ...this.pending, ...this.clean(patch) };
    this.persist();
  }

  private adoptBase(revision: unknown, generation: unknown): void {
    if (typeof revision === "number" && Number.isSafeInteger(revision) && revision >= 0) {
      this.baseRevision = revision;
    }
    if (typeof generation === "number" && Number.isSafeInteger(generation) && generation >= 0) {
      this.resetGeneration = generation;
    }
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
          const next = await this.options.send(prepared, this.baseRevision, this.resetGeneration);
          this.failures = 0;
          this.inFlight = undefined;
          if (next && typeof next === "object") {
            this.adoptBase(next.revision, next.resetGeneration);
          }
          this.persist();
        } catch (error) {
          this.inFlight = undefined;
          if (error instanceof ResetConflictError) {
            this.adoptBase(error.server.revision, error.server.resetGeneration);
            this.notifyError(error, true);
            continue;
          }
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

// Merges recovery records from one reset generation: numeric reading
// position is monotonic within a lineage, so the farther-along side wins
// and takes its percent/locator with it, while explicit completion intent
// survives. Records from a lower generation describe a lineage the server
// explicitly discarded and are excluded from the merge entirely - never
// max-merged into the current one; the storage layer keeps them in place
// as quarantined evidence.
export function mergeStoredProgress(records: readonly StoredProgress[]): StoredProgress | null {
  if (records.length === 0) return null;
  const newest = records.reduce((max, r) => Math.max(max, r.resetGeneration), 0);
  let patch: ProgressPatch = {};
  let baseRevision = Number.MAX_SAFE_INTEGER;
  for (const record of records) {
    if (record.resetGeneration !== newest) continue;
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
  return Object.keys(patch).length > 0 ? { baseRevision: baseRevision === Number.MAX_SAFE_INTEGER ? 0 : baseRevision, resetGeneration: newest, patch } : null;
}

// Durable queue storage as immutable, uniquely keyed records. Every publish
// writes a NEW key (writer id + monotonic sequence) and never rewrites a
// key that already exists, so a concurrent reader in another tab can only
// ever retire the exact record it captured: a newer record published by a
// live writer lands under a different key and cannot be removed by a
// compare-then-delete racing against that writer. Recovery publishes the
// merged replacement BEFORE retiring any captured source (write-before-
// retire), same-producer supersession removes the writer's own previous
// record only after the replacement is durable, and clear() acknowledges
// the exact published version rather than whatever value occupies a
// mutable key. Records from an older reset generation are left in place as
// quarantined evidence; they are never merged into the current lineage.
export function createProgressStorage(storage: Storage, prefix: string, writerId: string): QueueStorage {
  let seq = 0;
  let lastKey: string | undefined;
  const publish = (patch: ProgressPatch, baseRevision: number, resetGeneration: number): void => {
    seq += 1;
    const key = `${prefix}${writerId}-${seq}`;
    storage.setItem(key, JSON.stringify({ baseRevision, resetGeneration, patch }));
    lastKey = key;
  };
  return {
    load: () => {
      const keys = Array.from({ length: storage.length }, (_, i) => storage.key(i));
      const records: { key: string; raw: string; record: StoredProgress }[] = [];
      for (const key of keys) {
        if (!key || !key.startsWith(prefix)) continue;
        const raw = storage.getItem(key);
        if (raw === null) continue;
        try {
          records.push({ key, raw, record: parseStoredProgress(JSON.parse(raw)) });
        } catch {
          try { storage.removeItem(key); } catch {}
        }
      }
      if (records.length === 0) return null;
      const merged = mergeStoredProgress(records.map((r) => r.record));
      if (!merged) return null;
      let published = false;
      try {
        publish(merged.patch, merged.baseRevision, merged.resetGeneration);
        published = true;
      } catch {}
      if (published) {
        for (const source of records) {
          if (source.record.resetGeneration !== merged.resetGeneration) continue;
          try {
            if (storage.getItem(source.key) === source.raw) storage.removeItem(source.key);
          } catch {}
        }
      }
      return merged;
    },
    save: (patch, baseRevision, resetGeneration) => {
      const previous = lastKey;
      try {
        publish(patch, baseRevision, resetGeneration);
      } catch { return; }
      if (previous !== undefined) {
        try { storage.removeItem(previous); } catch {}
      }
    },
    clear: () => {
      const key = lastKey;
      lastKey = undefined;
      if (key !== undefined) {
        try { storage.removeItem(key); } catch {}
      }
    },
  };
}
