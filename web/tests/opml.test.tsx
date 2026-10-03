import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PodcastsView } from "../src/views/podcasts";
import { api } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: () => "" };
});
vi.mock("../src/toast", () => ({ toast: vi.fn() }));
const request = vi.mocked(api);
let root: HTMLDivElement;

beforeEach(() => {
  vi.useFakeTimers();
  request.mockReset();
  request.mockImplementation(async (path, opts) => {
    if (path === "/podcasts") return [];
    if (opts?.method === "POST") return { status: "running", added: 0, failed: 0, total: 3 };
    return { status: "idle", added: 0, failed: 0, total: 0 };
  });
  root = document.createElement("div");
  document.body.append(root);
});

afterEach(() => {
  act(() => render(null, root));
  root.remove();
  vi.useRealTimers();
});

async function mount() {
  await act(async () => render(<PodcastsView />, root));
}

async function selectFile() {
  const input = root.querySelector<HTMLInputElement>('input[type="file"]')!;
  Object.defineProperty(input, "files", { configurable: true, value: [{ text: async () => "<opml />" }] });
  await act(async () => {
    input.dispatchEvent(new Event("change", { bubbles: true }));
    for (let i = 0; i < 6; i++) await Promise.resolve();
  });
}

describe("OPML import status", () => {
  it("waits for completion and displays actual counts from the background job", async () => {
    await mount();
    await selectFile();
    expect(root.textContent).not.toContain("undefined");
    expect(root.textContent).toContain("Importing");
    expect(root.querySelector<HTMLInputElement>('input[type="file"]')!.disabled).toBe(true);
    request.mockImplementation(async (path) => path === "/podcasts" ? [] : { status: "done", added: 1, failed: 1, total: 3 });
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(root.textContent).toContain("Imported 1 new, 1 already subscribed, 1 failed");
    expect(root.querySelector<HTMLInputElement>('input[type="file"]')!.disabled).toBe(false);
    const calls = request.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(request).toHaveBeenCalledTimes(calls);
  });

  it("resumes watching an already running import when reopening podcasts", async () => {
    request.mockImplementation(async (path) => path === "/podcasts" ? [] : { status: "running", added: 1, failed: 0, total: 3 });
    await mount();
    expect(root.textContent).toContain("Importing");
    expect(root.querySelector<HTMLInputElement>('input[type="file"]')!.disabled).toBe(true);
  });

  it("does not treat an idle job after a server restart as success", async () => {
    await mount();
    await selectFile();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(root.textContent).toContain("Import status was lost");
    expect(root.textContent).not.toContain("Imported");
  });

  it("retries a transient status failure without submitting the import twice", async () => {
    await mount();
    await selectFile();
    request.mockRejectedValueOnce(new Error("offline"));
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(root.textContent).toContain("Retrying");
    expect(root.querySelector<HTMLInputElement>('input[type="file"]')!.disabled).toBe(true);
    request.mockResolvedValue({ status: "done", added: 2, failed: 0, total: 3 });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(root.textContent).toContain("Imported 2 new, 1 already subscribed, 0 failed");
    expect(request.mock.calls.filter(([, opts]) => opts?.method === "POST")).toHaveLength(1);
  });

  it("ignores a late initial status read after starting a newer import", async () => {
    let resolve!: (value: unknown) => void;
    request.mockImplementation((path, opts) => {
      if (path === "/podcasts/import-opml/status") return new Promise((res) => { resolve = res; });
      return Promise.resolve(path === "/podcasts" ? [] : { status: "running", added: 0, failed: 0, total: 3 });
    });
    await mount();
    await selectFile();
    await act(async () => resolve({ status: "running", added: 19, failed: 0, total: 20 }));
    expect(root.textContent).toContain("Importing 3 feeds");
    expect(root.textContent).not.toContain("19 new");
  });

  it("stops polling on unmount and ignores an in-flight completion", async () => {
    await mount();
    await selectFile();
    let resolve!: (value: unknown) => void;
    request.mockImplementationOnce(() => new Promise((res) => { resolve = res; }));
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    act(() => render(null, root));
    const calls = request.mock.calls.length;
    await act(async () => resolve({ status: "done", added: 3, failed: 0, total: 3 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(request).toHaveBeenCalledTimes(calls);
  });

  it("follows the existing import after a duplicate-start response", async () => {
    await mount();
    request.mockImplementation(async (path, opts) => {
      if (opts?.method === "POST") return { status: 409, error: "import already running" };
      return path === "/podcasts" ? [] : { status: "running", added: 1, failed: 0, total: 3 };
    });
    await selectFile();
    expect(root.textContent).toContain("Importing 3 feeds: 1 new");
    expect(root.textContent).not.toContain("import already running");
  });

  it("shows an import rejection without reporting success or polling", async () => {
    await mount();
    request.mockResolvedValue({ error: "no feed urls found", status: 400 });
    await selectFile();
    expect(root.textContent).toContain("no feed urls found");
    expect(root.textContent).not.toContain("Imported");
    const calls = request.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(request).toHaveBeenCalledTimes(calls);
  });


  it.each([408, 429, 500, 503])("retries HTTP %s status failures without enabling another import", async (status) => {
    await mount();
    await selectFile();
    request.mockResolvedValueOnce({ error: "temporary failure", status });
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(root.textContent).toContain("Retrying");
    expect(root.querySelector<HTMLInputElement>('input[type="file"]')!.disabled).toBe(true);
    request.mockResolvedValue({ status: "done", added: 3, failed: 0, total: 3 });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(root.textContent).toContain("Imported 3 new");
  });


  it("refreshes subscriptions when returning from a show after the import finishes", async () => {
    const first = { id: 1, title: "Original podcast", hasCover: false, maxEpisodes: 3, episodes: [] };
    let finished = false;
    request.mockImplementation(async (path, opts) => {
      if (path === "/podcasts") return finished ? [first, { ...first, id: 2, title: "Imported podcast" }] : [first];
      if (path === "/podcasts/1") return first;
      if (opts?.method === "POST") return { status: "running", added: 0, failed: 0, total: 1 };
      return { status: finished ? "done" : "idle", added: finished ? 1 : 0, failed: 0, total: 1 };
    });
    await mount();
    await selectFile();
    await act(async () => root.querySelector<HTMLButtonElement>("button.cover-card")!.click());
    finished = true;
    const back = Array.from(root.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "Podcasts")!;
    await act(async () => back.click());
    expect(root.textContent).toContain("Imported podcast");
  });

});
