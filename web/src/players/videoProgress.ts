export type VideoProgressPatch = { position: number; duration: number; finished: boolean };

export class VideoProgressSaver {
  private position = 0;
  private finished = false;
  private completion: VideoProgressPatch | null = null;
  private tail = Promise.resolve();

  constructor(
    private readonly duration: number,
    private readonly send: (patch: VideoProgressPatch) => Promise<unknown>,
  ) {}

  save(position: number, finished = false): Promise<void> {
    if (!Number.isFinite(position) || (position <= 0 && !finished)) return Promise.resolve();
    const next = this.tail.then(async () => {
      const patch = this.completion ?? { position, duration: this.duration, finished };
      if (this.finished && !patch.finished) return;
      if (this.finished === patch.finished && Math.abs(this.position - patch.position) < 0.5) return;
      if (patch.finished) this.completion = patch;
      try {
        await this.send(patch);
        this.position = patch.position;
        this.finished = patch.finished;
        if (this.completion === patch) this.completion = null;
      } catch {}
    });
    this.tail = next;
    return next;
  }
}
