import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CbzReader } from "../src/reader/cbz";

const mocks = vi.hoisted(() => ({ files: {} as Record<string, unknown>, saves: [] as { edition: number; body: any }[] }));
vi.mock("jszip", () => ({ default: { loadAsync: vi.fn(async () => ({ files: mocks.files })) } }));
vi.mock("../src/reader/shared", async (original) => ({ ...await original<object>(), useProgressSaver: (edition: number) => ({ state: "idle", save: (body: unknown) => mocks.saves.push({ edition, body }) }) }));

let root: HTMLDivElement;
let observers: { callback: IntersectionObserverCallback; options: IntersectionObserverInit }[];
let scroll: ReturnType<typeof vi.fn>;

function entry(name: string) {
  return { name, async: async () => new Blob([new Uint8Array([1])]), internalStream: () => {
    const callbacks = new Map<string, Function>();
    const stream = {
      on: (event: string, callback: Function) => { callbacks.set(event, callback); return stream; },
      pause: () => stream,
      resume: () => { queueMicrotask(() => { callbacks.get("data")?.(new Uint8Array([1])); callbacks.get("end")?.(); }); return stream; },
    };
    return stream;
  } };
}
async function settle() { await act(async () => { for (let i = 0; i < 40; i++) await Promise.resolve(); await vi.dynamicImportSettled(); }); }
function observe(observer: typeof observers[number], entries: { page: number; visible: boolean }[]) {
  act(() => observer.callback(entries.map(({ page, visible }) => ({ target: root.querySelector(`[data-page="${page}"]`)!, isIntersecting: visible })) as IntersectionObserverEntry[], {} as IntersectionObserver));
}
function pageInput() { return root.querySelector<HTMLInputElement>('input[aria-label="Page"]')!; }

beforeEach(() => {
  root = document.createElement("div"); document.body.append(root);
  observers = []; mocks.saves.length = 0; localStorage.clear();
  mocks.files = Object.fromEntries(Array.from({ length: 8 }, (_, i) => { const name = `${i + 1}.jpg`; return [name, entry(name)]; }));
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(new Uint8Array([1])))));
  vi.stubGlobal("URL", { createObjectURL: vi.fn(() => `blob:${Math.random()}`), revokeObjectURL: vi.fn() });
  vi.stubGlobal("IntersectionObserver", class {
    constructor(callback: IntersectionObserverCallback, options: IntersectionObserverInit) { observers.push({ callback, options }); }
    observe() {} disconnect() {}
  });
  scroll = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scroll });
  Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: vi.fn() });
});
afterEach(() => { act(() => render(null, root)); root.remove(); vi.unstubAllGlobals(); });

describe("comic reader navigation", () => {
  it("marks the final double-page spread finished for even-length books", async () => {
    localStorage.setItem("libteca-cbz-mode", "double");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={{ page: 7 }} onBack={() => {}} />, root));
    await settle();
    expect(mocks.saves).toContainEqual({ edition: 1, body: { page: 8, percent: 1, locator: "8", finished: true } });
    expect(root.querySelectorAll("img")).toHaveLength(2);
  });

  it("scrolls webtoon pages when keyboard navigation changes the selected page", async () => {
    localStorage.setItem("libteca-cbz-mode", "webtoon");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={null} onBack={() => {}} />, root));
    await settle();
    act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight" })));
    await settle();
    expect(pageInput().value).toBe("2");
    expect(scroll.mock.instances.some((element: HTMLElement) => element.dataset.page === "1")).toBe(true);
  });

  it("does not save prefetched pages that have not reached the viewport", async () => {
    localStorage.setItem("libteca-cbz-mode", "webtoon");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={null} onBack={() => {}} />, root));
    await settle(); mocks.saves.length = 0;
    const preload = observers.find((observer) => observer.options.rootMargin === "60% 0px")!;
    observe(preload, [{ page: 3, visible: true }]);
    await settle();
    expect(pageInput().value).toBe("1");
    expect(mocks.saves).toEqual([]);
  });

  it("tracks the first actually visible webtoon page and advances when it leaves", async () => {
    localStorage.setItem("libteca-cbz-mode", "webtoon");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={null} onBack={() => {}} />, root));
    await settle();
    const viewport = observers.find((observer) => !observer.options.rootMargin)!;
    observe(viewport, [{ page: 2, visible: true }, { page: 3, visible: true }]);
    await settle();
    expect(pageInput().value).toBe("3");
    observe(viewport, [{ page: 2, visible: false }]); await settle();
    expect(pageInput().value).toBe("4");
  });

  it("restores webtoon progress and supports slider jumps", async () => {
    localStorage.setItem("libteca-cbz-mode", "webtoon");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={{ page: 4 }} onBack={() => {}} />, root));
    await settle();
    expect(scroll.mock.instances.some((element: HTMLElement) => element.dataset.page === "3")).toBe(true);
    act(() => { pageInput().value = "7"; pageInput().dispatchEvent(new Event("input", { bubbles: true })); });
    await settle();
    expect(scroll.mock.instances.some((element: HTMLElement) => element.dataset.page === "6")).toBe(true);
  });

  it("ignores detached webtoon observer callbacks after changing reader mode", async () => {
    localStorage.setItem("libteca-cbz-mode", "webtoon");
    act(() => render(<CbzReader editionId={1} title="Comic" progress={null} onBack={() => {}} />, root));
    await settle();
    const viewport = observers.find((observer) => !observer.options.rootMargin)!;
    const oldPage = root.querySelector('[data-page="4"]')!;
    act(() => root.querySelector<HTMLButtonElement>('[aria-label="Single page"]')!.click());
    act(() => viewport.callback([{ target: oldPage, isIntersecting: true }] as IntersectionObserverEntry[], {} as IntersectionObserver));
    await settle();
    expect(pageInput().value).toBe("1");
  });

  it("drops the old book immediately when its edition changes", async () => {
    act(() => render(<CbzReader editionId={1} title="First" progress={{ page: 8 }} onBack={() => {}} />, root));
    await settle(); mocks.saves.length = 0;
    vi.mocked(fetch).mockImplementation(() => new Promise(() => {}));
    act(() => render(<CbzReader editionId={2} title="Second" progress={null} onBack={() => {}} />, root));
    expect(root.textContent).toContain("Loading comic");
    expect(root.querySelector("img")).toBeNull();
    expect(mocks.saves).toEqual([]);
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  });
});
