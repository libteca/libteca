import JSZip from "jszip";
import { afterEach, describe, expect, it, vi } from "vitest";
import { extractCapped, readBoundedBody, type ZipEntryLike, type ZipEntryStream } from "../src/reader/resources";
import { PageStore } from "../src/reader/cbz";

function entry(name = "page.jpg") {
  const listeners = new Map<string, Function>();
  const stream: ZipEntryStream = {
    on: vi.fn((event: string, callback: Function) => { listeners.set(event, callback); return stream; }),
    pause: vi.fn(() => stream),
    resume: vi.fn(() => stream),
  };
  return { name, internalStream: vi.fn(() => stream), stream, emit: (event: string, value?: unknown) => listeners.get(event)?.(value) };
}

async function settle() { for (let i = 0; i < 12; i++) await Promise.resolve(); }

afterEach(() => vi.unstubAllGlobals());

describe("bounded reader downloads", () => {
  it("accepts a body exactly at the cap", async () => {
    const response = new Response(new Uint8Array([1, 2, 3]));
    expect(new Uint8Array(await readBoundedBody(response, 3))).toEqual(new Uint8Array([1, 2, 3]));
  });

  it.each([undefined, "1"])("rejects delivered excess with Content-Length %s before reading the whole body", async (length) => {
    let chunks = 0;
    const cancel = vi.fn();
    const response = new Response(new ReadableStream({
      pull(controller) { chunks++; controller.enqueue(new Uint8Array(4)); }, cancel,
    }), { headers: length ? { "Content-Length": length } : undefined });
    const buffer = vi.spyOn(response, "arrayBuffer");
    await expect(readBoundedBody(response, 5)).rejects.toThrow("Book too large");
    expect(chunks).toBeLessThanOrEqual(3);
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(buffer).not.toHaveBeenCalled();
  });

  it("cancels a declared oversized download without reading it", async () => {
    const cancel = vi.fn();
    const response = new Response(new ReadableStream({ cancel }), { headers: { "Content-Length": "1000" } });
    await expect(readBoundedBody(response, 10)).rejects.toThrow("Book too large");
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("cancels a stalled read when the reader closes", async () => {
    const cancel = vi.fn();
    const signal = new AbortController();
    const response = new Response(new ReadableStream({ cancel }));
    const promise = readBoundedBody(response, 10, signal.signal);
    signal.abort();
    await expect(promise).rejects.toMatchObject({ name: "AbortError" });
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("does not fall back to an unbounded arrayBuffer when streaming is unavailable", async () => {
    const response = new Response(null);
    const buffer = vi.spyOn(response, "arrayBuffer");
    await expect(readBoundedBody(response, 10)).rejects.toThrow("no readable body");
    expect(buffer).not.toHaveBeenCalled();
  });
});

describe("bounded comic extraction", () => {
  it("rejects and pauses on the first overflowing output chunk", async () => {
    const source = entry();
    const promise = extractCapped(source, 5);
    source.emit("data", new Uint8Array(4));
    source.emit("data", new Uint8Array(2));
    source.emit("data", new Uint8Array(100));
    source.emit("end");
    await expect(promise).rejects.toThrow("Page too large");
    expect(source.stream.pause).toHaveBeenCalledTimes(1);
  });

  it("keeps exact-limit pages and propagates decompression failures", async () => {
    const source = entry();
    const promise = extractCapped(source, 5);
    source.emit("data", new Uint8Array(2));
    source.emit("data", new Uint8Array(3));
    source.emit("end");
    expect((await promise).size).toBe(5);
    const broken = entry();
    const failed = extractCapped(broken, 5);
    broken.emit("error", new Error("invalid zip"));
    await expect(failed).rejects.toThrow("invalid zip");
  });

  it("enforces the limit during actual JSZip DEFLATE streaming", async () => {
    const zip = new JSZip();
    zip.file("large.jpg", new Uint8Array(2 * 1024 * 1024));
    const bytes = await zip.generateAsync({ type: "uint8array", compression: "DEFLATE" });
    const loaded = await JSZip.loadAsync(bytes);
    const file = loaded.file("large.jpg")!;
    const accumulate = vi.spyOn(file, "async");
    await expect(extractCapped(file as unknown as ZipEntryLike, 64 * 1024)).rejects.toThrow("Page too large");
    expect(accumulate).not.toHaveBeenCalled();
  });

  it("aborts extraction without publishing or notifying after close", async () => {
    const source = entry();
    const notify = vi.fn();
    const create = vi.fn(() => "blob:page");
    vi.stubGlobal("URL", { createObjectURL: create, revokeObjectURL: vi.fn() });
    const store = new PageStore([source], notify);
    store.ensureAround(0, 0);
    store.revoke();
    source.emit("data", new Uint8Array(1));
    source.emit("end");
    await settle();
    expect(source.stream.pause).toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();
  });

  it("drops queued and active pages outside the new neighborhood after a jump", async () => {
    const pages = Array.from({ length: 40 }, (_, i) => entry(`${i}.jpg`));
    const store = new PageStore(pages, vi.fn());
    store.ensureAround(30, 3);
    store.ensureAround(0, 0);
    await settle();
    expect(pages[30].stream.pause).toHaveBeenCalled();
    expect(pages[0].internalStream).toHaveBeenCalledTimes(1);
    for (const i of [27, 28, 29, 31, 32, 33]) expect(pages[i].internalStream).not.toHaveBeenCalled();
    store.revoke();
  });

  it("can jump back to a page whose previous extraction was just cancelled", async () => {
    const pages = Array.from({ length: 40 }, (_, i) => entry(`${i}.jpg`));
    const store = new PageStore(pages, vi.fn());
    store.ensureAround(0, 0);
    store.ensureAround(30, 0);
    store.ensureAround(0, 0);
    await settle();
    expect(pages[0].internalStream).toHaveBeenCalledTimes(2);
    expect(pages[30].internalStream).not.toHaveBeenCalled();
    store.revoke();
  });
});
