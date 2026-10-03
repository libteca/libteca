import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiWithDeadline } from "../src/api";
import { PdfReader } from "../src/reader/pdf";

const mocks = vi.hoisted(() => ({ saves: [] as { edition: number; body: unknown }[] }));
vi.mock("../src/api", async (original) => ({ ...await original<object>(), apiWithDeadline: vi.fn() }));
vi.mock("../src/reader/shared", async (original) => ({ ...await original<object>(), useProgressSaver: (edition: number) => ({ state: "idle", save: (body: unknown) => mocks.saves.push({ edition, body }) }) }));

let root: HTMLDivElement;
async function settle() { await act(async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); }); }
function input() { return root.querySelector<HTMLInputElement>('input[aria-label="Page"]')!; }
function commit(page: string) { act(() => { input().value = page; input().dispatchEvent(new Event("input", { bubbles: true })); }); act(() => input().dispatchEvent(new Event("change", { bubbles: true }))); }

beforeEach(() => {
  root = document.createElement("div"); document.body.append(root);
  mocks.saves.length = 0; vi.mocked(apiWithDeadline).mockReset().mockResolvedValue({ page: 25, pageCount: 100, revision: 3 });
});
afterEach(() => { act(() => render(null, root)); root.remove(); });

describe("PDF reader resume and lifecycle", () => {
  it("keeps the page control and embed at page one when reopening a finished book", async () => {
    vi.mocked(apiWithDeadline).mockResolvedValue({ page: 90, pageCount: 100, isFinished: true });
    act(() => render(<PdfReader editionId={1} title="Book" progress={{ page: 90, isFinished: true }} onBack={() => {}} />, root));
    await settle();
    expect(input().value).toBe("1");
    expect(root.querySelector("embed")!.src).toContain("page=1");
    commit("2");
    expect(mocks.saves).toEqual([{ edition: 1, body: { page: 2, percent: 0.02 } }]);
  });

  it("uses already loaded progress if the extra page-count request fails", async () => {
    vi.mocked(apiWithDeadline).mockRejectedValue(new Error("offline"));
    act(() => render(<PdfReader editionId={1} title="Book" progress={{ page: 42 }} onBack={() => {}} />, root));
    await settle();
    expect(input().value).toBe("42");
    expect(root.querySelector("embed")!.src).toContain("page=42");
    expect(mocks.saves).toEqual([]);
  });

  it("bounds an old stored page to the current document length", async () => {
    vi.mocked(apiWithDeadline).mockResolvedValue({ page: 200, pageCount: 100 });
    act(() => render(<PdfReader editionId={1} title="Book" progress={{ page: 200 }} onBack={() => {}} />, root));
    await settle();
    expect(input().value).toBe("100");
    expect(root.querySelector("embed")!.src).toContain("page=100");
    expect(mocks.saves).toEqual([]);
  });

  it("disables mutation until the resume request resolves", async () => {
    vi.mocked(apiWithDeadline).mockImplementation(() => new Promise(() => {}));
    act(() => render(<PdfReader editionId={1} title="Book" progress={null} onBack={() => {}} />, root));
    expect(input().disabled).toBe(true);
    expect([...root.querySelectorAll("button")].find((button) => button.textContent === "Mark finished")!.disabled).toBe(true);
  });

  it("does not carry page count, finished status, or resume into another edition", async () => {
    vi.mocked(apiWithDeadline).mockResolvedValueOnce({ page: 90, pageCount: 100, isFinished: true }).mockResolvedValueOnce({ page: 2, pageCount: 5, isFinished: false });
    act(() => render(<PdfReader editionId={1} title="First" progress={{ isFinished: true }} onBack={() => {}} />, root));
    await settle();
    act(() => render(<PdfReader editionId={2} title="Second" progress={null} onBack={() => {}} />, root));
    expect(root.querySelector("embed")).toBeNull();
    await settle();
    expect(input().value).toBe("2"); expect(input().max).toBe("5");
    expect(root.textContent).not.toContain("Finished");
    commit("4");
    expect(mocks.saves).toEqual([{ edition: 2, body: { page: 4, percent: 0.8 } }]);
  });

  it("aborts its pending page-count request when closed", async () => {
    vi.mocked(apiWithDeadline).mockImplementation(() => new Promise(() => {}));
    act(() => render(<PdfReader editionId={1} title="Book" progress={null} onBack={() => {}} />, root));
    const signal = vi.mocked(apiWithDeadline).mock.calls[0][1]!.signal;
    act(() => render(null, root));
    expect(signal?.aborted).toBe(true);
  });

  it("keeps known progress when the metadata response reports an HTTP error", async () => {
    vi.mocked(apiWithDeadline).mockResolvedValue({ error: "failed", status: 503 });
    act(() => render(<PdfReader editionId={1} title="Book" progress={{ page: 42 }} onBack={() => {}} />, root));
    await settle();
    expect(input().value).toBe("42");
    expect(root.querySelector("embed")!.src).toContain("page=42");
  });

  it("saves once across change plus blur and ignores invalid inputs", async () => {
    act(() => render(<PdfReader editionId={1} title="Book" progress={null} onBack={() => {}} />, root));
    await settle(); commit("35");
    act(() => input().dispatchEvent(new Event("blur", { bubbles: true })));
    commit(""); commit("9007199254740992");
    expect(input().value).toBe("35");
    expect(mocks.saves).toEqual([{ edition: 1, body: { page: 35, percent: 0.35 } }]);
  });
});
