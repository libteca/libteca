import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { CSSProperties } from "preact";
import { media } from "../api";
import { c } from "../styles";
import { extractReserved, readBoundedBodyReserved, type ZipEntryLike } from "./resources";
import {
  clamp01, IconChevLeft, IconChevRight, IconFitHeight, IconFitWidth, IconPageDouble,
  IconPageSingle, IconRtl, IconWebtoon, isTypingTarget, loadPref, pageImageNames, pagePercent,
  PagePill, pairStart, ReaderMessage, readerControls, readerOverlay, readerStage, savePref, stepPage,
  TopBar, TapZones, toolBtn, toolBtnActive, toolBtnCls, useProgressSaver,
  type FitMode, type ProgressPost, type ReaderMode, type ReadingProgress,
} from "./shared";

import { admitZipDirectory } from "./archiveAdmission";
import { blobDimensions, ReaderResourceBudget, sharedReaderBudget, withDecode } from "./resourceBudget";

const MODE_KEY = "libteca-cbz-mode";
const FIT_KEY = "libteca-cbz-fit";
const RTL_KEY = "libteca-cbz-rtl";
const PREFETCH = 3;
const EVICT_RADIUS = 10;
const ARCHIVE_MAX_BYTES = 512 << 20;
const PAGE_MAX_BYTES = 256 << 20;
const MAX_ENTRIES = 10000;

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

export class PageStore {
  private urls: (string | null)[] = [];
  private releases = new Map<number, () => void>();
  private errors = new Map<number, string>();
  private queue: number[] = [];
  private running = false;
  private revoked = false;
  private center = 0;
  private active: { index: number; controller: AbortController } | null = null;

  constructor(private entries: ZipEntryLike[], private notify: () => void, private budget: ReaderResourceBudget = sharedReaderBudget(), private releaseArchive: () => void = () => {}) {}

  error(i: number): string | undefined { return this.errors.get(i); }

  get count(): number { return this.entries.length; }

  ready(i: number): boolean { return this.urls[i] !== undefined; }

  status(i: number): "loading" | "error" | "ready" {
    return this.urls[i] === null ? "error" : typeof this.urls[i] === "string" ? "ready" : "loading";
  }

  retry(i: number): void {
    if (this.revoked || i < 0 || i >= this.count) return;
    if (this.urls[i] !== null) return;
    delete this.urls[i];
    this.ensure(i, true);
    this.notify();
  }

  url(i: number): string | null {
    const u = this.urls[i];
    return typeof u === "string" ? u : null;
  }

  ensure(i: number, front = false): void {
    if (this.revoked || i < 0 || i >= this.count || this.urls[i] !== undefined || (this.active?.index === i && !this.active.controller.signal.aborted)) return;
    const qi = this.queue.indexOf(i);
    if (qi >= 0) {
      if (front) { this.queue.splice(qi, 1); this.queue.unshift(i); }
      return;
    }
    if (front) this.queue.unshift(i); else this.queue.push(i);
    void this.drain();
  }

  ensureAround(i: number, radius: number): void {
    this.center = i;
    this.queue = this.queue.filter((j) => Math.abs(j - i) <= EVICT_RADIUS);
    if (this.active && Math.abs(this.active.index - i) > EVICT_RADIUS) this.active.controller.abort();
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
      this.releases.get(j)?.(); this.releases.delete(j); this.errors.delete(j);
      delete this.urls[j];
      evicted = true;
    }
    if (evicted) this.notify();
  }

  revoke(): void {
    this.revoked = true;
    this.active?.controller.abort();
    this.queue = [];
    for (const u of this.urls) if (typeof u === "string") URL.revokeObjectURL(u);
    this.urls = [];
    for (const release of this.releases.values()) release();
    this.releases.clear(); this.errors.clear(); this.entries = []; this.releaseArchive();
  }

  private async drain(): Promise<void> {
    if (this.running) return;
    this.running = true;
    try {
      while (this.queue.length) {
        const i = this.queue.shift()!;
        if (this.revoked) return;
        if (this.urls[i] !== undefined || Math.abs(i - this.center) > EVICT_RADIUS) continue;
        const controller = new AbortController();
        this.active = { index: i, controller };
let releasePage: (() => void) | undefined;
        let releasePixels: (() => void) | undefined;
        let url: string | undefined;
        try {
          const result = await extractReserved(this.entries[i], PAGE_MAX_BYTES, this.budget, controller.signal);
          releasePage = result.release;
          const blob = typedBlob(this.entries[i], result.blob);
          if (this.revoked) return;
          if (controller.signal.aborted || Math.abs(i - this.center) > EVICT_RADIUS) continue;
          if (this.budget.enabled("decodedPixels") || this.budget.enabled("activeDecodes")) {
            const dimensions = await blobDimensions(blob, this.budget);
            const decoded = await withDecode(this.budget, dimensions.width, dimensions.height, async () => {
              url = URL.createObjectURL(blob);
              const image = new Image();
              image.src = url;
              await image.decode();
            }, controller.signal);
            releasePixels = decoded.release;
          }
          if (this.revoked || controller.signal.aborted || Math.abs(i - this.center) > EVICT_RADIUS) continue;
          url ??= URL.createObjectURL(blob);
          this.urls[i] = url;
          const pageRelease = releasePage, pixelRelease = releasePixels;
          this.releases.set(i, () => { pageRelease?.(); pixelRelease?.(); });
          releasePage = undefined; releasePixels = undefined; url = undefined;
          this.errors.delete(i);
          this.notify();
        } catch (error) {
          if (!this.revoked && !controller.signal.aborted) {
            this.urls[i] = null;
            this.errors.set(i, (error as Error).message || "This page could not be decoded.");
            this.notify();
          }
        } finally {
          if (url) URL.revokeObjectURL(url);
          releasePage?.(); releasePixels?.();
          this.active = null;
        }
      }
    } finally {
      this.running = false;
    }
  }
}

function PageImg(props: { i: number; url: string | null; status: "loading" | "error" | "ready"; onRetry?: () => void; error?: string; style?: CSSProperties }) {
  if (props.status === "error") {
    return (
      <div role="alert" style={{ ...props.style, display: "flex", flexDirection: "column", gap: "0.6rem", alignItems: "center", justifyContent: "center", color: c.muted, fontSize: "0.82rem", background: c.bgRaised, minHeight: "45vh" }}>
        <span>{props.error || "This page could not be decoded."}</span>
        {props.onRetry && <button style={{ ...toolBtn, border: `1px solid ${c.line}`, width: "auto", padding: "0.3rem 0.9rem", fontSize: "0.78rem", color: c.textDim }} onClick={props.onRetry}>Retry page</button>}
      </div>
    );
  }
  if (props.url) return <img src={props.url} alt={`Page ${props.i + 1}`} style={props.style} draggable={false} />;
  return (
    <div style={{ ...props.style, display: "flex", alignItems: "center", justifyContent: "center", color: c.muted, fontSize: "0.82rem", background: c.bgRaised, minHeight: "45vh" }}>
      loading…
    </div>
  );
}

type CbzReaderProps = { editionId: number; title: string; progress: ReadingProgress | null; onBack: () => void };

export function CbzReader(props: CbzReaderProps) {
  return <CbzSession key={props.editionId} {...props} />;
}

function CbzSession(props: CbzReaderProps) {
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [count, setCount] = useState(0);
  const [page, setPage] = useState(0);
  const [mode, setMode] = useState<ReaderMode>(() => loadPref<ReaderMode>(MODE_KEY, "single", ["single", "double", "webtoon"]));
  const [fit, setFit] = useState<FitMode>(() => loadPref<FitMode>(FIT_KEY, "height", ["width", "height"]));
  const [rtl, setRtl] = useState(() => { try { return localStorage.getItem(RTL_KEY) === "1"; } catch { return false; } });
  const [tick, bump] = useState(0);
  const saver = useProgressSaver(props.editionId, props.progress?.revision ?? 0, {
    page: props.progress?.page, percent: props.progress?.percent,
    resetGeneration: props.progress?.resetGeneration,
  });
  const storeRef = useRef<PageStore | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pageEls = useRef(new Map<number, HTMLElement>());
  const visibleRef = useRef(new Set<number>());
  const pendingScroll = useRef<number | null>(null);

  useEffect(() => {
    let alive = true;
    const controller = new AbortController();
    (async () => {
      let releaseArchive: (() => void) | undefined;
      try {
        const res = await fetch(media(`/editions/${props.editionId}/download`), { signal: controller.signal });
        const budget = sharedReaderBudget();
        const downloaded = await readBoundedBodyReserved(res, ARCHIVE_MAX_BYTES, budget, controller.signal);
        releaseArchive = downloaded.release;
        const directory = admitZipDirectory(downloaded.buffer, budget, MAX_ENTRIES);
        releaseArchive = () => { directory.release(); downloaded.release(); };
        const buf = downloaded.buffer;
        if (!alive) return;
        const JSZip = (await import("jszip")).default;
        if (!alive) return;
        const zip = await JSZip.loadAsync(buf);
        if (!alive) return;
        const names = pageImageNames(Object.keys(zip.files));
        if (names.length > MAX_ENTRIES) throw new Error("Archive has too many pages");
        const store = new PageStore(names.map((n) => zip.files[n] as unknown as ZipEntryLike), () => bump((n) => n + 1), budget, releaseArchive);
        releaseArchive = undefined;
        storeRef.current = store;
        setCount(store.count);
        const p = props.progress;
        let start = 0;
        if (p && !p.isFinished) {
          const raw = p.page && p.page > 0 ? p.page : p.locator ? Number(p.locator) : NaN;
          if (Number.isFinite(raw)) start = Math.max(0, Math.min(store.count - 1, Math.floor(raw) - 1));
        }
        pendingScroll.current = start > 0 ? start : null;
        setPage(start);
        store.ensureAround(start, PREFETCH);
        setPhase("ready");
      } catch (err) {
        if (alive && !(err instanceof DOMException && err.name === "AbortError")) {
          setError(String((err as Error)?.message || err));
          setPhase("error");
        }
      } finally { releaseArchive?.(); }
    })();
    return () => { alive = false; controller.abort(); storeRef.current?.revoke(); storeRef.current = null; };
  }, [props.editionId]);

  useEffect(() => {
    if (phase !== "ready" || count <= 0) return;
    const reached = mode === "double" ? Math.min(pairStart(page) + 2, count) : page + 1;
    const body: ProgressPost = { page: reached, percent: pagePercent(reached, count), locator: String(reached) };
    if (reached >= count) body.finished = true;
    saver.save(body);
  }, [page, count, phase, mode]);

  useEffect(() => {
    if (phase !== "ready") return;
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
      if (next != null && mode === "webtoon") pendingScroll.current = next;
      return next == null ? cur : next;
    });
  }, [mode]);

  useEffect(() => {
    if (phase !== "ready" || mode !== "webtoon") return;
    const root = scrollRef.current;
    if (!root) return;
    visibleRef.current.clear();
    let active = true;
    const preload = new IntersectionObserver((entries) => {
      if (!active) return;
      const store = storeRef.current;
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const i = Number((entry.target as HTMLElement).dataset.page);
        if (Number.isFinite(i)) store?.ensure(i);
      }
    }, { root, rootMargin: "60% 0px", threshold: 0 });
    const io = new IntersectionObserver((entries) => {
      if (!active) return;
      const vis = visibleRef.current;
      for (const entry of entries) {
        const i = Number((entry.target as HTMLElement).dataset.page);
        if (!Number.isFinite(i)) continue;
        if (entry.isIntersecting) vis.add(i);
        else vis.delete(i);
      }
      if (vis.size && pendingScroll.current == null) setPage(Math.min(...vis));
    }, { root, threshold: 0 });
    for (const el of pageEls.current.values()) { preload.observe(el); io.observe(el); }
    return () => { active = false; preload.disconnect(); io.disconnect(); visibleRef.current.clear(); };
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
  const pageStatus = (i: number): "loading" | "error" | "ready" => storeRef.current?.status(i) ?? "loading";
  const retryPage = (i: number) => storeRef.current?.retry(i);

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
                <PageImg i={i} url={pageUrl(i)} status={pageStatus(i)} error={storeRef.current?.error(i)} onRetry={() => retryPage(i)} style={{ width: "100%", height: "auto", objectFit: "contain", display: "block" }} />
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
                ? (pairStart(page) + 1 < count ? [pairStart(page), pairStart(page) + 1] : [pairStart(page)]).map((i) => <PageImg key={i} i={i} url={pageUrl(i)} status={pageStatus(i)} error={storeRef.current?.error(i)} onRetry={() => retryPage(i)} style={pageStyle} />)
                : <PageImg i={page} url={pageUrl(page)} status={pageStatus(page)} error={storeRef.current?.error(page)} onRetry={() => retryPage(page)} style={pageStyle} />}
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
