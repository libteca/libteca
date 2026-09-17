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
  id: number; format: string; title: string; duration: number; position?: number; isFinished?: boolean;
  seasonNum?: number; episodeNum?: number; page?: number; percent?: number; pageCount?: number;
  files: { id: number; seq: number; duration: number; size: number; videoCodec?: string; codec?: string; width?: number; height?: number }[];
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

export function setToken(t: string) { token = t; }

export const api = async (path: string, opts: RequestInit = {}) => {
  const headers = new Headers(opts.headers);
  if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(`/api/core${path}`, { ...opts, headers });
  if (res.status === 401) {
    setToken("");
    try { localStorage.removeItem("libteca-token"); } catch { /* storage unavailable */ }
    location.reload();
    throw new Error("unauthorized");
  }
  const text = await res.text();
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
// Authorization headers. The server's auth middleware falls back to a
// `token` query parameter (see internal/auth/auth.go), so media URLs
// authenticate exactly the way covers are expected to.
export function media(path: string) {
  const p = path.startsWith("/api/core") ? path.slice("/api/core".length) : path;
  return `/api/core${p}${p.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`;
}

export function readStoredToken(): string {
  try { return localStorage.getItem("libteca-token") || ""; } catch { return ""; }
}

export function clearStoredToken(): void {
  setToken("");
  try { localStorage.removeItem("libteca-token"); } catch { /* storage unavailable */ }
}
