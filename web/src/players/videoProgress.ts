import { getToken, APIError } from "../api";

export type VideoProgressPatch = { position: number; duration: number; finished: boolean };

type WriteScope = { tail: Promise<void>; generation: number };
const scopes = new Map<string, WriteScope>();

const terminalStatus = (status: number) =>
  status >= 400 && status < 500 && status !== 408 && status !== 429;

export class VideoProgressSaver {
  private acknowledged: VideoProgressPatch | null = null;
  private pending: VideoProgressPatch | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private retryDelayMs = 1000;
  private readonly scope: WriteScope;
  private readonly token: string;
  private readonly generation: number;

  constructor(
    private readonly endpoint: string,
    private readonly duration: number,
    private readonly send: (patch: VideoProgressPatch) => Promise<unknown>,
    token = getToken(),
  ) {
    this.token = token;
    const key = `${token}\n${endpoint}`;
    this.scope = scopes.get(key) ?? { tail: Promise.resolve(), generation: 0 };
    scopes.set(key, this.scope);
    this.generation = ++this.scope.generation;
  }

  private stale(): boolean {
    return getToken() !== this.token || this.generation !== this.scope.generation;
  }

  private clearRetry(): void {
    if (this.retryTimer !== undefined) {
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
    }
  }

  save(position: number, finished = false, explicit = false): Promise<void> {
    if (!Number.isFinite(position) || position < 0) return Promise.resolve();
    const next = this.scope.tail.then(async () => {
      if (this.stale()) {
        this.pending = null;
        this.clearRetry();
        return;
      }
      const patch = this.pending?.finished ? this.pending : { position, duration: this.duration, finished };
      if (this.acknowledged?.finished && !patch.finished) return;
      if (!explicit && this.acknowledged !== null &&
        this.acknowledged.finished === patch.finished &&
        Math.abs(this.acknowledged.position - patch.position) < 0.5) return;
      this.clearRetry();
      this.pending = patch;
      try {
        await this.send(patch);
        if (this.stale()) return;
        this.acknowledged = patch;
        if (this.pending === patch) this.pending = null;
        this.retryDelayMs = 1000;
      } catch (error) {
        if (this.stale()) return;
        if (error instanceof APIError && terminalStatus(error.status)) {
          this.pending = null;
          return;
        }
        this.retryTimer = setTimeout(() => {
          this.retryTimer = undefined;
          if (this.pending) void this.save(this.pending.position, this.pending.finished, true);
        }, this.retryDelayMs);
        this.retryDelayMs = Math.min(this.retryDelayMs * 2, 30000);
      }
    });
    this.scope.tail = next;
    return next;
  }
}
