import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WorkView } from "../src/views/work";
import { api, getToken } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: vi.fn(() => "primary-session") };
});
vi.mock("../src/toast", () => ({ toast: vi.fn() }));
const request = vi.mocked(api);
let root: HTMLDivElement;
const work = (id = 1) => ({ id, title: `Book ${id}`, author: "Author", description: null, hasCover: false,
  editions: [{ id, title: "Book", format: "m4b", duration: 100, files: [{ id, seq: 1, duration: 100, size: 20 }], chapters: [] }] });

beforeEach(() => {
  vi.useFakeTimers();
  root = document.createElement("div");
  document.body.append(root);
  request.mockReset();
  vi.mocked(getToken).mockReturnValue(`primary-${Math.random()}`);
  request.mockImplementation(async (path, opts) => opts?.method === "POST" ? {} : path.startsWith("/works/") ? work(Number(path.split("/").pop())) : {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(async () => {
  await act(async () => render(null, root));
  vi.mocked(getToken).mockReturnValue("changed-after-test");
  await act(async () => vi.advanceTimersByTimeAsync(60000));
  root.remove();
  vi.useRealTimers();
  vi.restoreAllMocks();
});
async function settle() { await act(async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); }); }
async function mount(id = 1) {
  await act(async () => render(<WorkView id={id} />, root));
  await settle();
  await act(async () => [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Listen")!.click());
  await act(async () => vi.advanceTimersByTimeAsync(60));
  await settle();
  const audio = root.querySelector("audio")!;
  Object.defineProperty(audio, "readyState", { value: 4 });
  await act(async () => audio.dispatchEvent(new Event("loadedmetadata")));
  return audio;
}
async function event(audio: HTMLAudioElement, type: string, position: number) {
  audio.currentTime = position;
  await act(async () => audio.dispatchEvent(new Event(type)));
  await settle();
}
function writes() { return request.mock.calls.filter(([, opts]) => opts?.method === "POST").map(([path, opts]) => ({ path, ...JSON.parse(String(opts!.body)) })); }

describe("primary audio progress delivery", () => {
  it("serializes a slow position write before a rewind and completion", async () => {
    const audio = await mount();
    let resolve!: (value: unknown) => void;
    request.mockClear();
    request.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    await event(audio, "seeked", 70);
    await event(audio, "seeked", 20);
    await event(audio, "ended", 100);
    expect(writes()).toHaveLength(1);
    await act(async () => resolve({}));
    await settle();
    expect(writes().map((p) => [p.position, p.finished])).toEqual([[70, false], [20, false], [100, true]]);
  });

  it("retries a failed completion after the audio element is removed", async () => {
    const audio = await mount();
    request.mockClear();
    request.mockResolvedValueOnce({ error: "unavailable", status: 503 });
    await event(audio, "ended", 100);
    expect(root.querySelector("audio")).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    await settle();
    expect(writes().map((p) => [p.position, p.finished])).toEqual([[100, true], [100, true]]);
  });

  it("persists a seek back to the start", async () => {
    const audio = await mount();
    await event(audio, "seeked", 20);
    request.mockClear();
    await event(audio, "seeked", 0);
    expect(writes()).toContainEqual({ path: "/progress/1", position: 0, duration: 100, finished: false });
  });

  it("does not send queued writes under a newly signed-in session", async () => {
    const audio = await mount();
    let resolve!: (value: unknown) => void;
    request.mockClear();
    request.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    await event(audio, "seeked", 30);
    await event(audio, "seeked", 40);
    vi.mocked(getToken).mockReturnValue("other-user");
    await act(async () => resolve({}));
    await settle();
    expect(writes()).toHaveLength(1);
  });

  it("ignores stale work responses during rapid route replacement", async () => {
    let resolve!: (value: unknown) => void;
    request.mockImplementation((path) => path === "/works/1" ? new Promise((done) => { resolve = done; }) : Promise.resolve(path === "/works/2" ? work(2) : {}));
    await act(async () => render(<WorkView id={1} />, root));
    await act(async () => render(<WorkView id={2} />, root));
    await settle();
    await act(async () => resolve(work(1)));
    await settle();
    expect(root.textContent).toContain("Book 2");
    expect(root.textContent).not.toContain("Book 1");
  });
});

describe("primary audio replay", () => {
  it("starts at zero after completing an edition with an older saved resume", async () => {
    request.mockImplementation(async (path, opts) => {
      if (opts?.method === "POST") return {};
      if (path.startsWith("/works/")) { const result = work(); return { ...result, editions: result.editions.map((edition) => ({ ...edition, position: 35 })) }; }
      return {};
    });
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await act(async () => [...root.querySelectorAll("button")].find((b) => b.textContent?.trim().startsWith("Resume"))!.click());
    await settle();
    const first = root.querySelector("audio")!;
    await act(async () => first.dispatchEvent(new Event("loadedmetadata")));
    await event(first, "ended", 100);
    await act(async () => [...root.querySelectorAll("button")].find((b) => b.textContent?.includes("Listen again"))!.click());
    await settle();
    const next = root.querySelector("audio")!;
    await act(async () => next.dispatchEvent(new Event("loadedmetadata")));
    expect(next.currentTime).toBe(0);
    expect(writes().at(-1)).toMatchObject({ position: 0, finished: false });
  });
});

describe("primary audio originating account", () => {
  it("does not create a new-user saver when music auto-advances after login changes", async () => {
    request.mockImplementation(async (path, opts) => opts?.method === "POST" ? {} : path.startsWith("/works/") ? { ...work(), editions: [1, 2].map((id) => ({ ...work(id).editions[0], format: "audio", title: `Track ${id}` })) } : {});
    await act(async () => render(<WorkView id={1} />, root)); await settle();
    await act(async () => [...root.querySelectorAll("button")].find((b) => b.textContent?.includes("Track 1"))!.click()); await settle();
    const audio = root.querySelector("audio")!;
    await act(async () => audio.dispatchEvent(new Event("loadedmetadata")));
    request.mockClear();
    vi.mocked(getToken).mockReturnValue("replacement-account");
    await event(audio, "ended", 100);
    expect(writes()).toEqual([]);
  });
  it("discards work data arriving after the originating login changes", async () => {
    let resolve!: (value: unknown) => void;
    request.mockImplementation((path) => path.startsWith("/works/") ? new Promise((done) => { resolve = done; }) : Promise.resolve({}));
    await act(async () => render(<WorkView id={1} />, root));
    vi.mocked(getToken).mockReturnValue("replacement-account");
    await act(async () => resolve(work())); await settle();
    expect(root.textContent).not.toContain("Book 1");
  });
});
