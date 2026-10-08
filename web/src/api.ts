import { confirmMediaOwner, getMediaIdentity, mediaIdentityReady, mediaProtocolActive, setMediaToken } from "./mediaProgressIdentity";
export { getMediaIdentity } from "./mediaProgressIdentity";
import { mediaProgress, isMediaProgressPath } from "./mediaProgress";
export type Work = {
  id: number; title: string; author: string | null; subtitle: string | null;
  hasCover: boolean; editions: { id: number; format: string; duration: number; files: number }[];
  percent?: number;
};

export type WorkDetail = {
  id: number; libraryId?: number; libraryName?: string;
  title: string; subtitle?: string | null; author: string | null; description: string | null; hasCover: boolean; hasFanart?: boolean;
  genres?: string[];
  editions: EditionDetail[];
};

export type EditionDetail = {
  id: number; format: string; title: string; duration: number; generation?: string; revision?: number; resetGeneration?: number; deleted?: boolean; position?: number; isFinished?: boolean;
  available?: boolean; unavailableReason?: string; durationKnown?: boolean; resumeConflict?: boolean; resumeFileId?: number; resumeFileOffset?: number; progressGeneration?: string;
  seasonNum?: number; episodeNum?: number; page?: number; percent?: number; pageCount?: number;
  files: { id: number; seq: number; duration: number; size: number; videoCodec?: string; codec?: string; width?: number; height?: number; sha256?: string }[];
  chapters: { title: string; start: number; end: number; fileId: number }[];
};

export type Library = { id: number; name: string; type: LibraryType; path?: string };

export type LibraryType =
  | "movies" | "tv" | "music" | "audiobooks"
  | "books" | "comics" | "podcasts" | "games";

export type ResumeItem = {
  workId: number; editionId: number; libraryId: number; libraryType: string;
  title: string; author: string | null; hasCover: boolean;
  positionSecs: number; durationSecs: number; percent: number; updatedAt: number;
};

export type SearchItem = {
  workId: number; libraryId: number; libraryType: string;
  title: string; author: string | null; hasCover: boolean; percent: number;
};

export type RecentItem = {
  workId: number; libraryId: number; libraryType: string;
  title: string; author: string | null; hasCover: boolean; addedAt: number;
};

export type NextUpItem = {
  workId: number; editionId: number; title: string; episodeTitle: string;
  seasonNum: number; episodeNum: number; hasCover: boolean;
};

export type ScanEvent = {
  jobId?: number; libraryId?: number; status: string; error?: string;
  filesSeen: number; filesProbed: number; filesAdded: number; filesUpdated: number; worksChanged: number;
  currentPath?: string; startedAt?: number; finishedAt?: number;
};

export type PlaybackInfo = { mode: "direct" | "hls"; fileId: number; sessionId?: string };

let token = "";

export function getToken() { return token; }

export function setToken(t: string) { token = t; setMediaToken(t); }

export const apiTransport = async (path: string, opts: RequestInit = {}) => {
  const headers = new Headers(opts.headers);
  if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  // Capture the token used for THIS request: a slow response from before a
  // re-login must not clear the newer credential on a 401.
  const usedToken = token;
  if (usedToken && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${usedToken}`);
  const res = await fetch(`/api/core${path}`, { ...opts, headers });
  if (res.status === 401) {
    if (usedToken === token) {
      setToken("");
      try { localStorage.removeItem("libteca-token"); } catch { /* storage unavailable */ }
      location.reload();
    }
    throw new Error("unauthorized");
  }
  const text = await res.text();
  const value = normalizeAPIResponse(res, text);
  if (path === "/me" && res.ok && usedToken === token && Number.isSafeInteger(value?.id)) {
    confirmMediaOwner(value.id);
    void mediaProgress.replay();
  }
  if (res.ok && usedToken === token && /^\/works\/\d+$/.test(path) && Array.isArray(value?.editions)) {
    for (const edition of value.editions) mediaProgress.rememberEdition(edition);
  }
  return value;
};

export const api = async (path: string, opts: RequestInit = {}) => {
  if (opts.method === "POST" && isMediaProgressPath(path) && typeof opts.body === "string" && mediaProtocolActive()) {
    await mediaIdentityReady();
    const patch = JSON.parse(opts.body);
    if (typeof patch.position === "number" && patch.page === undefined && patch.percent === undefined && patch.locator === undefined) {
      if (!getMediaIdentity().ownerId) throw new Error("Progress requires a verified login. Reconnect before saving.");
      return mediaProgress.enqueue(path, patch);
    }
  }
  return apiTransport(path, opts);
};

export class RequestTimeoutError extends Error {}

export async function apiWithDeadline(path: string, opts: RequestInit = {}, timeoutMs = 15000) {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let relay: (() => void) | undefined;
  const canceled = new Promise<never>((_, reject) => {
    relay = () => {
      controller.abort(opts.signal?.reason);
      reject(opts.signal?.reason ?? new DOMException("Request aborted", "AbortError"));
    };
    if (opts.signal?.aborted) relay();
    else opts.signal?.addEventListener("abort", relay, { once: true });
    timer = setTimeout(() => {
      controller.abort();
      reject(new RequestTimeoutError("Request timed out"));
    }, timeoutMs);
  });
  try {
    return await Promise.race([api(path, { ...opts, signal: controller.signal }), canceled]);
  } finally {
    clearTimeout(timer);
    if (relay) opts.signal?.removeEventListener("abort", relay);
  }
}

// fetch with a hard deadline covering headers and body consumption. A
// request that never resolves must not wedge the single-flight progress
// queue forever; parent signals (reader teardown) are honored alongside the
// timeout.
export async function fetchWithDeadline(
  input: string,
  init: RequestInit = {},
  timeoutMs = 15000,
): Promise<{ response: Response; text: string }> {
  const controller = new AbortController();
  const parent = init.signal;
  let timedOut = false;
  const relay = () => controller.abort(parent?.reason);
  if (parent?.aborted) relay();
  else parent?.addEventListener("abort", relay, { once: true });
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  try {
    const response = await fetch(input, { ...init, signal: controller.signal });
    const text = await response.text();
    return { response, text };
  } catch (error) {
    if (timedOut) throw new RequestTimeoutError("request timed out");
    throw error;
  } finally {
    clearTimeout(timer);
    parent?.removeEventListener("abort", relay);
  }
}

export function normalizeAPIResponse(res: Response, text: string): any {
  let payload: any = {};
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      return {
        error: res.ok ? "Invalid JSON response from server" : `Server error (${res.status})`,
        status: res.status,
      };
    }
  }
  if (!res.ok) {
    const problem =
      payload !== null && typeof payload === "object" && !Array.isArray(payload)
        ? (payload as Record<string, unknown>)
        : {};
    const message = [problem.error, problem.detail, problem.title].find(
      (value): value is string => typeof value === "string" && value.length > 0
    );
    return { ...problem, error: message || `Server error (${res.status})`, status: res.status };
  }
  return payload;
};

export class APIError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

// api() resolves error-shaped objects for callers that inspect them; a
// mutation whose completion means success must go through apiChecked, which
// turns those shapes into real rejections. Without this a failed save was
// indistinguishable from a successful one.
export const apiChecked = async <T = unknown>(path: string, opts: RequestInit = {}): Promise<T> => {
  const value = await api(path, opts);
  if (value !== null && typeof value === "object" && typeof (value as Record<string, unknown>).error === "string") {
    const rec = value as Record<string, unknown>;
    throw new APIError(rec.error as string, typeof rec.status === "number" ? rec.status : 0);
  }
  return value as T;
};

// Media elements (img/video/audio/track) and EventSource cannot send
// Authorization headers. The server's auth middleware accepts the HttpOnly
// libteca-media cookie on read-only media routes only (set at login and by
// /me), so media URLs carry no credential at all (AUD-01). Mutations
// (progress posts, the HLS stop) always go through api()/fetch with the
// bearer header.
export function media(path: string) {
  const p = path.startsWith("/api/core") ? path.slice("/api/core".length) : path;
  return `/api/core${p}`;
}

export function readStoredToken(): string {
  try { return localStorage.getItem("libteca-token") || ""; } catch { return ""; }
}

export function clearStoredToken(): void {
  setToken("");
  try { localStorage.removeItem("libteca-token"); } catch { /* storage unavailable */ }
}
