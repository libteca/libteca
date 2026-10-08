import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { VideoPlayer } from "../src/players/video";
import { apiChecked, setToken, type WorkDetail } from "../src/api";
vi.mock("../src/api", async original => ({ ...await original<object>(), apiChecked: vi.fn(), api: vi.fn(async () => ({})) }));
const checked = vi.mocked(apiChecked);
let root: HTMLDivElement, revision = 0;
const work = (firstDuration = 10): WorkDetail => ({ id: 1, title: "Multipart", author: null, description: null, hasCover: false,
  editions: [{ id: 7, title: "Movie", format: "video", duration: firstDuration + 20, generation: "7:1", position: 0,
    files: [{ id: 3, seq: 1, duration: firstDuration, size: 100, videoCodec: "h264" }, { id: 4, seq: 2, duration: 20, size: 100, videoCodec: "h264" }], chapters: [{ title: "Part two", start: 10, end: 30, fileId: 4 }] }] });
async function flush() { await act(async () => { for (let i = 0; i < 30; i++) await Promise.resolve(); }); }
async function mount(w = work(), onClose = vi.fn()) { await act(async () => { render(<VideoPlayer w={w} editionId={7} onClose={onClose} onSelectEdition={vi.fn()} />, root); }); await flush(); return onClose; }
function video(duration: number) {
  const v = root.querySelector("video")!;
  Object.defineProperty(v, "duration", { configurable: true, value: duration });
  Object.defineProperty(v, "seekable", { configurable: true, value: { length: 1, start: () => 0, end: () => duration } });
  return v;
}
async function event(v: HTMLVideoElement, name: string) { await act(async () => { v.dispatchEvent(new Event(name)); }); await flush(); }
async function seek(position: number) { const slider = root.querySelector<HTMLInputElement>('.vscrub input')!; await act(async () => { slider.value = String(position); slider.dispatchEvent(new Event("input", { bubbles: true })); }); await flush(); }
const sessions = () => checked.mock.calls.filter(([path]) => path.endsWith("/playback-sessions")).map(([, opts]) => JSON.parse(String(opts!.body)));
const saves = () => checked.mock.calls.filter(([path, opts]) => path === "/progress/7" && opts?.method === "POST").map(([, opts]) => JSON.parse(String(opts!.body)));
beforeEach(() => {
  setToken(""); root = document.createElement("div"); document.body.append(root); revision = 0; checked.mockReset();
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(); vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })));
  checked.mockImplementation(async (path, opts) => {
    if (path.endsWith("/playback-sessions")) { const body = JSON.parse(String(opts!.body)); return { mode: "direct", fileId: body.fileId }; }
    if (path === "/progress/7" && opts?.method === "POST") return { revision: ++revision, resetGeneration: 0 };
    if (path === "/progress/7") return { revision, resetGeneration: 0 };
    return {};
  });
});
afterEach(async () => { await act(async () => { render(null, root); }); root.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
describe("multipart video DOM lifecycle", () => {
  it("selects a cumulative seek in the second file and persists its exact cumulative position", async () => {
    await mount(); const first = video(10); await event(first, "loadedmetadata"); await seek(15);
    expect(sessions().map(s => [s.fileId, s.fileOffsetSecs])).toEqual([[3, 0], [4, 5]]);
    const second = video(20); await event(second, "loadedmetadata"); expect(second.currentTime).toBe(5);
    second.currentTime = 8; await event(second, "timeupdate"); await event(second, "pause"); expect(saves().at(-1).position).toBe(18);
    expect(saves().every(p => p.expectedGeneration === "7:1")).toBe(true);
  });
  it("shares the saver across part completion and marks only the final file finished", async () => {
    const close = await mount(); const first = video(10); await event(first, "loadedmetadata"); first.currentTime = 10; await event(first, "ended");
    expect(close).not.toHaveBeenCalled(); expect(saves().map(p => [p.position, p.finished])).toEqual([[10, false]]);
    const second = video(20); await event(second, "loadedmetadata"); second.currentTime = 20; await event(second, "ended");
    expect(saves().map(p => [p.position, p.finished])).toEqual([[10, false], [30, true]]); expect(close).toHaveBeenCalledTimes(1);
    await event(first, "pause"); await event(first, "ended"); await event(second, "pause"); expect(saves()).toHaveLength(2);
  });
  it("allows repeated seeks back to the same cross-file offset", async () => {
    await mount(); await event(video(10), "loadedmetadata"); await seek(15); await event(video(20), "loadedmetadata");
    await seek(2); await event(video(10), "loadedmetadata"); await seek(15); await event(video(20), "loadedmetadata");
    expect(sessions().map(s => [s.fileId, s.fileOffsetSecs])).toEqual([[3, 0], [4, 5], [3, 2], [4, 5]]);
  });
  it("refuses cumulative seeks across an unknown duration with a visible error", async () => {
    await mount(work(0)); await event(video(11), "loadedmetadata"); await seek(15);
    expect(sessions()).toHaveLength(1); expect(root.querySelector('[role="alert"]')?.textContent).toContain("known duration");
    expect(saves().some(p => p.finished)).toBe(false);
  });
  it("retries direct playback as selected-file HLS from the last observed offset", async () => {
    await mount(); const v = video(10); await event(v, "loadedmetadata"); v.currentTime = 6; await event(v, "timeupdate"); await event(v, "error");
    expect(sessions().map(s => [s.fileId, s.fileOffsetSecs, s.mode])).toEqual([[3, 0, "auto"], [3, 6, "hls"]]);
  });
  it("disposes of an HLS ticket returned after the component closes", async () => {
    let release!: (v: any) => void; checked.mockImplementationOnce(() => new Promise(resolve => { release = resolve; }));
    await mount(); await act(async () => { render(null, root); }); release({ mode: "hls", fileId: 3, sessionId: "late-ticket" }); await flush();
    expect(checked).toHaveBeenCalledWith("/hls/late-ticket", { method: "DELETE" });
  });
});

it("resumes the saved canonical part after reorder on reload", async () => {
 const w=work(); const e=w.editions[0]; e.files.reverse(); e.generation="7:2"; e.progressGeneration="7:1"; e.position=15; e.resumeFileId=4; e.resumeFileOffset=5;
 await mount(w); expect(sessions().map(s=>[s.fileId,s.fileOffsetSecs])).toEqual([[4,5]]);
 await event(video(20),"loadedmetadata"); expect(video(20).currentTime).toBe(5);
});
it("requires an explicit start when saved content changed", async () => {
 const w=work(); w.editions[0].resumeConflict=true; w.editions[0].position=0;
 await mount(w); expect(sessions()).toHaveLength(0); expect(root.querySelector('[role="alert"]')?.textContent).toContain("changed media");
 const button=Array.from(root.querySelectorAll("button")).find(b=>b.textContent==="Start from beginning")!;
 await act(async()=>button.click()); await flush(); expect(sessions().map(s=>[s.fileId,s.fileOffsetSecs])).toEqual([[3,0]]); expect(saves()[0].position).toBe(0);
});
