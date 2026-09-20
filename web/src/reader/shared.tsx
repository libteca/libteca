import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren, CSSProperties } from "preact";
import { APIError, fetchWithDeadline, getToken, media, normalizeAPIResponse, RequestTimeoutError } from "../api";
import {
  mergeServerProgress, parseStoredProgress, ProgressQueue,
  type ProgressPatch as QueuePatch, type ServerProgress,
} from "../progressQueue";
import { currentUser } from "../user";
import { c, font, iconBtn } from "../styles";

export type ReaderMode = "single" | "double" | "webtoon";
export type FitMode = "width" | "height";

export type ReadingProgress = {
  position?: number; duration?: number; isFinished?: boolean;
  page?: number; percent?: number; locator?: string; revision?: number;
};

export type ProgressPost = { page?: number; percent?: number; locator?: string; finished?: boolean };

export type SaveState = "idle" | "saving" | "saved" | "error";

const SAVE_INTERVAL_MS = 5000;

export function clamp01(n: number): number {
  return n < 0 ? 0 : n > 1 ? 1 : n;
}

const PAGE_IMAGE_RE = /\.(jpe?g|png|gif|webp|avif|bmp)$/i;

// Byte-based natural comparison identical to the server's natural.Less
// (digit runs compare numerically, everything else bytewise). The previous
// locale-aware Intl.Collator ordered pages differently from Go for mixed
// case, so page numbers and saved progress could identify different images
// depending on the client. The extension set matches the server exactly.
const pageEncoder = new TextEncoder();

export function comparePages(left: string, right: string): number {
  const a = pageEncoder.encode(left);
  const b = pageEncoder.encode(right);
  const digit = (n: number) => n >= 48 && n <= 57;
  let i = 0, j = 0;
  while (i < a.length && j < b.length) {
    if (digit(a[i]) && digit(b[j])) {
      let ei = i, ej = j;
      while (ei < a.length && digit(a[ei])) ei++;
      while (ej < b.length && digit(b[ej])) ej++;
      let si = i, sj = j;
      while (si < ei && a[si] === 48) si++;
      while (sj < ej && b[sj] === 48) sj++;
      if (ei - si !== ej - sj) return (ei - si) - (ej - sj);
      for (let k = 0; k < ei - si; k++) {
        if (a[si + k] !== b[sj + k]) return a[si + k] - b[sj + k];
      }
      i = ei;
      j = ej;
    } else {
      if (a[i] !== b[j]) return a[i] - b[j];
      i++;
      j++;
    }
  }
  if (a.length - i !== b.length - j) return (a.length - i) - (b.length - j);
  for (let k = 0; k < Math.min(a.length, b.length); k++) {
    if (a[k] !== b[k]) return a[k] - b[k];
  }
  return a.length - b.length;
}

export function pageImageNames(names: readonly string[]): string[] {
  const keep = names.filter((n) => {
    const slash = n.lastIndexOf("/");
    const base = slash >= 0 ? n.slice(slash + 1) : n;
    if (!PAGE_IMAGE_RE.test(base)) return false;
    if (base.startsWith(".") || base.startsWith("._")) return false;
    return !n.toUpperCase().includes("__MACOSX");
  });
  return keep.sort(comparePages);
}

export function pagePercent(page: number, count: number): number {
  if (count <= 0) return 0;
  return clamp01(page / count);
}

export function pairStart(index: number): number {
  return index - (index % 2);
}

export function stepPage(i: number, dir: 1 | -1, mode: ReaderMode, count: number): number | null {
  if (count <= 0 || i < 0 || i >= count) return null;
  const base = mode === "double" ? pairStart(i) : i;
  const stride = mode === "double" ? 2 : 1;
  const next = base + dir * stride;
  if (next < 0 || next >= count) return null;
  return next;
}

export function loadPref<T extends string>(key: string, fallback: T, valid: readonly T[]): T {
  try {
    const v = localStorage.getItem(key);
    return valid.includes(v as T) ? (v as T) : fallback;
  } catch {
    return fallback;
  }
}

export function savePref(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch { /* storage unavailable */ }
}

class AccountPausedError extends Error {
  constructor() { super("reader account changed; delivery stopped"); }
}

function progressRetryable(error: unknown): boolean {
  if (error instanceof AccountPausedError) return false;
  if (error instanceof RequestTimeoutError) return true;
  if (error instanceof APIError) {
    return error.status === 408 || error.status === 409 || error.status === 429 || error.status >= 500;
  }
  return true;
}

// One serial, merge-preserving progress queue per (user, edition). The
// durable record carries the base revision the patch was based on, so an
// offline patch replayed after another device advanced the server still
// presents its ORIGINAL base - the conditional write rejects it and the
// rebase/merge machinery runs instead of the server silently accepting a
// relabeled stale patch. Storage is scoped to the authenticated user id
// from /me (never a bearer token, never unscoped): an undelivered patch
// left by account A cannot replay into account B on a shared browser. Each
// queue instance owns a private storage record (per-tab key), absorbing
// and re-publishing any foreign pending records on mount, so two tabs
// acknowledge only their own operations and cannot erase each other's
// undelivered work. The sender folds every automatic save through a
// remote high-water mark (seeded from the loaded server progress and fed
// by every 409) so the conflict winner's position survives until the
// reader genuinely passes it. Lifecycle beacons (pagehide/visibility) send
// the SAME conditional operation (patch + base revision); a rejected
// beacon is not a loss - the patch stays durably stored and replays on
// the next open.
export function useProgressSaver(editionId: number, baseRevision?: number, initialRemote?: ServerProgress) {
  const [state, setState] = useState<SaveState>("idle");
  const timer = useRef<number | undefined>(undefined);
  const lastSent = useRef(0);
  const userId = currentUser();
  const queueRef = useRef<{ edition: number; user: number; queue: ProgressQueue } | null>(null);
  if (userId == null) {
    queueRef.current?.queue.stop();
    queueRef.current = null;
  } else if (queueRef.current === null || queueRef.current.edition !== editionId || queueRef.current.user !== userId) {
    queueRef.current?.queue.stop();
    const scopeUser = userId;
    const prefix = `libteca-progress-v2-u${scopeUser}-e${editionId}-`;
    const ownKey = `${prefix}${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
    const queueStorage = {
      load: (): { baseRevision: number; patch: QueuePatch } | null => {
        try { localStorage.removeItem(`libteca-progress-queue-${editionId}`); } catch { /* storage unavailable */ }
        const foreignKeys: string[] = [];
        const records: { baseRevision: number; patch: QueuePatch }[] = [];
        try {
          for (let i = 0; i < localStorage.length; i++) {
            const key = localStorage.key(i);
            if (!key || !key.startsWith(prefix) || key === ownKey) continue;
            const raw = localStorage.getItem(key);
            if (raw === null) continue;
            try {
              records.push(parseStoredProgress(JSON.parse(raw)));
              foreignKeys.push(key);
            } catch {
              try { localStorage.removeItem(key); } catch { /* storage unavailable */ }
            }
          }
        } catch { return null; }
        if (foreignKeys.length === 0) return null;
        let merged: QueuePatch = {};
        let base = Number.MAX_SAFE_INTEGER;
        let finished: boolean | undefined;
        for (const record of records) {
          merged = mergeServerProgress(merged as ServerProgress, record.patch);
          if (record.patch.finished !== undefined) finished = record.patch.finished;
          base = Math.min(base, record.baseRevision);
        }
        if (finished !== undefined && merged.finished === undefined) merged.finished = finished;
        try {
          if (Object.keys(merged).length > 0) {
            localStorage.setItem(ownKey, JSON.stringify({ baseRevision: base, patch: merged }));
          }
          for (const key of foreignKeys) localStorage.removeItem(key);
        } catch { /* storage unavailable */ }
        return Object.keys(merged).length > 0 ? { baseRevision: base, patch: merged } : null;
      },
      save: (patch: QueuePatch, base: number): void => {
        try {
          if (Object.keys(patch).length === 0) localStorage.removeItem(ownKey);
          else localStorage.setItem(ownKey, JSON.stringify({ baseRevision: base, patch }));
        } catch { /* storage unavailable */ }
      },
    };
    const remote: ServerProgress = { ...(initialRemote ?? {}) };
    const send = async (patch: QueuePatch, base: number): Promise<number | void> => {
      if (currentUser() !== scopeUser) throw new AccountPausedError();
      setState("saving");
      let current = mergeServerProgress(remote, patch);
      let revision = base;
      if (Object.keys(current).length === 0) {
        setState("saved");
        window.setTimeout(() => setState((s) => (s === "saved" ? "idle" : s)), 1600);
        return;
      }
      for (let attempt = 0; ; attempt++) {
        const headers = new Headers({ "Content-Type": "application/json" });
        const usedToken = getToken();
        if (usedToken) headers.set("Authorization", `Bearer ${usedToken}`);
        const { response, text } = await fetchWithDeadline(
          `/api/core/progress/${editionId}`,
          { method: "POST", headers, body: JSON.stringify({ ...current, revision }) },
          15000,
        );
        const res = normalizeAPIResponse(response, text);
        if (typeof res.error === "string") {
          const cur = (res as Record<string, unknown>).current as Record<string, unknown> | undefined;
          if (response.status === 409 && cur !== null && typeof cur === "object" &&
              typeof cur.revision === "number" && attempt < 3) {
            revision = cur.revision;
            for (const key of ["page", "percent", "locator", "isFinished"] as const) {
              if (cur[key] !== undefined) (remote as Record<string, unknown>)[key] = cur[key];
            }
            remote.revision = cur.revision;
            current = mergeServerProgress(cur as ServerProgress, current);
            if (Object.keys(current).length === 0) {
              setState("saved");
              window.setTimeout(() => setState((s) => (s === "saved" ? "idle" : s)), 1600);
              return;
            }
            continue;
          }
          throw new APIError(res.error, response.status);
        }
        if (typeof res.revision === "number") revision = res.revision;
        setState("saved");
        window.setTimeout(() => setState((s) => (s === "saved" ? "idle" : s)), 1600);
        return revision;
      }
    };
    queueRef.current = {
      edition: editionId,
      user: scopeUser,
      queue: new ProgressQueue({
        storage: queueStorage,
        initialBaseRevision: baseRevision ?? 0,
        send,
        onError: () => setState("error"),
        shouldRetry: progressRetryable,
      }),
    };
  }
  const queue = queueRef.current?.queue;
  if (!queue) {
    const noop = () => {};
    return { save: noop, flush: noop, state: "idle" as SaveState };
  }

  const postBeacon = useCallback(async (body: ProgressPost & { revision?: number }) => {
    const url = media(`/progress/${editionId}`);
    const payload = JSON.stringify(body);
    let sent = false;
    if (navigator.sendBeacon) {
      try { sent = navigator.sendBeacon(url, new Blob([payload], { type: "application/json" })); } catch { sent = false; }
    }
    if (!sent) {
      try { await fetch(url, { method: "POST", body: payload, headers: { "Content-Type": "application/json" }, keepalive: true }); } catch { /* best effort */ }
    }
  }, [editionId]);

  const deliver = useCallback(() => {
    if (timer.current !== undefined) { clearTimeout(timer.current); timer.current = undefined; }
    lastSent.current = Date.now();
    void queue.flush();
  }, [queue]);

  const save = useCallback((body: ProgressPost) => {
    const wait = SAVE_INTERVAL_MS - (Date.now() - lastSent.current);
    queue.enqueue(body);
    if (wait <= 0) { deliver(); return; }
    if (timer.current === undefined) {
      timer.current = window.setTimeout(() => { timer.current = undefined; deliver(); }, wait);
    }
  }, [deliver, queue]);

  const flush = useCallback(() => {
    if (timer.current !== undefined) { clearTimeout(timer.current); timer.current = undefined; }
    const pending = queue.snapshot();
    if (Object.keys(pending).length === 0) return;
    void postBeacon({ ...pending as ProgressPost, revision: queue.base() });
  }, [postBeacon, queue]);

  useEffect(() => {
    if (Object.keys(queue.snapshot()).length > 0) deliver();
  }, [queue, deliver]);

  useEffect(() => {
    const onVis = () => { if (document.visibilityState === "hidden") flush(); };
    const onLeave = () => flush();
    document.addEventListener("visibilitychange", onVis);
    addEventListener("pagehide", onLeave);
    addEventListener("beforeunload", onLeave);
    return () => {
      document.removeEventListener("visibilitychange", onVis);
      removeEventListener("pagehide", onLeave);
      removeEventListener("beforeunload", onLeave);
      flush();
      queue.stop();
    };
  }, [flush, queue]);

  return { save, flush, state };
}

export function isTypingTarget(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null;
  return !!t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable);
}

/* ---- UI below ---- */

type IconProps = { size?: number };

function svgProps(size: number) {
  return {
    width: size, height: size, viewBox: "0 0 24 24",
    fill: "none", stroke: "currentColor", strokeWidth: 1.8,
    strokeLinecap: "round" as const, strokeLinejoin: "round" as const,
    style: { display: "block", flexShrink: 0 },
  };
}

export function IconArrowLeft(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)}>
      <path d="M19 12H5M11 5.5L4.5 12l6.5 6.5" />
    </svg>
  );
}

export function IconContents(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M8.5 6H20M8.5 12H20M8.5 18H20" />
      <path d="M4.5 6h0.01M4.5 12h0.01M4.5 18h0.01" strokeWidth={2.4} />
    </svg>
  );
}

export function IconPlus(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M12 5.5v13M5.5 12h13" />
    </svg>
  );
}

export function IconMinus(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M5.5 12h13" />
    </svg>
  );
}

export function IconPageSingle(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="7" y="4.5" width="10" height="15" rx="1.5" />
    </svg>
  );
}

export function IconPageDouble(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="3.5" y="4.5" width="7.5" height="15" rx="1.5" />
      <rect x="13" y="4.5" width="7.5" height="15" rx="1.5" />
    </svg>
  );
}

export function IconWebtoon(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M12 4.5v15" />
      <path d="M8.5 8L12 4.5 15.5 8" />
      <path d="M8.5 16l3.5 3.5 3.5-3.5" />
    </svg>
  );
}

export function IconFitWidth(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4 12h16M8 8l-4 4 4 4M16 8l4 4-4 4" />
    </svg>
  );
}

export function IconFitHeight(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M12 4v16M8 8l4-4 4 4M8 16l4 4 4-4" />
    </svg>
  );
}

export function IconRtl(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M17.5 8.5H6.5L9.5 5.5" />
      <path d="M6.5 15.5h11M14.5 18.5l3-3-3-3" />
    </svg>
  );
}

export function IconClose(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />
    </svg>
  );
}

export function IconDownload(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 15)}>
      <path d="M12 4v11M7.5 10.5L12 15l4.5-4.5" />
      <path d="M5 19.5h14" />
    </svg>
  );
}

export function IconChevLeft(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)}>
      <path d="M14.5 5.5L8 12l6.5 6.5" />
    </svg>
  );
}

export function IconChevRight(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)}>
      <path d="M9.5 5.5L16 12l-6.5 6.5" />
    </svg>
  );
}

export function IconCheckSmall(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 14)}>
      <path d="M5 12.5l4.5 4.5L19 7.5" />
    </svg>
  );
}

export const readerOverlay: CSSProperties = {
  position: "fixed", inset: 0, zIndex: 60,
  background: c.bg, display: "flex", flexDirection: "column", fontFamily: font,
};

export const readerBar: CSSProperties = {
  position: "relative",
  display: "flex", alignItems: "center", gap: "0.4rem",
  padding: "0.35rem 0.9rem", minHeight: "3.5rem",
  background: "rgba(12, 13, 15, 0.78)",
  backdropFilter: "blur(20px) saturate(160%)", WebkitBackdropFilter: "blur(20px) saturate(160%)",
  borderBottom: "1px solid rgba(255,255,255,0.06)",
};

export const readerControls: CSSProperties = {
  display: "flex", alignItems: "center", gap: "0.25rem", flexWrap: "wrap", rowGap: "0.4rem",
  padding: "0.35rem 0.9rem", borderBottom: `1px solid ${c.lineSoft}`,
};

export const readerStage: CSSProperties = {
  position: "relative", flex: 1, minHeight: 0, display: "flex", overflow: "hidden",
  userSelect: "none", WebkitUserSelect: "none",
};

export const toolBtnCls = "lt-tool";

export const toolBtn: CSSProperties = {
  ...iconBtn, width: "2.5rem", height: "2.5rem", borderRadius: "50%",
  transition: "background 0.15s ease, color 0.15s ease",
};

export function toolBtnActive(active: boolean): CSSProperties {
  return active ? { ...toolBtn, color: c.accent, background: c.accentSoft } : toolBtn;
}

export function ReaderChrome() {
  return (
    <style>{`
      .lt-tool:not(:disabled):hover{background:rgba(255,255,255,0.08)}
      .lt-tool:disabled{opacity:0.4;cursor:default}
      @keyframes rd-in{from{opacity:0}}
      @keyframes rd-scrim-in{from{opacity:0}}
      @keyframes rd-drawer-in{from{transform:translateX(100%)}}
      .rd-scrim{animation:rd-scrim-in 200ms ease}
      .rd-drawer{animation:rd-drawer-in 240ms var(--ease)}
      .rd-in{animation:rd-in 200ms var(--ease)}
      .tap-hint{opacity:0;transition:opacity 160ms ease}
      @media (hover:hover){.tap-zone:hover .tap-hint{opacity:1}}
      .lt-edge{transition:background 150ms ease,color 150ms ease;opacity:0.9}
      .lt-edge:hover{opacity:1;background:rgba(20,21,24,0.92);color:#f5f5f7}
      @media (prefers-reduced-motion:reduce){.rd-in,.rd-scrim,.rd-drawer{animation:none}.tap-hint,.lt-edge{transition:none}}
    `}</style>
  );
}

export function PagePill(props: { text: string; watch: string | number }) {
  const [lit, setLit] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    setLit(true);
    if (timer.current !== undefined) clearTimeout(timer.current);
    timer.current = window.setTimeout(() => { timer.current = undefined; setLit(false); }, 1600);
    return () => { if (timer.current !== undefined) { clearTimeout(timer.current); timer.current = undefined; } };
  }, [props.watch, props.text]);
  if (!props.text) return null;
  return (
    <div style={{
      position: "absolute", bottom: "1.25rem", left: "50%", transform: "translateX(-50%)", zIndex: 8,
      background: "rgba(12, 13, 15, 0.78)",
      backdropFilter: "blur(20px) saturate(160%)", WebkitBackdropFilter: "blur(20px) saturate(160%)",
      border: "1px solid rgba(255,255,255,0.08)", borderRadius: "999px",
      padding: "0.45rem 0.9rem", fontSize: "0.78rem", color: c.textDim,
      fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap", pointerEvents: "none",
      opacity: lit ? 1 : 0.25, transition: "opacity 0.45s ease",
    }}>{props.text}</div>
  );
}

export const drawerPanel: CSSProperties = {
  position: "absolute", top: 0, right: 0, bottom: 0, width: "min(20rem, 85vw)",
  background: c.bgRaised, borderLeft: `1px solid ${c.line}`, zIndex: 20,
  display: "flex", flexDirection: "column", boxShadow: "0 0 34px rgba(0,0,0,0.5)",
};

export const drawerItem: CSSProperties = {
  display: "block", width: "100%", textAlign: "left", background: "none", border: "none",
  borderBottom: `1px solid ${c.lineSoft}`, color: c.textDim, fontFamily: font, fontSize: "0.88rem",
  padding: "0.65rem 1rem", cursor: "pointer",
};

export function ReaderMessage(props: { text: string; onBack?: () => void }) {
  return (
    <div style={{ position: "absolute", inset: 0, display: "flex", flexDirection: "column", gap: "0.8rem", alignItems: "center", justifyContent: "center", color: c.muted, fontSize: "0.9rem", padding: "1rem", textAlign: "center" }}>
      <span>{props.text}</span>
      {props.onBack && <button style={{ ...toolBtn, border: `1px solid ${c.line}`, width: "auto", padding: "0.35rem 1rem", fontSize: "0.82rem", gap: "0.35rem", color: c.textDim }} onClick={props.onBack}><IconArrowLeft size={15} /> Back</button>}
    </div>
  );
}

export function TopBar(props: { title: string; meta?: string; saveState: SaveState | null; onBack: () => void; children?: ComponentChildren }) {
  return (
    <div style={readerBar}>
      <ReaderChrome />
      <button className={toolBtnCls} style={toolBtn} aria-label="Back" title="Back (Esc)" onClick={props.onBack}><IconArrowLeft size={17} /></button>
      {props.saveState && props.saveState !== "idle" && (
        <span style={{ color: props.saveState === "error" ? c.danger : c.muted, fontSize: "0.72rem", flexShrink: 0 }}>
          {props.saveState === "saving" ? "saving…" : props.saveState === "saved" ? "saved" : "save failed"}
        </span>
      )}
      <div style={{ position: "absolute", left: "50%", top: "50%", transform: "translate(-50%, -50%)", display: "flex", flexDirection: "column", alignItems: "center", gap: "0.12rem", maxWidth: "min(30rem, 55vw)", pointerEvents: "none", textAlign: "center" }}>
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 600, fontSize: "0.92rem", lineHeight: 1.25, maxWidth: "100%" }}>{props.title}</span>
        {props.meta && (
          <span style={{ color: c.muted, fontSize: "0.68rem", lineHeight: 1.45, background: "rgba(255,255,255,0.06)", borderRadius: "999px", padding: "0.02rem 0.55rem", maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.meta}</span>
        )}
      </div>
      <span style={{ flex: 1 }} />
      {props.children != null && <div style={{ display: "flex", alignItems: "center", gap: "0.15rem", flexShrink: 0 }}>{props.children}</div>}
    </div>
  );
}

export function TapZones(props: { onLeft: () => void; onRight: () => void; leftLabel?: string; rightLabel?: string }) {
  const zone: CSSProperties = {
    position: "absolute", top: 0, bottom: 0, width: "33.333%", zIndex: 5,
    background: "transparent", border: "none", padding: 0, cursor: "pointer",
  };
  const hint: CSSProperties = {
    position: "absolute", top: "50%", transform: "translateY(-50%)",
    width: "2.4rem", height: "2.4rem", borderRadius: "50%",
    display: "flex", alignItems: "center", justifyContent: "center",
    background: "rgba(12,13,15,0.72)", backdropFilter: "blur(12px)", WebkitBackdropFilter: "blur(12px)",
    border: "1px solid rgba(255,255,255,0.08)", color: c.textDim, pointerEvents: "none",
  };
  return (
    <div style={{ position: "absolute", inset: 0, pointerEvents: "none" }}>
      <button aria-label={props.leftLabel ?? "Previous page"} className="tap-zone" style={{ ...zone, left: 0, pointerEvents: "auto" }} onClick={props.onLeft}>
        <span className="tap-hint" style={{ ...hint, left: "0.55rem" }}><IconChevLeft size={17} /></span>
      </button>
      <button aria-label={props.rightLabel ?? "Next page"} className="tap-zone" style={{ ...zone, right: 0, pointerEvents: "auto" }} onClick={props.onRight}>
        <span className="tap-hint" style={{ ...hint, right: "0.55rem" }}><IconChevRight size={17} /></span>
      </button>
    </div>
  );
}
