export function fmt(secs: number) {
  if (!isFinite(secs) || secs < 0) secs = 0;
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = Math.floor(secs % 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m ${s}s`;
}

export function fmtClock(secs: number) {
  if (!isFinite(secs) || secs < 0) secs = 0;
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = Math.floor(secs % 60);
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return h > 0 ? `${h}:${mm}:${String(s).padStart(2, "0")}` : `${mm}:${String(s).padStart(2, "0")}`;
}

export function fmtRel(ms: number | null | undefined) {
  if (!ms) return "";
  const d = Date.now() - ms;
  if (d < 0) return "just now";
  const min = Math.floor(d / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  const days = Math.floor(h / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(ms).toLocaleDateString();
}

export function debounce<A extends unknown[]>(fn: (...args: A) => void, ms: number) {
  let t: number | undefined;
  const wrapped = (...args: A) => {
    if (t !== undefined) clearTimeout(t);
    t = window.setTimeout(() => fn(...args), ms);
  };
  wrapped.cancel = () => { if (t !== undefined) clearTimeout(t); };
  return wrapped;
}

const TYPE_LABELS: Record<string, string> = {
  movies: "Movies", tv: "TV", music: "Music", audiobooks: "Audiobooks",
  books: "Books", comics: "Comics", podcasts: "Podcasts",
};

export function typeLabel(t: string) {
  return TYPE_LABELS[t] || t;
}

const FORMAT_LABELS: Record<string, string> = {
  m4b: "M4B", mp3: "MP3", audio: "AUDIO", video: "VIDEO",
  epub: "EPUB", cbz: "CBZ", pdf: "PDF",
};

export function formatLabel(f: string) {
  return FORMAT_LABELS[f] || f.toUpperCase();
}

export function editionState(e: { duration: number; position?: number; isFinished?: boolean }) {
  if (e.isFinished) return { label: "Finished", pct: 1 };
  if (e.position && e.position > 0) {
    const pct = e.duration > 0 ? Math.min(1, e.position / e.duration) : 0;
    return { label: `${fmt(e.duration - e.position)} left`, pct };
  }
  return { label: "Not started", pct: 0 };
}
