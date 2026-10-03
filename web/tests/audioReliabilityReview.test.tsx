import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WorkView } from "../src/views/work";
import { api, getToken } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: vi.fn(() => "review") };
});
vi.mock("../src/toast", () => ({ toast: vi.fn() }));
const request = vi.mocked(api);
let root: HTMLDivElement;
let music = false;
let alternateEdition = false;

function fixture() {
  const base = { id: 1, title: "Review Work", author: "Author", description: null, hasCover: false };
  if (music) return { ...base, editions: [1, 2].map((id) => ({ id, title: `Track ${id}`, format: "audio", duration: id === 1 ? 40 : 60, files: [{ id, seq: 1, duration: id === 1 ? 40 : 60, size: 20 }], chapters: [] })) };
  return { ...base, editions: [{ id: 1, title: "Book", format: "m4b", duration: 100, files: [{ id: 1, seq: 1, duration: 40, size: 20 }, { id: 2, seq: 2, duration: 60, size: 20 }], chapters: [] }, ...(alternateEdition ? [{ id: 2, title: "Alternate", format: "mp3", duration: 100, files: [{ id: 3, seq: 1, duration: 100, size: 20 }], chapters: [] }] : [])] };
}

async function settle() { await act(async () => { for (let i = 0; i < 25; i++) await Promise.resolve(); }); }
async function clickText(text: string) {
  const button = [...root.querySelectorAll("button")].find((b) => b.textContent?.includes(text));
  expect(button).toBeTruthy();
  await act(async () => button!.click());
  await settle();
}
async function metadata() {
  const audio = root.querySelector("audio")!;
  Object.defineProperty(audio, "readyState", { value: 4, configurable: true });
  await act(async () => audio.dispatchEvent(new Event("loadedmetadata")));
  await settle();
  return audio;
}
async function event(audio: HTMLAudioElement, type: string, position: number) {
  audio.currentTime = position;
  await act(async () => audio.dispatchEvent(new Event(type)));
  await settle();
}
function writes() {
  return request.mock.calls.filter(([, opts]) => opts?.method === "POST").map(([path, opts]) => ({ path, ...JSON.parse(String(opts!.body)) }));
}

beforeEach(() => {
  root = document.createElement("div");
  document.body.append(root);
  music = false;
  alternateEdition = false;
  vi.mocked(getToken).mockReturnValue(`review-${Math.random()}`);
  request.mockReset().mockImplementation(async (path, opts) => opts?.method === "POST" ? {} : path === "/works/1" ? fixture() : {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(async () => {
  await act(async () => render(null, root));
  vi.mocked(getToken).mockReturnValue("logged-out");
  await settle();
  root.remove();
  vi.restoreAllMocks();
});

describe("independent audio reliability review", () => {
  it("starts a fresh music progress generation when automatically revisiting a completed track", async () => {
    music = true;
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await clickText("Track 2");
    const audio = await metadata();
    await event(audio, "ended", 60);
    await clickText("Track 1");
    await metadata();
    await event(audio, "play", 0);
    await event(audio, "ended", 40);
    await metadata();
    request.mockClear();
    await event(audio, "seeked", 5);
    expect(writes()).toContainEqual({ path: "/progress/2", position: 5, duration: 60, finished: false });
  });

  it("ignores an ended event while a replacement primary file is still loading", async () => {
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await clickText("Listen");
    const audio = await metadata();
    await event(audio, "seeked", 20);
    await clickText("Part 2");
    request.mockClear();
    await act(async () => audio.dispatchEvent(new Event("ended")));
    await settle();
    expect(root.querySelector("audio")).toBe(audio);
    expect(writes().some((patch) => patch.finished)).toBe(false);
  });

  it("resumes a primary edition locally after switching to another edition and back", async () => {
    alternateEdition = true;
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await clickText("Listen");
    const audio = await metadata();
    await event(audio, "seeked", 25);
    await clickText("MP3");
    await clickText("M4B");
    const start = [...root.querySelectorAll("button")].find((b) => b.textContent?.includes("Listen") || b.textContent?.includes("Resume"));
    await act(async () => start!.click());
    await settle();
    const returned = await metadata();
    expect(returned.currentTime).toBe(25);
    expect(writes().at(-1)).toMatchObject({ path: "/progress/1", position: 25, finished: false });
  });

  it("keeps the requested primary chapter position newer than outgoing-file cleanup", async () => {
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await clickText("Listen");
    const audio = await metadata();
    await event(audio, "seeked", 20);
    request.mockClear();
    await clickText("Part 2");
    expect(audio.src).toContain("/stream/2");
    expect(writes().at(-1)).toMatchObject({ path: "/progress/1", position: 40, finished: false });
    await metadata();
    expect(writes().at(-1)).toMatchObject({ path: "/progress/1", position: 40, finished: false });
  });

  it("restarts a finished music track when seeking back through the player timeline", async () => {
    music = true;
    await act(async () => render(<WorkView id={1} />, root));
    await settle();
    await clickText("Track 1");
    const audio = await metadata();
    await event(audio, "ended", 40);
    await metadata();
    await event(audio, "seeked", 10);
    expect(writes()).toContainEqual({ path: "/progress/1", position: 40, duration: 40, finished: true });
    request.mockClear();
    const seek = root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!;
    seek.value = "20";
    await act(async () => seek.dispatchEvent(new Event("input", { bubbles: true })));
    await settle();
    await metadata();
    await event(audio, "seeked", 20);
    expect(writes()).toContainEqual({ path: "/progress/1", position: 20, duration: 40, finished: false });
  });
});
