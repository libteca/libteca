export type Work = {
  id: number; title: string; author: string | null; subtitle: string | null;
  hasCover: boolean; editions: { id: number; format: string; duration: number; files: number }[];
};

export type WorkDetail = {
  id: number; libraryId?: number; libraryName?: string;
  title: string; author: string | null; description: string | null; hasCover: boolean;
  editions: EditionDetail[];
};

export type EditionDetail = {
  id: number; format: string; title: string; duration: number; position?: number; isFinished?: boolean;
  seasonNum?: number; episodeNum?: number;
  files: { id: number; seq: number; duration: number; size: number }[];
  chapters: { title: string; start: number; end: number; fileId: number }[];
};

export type Library = { id: number; name: string; type: string; path: string };

export type LibraryType = "movies" | "tv" | "music" | "audiobooks" | "books" | "comics";

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

export type ScanEvent = {
  jobId?: number; libraryId?: number; status: string; error?: string;
  filesSeen: number; filesProbed: number; filesAdded: number; filesUpdated: number; worksChanged: number;
  currentPath?: string; startedAt?: number; finishedAt?: number;
};

let token = "";

export function getToken() { return token; }

export function setToken(t: string) { token = t; }

export const api = async (path: string, opts: RequestInit = {}) => {
  const res = await fetch(`/api/core${path}`, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(opts.headers || {}) },
  });
  if (res.status === 401) { location.hash = "#/"; throw new Error("unauthorized"); }
  return res.json();
};

// Media elements (img/video/audio/track) and EventSource cannot send
// Authorization headers. The server's auth middleware falls back to a
// `token` query parameter (see internal/auth/auth.go), so media URLs
// authenticate exactly the way covers are expected to.
export function media(path: string) {
  return `/api/core${path}${path.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`;
}
