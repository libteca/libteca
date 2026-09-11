import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { CSSProperties } from "preact";
import { media } from "../api";
import { c } from "../styles";
import {
  clamp01, IconChevLeft, IconChevRight, IconFitHeight, IconFitWidth, IconPageDouble,
  IconPageSingle, IconRtl, IconWebtoon, isTypingTarget, loadPref, pageImageNames, pagePercent,
  PagePill, pairStart, ReaderMessage, readerControls, readerOverlay, readerStage, savePref, stepPage,
  TopBar, TapZones, toolBtn, toolBtnActive, toolBtnCls, useProgressSaver,
  type FitMode, type ProgressPost, type ReaderMode, type ReadingProgress,
} from "./shared";

const MODE_KEY = "libteca-cbz-mode";
const FIT_KEY = "libteca-cbz-fit";
const RTL_KEY = "libteca-cbz-rtl";
const PREFETCH = 3;
const EVICT_RADIUS = 10;

type ZipEntryLike = { name: string; async(type: "blob"): Promise<Blob> };

const IMG_MIME: Record<string, string> = {
  png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif",
  webp: "image/webp", avif: "image/avif", bmp: "image/bmp", jxl: "image/jxl",
};

function typedBlob(entry: ZipEntryLike, blob: Blob): Blob {
  if (blob.type) return blob;
  const ext = entry.name.slice(entry.name.lastIndexOf(".") + 1).toLowerCase();
  const mime = IMG_MIME[ext];
  return mime ? new Blob([blob], { type: mime }) : blob;
}

class PageStore {
  private urls: (string | null)[] = [];
  private queue: number[] = [];
  private running = false;
  private revoked = false;

  constructor(private entries: ZipEntryLike[], private notify: () => void) {}

  get count(): number { return this.entries.length; }

  ready(i: number): boolean { return this.urls[i] !== undefined; }

  url(i: number): string | null {
    const u = this.urls[i];
    return typeof u === "string" ? u : null;
  }

  ensure(i: number, front = false): void {
    if (this.revoked || i < 0 || i >= this.count || this.urls[i] !== undefined) return;
    const qi = this.queue.indexOf(i);
    if (qi >= 0) {
      if (front) { this.queue.splice(qi, 1); this.queue.unshift(i); }
      return;
    }
    if (front) this.queue.unshift(i); else this.queue.push(i);
    void this.drain();
  }

  ensureAround(i: number, radius: number): void {
    this.evictFar(i);
    this.ensure(i, true);
    for (let d = 1; d <= radius; d++) {
      this.ensure(i + d, true);
      this.ensure(i - d, false);
    }
  }

  private evictFar(i: number): void {
    let evicted = false;
    for (let j = 0; j < this.urls.length; j++) {
      if (Math.abs(j - i) <= EVICT_RADIUS) continue;
      const u = this.urls[j];
      if (u === undefined) {
        const qi = this.queue.indexOf(j);
        if (qi >= 0) this.queue.splice(qi, 1);
        continue;
      }
      if (typeof u === "string") URL.revokeObjectURL(u);
      delete this.urls[j];
      evicted = true;
    }
    if (evicted) this.notify();
  }

  warmAll(): void {
    for (let i = 0; i < this.count; i++) this.ensure(i, false);
  }

  revoke(): void {
    this.revoked = true;
    this.queue = [];
    for (const u of this.urls) if (typeof u === "string") URL.revokeObjectURL(u);
    this.urls = [];
  }

  private async drain(): Promise<void> {
    if (this.running) return;
    this.running = true;
    try {
      while (this.queue.length) {
        const i = this.queue.shift()!;
        if (this.revoked) return;
        if (this.urls[i] !== undefined) continue;
        try {
          const blob = typedBlob(this.entries[i], await this.entries[i].async("blob"));
          if (this.revoked) return;
          this.urls[i] = URL.createObjectURL(blob);
          this.notify();
        } catch {
          this.urls[i] = null;
          this.notify();
        }
      }
    } finally {
      this.running = false;
    }
  }
}

function PageImg(props: { i: number; url: string | null; style?: CSSProperties }) {
  if (props.url) return <img src={props.url} alt={`Page ${props.i + 1}`} style={props.style} draggable={false} />;
  return (
    <div style={{ ...props.style, display: "flex", alignItems: "center", justifyContent: "center", color: c.muted, fontSize: "0.82rem", background: c.bgRaised, minHeight: "45vh" }}>
      loading…
    </div>
  );
}

export function CbzReader(props: { editionId: number; title: string; progress: ReadingProgress | null; onBack: () => void }) {
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [count, setCount] = useState(0);
  const [page, setPage] = useState(0);
  const [mode, setMode] = useState<ReaderMode>(() => loadPref<ReaderMode>(MODE_KEY, "single", ["single", "double", "webtoon"]));
  const [fit, setFit] = useState<FitMode>(() => loadPref<FitMode>(FIT_KEY, "height", ["width", "height"]));
  const [rtl, setRtl] = useState(() => { try { return localStorage.getItem(RTL_KEY) === "1"; } catch { return false; } });
  const [tick, bump] = useState(0);
  const saver = useProgressSaver(props.editionId);
  const storeRef = useRef<PageStore | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pageEls = useRef(new Map<number, HTMLElement>());
  const visibleRef = useRef(new Set<number>());
  const pendingScroll = useRef<number | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const res = await fetch(media(`/editions/${props.editionId}/download`));
        if (!res.ok) throw new Error(`Download failed (${res.status})`);
        const buf = await res.arrayBuffer();
        const JSZip = (await import("jszip")).default;
        const zip = await JSZip.loadAsync(buf);
        if (!alive) return;
        const names = pageImageNames(Object.keys(zip.files));
        const store = new PageStore(names.map((n) => zip.files[n] as unknown as ZipEntryLike), () => bump((n) => n + 1));
        storeRef.current = store;
        setCount(store.count);
        const p = props.progress;
        let start = 0;
        if (p && !p.isFinished) {
          const raw = p.page && p.page > 0 ? p.page : p.locator ? Number(p.locator) : NaN;
          if (!isNaN(raw)) start = Math.max(0, Math.min(store.count - 1, Math.floor(raw) - 1));
        }
        pendingScroll.current = start > 0 ? start : null;
        setPage(start);
        store.ensureAround(start, PREFETCH);
        setPhase("ready");
      } catch (err) {
        if (alive) {
          setError(String((err as Error)?.message || err));
          setPhase("error");
        }
      }
    })();
    return () => { alive = false; storeRef.current?.revoke(); storeRef.current = null; };
  }, [props.editionId]);

  useEffect(() => {
    if (phase !== "ready" || count <= 0) return;
    const body: ProgressPost = { page: page + 1, percent: pagePercent(page + 1, count), locator: String(page + 1) };
    if (page >= count - 1) body.finished = true;
    saver.save(body);
  }, [page, count, phase]);

  useEffect(() => {
    if (phase !== "ready" || mode === "webtoon") return;
    const store = storeRef.current;
    if (!store) return;
    const base = mode === "double" ? pairStart(page) : page;
    store.ensureAround(base, PREFETCH);
    if (mode === "double") store.ensure(base + 1, true);
  }, [page, mode, phase]);

  useEffect(() => {
    if (mode === "webtoon") pendingScroll.current = page;
  }, [mode]);

  const go = useCallback((dir: 1 | -1) => {
    const store = storeRef.current;
    if (!store || store.count === 0) return;
    setPage((cur) => {
      const next = stepPage(cur, dir, mode === "double" ? "double" : "single", store.count);
      return next == null ? cur : next;
    });
  }, [mode]);

  useEffect(() => {
    if (phase !== "ready" || mode !== "webtoon") return;
    const root = scrollRef.current;
    if (!root) return;
    const io = new IntersectionObserver((entries) => {
      const vis = visibleRef.current;
      const store = storeRef.current;
      let changed = false;
      for (const en of entries) {
        const i = Number((en.target as HTMLElement).dataset.page);
        if (isNaN(i)) continue;
        if (en.isIntersecting) {
          if (store) store.ensureAround(i, 2);
          if (!vis.has(i)) { vis.add(i); changed = true; }
        }
        else if (vis.delete(i)) changed = true;
      }
      if (changed && vis.size && pendingScroll.current == null) {
        let best = -1;
        let bestTop = Infinity;
        for (const i of vis) {
          const el = pageEls.current.get(i);
          if (!el) continue;
          const top = el.getBoundingClientRect().top;
          if (top >= 0 && top < bestTop) { bestTop = top; best = i; }
        }
        setPage(best >= 0 ? best : Math.min(...vis));
      }
    }, { root, rootMargin: "60% 0px", threshold: 0 });
    for (const el of pageEls.current.values()) io.observe(el);
    return () => io.disconnect();
  }, [phase, mode]);

  useEffect(() => {
    if (phase !== "ready" || mode !== "webtoon") return;
    const target = pendingScroll.current;
    if (target == null) return;
    if (target <= 0) {
      scrollRef.current?.scrollTo({ top: 0 });
      pendingScroll.current = null;
      return;
    }
    const store = storeRef.current;
    if (store && store.ready(target)) {
      const el = pageEls.current.get(target);
      if (el) {
        el.scrollIntoView({ block: "start" });
        pendingScroll.current = null;
        return;
      }
    }
    store?.ensureAround(target, 1);
  }, [phase, mode, tick, page]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e)) return;
      if (e.key === "ArrowLeft") { e.preventDefault(); if (rtl) go(1); else go(-1); }
      else if (e.key === "ArrowRight") { e.preventDefault(); if (rtl) go(-1); else go(1); }
      else if (e.key === "Escape") { e.preventDefault(); props.onBack(); }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [rtl, go, props.onBack]);

  const setModeAndPersist = (m: ReaderMode) => {
    setMode(m);
    savePref(MODE_KEY, m);
    visibleRef.current.clear();
  };
  const setFitAndPersist = (f: FitMode) => {
    setFit(f);
    savePref(FIT_KEY, f);
  };
  const toggleRtl = () => {
    const next = !rtl;
    setRtl(next);
    savePref(RTL_KEY, next ? "1" : "0");
  };

  const pageStyle: CSSProperties = mode === "double"
    ? (fit === "width"
      ? { width: "50%", height: "auto", objectFit: "contain", display: "block" }
      : { height: "100%", maxWidth: "50%", objectFit: "contain", display: "block" })
    : (fit === "width"
      ? { width: "100%", height: "auto", objectFit: "contain", display: "block" }
      : { height: "100%", maxWidth: "100%", objectFit: "contain", display: "block" });

  const pageUrl = (i: number): string | null => storeRef.current?.url(i) ?? null;

  const percent = count > 0 ? clamp01((page + 1) / count) : 0;
  const pillText = phase === "ready" && count > 0 ? `Page ${page + 1} / ${count} · ${Math.round(percent * 100)}%` : "";

  return (
    <div className="rd-in" style={readerOverlay}>
      <TopBar title={props.title} saveState={saver.state} onBack={props.onBack} />
      <div style={readerControls}>
        <button className={toolBtnCls} style={toolBtnActive(mode === "single")} aria-label="Single page" title="Single page" onClick={() => setModeAndPersist("single")}><IconPageSingle size={15} /></button>
        <button className={toolBtnCls} style={toolBtnActive(mode === "double")} aria-label="Double page spread" title="Double page spread" onClick={() => setModeAndPersist("double")}><IconPageDouble size={15} /></button>
        <button className={toolBtnCls} style={toolBtnActive(mode === "webtoon")} aria-label="Webtoon (vertical scroll)" title="Webtoon (vertical scroll)" onClick={() => setModeAndPersist("webtoon")}><IconWebtoon size={15} /></button>
        <span style={{ width: "1px", height: "1.1rem", background: c.line, margin: "0 0.35rem" }} />
        <button className={toolBtnCls} style={toolBtnActive(rtl)} aria-label="Right-to-left (manga)" title="Right-to-left (manga)" onClick={toggleRtl}><IconRtl size={15} /></button>
        <span style={{ width: "1px", height: "1.1rem", background: c.line, margin: "0 0.35rem" }} />
        <button className={toolBtnCls} style={toolBtnActive(fit === "width")} aria-label="Fit width" title="Fit width" disabled={mode === "webtoon"} onClick={() => setFitAndPersist("width")}><IconFitWidth size={15} /></button>
        <button className={toolBtnCls} style={toolBtnActive(fit === "height")} aria-label="Fit height" title="Fit height" disabled={mode === "webtoon"} onClick={() => setFitAndPersist("height")}><IconFitHeight size={15} /></button>
        <span style={{ flex: 1 }} />
        <input
          type="range" min={1} max={Math.max(1, count)} value={page + 1} disabled={count <= 1}
          style={{ width: "clamp(6rem, 20vw, 12rem)", accentColor: c.accent }}
          onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); if (v >= 1) { if (mode === "webtoon") pendingScroll.current = v - 1; setPage(v - 1); } }}
          aria-label="Page"
        />
      </div>
      <div style={readerStage}>
        <PagePill text={pillText} watch={page} />
        {phase !== "ready" && <ReaderMessage text={phase === "error" ? error || "Could not open this comic." : "Loading comic…"} onBack={props.onBack} />}
        {phase === "ready" && count === 0 && <ReaderMessage text="No pages found in this archive." onBack={props.onBack} />}
        {phase === "ready" && count > 0 && mode === "webtoon" && (
          <div
            ref={scrollRef}
            style={{ flex: 1, minHeight: 0, overflowY: "auto", overflowX: "hidden", display: "flex", flexDirection: "column", alignItems: "center" }}
          >
            {Array.from({ length: count }, (_, i) => (
              <div key={i} data-page={i} ref={(el) => { if (el) pageEls.current.set(i, el); else pageEls.current.delete(i); }} style={{ width: "100%", maxWidth: "56rem", minHeight: "40vh", display: "flex", justifyContent: "center" }}>
                <PageImg i={i} url={pageUrl(i)} style={{ width: "100%", height: "auto", objectFit: "contain", display: "block" }} />
              </div>
            ))}
          </div>
        )}
        {phase === "ready" && count > 0 && mode !== "webtoon" && (
          <>
            <div
              style={{
                flex: 1, minHeight: 0, display: "flex", flexDirection: rtl ? "row-reverse" : "row",
                alignItems: fit === "width" ? "flex-start" : "center", justifyContent: "center",
                overflowY: fit === "width" ? "auto" : "hidden", overflowX: "hidden",
              }}
            >
              {mode === "double"
                ? (pairStart(page) + 1 < count ? [pairStart(page), pairStart(page) + 1] : [pairStart(page)]).map((i) => <PageImg key={i} i={i} url={pageUrl(i)} style={pageStyle} />)
                : <PageImg i={page} url={pageUrl(page)} style={pageStyle} />}
            </div>
            <TapZones onLeft={() => (rtl ? go(1) : go(-1))} onRight={() => (rtl ? go(-1) : go(1))} leftLabel={rtl ? "Next page" : "Previous page"} rightLabel={rtl ? "Previous page" : "Next page"} />
            <button aria-label={rtl ? "Next page" : "Previous page"} className="lt-edge" style={{ position: "absolute", left: "0.55rem", top: "50%", transform: "translateY(-50%)", zIndex: 6, ...toolBtn, background: "rgba(12,13,15,0.72)" }} onClick={() => (rtl ? go(1) : go(-1))}><IconChevLeft size={18} /></button>
            <button aria-label={rtl ? "Previous page" : "Next page"} className="lt-edge" style={{ position: "absolute", right: "0.55rem", top: "50%", transform: "translateY(-50%)", zIndex: 6, ...toolBtn, background: "rgba(12,13,15,0.72)" }} onClick={() => (rtl ? go(-1) : go(1))}><IconChevRight size={18} /></button>
          </>
        )}
      </div>
    </div>
  );
}
