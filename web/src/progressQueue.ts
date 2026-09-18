export type ProgressPatch = {
  page?: number; percent?: number; locator?: string; finished?: boolean;
};

type QueueOptions = {
  send: (patch: ProgressPatch) => Promise<void>;
  onError?: (error: unknown) => void;
  retryBaseMs?: number;
  maxRetryMs?: number;
};

// Serial, merge-preserving delivery queue for reader progress patches.
// Enqueued patches merge field-by-field (a completion update never wipes the
// page state queued before it), exactly one delivery runs at a time (an
// older request can never overtake a newer one), and a failed delivery is
// reinserted UNDER newer patches and retried with backoff instead of being
// dropped.
export class ProgressQueue {
  private pending: ProgressPatch = {};
  private sending = false;
  private stopped = false;
  private failures = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly options: QueueOptions) {}

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

  enqueue(patch: ProgressPatch): void {
    if (this.stopped) return;
    this.pending = { ...this.pending, ...this.clean(patch) };
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
        try {
          await this.options.send(batch);
          this.failures = 0;
        } catch (error) {
          this.pending = { ...batch, ...this.pending };
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
