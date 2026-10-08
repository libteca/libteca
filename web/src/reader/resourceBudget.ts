export type ResourceLimits = Partial<Record<"retainedBytes" | "decodedPixels" | "activeDecodes" | "documentNodes", number>>;
export class ResourceLimitError extends Error {
  readonly retryable = true;
  constructor(readonly resource: keyof ResourceLimits, readonly limit: number, readonly requested: number) {
    super(`${resource} limit ${limit} exceeded by reservation ${requested}`);
    this.name = "ResourceLimitError";
  }
}
export class ReaderResourceBudget {
  private used = { retainedBytes: 0, decodedPixels: 0, activeDecodes: 0, documentNodes: 0 };
  constructor(private readonly limits: ResourceLimits = {}) {
    for (const value of Object.values(limits)) {
      if (!Number.isSafeInteger(value) || value! < 0) throw new RangeError("Resource limits must be nonnegative safe integers");
    }
  }
  reserve(amounts: ResourceLimits): () => void {
    for (const [name, amount] of Object.entries(amounts)) {
      const resource = name as keyof ResourceLimits;
      const requested = amount!;
      if (!Number.isSafeInteger(requested) || requested < 0 || !Number.isSafeInteger(this.used[resource] + requested)) throw new RangeError("Invalid resource reservation");
      const limit = this.limits[resource] ?? 0;
      if (limit > 0 && requested > limit - this.used[resource]) throw new ResourceLimitError(resource, limit, requested);
    }
    for (const [name, amount] of Object.entries(amounts)) this.used[name as keyof ResourceLimits] += amount!;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      for (const [name, amount] of Object.entries(amounts)) this.used[name as keyof ResourceLimits] -= amount!;
    };
  }
  enabled(resource: keyof ResourceLimits): boolean { return (this.limits[resource] ?? 0) > 0; }
  snapshot() { const { documentNodes, ...used } = this.used; return documentNodes || this.enabled("documentNodes") ? { ...used, documentNodes } : used; }
}
export function decodedPixels(width: number, height: number): number {
  if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width <= 0 || height <= 0 || !Number.isSafeInteger(width * height)) throw new RangeError("Invalid image dimensions");
  return width * height;
}
export async function withDecode<T>(budget: ReaderResourceBudget, width: number, height: number, decode: () => Promise<T>, signal?: AbortSignal): Promise<{ value: T; release: () => void }> {
  if (signal?.aborted) throw new DOMException("Reader closed", "AbortError");
  const releasePixels = budget.reserve({ decodedPixels: decodedPixels(width, height) });
  let releaseJob: (() => void) | undefined;
  try {
    releaseJob = budget.reserve({ activeDecodes: 1 });
    const value = await decode();
    if (signal?.aborted) throw new DOMException("Reader closed", "AbortError");
    return { value, release: releasePixels };
  } catch (error) { releasePixels(); throw error; }
  finally { releaseJob?.(); }
}

export function imageDimensions(header: Uint8Array): { width: number; height: number } {
  const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
  let width = 0, height = 0;
  if (header.length >= 24 && view.getUint32(0) === 0x89504e47 && view.getUint32(4) === 0x0d0a1a0a && view.getUint32(12) === 0x49484452) {
    width = view.getUint32(16); height = view.getUint32(20);
  } else if (header.length >= 10 && /^GIF8[79]a$/.test(String.fromCharCode(...header.slice(0, 6)))) {
    width = view.getUint16(6, true); height = view.getUint16(8, true);
  } else if (header.length >= 26 && header[0] === 0x42 && header[1] === 0x4d && view.getUint32(14, true) >= 40) {
    width = view.getInt32(18, true); height = Math.abs(view.getInt32(22, true));
} else if (header.length >= 30 && view.getUint32(0) === 0x52494646 && view.getUint32(8) === 0x57454250) {
    const kind = view.getUint32(12);
    if (kind === 0x56503858) {
      width = 1 + header[24] + (header[25] << 8) + (header[26] << 16);
      height = 1 + header[27] + (header[28] << 8) + (header[29] << 16);
    } else if (kind === 0x5650384c && header[20] === 0x2f) {
      const bits = view.getUint32(21, true);
      width = 1 + (bits & 0x3fff); height = 1 + ((bits >>> 14) & 0x3fff);
    } else if (kind === 0x56503820 && header[23] === 0x9d && header[24] === 1 && header[25] === 0x2a) {
      width = view.getUint16(26, true) & 0x3fff; height = view.getUint16(28, true) & 0x3fff;
    }
  } else if (header.length >= 4 && header[0] === 0xff && header[1] === 0xd8) {
    let offset = 2;
    while (offset + 4 <= header.length) {
      if (header[offset] !== 0xff) throw new Error("Invalid JPEG dimensions");
      while (header[offset] === 0xff) offset++;
      const marker = header[offset++];
      if (marker === 0xd9 || marker === 0xda) break;
      if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7)) continue;
      if (offset + 2 > header.length) break;
      const length = view.getUint16(offset);
      if (length < 2) throw new Error("Invalid JPEG dimensions");
      if (marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker) && length >= 7 && offset + 7 <= header.length) {
        height = view.getUint16(offset + 3); width = view.getUint16(offset + 5); break;
      }
      offset += length;
    }
  }
  if (width === 0 || height === 0) throw new Error("Image dimensions unavailable before decode");
  decodedPixels(width, height);
  return { width, height };
}

export async function blobDimensions(blob: Blob, budget: ReaderResourceBudget): Promise<{ width: number; height: number }> {
  const length = Math.min(blob.size, 65536);
  const release = budget.reserve({ retainedBytes: length });
  try { return imageDimensions(new Uint8Array(await blob.slice(0, length).arrayBuffer())); }
  finally { release(); }
}

const sharedBudgets = new Map<string, ReaderResourceBudget>();
export function sharedReaderBudget(): ReaderResourceBudget {
  let configured = "";
  try { configured = localStorage.getItem("libteca-reader-resource-limits") ?? ""; } catch {}
  const existing = sharedBudgets.get(configured);
  if (existing) return existing;
  const parsed: unknown = configured ? JSON.parse(configured) : {};
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new RangeError("Invalid reader resource configuration");
  for (const key of Object.keys(parsed)) if (!["retainedBytes", "decodedPixels", "activeDecodes", "documentNodes"].includes(key)) throw new RangeError("Unknown reader resource limit");
  const budget = new ReaderResourceBudget(parsed as ResourceLimits);
  sharedBudgets.set(configured, budget);
  return budget;
}
