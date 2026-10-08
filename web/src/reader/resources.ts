import { ReaderResourceBudget } from "./resourceBudget";

export type ZipEntryStream = {
  on(event: "data", callback: (chunk: Uint8Array) => void): ZipEntryStream;
  on(event: "end", callback: () => void): ZipEntryStream;
  on(event: "error", callback: (error: Error) => void): ZipEntryStream;
  pause(): ZipEntryStream;
  resume(): ZipEntryStream;
};

export type ZipEntryLike = { name: string; internalStream(type: "uint8array"): ZipEntryStream };

export async function extractCapped(entry: ZipEntryLike, limit: number, signal?: AbortSignal): Promise<Blob> {
  const result = await extractReserved(entry, limit, new ReaderResourceBudget(), signal);
  result.release();
  return result.blob;
}

export function extractReserved(entry: ZipEntryLike, limit: number, budget: ReaderResourceBudget, signal?: AbortSignal): Promise<{ blob: Blob; release: () => void }> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException("Reader closed", "AbortError")); return; }
    const stream = entry.internalStream("uint8array");
    const parts: Uint8Array<ArrayBuffer>[] = [];
    const retained: (() => void)[] = [];
    const copies: (() => void)[] = [];
    const release = () => { for (const done of retained.splice(0)) done(); for (const done of copies.splice(0)) done(); };
    let size = 0;
    let settled = false;
    const fail = (error: Error) => {
      if (settled) return;
      settled = true;
      stream.pause();
      parts.length = 0;
      release();
      signal?.removeEventListener("abort", abort);
      reject(error);
    };
    const abort = () => fail(new DOMException("Reader closed", "AbortError"));
    signal?.addEventListener("abort", abort, { once: true });
    try { stream.on("data", (chunk) => {
      if (settled) return;
      if (chunk.byteLength > limit - size) { fail(new Error("Page too large")); return; }
      try {
        retained.push(budget.reserve({ retainedBytes: chunk.byteLength }));
        copies.push(budget.reserve({ retainedBytes: chunk.byteLength }));
        parts.push(new Uint8Array(chunk));
        size += chunk.byteLength;
      } catch (error) { fail(error as Error); }
    }).on("error", fail).on("end", () => {
      if (settled) return;
      settled = true;
      signal?.removeEventListener("abort", abort);
      try {
        const blob = new Blob(parts);
        for (const done of copies.splice(0)) done();
        resolve({ blob, release });
      } catch (error) { release(); reject(error); }
      parts.length = 0;
    }).resume(); } catch (error) { fail(error as Error); }
  });
}

export async function readBoundedBody(response: Response, limit: number, signal?: AbortSignal): Promise<ArrayBuffer> {
  const result = await readBoundedBodyReserved(response, limit, new ReaderResourceBudget(), signal);
  result.release();
  return result.buffer;
}

export async function readBoundedBodyReserved(response: Response, limit: number, budget: ReaderResourceBudget, signal?: AbortSignal): Promise<{ buffer: ArrayBuffer; release: () => void }> {
  const cancel = () => { void response.body?.cancel().catch(() => {}); };
  if (!response.ok) { cancel(); throw new Error(`Download failed (${response.status})`); }
  const length = Number(response.headers.get("Content-Length"));
  if (Number.isFinite(length) && length > limit) { cancel(); throw new Error("Book too large"); }
  if (signal?.aborted) { cancel(); throw new DOMException("Reader closed", "AbortError"); }
  if (!response.body) throw new Error("Book response has no readable body");
  const reader = response.body.getReader();
  const abort = () => { void reader.cancel().catch(() => {}); };
  signal?.addEventListener("abort", abort, { once: true });
  const parts: Uint8Array[] = [];
  const retained: (() => void)[] = [];
  const copies: (() => void)[] = [];
  const release = () => { for (const done of retained.splice(0)) done(); for (const done of copies.splice(0)) done(); };
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (signal?.aborted) throw new DOMException("Reader closed", "AbortError");
      if (done) break;
      if (value.byteLength > limit - size) throw new Error("Book too large");
      retained.push(budget.reserve({ retainedBytes: value.byteLength }));
      copies.push(budget.reserve({ retainedBytes: value.byteLength }));
      size += value.byteLength;
      parts.push(value);
    }
  } catch (error) {
    release();
    void reader.cancel().catch(() => {});
    throw error;
  } finally {
    signal?.removeEventListener("abort", abort);
    reader.releaseLock();
  }
  try {
    const out = new Uint8Array(size);
    let offset = 0;
    for (const part of parts) {
      out.set(part, offset);
      offset += part.byteLength;
    }
    parts.length = 0;
    for (const done of copies.splice(0)) done();
    return { buffer: out.buffer, release };
  } catch (error) { release(); throw error; }
}
