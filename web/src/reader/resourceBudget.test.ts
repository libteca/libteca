import { describe, expect, it } from "vitest";
import { decodedPixels, ReaderResourceBudget, ResourceLimitError, withDecode } from "./resourceBudget";

describe("reader resource reservations", () => {
  it.each([9, 10, 11])("checks boundary %s before retention", (bytes) => {
    const budget = new ReaderResourceBudget({ retainedBytes: 10 });
    if (bytes > 10) expect(() => budget.reserve({ retainedBytes: bytes })).toThrow(ResourceLimitError);
    else { const release = budget.reserve({ retainedBytes: bytes }); expect(budget.snapshot().retainedBytes).toBe(bytes); release(); release(); }
    expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("rejects concurrent reservations atomically", () => {
    const budget = new ReaderResourceBudget({ retainedBytes: 10, decodedPixels: 100 });
    const release = budget.reserve({ retainedBytes: 6 });
    expect(() => budget.reserve({ retainedBytes: 5, decodedPixels: 50 })).toThrow(ResourceLimitError);
    expect(budget.snapshot().decodedPixels).toBe(0);
    release();
    budget.reserve({ retainedBytes: 10 })();
  });
  it("rejects huge pixels before decoding", async () => {
    const budget = new ReaderResourceBudget({ decodedPixels: 100 });
    let called = false;
    await expect(withDecode(budget, 10000, 10000, async () => { called = true; })).rejects.toThrow(ResourceLimitError);
    expect(called).toBe(false);
  });
  it("keeps pixels reserved until release and limits concurrent decode", async () => {
    const budget = new ReaderResourceBudget({ activeDecodes: 1 });
    let finish!: () => void;
    const pending = withDecode(budget, 2, 3, () => new Promise<void>((resolve) => { finish = resolve; }));
    await expect(withDecode(budget, 2, 3, async () => {})).rejects.toThrow(ResourceLimitError);
    expect(budget.snapshot().decodedPixels).toBe(6);
    finish();
    const retained = await pending;
    expect(budget.snapshot()).toEqual({ retainedBytes: 0, decodedPixels: 6, activeDecodes: 0 });
    retained.release();
    expect(budget.snapshot().decodedPixels).toBe(0);
  });
  it("releases failed or cancelled decode after it settles", async () => {
    const budget = new ReaderResourceBudget();
    await expect(withDecode(budget, 2, 3, async () => { throw new Error("bad image"); })).rejects.toThrow("bad image");
    const controller = new AbortController();
    await expect(withDecode(budget, 2, 3, async () => { controller.abort(); }, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(budget.snapshot()).toEqual({ retainedBytes: 0, decodedPixels: 0, activeDecodes: 0 });
  });
  it("validates dimensions and configuration", () => {
    expect(() => decodedPixels(Number.MAX_SAFE_INTEGER, 2)).toThrow(RangeError);
    expect(() => new ReaderResourceBudget({ retainedBytes: -1 })).toThrow(RangeError);
  });
});

import { imageDimensions } from "./resourceBudget";

describe("dimensions before decode", () => {
  it("finds huge PNG dimensions in a tiny header", () => {
    const header = new Uint8Array(24);
    const view = new DataView(header.buffer);
    view.setUint32(0, 0x89504e47); view.setUint32(4, 0x0d0a1a0a); view.setUint32(12, 0x49484452);
    view.setUint32(16, 10000); view.setUint32(20, 20000);
    expect(imageDimensions(header)).toEqual({ width: 10000, height: 20000 });
    const budget = new ReaderResourceBudget({ decodedPixels: 1000000 });
    expect(() => budget.reserve({ decodedPixels: decodedPixels(10000, 20000) })).toThrow(ResourceLimitError);
  });
  it("rejects malformed or unknown headers", () => {
    expect(() => imageDimensions(new Uint8Array(10))).toThrow("unavailable before decode");
    expect(() => imageDimensions(new Uint8Array([255, 216, 255, 192, 0, 0]))).toThrow("Invalid JPEG");
  });
});

import { vi } from "vitest";
import { extractReserved, readBoundedBodyReserved, type ZipEntryLike } from "./resources";
import { PageStore } from "./cbz";

function resourceEntry(name: string, bytes: Uint8Array): ZipEntryLike {
  const callbacks = new Map<string, Function>();
  const stream = {
    on(event: string, callback: Function) { callbacks.set(event, callback); return stream; },
    pause() { return stream; },
    resume() { queueMicrotask(() => { callbacks.get("data")?.(bytes); callbacks.get("end")?.(); }); return stream; },
  };
  return { name, internalStream: () => stream };
}

describe("retained reader integration", () => {
  it("reserves chunk and blob allocation, keeps bytes until release", async () => {
    const budget = new ReaderResourceBudget({ retainedBytes: 6 });
    const result = await extractReserved(resourceEntry("page.jpg", new Uint8Array(3)), 10, budget);
    expect(result.blob.size).toBe(3);
    expect(budget.snapshot().retainedBytes).toBe(3);
    await expect(extractReserved(resourceEntry("page.jpg", new Uint8Array(3)), 10, budget)).rejects.toThrow(ResourceLimitError);
    expect(budget.snapshot().retainedBytes).toBe(3);
    result.release(); result.release();
    expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("reserves downloaded archive through the caller lifetime", async () => {
    const budget = new ReaderResourceBudget({ retainedBytes: 6 });
    const result = await readBoundedBodyReserved(new Response(new Uint8Array(3)), 10, budget);
    expect(result.buffer.byteLength).toBe(3);
    expect(budget.snapshot().retainedBytes).toBe(3);
    result.release();
    expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("releases retained pages and archive on close and reports limit failures", async () => {
    vi.stubGlobal("URL", { createObjectURL: () => "blob:page", revokeObjectURL: vi.fn() });
    try {
      const budget = new ReaderResourceBudget({ retainedBytes: 3 });
      const archiveRelease = budget.reserve({ retainedBytes: 1 });
      const store = new PageStore([resourceEntry("page.jpg", new Uint8Array(1)), resourceEntry("other.jpg", new Uint8Array(2))], () => {}, budget, archiveRelease);
      store.ensure(0);
      for (let i = 0; i < 10; i++) await Promise.resolve();
      expect(store.status(0)).toBe("ready");
      expect(budget.snapshot().retainedBytes).toBe(2);
      store.ensure(1);
      for (let i = 0; i < 10; i++) await Promise.resolve();
      expect(store.status(1)).toBe("error");
      expect(store.error(1)).toContain("retainedBytes limit");
      store.revoke();
      expect(budget.snapshot().retainedBytes).toBe(0);
      expect(URL.revokeObjectURL).toHaveBeenCalled();
    } finally { vi.unstubAllGlobals(); }
  });
  it("refuses oversized pixels before object URL publication", async () => {
    vi.stubGlobal("URL", { createObjectURL: vi.fn(), revokeObjectURL: vi.fn() });
    try {
      const header = new Uint8Array(24);
      const view = new DataView(header.buffer);
      view.setUint32(0, 0x89504e47); view.setUint32(4, 0x0d0a1a0a); view.setUint32(12, 0x49484452);
      view.setUint32(16, 10000); view.setUint32(20, 20000);
      const budget = new ReaderResourceBudget({ decodedPixels: 100 });
      const store = new PageStore([resourceEntry("page.png", header)], () => {}, budget);
      store.ensure(0);
      for (let i = 0; i < 20; i++) await Promise.resolve();
      expect(store.status(0)).toBe("error");
      expect(store.error(0)).toContain("decodedPixels limit");
      expect(URL.createObjectURL).not.toHaveBeenCalled();
      expect(budget.snapshot().retainedBytes).toBe(0);
      store.revoke();
    } finally { vi.unstubAllGlobals(); }
  });
});

it("releases pages during repeated distant seeks", async () => {
  vi.stubGlobal("URL", { createObjectURL: () => "blob:page", revokeObjectURL: vi.fn() });
  try {
    const budget = new ReaderResourceBudget({ retainedBytes: 2 });
    const store = new PageStore(Array.from({ length: 25 }, (_, i) => resourceEntry(`${i}.jpg`, new Uint8Array(1))), () => {}, budget);
    for (const page of [0, 20, 0, 20]) {
      store.ensureAround(page, 0);
      for (let i = 0; i < 10; i++) await Promise.resolve();
      expect(store.status(page)).toBe("ready");
      expect(budget.snapshot().retainedBytes).toBe(1);
    }
    store.revoke();
    expect(budget.snapshot().retainedBytes).toBe(0);
  } finally { vi.unstubAllGlobals(); }
});

it("keeps an active decode reserved until cancellation settles", async () => {
  let finish: (() => void) | undefined;
  vi.stubGlobal("Image", class { src = ""; decode() { return new Promise<void>((resolve) => { finish = resolve; }); } });
  vi.stubGlobal("URL", { createObjectURL: () => "blob:page", revokeObjectURL: vi.fn() });
  try {
    const header = new Uint8Array(24), view = new DataView(header.buffer);
    view.setUint32(0, 0x89504e47); view.setUint32(4, 0x0d0a1a0a); view.setUint32(12, 0x49484452);
    view.setUint32(16, 2); view.setUint32(20, 3);
    const budget = new ReaderResourceBudget({ decodedPixels: 100, activeDecodes: 1 });
    const store = new PageStore([resourceEntry("page.png", header)], () => {}, budget);
    store.ensure(0);
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(finish).toBeDefined();
    expect(budget.snapshot().activeDecodes).toBe(1);
    store.revoke();
    expect(budget.snapshot().activeDecodes).toBe(1);
    finish!();
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(budget.snapshot()).toEqual({ retainedBytes: 0, decodedPixels: 0, activeDecodes: 0 });
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  } finally { finish?.(); vi.unstubAllGlobals(); }
});
