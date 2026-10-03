export type ZipEntryStream = {
  on(event: "data", callback: (chunk: Uint8Array) => void): ZipEntryStream;
  on(event: "end", callback: () => void): ZipEntryStream;
  on(event: "error", callback: (error: Error) => void): ZipEntryStream;
  pause(): ZipEntryStream;
  resume(): ZipEntryStream;
};

export type ZipEntryLike = { name: string; internalStream(type: "uint8array"): ZipEntryStream };

export function extractCapped(entry: ZipEntryLike, limit: number, signal?: AbortSignal): Promise<Blob> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException("Reader closed", "AbortError")); return; }
    const stream = entry.internalStream("uint8array");
    const parts: Uint8Array<ArrayBuffer>[] = [];
    let size = 0;
    let settled = false;
    const fail = (error: Error) => {
      if (settled) return;
      settled = true;
      stream.pause();
      parts.length = 0;
      signal?.removeEventListener("abort", abort);
      reject(error);
    };
    const abort = () => fail(new DOMException("Reader closed", "AbortError"));
    signal?.addEventListener("abort", abort, { once: true });
    stream.on("data", (chunk) => {
      if (settled) return;
      if (chunk.byteLength > limit - size) { fail(new Error("Page too large")); return; }
      size += chunk.byteLength;
      parts.push(new Uint8Array(chunk));
    }).on("error", fail).on("end", () => {
      if (settled) return;
      settled = true;
      signal?.removeEventListener("abort", abort);
      try { resolve(new Blob(parts)); } catch (error) { reject(error); }
      parts.length = 0;
    }).resume();
  });
}

export async function readBoundedBody(response: Response, limit: number, signal?: AbortSignal): Promise<ArrayBuffer> {
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
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (signal?.aborted) throw new DOMException("Reader closed", "AbortError");
      if (done) break;
      if (value.byteLength > limit - size) throw new Error("Book too large");
      size += value.byteLength;
      parts.push(value);
    }
  } catch (error) {
    void reader.cancel().catch(() => {});
    throw error;
  } finally {
    signal?.removeEventListener("abort", abort);
    reader.releaseLock();
  }
  const out = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.byteLength;
  }
  return out.buffer;
}
