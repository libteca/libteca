import { getMediaIdentity } from "../mediaProgressIdentity";
import { mediaProgress } from "../mediaProgress";
import { apiWithDeadline, getToken } from "../api";

export type AudioProgressPatch = { position: number; duration: number; finished: boolean };
type WriteScope = { tail: Promise<void>; generation: number };
const scopes = new Map<string, WriteScope>();

export class AudioProgressSaver {
  private readonly scope: WriteScope;
  private generation: number;
  private acknowledged: AudioProgressPatch | null = null;
  private pending: AudioProgressPatch | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private retryDelay = 1000;
  private explicit = false;
  private lastPosition = 0;
  private readonly timelineGeneration: string | undefined;

  constructor(private readonly path: string, private readonly duration: number, private readonly token = getToken()) {
    const key = `${this.token}\n${path}`;
    this.scope = scopes.get(key) ?? { tail: Promise.resolve(), generation: 0 };
    scopes.set(key, this.scope);
    this.generation = this.scope.generation;
    this.timelineGeneration = mediaProgress.generation(path);
    if (getMediaIdentity().ownerId) void mediaProgress.prepare(path);
  }

  begin(): void {
    this.generation = ++this.scope.generation;
    this.acknowledged = null;
    this.pending = null;
    this.retryDelay = 1000;
    clearTimeout(this.retryTimer);
    this.retryTimer = undefined;
  }

  start(position: number): Promise<void> {
    this.begin();
    this.explicit = true;
    return this.save(position);
  }

  save(position: number, finished = false, explicitSeek = false): Promise<void> {
    if (!Number.isFinite(position) || position < 0) return Promise.resolve();
    if (finished && this.duration <= 0 && position === 0) position = this.lastPosition;
    this.lastPosition = position;
    const generation = this.generation;
    const explicit = this.explicit || explicitSeek;
    this.explicit = false;
    if (getMediaIdentity().ownerId) {
      if (getToken() !== this.token || generation !== this.scope.generation) return Promise.resolve();
      if (this.acknowledged?.finished && !finished && !explicit) return Promise.resolve();
      if (explicit) this.acknowledged = null;
      const patch = { position, duration: this.duration, finished };
      return mediaProgress.enqueue(this.path, { ...patch, ...(this.timelineGeneration ? { expectedGeneration: this.timelineGeneration } : {}), ...(explicit ? { mediaIntent: position === 0 ? "restart" as const : "seek" as const } : {}) }).then((result) => { if ((result as { queued?: boolean })?.queued) this.acknowledged = patch; }).catch(() => {});
    }
    const next = this.scope.tail.then(async () => {
      if (getToken() !== this.token || generation !== this.scope.generation) {
        this.pending = null;
        clearTimeout(this.retryTimer);
        this.retryTimer = undefined;
        return;
      }
      const patch = this.pending?.finished ? this.pending : { position, duration: this.duration, finished };
      if (this.acknowledged?.finished && !patch.finished) return;
      if (!explicit && this.acknowledged?.finished === patch.finished && Math.abs(this.acknowledged.position - patch.position) < 0.5) return;
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
      this.pending = patch;
      try {
        const result = await apiWithDeadline(this.path, { method: "POST", keepalive: true, body: JSON.stringify({ ...patch, ...(getMediaIdentity().ownerId && explicit ? { mediaIntent: position === 0 ? "restart" : "seek" } : {}) }) });
        if (result?.error) throw new Error(result.error);
        if (generation !== this.scope.generation) return;
        this.acknowledged = patch;
        if (this.pending === patch) this.pending = null;
        this.retryDelay = 1000;
      } catch {
        if (getToken() !== this.token || generation !== this.scope.generation) return;
        this.retryTimer = setTimeout(() => {
          this.retryTimer = undefined;
          if (this.pending) void this.save(this.pending.position, this.pending.finished);
        }, this.retryDelay);
        this.retryDelay = Math.min(this.retryDelay * 2, 30000);
      }
    });
    this.scope.tail = next;
    return next;
  }
}
