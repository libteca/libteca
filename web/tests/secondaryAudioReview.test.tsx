import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PlaylistsView } from "../src/views/playlists";
import { PodcastsView } from "../src/views/podcasts";
import { SessionAudio } from "../src/players/sessionAudio";
import { api } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: () => "" };
});
vi.mock("../src/opml", () => ({ OPMLImport: () => null }));
vi.mock("../src/toast", () => ({ toast: vi.fn() }));

const request = vi.mocked(api);
let root: HTMLDivElement;
const podcast = {
  id: 1, title: "Review Show", hasCover: false, autoDownload: false, maxEpisodes: 3, episodeCount: 2, downloadedCount: 2,
  episodes: [1, 2].map((id) => ({ id, podcastId: 1, title: `Episode ${id}`, durationSecs: 100, hasFile: true, streamUrl: `/podcasts/episodes/${id}/stream`, positionSecs: id * 10 })),
};
function deferred() {
  let resolve!: (value: unknown) => void;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}
function playlist(id: number, name: string) { return { id, name, owner: "Reader", songCount: 1, durationSecs: 100, items: [{ editionId: id, title: `Track ${id}`, format: "audio", durationSecs: 100, workId: id, workTitle: `Album ${id}`, hasCover: false }] }; }
async function settle() { await act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); }); }
async function click(selector: string) { await act(async () => root.querySelector<HTMLButtonElement>(selector)!.click()); await settle(); }
function saves() { return request.mock.calls.filter(([, opts]) => opts?.method === "POST").map(([path, opts]) => ({ path, ...JSON.parse(String(opts!.body)) })); }

beforeEach(() => {
  root = document.createElement("div"); document.body.append(root);
  request.mockReset().mockImplementation(async (path, opts) => {
    if (opts?.method === "POST") return {};
    if (path === "/podcasts") return [podcast];
    if (path === "/podcasts/1") return podcast;
    return {};
  });
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(async () => { await act(async () => render(null, root)); root.remove(); vi.restoreAllMocks(); });

describe("independent secondary audio lifecycle review", () => {
  it("resumes a revisited podcast from its latest local playback position", async () => {
    await act(async () => render(<PodcastsView />, root)); await settle();
    await click("button.cover-card");
    await click('button[aria-label="Play Episode 1"]');
    const first = root.querySelector("audio")!;
    await act(async () => first.dispatchEvent(new Event("loadedmetadata")));
    first.currentTime = 35;
    await act(async () => first.dispatchEvent(new Event("timeupdate")));
    await click('button[aria-label="Play Episode 2"]');
    await click('button[aria-label="Play Episode 1"]');
    const returned = root.querySelector("audio")!;
    await act(async () => returned.dispatchEvent(new Event("loadedmetadata")));
    expect(saves()).toContainEqual(expect.objectContaining({ path: "/podcasts/episodes/1/progress", position: 35 }));
    expect(returned.currentTime).toBe(35);
  });

  it("retains a pending podcast resume when switched away before metadata arrives", async () => {
    await act(async () => render(<PodcastsView />, root)); await settle();
    await click("button.cover-card");
    await click('button[aria-label="Play Episode 1"]');
    await click('button[aria-label="Play Episode 2"]');
    await click('button[aria-label="Play Episode 1"]');
    const returned = root.querySelector("audio")!;
    await act(async () => returned.dispatchEvent(new Event("loadedmetadata")));
    expect(returned.currentTime).toBe(10);
  });

  it("serializes writes when a new session reopens the same progress endpoint", async () => {
    const firstWrite = deferred();
    request.mockImplementation((_path, opts) => opts?.method === "POST" && saves().length === 1 ? firstWrite.promise : Promise.resolve({}));
    const props = { src: "/one.mp3", progressPath: "/progress/1", duration: 100, audioRef: { current: null as HTMLAudioElement | null }, onPlaying: vi.fn(), onTime: vi.fn() };
    await act(async () => render(<SessionAudio key="first" {...props} />, root));
    const first = root.querySelector("audio")!; first.currentTime = 35;
    await act(async () => first.dispatchEvent(new Event("pause"))); await settle();
    await act(async () => render(<SessionAudio key="second" {...props} />, root));
    const second = root.querySelector("audio")!; second.currentTime = 70;
    await act(async () => second.dispatchEvent(new Event("pause"))); await settle();
    const beforeAcknowledgement = saves().length;
    await act(async () => firstWrite.resolve({})); await settle();
    expect(beforeAcknowledgement).toBe(1);
    expect(saves().at(-1)).toMatchObject({ path: "/progress/1", position: 70 });
  });

  it("ignores late old playlist detail results after route navigation", async () => {
    const old = deferred(), current = deferred();
    request.mockImplementation((path) => path === "/playlists/1" ? old.promise : path === "/playlists/2" ? current.promise : Promise.resolve({}));
    await act(async () => render(<PlaylistsView id={1} />, root));
    await act(async () => render(<PlaylistsView id={2} />, root));
    await act(async () => current.resolve(playlist(2, "Current list"))); await settle();
    expect(root.textContent).toContain("Current list");
    await act(async () => old.resolve(playlist(1, "Previous list"))); await settle();
    expect(root.textContent).toContain("Current list");
    expect(root.textContent).not.toContain("Previous list");
  });
});
