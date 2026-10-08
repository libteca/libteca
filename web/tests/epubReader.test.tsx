import JSZip from "jszip";
import { sharedReaderBudget } from "../src/reader/resourceBudget";
import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EpubReader } from "../src/reader/epub";

const mocks = vi.hoisted(() => ({ makeBook: vi.fn(), books: [] as any[], renditions: [] as any[], saves: [] as { edition: number; body: unknown }[] }));
vi.mock("../src/reader/shared", async (original) => ({ ...await original<object>(), useProgressSaver: (edition: number) => ({ state: "idle", save: (body: unknown) => mocks.saves.push({ edition, body }) }) }));
vi.mock("epubjs", () => ({
  Book: class {
    constructor() { const book = mocks.makeBook(); mocks.books.push(book); return book; }
  },
  Rendition: class {
    listeners = new Map<string, Function[]>();
    themes = { default: vi.fn(), override: vi.fn(), fontSize: vi.fn() };
    attachTo = vi.fn().mockResolvedValue(undefined);
    display = vi.fn().mockResolvedValue(undefined);
    next = vi.fn().mockResolvedValue(undefined);
    prev = vi.fn().mockResolvedValue(undefined);
    destroy = vi.fn();
    currentLocation = vi.fn(() => ({ start: { cfi: "epubcfi(current)" } }));
    on = vi.fn((event: string, callback: Function) => this.listeners.set(event, [...this.listeners.get(event) ?? [], callback]));
    off = vi.fn((event: string, callback: Function) => this.listeners.set(event, (this.listeners.get(event) ?? []).filter((f) => f !== callback)));
    emit(event: string, value: unknown) { for (const callback of this.listeners.get(event) ?? []) callback(value); }
    constructor() { mocks.renditions.push(this); }
  },
}));

let root: HTMLDivElement;
let epubBytes: ArrayBuffer;
const progress = { page: 1, percent: 0.2, locator: "epubcfi(saved)", revision: 3 };

function book() {
  return {
    open: vi.fn().mockResolvedValue(undefined), destroy: vi.fn(), replacements: vi.fn().mockResolvedValue(undefined),
    loaded: { navigation: Promise.resolve({ toc: [{ label: "Chapter", href: "one.xhtml" }] }) },
    locations: { generate: vi.fn().mockResolvedValue([]), save: vi.fn(() => "[]"), load: vi.fn(), length: vi.fn(() => 1), percentageFromCfi: vi.fn(() => 0.4), cfiFromPercentage: vi.fn(() => "epubcfi(percent)") },
  };
}

async function settle() {
  await act(async () => { for (let i = 0; i < 30; i++) await Promise.resolve(); await vi.dynamicImportSettled(); });
}

beforeEach(async () => {
  const archive = new JSZip(); archive.file("one.xhtml", "<html><body>chapter</body></html>"); epubBytes = await archive.generateAsync({ type: "arraybuffer" });
  root = document.createElement("div"); document.body.append(root);
  mocks.books.length = 0; mocks.renditions.length = 0; mocks.saves.length = 0;
  mocks.makeBook.mockReset().mockImplementation(book);
  localStorage.clear();
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new Uint8Array([0]).buffer) } });
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(epubBytes))));
});

afterEach(() => { act(() => render(null, root)); root.remove(); vi.unstubAllGlobals(); });

describe("EPUB reader lifecycle", () => {
  it("restores a saved CFI and forwards iframe arrow and Escape events", async () => {
    const back = vi.fn();
    act(() => render(<EpubReader editionId={1} title="Book" progress={progress} onBack={back} />, root));
    await settle();
    const rendition = mocks.renditions[0];
    expect(rendition.display).toHaveBeenCalledWith("epubcfi(saved)");
    act(() => rendition.emit("keydown", new KeyboardEvent("keydown", { key: "ArrowRight" })));
    act(() => rendition.emit("keydown", new KeyboardEvent("keydown", { key: "ArrowLeft" })));
    act(() => rendition.emit("keydown", new KeyboardEvent("keydown", { key: "Escape" })));
    await settle();
    expect(rendition.next).toHaveBeenCalledTimes(1);
    expect(rendition.prev).toHaveBeenCalledTimes(1);
    expect(back).toHaveBeenCalledTimes(1);
  });

  it("leaves typing inside EPUB content alone", async () => {
    act(() => render(<EpubReader editionId={1} title="Book" progress={progress} onBack={() => {}} />, root));
    await settle();
    const event = new KeyboardEvent("keydown", { key: "ArrowRight" });
    Object.defineProperty(event, "target", { value: document.createElement("input") });
    act(() => mocks.renditions[0].emit("keydown", event));
    expect(mocks.renditions[0].next).not.toHaveBeenCalled();
  });

  it("restores percentage using a cached index without regenerating it", async () => {
    localStorage.setItem("libteca:epub-loc:v2:1:1000:00", '["epubcfi(cached)"]');
    act(() => render(<EpubReader editionId={1} title="Book" progress={{ percent: 0.7 }} onBack={() => {}} />, root));
    await settle();
    expect(mocks.books[0].locations.generate).not.toHaveBeenCalled();
    expect(mocks.books[0].locations.cfiFromPercentage).toHaveBeenCalledWith(0.7);
    expect(mocks.renditions[0].display).toHaveBeenCalledWith("epubcfi(percent)");
  });

  it("does not turn a background index failure into an unreadable book", async () => {
    const failed = book(); failed.locations.generate.mockRejectedValue(new Error("index failed"));
    mocks.makeBook.mockReturnValue(failed);
    act(() => render(<EpubReader editionId={1} title="Book" progress={progress} onBack={() => {}} />, root));
    await settle();
    expect(root.textContent).not.toContain("index failed");
    expect(root.querySelector('[aria-label="Next page"]')).not.toBeNull();
  });

  it("saves the current CFI with its percentage once background indexing finishes", async () => {
    act(() => render(<EpubReader editionId={1} title="Book" progress={progress} onBack={() => {}} />, root));
    await settle();
    expect(mocks.saves).toContainEqual({ edition: 1, body: { locator: "epubcfi(current)", percent: 0.4 } });
  });

  it("discards old relocation and key events when the edition changes", async () => {
    const back = vi.fn();
    act(() => render(<EpubReader editionId={1} title="First" progress={progress} onBack={back} />, root));
    await settle();
    const old = mocks.renditions[0];
    const oldRelocations = [...old.listeners.get("relocated")];
    const oldKey = old.listeners.get("keydown")?.[0];
    act(() => render(<EpubReader editionId={2} title="Second" progress={null} onBack={back} />, root));
    await settle(); mocks.saves.length = 0;
    act(() => { for (const callback of oldRelocations) callback({ start: { cfi: "epubcfi(stale)", href: "old.xhtml" }, atEnd: true }); oldKey?.(new KeyboardEvent("keydown", { key: "ArrowRight" })); });
    expect(mocks.saves).toEqual([]);
    expect(mocks.renditions[1].next).not.toHaveBeenCalled();
    expect(old.destroy).toHaveBeenCalled();
  });

  it("does not create a book if it closes while hashing the download", async () => {
    let digest!: (value: ArrayBuffer) => void;
    vi.stubGlobal("crypto", { subtle: { digest: vi.fn(() => new Promise<ArrayBuffer>((resolve) => { digest = resolve; })) } });
    act(() => render(<EpubReader editionId={1} title="Book" progress={null} onBack={() => {}} />, root));
    await settle();
    act(() => render(null, root));
    digest(new ArrayBuffer(1)); await settle();
    expect(mocks.books).toHaveLength(0);
  });

  it("reports page navigation failures without an unhandled rejection", async () => {
    act(() => render(<EpubReader editionId={1} title="Book" progress={progress} onBack={() => {}} />, root));
    await settle();
    mocks.renditions[0].next.mockRejectedValue(new Error("missing chapter"));
    act(() => mocks.renditions[0].emit("keydown", new KeyboardEvent("keydown", { key: "ArrowRight" })));
    await settle();
    expect(root.textContent).toContain("Could not change pages. Try again.");
    expect(root.querySelector('[aria-label="Next page"]')).not.toBeNull();
  });

  it("uses incremental download reads instead of buffering the whole response", async () => {
    const response = new Response(epubBytes);
    const buffer = vi.spyOn(response, "arrayBuffer");
    vi.mocked(fetch).mockResolvedValue(response);
    act(() => render(<EpubReader editionId={1} title="Book" progress={null} onBack={() => {}} />, root));
    await settle();
    expect(mocks.books).toHaveLength(1);
    expect(buffer).not.toHaveBeenCalled();
  });
});

it("retains configured shared compressed and directory capacity through close even if destruction fails",async()=>{
 localStorage.setItem("libteca-reader-resource-limits",JSON.stringify({retainedBytes:4096}));
 const budget=sharedReaderBudget();const failing=book();failing.destroy.mockImplementation(()=>{throw new Error("destroy failed")});mocks.makeBook.mockReturnValue(failing);
 act(()=>render(<EpubReader editionId={5} title="Book" progress={null} onBack={()=>{}}/>,root));await settle();
 expect(budget.snapshot().retainedBytes).toBeGreaterThanOrEqual(epubBytes.byteLength);
 act(()=>render(null,root));expect(budget.snapshot().retainedBytes).toBe(0);
});
it("refuses low shared limits before opening EPUB and releases failed admission",async()=>{
 localStorage.setItem("libteca-reader-resource-limits",JSON.stringify({retainedBytes:1}));const budget=sharedReaderBudget();
 act(()=>render(<EpubReader editionId={6} title="Book" progress={null} onBack={()=>{}}/>,root));await settle();
 expect(mocks.books).toHaveLength(0);expect(root.textContent).toContain("retainedBytes");expect(budget.snapshot().retainedBytes).toBe(0);
});
