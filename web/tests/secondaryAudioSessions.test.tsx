import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PlaylistsView } from "../src/views/playlists";
import { PodcastsView } from "../src/views/podcasts";
import { api, getToken } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path, getToken: vi.fn(() => "") };
});
vi.mock("../src/opml", () => ({ OPMLImport: () => null }));
vi.mock("../src/toast", () => ({ toast: vi.fn() }));

const request = vi.mocked(api);
let root: HTMLDivElement;
let play: ReturnType<typeof vi.spyOn>;
let pause: ReturnType<typeof vi.spyOn>;
const playlist = {
  id: 1, name: "Favorites", owner: "Reader", songCount: 2, durationSecs: 200,
  items: [1, 2].map((id) => ({ editionId: id, position: id, title: `Track ${id}`, format: "audio", durationSecs: 100, workId: id, workTitle: `Album ${id}`, hasCover: false })),
};
const podcast = {
  id: 1, title: "Test Show", hasCover: false, autoDownload: false, maxEpisodes: 3, episodeCount: 2, downloadedCount: 2,
  episodes: [1, 2].map((id) => ({ id, podcastId: 1, title: `Episode ${id}`, durationSecs: 100, hasFile: true, streamUrl: `/podcasts/episodes/${id}/stream`, positionSecs: id * 10 })),
};

beforeEach(() => {
  root = document.createElement("div");
  document.body.append(root);
  request.mockReset();
  vi.mocked(getToken).mockReturnValue("");
  request.mockImplementation(async (path, opts) => {
    if (opts?.method === "POST") return {};
    if (path === "/playlists/1") return playlist;
    if (path.startsWith("/works/")) { const id = Number(path.split("/").pop()); return { id, editions: [{ id, duration: 100, files: [{ id, seq: 1, duration: 100 }], chapters: [] }] }; }
    if (path === "/podcasts") return [podcast];
    if (path === "/podcasts/1") return podcast;
    return {};
  });
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(async () => {
  await act(async () => render(null, root));
  root.remove();
  vi.restoreAllMocks();
});

async function click(selector: string) {
  await act(async () => root.querySelector<HTMLButtonElement>(selector)!.click());
  await settle();
}

async function settle() {
  await act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
}

async function mountPlaylist() {
  await act(async () => render(<PlaylistsView id={1} />, root));
  await settle();
  await act(async () => [...root.querySelectorAll("button")].find((button) => button.textContent?.includes("Play all"))!.click());
  await settle();
  await act(async () => root.querySelector("audio")!.dispatchEvent(new Event("loadedmetadata")));
  request.mockClear();
}

async function mountPodcast() {
  await act(async () => render(<PodcastsView />, root));
  await settle();
  await click("button.cover-card");
  await click('button[aria-label="Play Episode 1"]');
}

function saves() {
  return request.mock.calls.filter(([, opts]) => opts?.method === "POST").map(([path, opts]) => ({ path, ...JSON.parse(String(opts!.body)) }));
}

describe("playlist and podcast media sessions", () => {
  it("shows a podcast list HTTP failure instead of an empty library", async () => {
    request.mockResolvedValue({ error: "unavailable", status: 503 });
    await act(async () => render(<PodcastsView />, root));
    await settle();
    expect(root.textContent).toContain("Couldn't load podcasts");
    expect(root.textContent).not.toContain("No subscriptions yet");
  });

  it("shows a podcast detail HTTP failure without rendering a malformed show", async () => {
    await act(async () => render(<PodcastsView />, root));
    await settle();
    request.mockResolvedValue({ error: "unavailable", status: 503 });
    await click("button.cover-card");
    expect(root.textContent).toContain("Couldn't load this podcast");
  });

  it("distinguishes a playlist server failure from a missing playlist", async () => {
    request.mockResolvedValue({ error: "unavailable", status: 503 });
    await act(async () => render(<PlaylistsView id={1} />, root));
    await settle();
    expect(root.textContent).toContain("Couldn't reach the server");
    expect(root.textContent).not.toContain("Playlist not found");
  });

  it("starts playing when Play all is selected", async () => {
    await mountPlaylist();
    expect(play.mock.instances).toContain(root.querySelector("audio"));
  });

  it("saves the outgoing playlist item and isolates its detached events", async () => {
    await mountPlaylist();
    const old = root.querySelector("audio")!;
    old.currentTime = 35;
    await click('button[aria-label="Next"]');
    const current = root.querySelector("audio")!;
    expect(current).not.toBe(old);
    expect(current.src).toContain("/stream/2");
    expect(pause.mock.instances).toContain(old);
    expect(saves()).toContainEqual(expect.objectContaining({ path: "/progress/1", position: 35 }));
    request.mockClear();
    await act(async () => { old.dispatchEvent(new Event("timeupdate")); old.dispatchEvent(new Event("pause")); old.dispatchEvent(new Event("ended")); });
    expect(saves()).toEqual([]);
    expect(root.querySelector("audio")).toBe(current);
  });

  it("saves the outgoing podcast and ignores its detached completion", async () => {
    await mountPodcast();
    const old = root.querySelector("audio")!;
    old.currentTime = 35;
    await click('button[aria-label="Play Episode 2"]');
    const current = root.querySelector("audio")!;
    expect(current).not.toBe(old);
    expect(current.src).toContain("/podcasts/episodes/2/stream");
    expect(pause.mock.instances).toContain(old);
    expect(saves()).toContainEqual(expect.objectContaining({ path: "/podcasts/episodes/1/progress", position: 35 }));
    request.mockClear();
    await act(async () => { old.dispatchEvent(new Event("ended")); old.dispatchEvent(new Event("timeupdate")); });
    expect(saves()).toEqual([]);
  });

  it("restores the selected podcast's own resume position", async () => {
    await mountPodcast();
    const first = root.querySelector("audio")!;
    await act(async () => first.dispatchEvent(new Event("loadedmetadata")));
    expect(first.currentTime).toBe(10);
    await click('button[aria-label="Play Episode 2"]');
    const second = root.querySelector("audio")!;
    await act(async () => second.dispatchEvent(new Event("loadedmetadata")));
    expect(second.currentTime).toBe(20);
    expect(root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!.value).toBe("20");
  });

  it("serializes completion after a delayed playlist position save", async () => {
    await mountPlaylist();
    let resolve!: (value: unknown) => void;
    request.mockImplementation((path, opts) => opts?.method === "POST" && path === "/progress/1" ? new Promise((done) => { resolve = done; }) : Promise.resolve({}));
    const old = root.querySelector("audio")!;
    old.currentTime = 30;
    await act(async () => old.dispatchEvent(new Event("timeupdate")));
    old.currentTime = 100;
    await act(async () => old.dispatchEvent(new Event("ended")));
    expect(saves()).toHaveLength(1);
    await act(async () => resolve({}));
    await settle();
    expect(saves()).toHaveLength(2);
    expect(saves()[1]).toMatchObject({ path: "/progress/1", position: 100, finished: true });
    await act(async () => resolve({}));
  });

  it("retries a rejected completion after teardown without changing it to unfinished", async () => {
    vi.useFakeTimers();
    await mountPlaylist();
    request.mockImplementation(async () => ({ error: "try again", status: 503 }));
    const old = root.querySelector("audio")!;
    old.currentTime = 100;
    await act(async () => old.dispatchEvent(new Event("ended")));
    await settle();
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    const first = saves().filter((save) => save.path === "/progress/1");
    vi.mocked(getToken).mockReturnValue("retry-test-ended");
    await act(async () => vi.advanceTimersByTimeAsync(60000));
    vi.useRealTimers();
    expect(first.length).toBeGreaterThanOrEqual(2);
    expect(first.every((save) => save.finished === true)).toBe(true);
  });

  it("does not deliver queued media work after the session changes", async () => {
    await mountPlaylist();
    let resolve!: (value: unknown) => void;
    request.mockImplementation(() => new Promise((done) => { resolve = done; }));
    const old = root.querySelector("audio")!;
    old.currentTime = 30;
    await act(async () => old.dispatchEvent(new Event("timeupdate")));
    old.currentTime = 100;
    await act(async () => old.dispatchEvent(new Event("ended")));
    vi.mocked(getToken).mockReturnValue("replacement-fixture");
    await act(async () => resolve({}));
    await settle();
    expect(saves()).toHaveLength(1);
  });
});
