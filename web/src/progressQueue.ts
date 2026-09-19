export type ProgressPatch = {
  page?: number; percent?: number; locator?: string; finished?: boolean;
};

export type ServerProgress = {
  revision?: number; page?: number; percent?: number;
  locator?: string; isFinished?: boolean;
};

type QueueStorage = {
  load: () => ProgressPatch;
  save: (patch: ProgressPatch) => void;
};

type QueueOptions = {
  send: (patch: ProgressPatch) => Promise<void>;
  onError?: (error: unknown) => void;
  retryBaseMs?: number;
  maxRetryMs?: number;
  storage?: QueueStorage;
};

// Serial, merge-preserving delivery queue for reader progress patches.
// Enqueued patches merge field-by-field (a completion update never wipes the
// page state queued before it), exactly one delivery runs at a time (an
// older request can never overtake a newer one), and a failed delivery is
// reinserted UNDER newer patches and retried with backoff instead of being
// dropped. When a storage adapter is supplied the union of the in-flight
// batch and the pending patch survives reloads: the entry is written on
// every state change and only cleared after a delivery succeeds, so a
// crash between take and ack replays the batch on the next open (replay is
// safe under the server's revision protocol).
export class ProgressQueue {
  private pending: ProgressPatch = {};
  private inFlight: ProgressPatch | undefined;
  private sending = false;
  private stopped = false;
  private failures = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly options: QueueOptions) {
    if (options.storage) {
      try {
        this.pending = this.clean(options.storage.load());
      } catch {
        this.pending = {};
      }
    }
  }

  snapshot(): ProgressPatch {
    return { ...this.pending };
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
      this.options.storage?.save({ ...this.inFlight, ...this.pending });
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
          await this.options.send(batch);
          this.failures = 0;
          this.inFlight = undefined;
          this.persist();
        } catch (error) {
          this.inFlight = undefined;
          this.pending = { ...batch, ...this.pending };
          this.persist();
          this.options.onError?.(error);
          this.failures += 1;
          if (!this.stopped) this.scheduleRetry();
          return;
        }
      }
    } finally {
      this.sending = false;
    }
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
// isFinished=true.
export function mergeServerProgress(server: ServerProgress, patch: ProgressPatch): ProgressPatch {
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
