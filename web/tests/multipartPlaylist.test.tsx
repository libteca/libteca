import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PlaylistsView } from "../src/views/playlists";
import { api, getToken } from "../src/api";
vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: vi.fn(() => "multipart") };
});
vi.mock("../src/toast", () => ({ toast: vi.fn() }));
const request = vi.mocked(api);
let root: HTMLDivElement;
let position = 0;
const playlist = { id: 1, name: "Books", owner: "Reader", songCount: 2, durationSecs: 200,
  items: [1, 2].map((id) => ({ editionId: id, position: id, title: `Book ${id}`, format: "m4b", durationSecs: 100, workId: id, workTitle: `Work ${id}` })) };
function work(id: number) { return { id, editions: [{ id, format: "m4b", duration: 100, position, files: [{ id: id * 10 + 1, seq: 1, duration: 40 }, { id: id * 10 + 2, seq: 2, duration: 60 }], chapters: [] }] }; }
async function settle() { await act(async () => { for (let i = 0; i < 25; i++) await Promise.resolve(); }); }
async function click(selector: string) { await act(async () => root.querySelector<HTMLButtonElement>(selector)!.click()); await settle(); }
async function mount() {
  await act(async () => render(<PlaylistsView id={1} />, root)); await settle();
  await act(async () => [...root.querySelectorAll("button")].find((b) => b.textContent?.includes("Play all"))!.click()); await settle();
  return metadata();
}
async function metadata() {
  const audio = root.querySelector("audio")!;
  Object.defineProperty(audio, "readyState", { value: 4 });
  await act(async () => audio.dispatchEvent(new Event("loadedmetadata"))); await settle();
  return audio;
}
function writes() { return request.mock.calls.filter(([, opts]) => opts?.method === "POST").map(([path, opts]) => ({ path, ...JSON.parse(String(opts!.body)) })); }
beforeEach(() => {
  root = document.createElement("div"); document.body.append(root); position = 0;
  vi.mocked(getToken).mockReturnValue(`multipart-${Math.random()}`);
  request.mockReset().mockImplementation(async (path, opts) => opts?.method === "POST" ? {} : path === "/playlists/1" ? playlist : path.startsWith("/works/") ? work(Number(path.split("/").pop())) : {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(async () => { await act(async () => render(null, root)); vi.mocked(getToken).mockReturnValue("logged-out"); root.remove(); vi.restoreAllMocks(); });

describe("multipart playlist edition timeline", () => {
  it("resumes in the second file at the file-relative offset", async () => {
    position = 65;
    const audio = await mount();
    expect(audio.src).toContain("/stream/12");
    expect(audio.currentTime).toBe(25);
    expect(root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!.value).toBe("65");
    expect(root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!.max).toBe("100");
  });
  it("seeks across file boundaries while saving cumulative progress", async () => {
    await mount();
    const input = root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!;
    input.value = "75";
    await act(async () => input.dispatchEvent(new Event("input", { bubbles: true }))); await settle();
    const second = await metadata();
    expect(second.src).toContain("/stream/12"); expect(second.currentTime).toBe(35);
    expect(writes().at(-1)).toMatchObject({ path: "/progress/1", position: 75, duration: 100, finished: false });
    input.value = "10";
    await act(async () => input.dispatchEvent(new Event("input", { bubbles: true }))); await settle();
    const first = await metadata();
    expect(first.src).toContain("/stream/11"); expect(first.currentTime).toBe(10);
    expect(writes().at(-1)).toMatchObject({ path: "/progress/1", position: 10, finished: false });
  });
  it("finishes only after the final file and then selects the next edition", async () => {
    const first = await mount(); first.currentTime = 40;
    await act(async () => first.dispatchEvent(new Event("ended"))); await settle();
    const second = await metadata();
    expect(second.src).toContain("/stream/12");
    expect(writes()).not.toContainEqual(expect.objectContaining({ finished: true }));
    second.currentTime = 60;
    await act(async () => second.dispatchEvent(new Event("ended"))); await settle();
    expect(root.querySelector("audio")!.src).toContain("/stream/21");
    expect(writes()).toContainEqual({ path: "/progress/1", position: 100, duration: 100, finished: true });
  });
  it("ignores detached prior-file events and keeps the local revisit offset", async () => {
    const first = await mount(); first.currentTime = 25;
    await act(async () => first.dispatchEvent(new Event("timeupdate"))); await settle();
    await click('button[aria-label="Next"]'); await metadata();
    request.mockClear();
    await act(async () => { first.dispatchEvent(new Event("ended")); first.dispatchEvent(new Event("timeupdate")); }); await settle();
    expect(writes()).toEqual([]);
    await click('button[aria-label="Previous"]');
    const revisited = await metadata();
    expect(revisited.src).toContain("/stream/11"); expect(revisited.currentTime).toBe(25);
  });
});
