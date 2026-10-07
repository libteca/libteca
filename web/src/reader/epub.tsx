import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { Book, NavItem, Rendition } from "epubjs";
import { media } from "../api";
import { c } from "../styles";
import { readBoundedBody } from "./resources";
import {
  drawerItem, drawerPanel, IconClose, IconContents, IconMinus, IconPlus,
  isTypingTarget, PagePill, ReaderMessage, readerOverlay, readerStage, TopBar, TapZones, toolBtn, toolBtnActive, toolBtnCls,
  useProgressSaver, type ProgressPost, type ReadingProgress,
} from "./shared";

const FONT_MIN = 70;
const FONT_MAX = 220;
const FONT_STEP = 10;
const LOC_CHUNK = 1000;
const EPUB_MAX_BYTES = 512 << 20;

// The generated-location cache is keyed by the CONTENT of this exact file
// (SHA-256 over the downloaded bytes plus the chunk size), never the bare
// edition id: a replaced/reimported EPUB under the same edition id must not
// resume against a CFI index built from different bytes. When Web Crypto is
// unavailable (insecure context) the cache is skipped entirely - locations
// are regenerated - rather than falling back to an unsafe id-only key.
async function epubLocationKey(editionId: number, data: ArrayBuffer, chunkSize: number): Promise<string | null> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null;
  try {
    const digest = await subtle.digest("SHA-256", data);
    const hex = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
    return `libteca:epub-loc:v2:${editionId}:${chunkSize}:${hex}`;
  } catch {
    return null;
  }
}

type TocEntry = { label: string; href: string; depth: number };

function cleanHref(h: string): string {
  const base = h.split("#")[0].split("?")[0];
  try { return decodeURIComponent(base).replace(/^\.?\//, ""); } catch { return base; }
}

function chapterLabel(toc: TocEntry[], href: string): string {
  if (!href) return "";
  const target = cleanHref(href);
  for (const t of toc) if (cleanHref(t.href) === target) return t.label;
  const base = target.slice(target.lastIndexOf("/") + 1);
  for (const t of toc) {
    const th = cleanHref(t.href);
    if (th === base || th.slice(th.lastIndexOf("/") + 1) === base) return t.label;
  }
  return "";
}

function sameChapter(a: string, b: string): boolean {
  if (!a || !b) return false;
  const x = cleanHref(a);
  const y = cleanHref(b);
  if (x === y) return true;
  const xb = x.slice(x.lastIndexOf("/") + 1);
  const yb = y.slice(y.lastIndexOf("/") + 1);
  return xb !== "" && xb === yb;
}

type EpubReaderProps = { editionId: number; title: string; progress: ReadingProgress | null; onBack: () => void };

export function EpubReader(props: EpubReaderProps) {
  return <EpubSession key={props.editionId} {...props} />;
}

function EpubSession(props: EpubReaderProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const bookRef = useRef<Book | null>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const locsReadyRef = useRef(false);
  const [phase, setPhase] = useState<"loading" | "indexing" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [warning, setWarning] = useState("");
  const [toc, setToc] = useState<TocEntry[]>([]);
  const [tocOpen, setTocOpen] = useState(false);
  const [fontSize, setFontSize] = useState(100);
  const [percent, setPercent] = useState<number | null>(null);
  const [sectionHref, setSectionHref] = useState("");
  const saver = useProgressSaver(props.editionId, props.progress?.revision ?? 0, {
    page: props.progress?.page, percent: props.progress?.percent,
    resetGeneration: props.progress?.resetGeneration,
  });
  const keyHandler = useRef<(e: KeyboardEvent) => void>(() => {});
  const navigate = useCallback((action: (rendition: Rendition) => Promise<unknown>) => {
    const rendition = renditionRef.current;
    if (!rendition) return;
    setWarning("");
    void Promise.resolve().then(() => {
      if (renditionRef.current === rendition) return action(rendition);
    }).catch(() => {
      if (renditionRef.current === rendition) setWarning("Could not change pages. Try again.");
    });
  }, []);

  useEffect(() => {
    let destroyed = false;
    const host = hostRef.current;
    if (!host) return;
    const controller = new AbortController();
    setPhase("loading");
    setError("");
    setToc([]);
    setTocOpen(false);
    setPercent(null);
    setSectionHref("");
    locsReadyRef.current = false;

    const persistLocations = (book: Book) => {
      if (!locKey) return;
      try { localStorage.setItem(locKey, book.locations.save()); } catch { /* storage unavailable */ }
    };

    let book: Book | null = null;
    let rendition: Rendition | null = null;
    let locKey: string | null = null;
    const onDocKey = (e: KeyboardEvent) => { if (!destroyed) keyHandler.current(e); };
    const release = () => {
      rendition?.off("keydown", onDocKey);
      try { rendition?.destroy(); } catch {}
      try { book?.destroy(); } catch {}
      if (renditionRef.current === rendition) renditionRef.current = null;
      if (bookRef.current === book) bookRef.current = null;
    };
    (async () => {
      try {
        const res = await fetch(media(`/editions/${props.editionId}/download`), { signal: controller.signal });
        const data = await readBoundedBody(res, EPUB_MAX_BYTES, controller.signal);
        if (destroyed) return;
        locKey = await epubLocationKey(props.editionId, data, LOC_CHUNK);
        if (destroyed) return;
        try { localStorage.removeItem(`libteca-epub-loc-${props.editionId}`); } catch { /* storage unavailable */ }
        const epubjs = await import("epubjs");
        if (destroyed) return;
        book = new epubjs.Book();
        await book.open(data);
        if (destroyed) { release(); return; }
        bookRef.current = book;

        rendition = new epubjs.Rendition(book, { width: "100%", height: "100%", spread: "auto", flow: "paginated" });
        renditionRef.current = rendition;
        await rendition.attachTo(host);
        if (destroyed) { release(); return; }
        rendition.on("keydown", onDocKey);
        rendition.themes.default({ body: { color: c.text, background: c.bg } });
        rendition.themes.override("color", c.text, true);
        rendition.themes.override("background", c.bg, true);
        rendition.themes.fontSize("100%");

        rendition.on("relocated", (loc: { start?: { cfi?: string }; atEnd?: boolean }) => {
          if (destroyed) return;
          const cfi = loc?.start?.cfi;
          if (!cfi) return;
          let pct: number | null = null;
          if (locsReadyRef.current) {
            const v = book!.locations.percentageFromCfi(cfi);
            if (typeof v === "number" && isFinite(v)) pct = v;
          }
          setPercent(pct);
          const body: ProgressPost = { locator: cfi };
          if (pct != null) body.percent = pct;
          if (loc?.atEnd) body.finished = true;
          saver.save(body);
        });

        rendition.on("relocated", (loc: { start?: { href?: string } }) => {
          if (!destroyed) setSectionHref(loc?.start?.href || "");
        });

        const nav = await book.loaded.navigation.catch(() => null);
        if (destroyed) { release(); return; }
        const flat: TocEntry[] = [];
        const walk = (items: NavItem[], depth: number) => {
          for (const it of items) {
            const label = (it.label || "").replace(/\s+/g, " ").trim();
            if (label && it.href) flat.push({ label, href: it.href, depth });
            if (it.subitems?.length) walk(it.subitems, depth + 1);
          }
        };
        if (nav?.toc) walk(nav.toc, 0);
        setToc(flat);

        let haveLocations = false;
        if (locKey) {
          try {
            const saved = localStorage.getItem(locKey);
            if (saved) {
              book.locations.load(JSON.parse(saved) as unknown as string);
              haveLocations = book.locations.length() > 0;
            }
          } catch { haveLocations = false; }
        }
        locsReadyRef.current = haveLocations;

        let target: string | undefined;
        const p = props.progress;
        if (p && !p.isFinished && p.locator && p.locator.startsWith("epubcfi(")) {
          target = p.locator;
        } else if (p && !p.isFinished && typeof p.percent === "number" &&
                   isFinite(p.percent) && p.percent > 0 && p.percent < 1) {
          if (!haveLocations) {
            setPhase("indexing");
            await book.locations.generate(LOC_CHUNK);
            if (destroyed) { release(); return; }
            haveLocations = true;
            locsReadyRef.current = true;
            persistLocations(book);
          }
          target = book.locations.cfiFromPercentage(p.percent);
        }
        try {
          await rendition.display(target);
        } catch {
          if (destroyed) return;
          await rendition.display();
        }
        if (destroyed) { release(); return; }
        setPhase("ready");

        if (!haveLocations) {
          try { await book.locations.generate(LOC_CHUNK); } catch {
            if (!destroyed) setWarning("Progress indexing failed. Reopen the book to retry.");
            return;
          }
          if (destroyed) { release(); return; }
          locsReadyRef.current = true;
          persistLocations(book);
          const cur = rendition.currentLocation() as { start?: { cfi?: string } } | null;
          if (cur?.start?.cfi) {
            const v = book.locations.percentageFromCfi(cur.start.cfi);
            if (typeof v === "number" && isFinite(v)) {
              setPercent(v);
              saver.save({ locator: cur.start.cfi, percent: v });
            }
          }
        }
      } catch (err) {
        if (!destroyed && !controller.signal.aborted) {
          release();
          setError(String((err as Error)?.message || err));
          setPhase("error");
        }
      }
    })();

    return () => {
      destroyed = true;
      controller.abort();
      release();
    };
  }, [props.editionId]);

  useEffect(() => {
    renditionRef.current?.themes.fontSize(`${fontSize}%`);
  }, [fontSize, phase]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e)) return;
      const r = renditionRef.current;
      if (e.key === "ArrowRight" && r) { e.preventDefault(); navigate((current) => current.next()); }
      else if (e.key === "ArrowLeft" && r) { e.preventDefault(); navigate((current) => current.prev()); }
      else if (e.key === "Escape") { if (tocOpen) setTocOpen(false); else props.onBack(); }
    };
    keyHandler.current = onKey;
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [tocOpen, props.onBack, navigate]);

  const chapter = chapterLabel(toc, sectionHref);
  const pillText = [percent != null ? `${Math.round(percent * 100)}%` : "", chapter].filter(Boolean).join(" · ");

  return (
    <div className="rd-in" style={readerOverlay}>
      <TopBar title={props.title} meta={phase === "indexing" ? "indexing…" : undefined} saveState={saver.state} onBack={props.onBack}>
        <button className={toolBtnCls} style={toolBtn} aria-label="Smaller text" title="Smaller text" disabled={fontSize <= FONT_MIN} onClick={() => setFontSize((f) => Math.max(FONT_MIN, f - FONT_STEP))}><IconMinus size={15} /></button>
        <span style={{ color: c.muted, fontSize: "0.72rem", minWidth: "2.6rem", textAlign: "center" }}>{fontSize}%</span>
        <button className={toolBtnCls} style={toolBtn} aria-label="Larger text" title="Larger text" disabled={fontSize >= FONT_MAX} onClick={() => setFontSize((f) => Math.min(FONT_MAX, f + FONT_STEP))}><IconPlus size={15} /></button>
        <button className={toolBtnCls} style={toolBtnActive(tocOpen)} aria-label="Contents" title="Contents" onClick={() => setTocOpen((v) => !v)}><IconContents size={15} /></button>
      </TopBar>
      <div style={readerStage}>
        <div ref={hostRef} style={{ position: "absolute", inset: 0 }} />
        {phase !== "ready" && <ReaderMessage text={phase === "error" ? error || "Could not open this EPUB." : phase === "indexing" ? "Indexing book for progress…" : "Loading book…"} onBack={props.onBack} />}
        {phase === "ready" && <TapZones onLeft={() => navigate((rendition) => rendition.prev())} onRight={() => navigate((rendition) => rendition.next())} />}
        {warning && phase === "ready" && <div role="status" style={{ position: "absolute", left: "1rem", right: "1rem", bottom: "3.5rem", zIndex: 12, padding: "0.6rem", background: c.bgRaised, color: c.textDim, fontSize: "0.8rem", textAlign: "center" }}>{warning}</div>}
        <PagePill text={pillText} watch={`${percent ?? ""}|${sectionHref}`} />
        {tocOpen && (
          <div>
            <button aria-label="Close contents" className="rd-scrim" style={{ position: "absolute", inset: 0, zIndex: 15, background: "rgba(0,0,0,0.45)", border: "none", padding: 0, cursor: "pointer" }} onClick={() => setTocOpen(false)} />
            <div className="rd-drawer" style={drawerPanel}>
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0.7rem 1rem", borderBottom: `1px solid ${c.line}` }}>
                <span style={{ fontWeight: 600, fontSize: "0.9rem" }}>Contents</span>
                <button className={toolBtnCls} style={toolBtn} aria-label="Close contents" title="Close" onClick={() => setTocOpen(false)}><IconClose size={15} /></button>
              </div>
              <div style={{ overflowY: "auto", flex: 1 }}>
                {toc.length === 0 && <p style={{ color: c.muted, fontSize: "0.85rem", padding: "1rem" }}>No table of contents.</p>}
                {toc.map((t, i) => {
                  const active = sameChapter(t.href, sectionHref);
                  return (
                    <button key={`${t.href}-${i}`} className="row-hit" style={{ ...drawerItem, position: "relative", paddingLeft: `${1 + t.depth * 0.7}rem`, ...(active ? { color: c.text, background: c.accentSoft } : {}) }} onClick={() => { setTocOpen(false); navigate((rendition) => rendition.display(t.href)); }}>
                      {active && <span style={{ position: "absolute", left: 0, top: "0.5rem", bottom: "0.5rem", width: "3px", borderRadius: "999px", background: c.accent }} />}
                      {t.label}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
