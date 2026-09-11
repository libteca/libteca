import { useEffect, useRef, useState } from "preact/hooks";
import type { Book, Contents, NavItem, Rendition } from "epubjs";
import { media } from "../api";
import { c } from "../styles";
import {
  drawerItem, drawerPanel, IconClose, IconContents, IconMinus, IconPlus,
  isTypingTarget, PagePill, ReaderMessage, readerOverlay, readerStage, TopBar, TapZones, toolBtn, toolBtnActive, toolBtnCls,
  useProgressSaver, type ProgressPost, type ReadingProgress,
} from "./shared";

const FONT_MIN = 70;
const FONT_MAX = 220;
const FONT_STEP = 10;
const LOC_CHUNK = 1000;

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

export function EpubReader(props: { editionId: number; title: string; progress: ReadingProgress | null; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const bookRef = useRef<Book | null>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const locsReadyRef = useRef(false);
  const [phase, setPhase] = useState<"loading" | "indexing" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [toc, setToc] = useState<TocEntry[]>([]);
  const [tocOpen, setTocOpen] = useState(false);
  const [fontSize, setFontSize] = useState(100);
  const [percent, setPercent] = useState<number | null>(null);
  const [sectionHref, setSectionHref] = useState("");
  const saver = useProgressSaver(props.editionId);
  const keyHandler = useRef<(e: KeyboardEvent) => void>(() => {});

  useEffect(() => {
    let destroyed = false;
    const host = hostRef.current;
    if (!host) return;

    const persistLocations = (book: Book) => {
      try { localStorage.setItem(`libteca-epub-loc-${props.editionId}`, book.locations.save()); } catch { /* storage unavailable */ }
    };

    let book: Book | null = null;
    let rendition: Rendition | null = null;
    let keyDoc: Document | null = null;
    const onDocKey = (e: Event) => keyHandler.current(e as KeyboardEvent);
    (async () => {
      try {
        const res = await fetch(media(`/editions/${props.editionId}/download`));
        if (!res.ok) throw new Error(`Download failed (${res.status})`);
        const data = await res.arrayBuffer();
        if (destroyed) return;
        const epubjs = await import("epubjs");
        book = new epubjs.Book();
        await book.open(data);
        if (destroyed) { try { book?.destroy(); } catch { /* already gone */ } return; }
        bookRef.current = book;

        rendition = new epubjs.Rendition(book, { width: "100%", height: "100%", spread: "auto", flow: "paginated" });
        renditionRef.current = rendition;
        await rendition.attachTo(host);
        rendition.on("rendered", (_section: unknown, contents: Contents) => {
          keyDoc?.removeEventListener("keydown", onDocKey);
          keyDoc = contents?.document || null;
          keyDoc?.addEventListener("keydown", onDocKey);
        });
        rendition.themes.default({ body: { color: c.text, background: c.bg } });
        rendition.themes.override("color", c.text, true);
        rendition.themes.override("background", c.bg, true);
        rendition.themes.fontSize("100%");

        rendition.on("relocated", (loc: { start?: { cfi?: string }; atEnd?: boolean }) => {
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
          setSectionHref(loc?.start?.href || "");
        });

        const nav = await book.loaded.navigation.catch(() => null);
        if (destroyed) { try { book?.destroy(); } catch { /* already gone */ } return; }
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

        const locKey = `libteca-epub-loc-${props.editionId}`;
        let haveLocations = false;
        try {
          const saved = localStorage.getItem(locKey);
          if (saved) {
            book.locations.load(JSON.parse(saved) as unknown as string);
            haveLocations = book.locations.length() > 0;
          }
        } catch { haveLocations = false; }
        locsReadyRef.current = haveLocations;

        let target: string | undefined;
        const p = props.progress;
        if (p && !p.isFinished && p.locator && p.locator.startsWith("epubcfi(")) {
          target = p.locator;
        } else if (p && !p.isFinished && p.percent && p.percent > 0 && p.percent < 1 && !haveLocations) {
          setPhase("indexing");
          await book.locations.generate(LOC_CHUNK);
          if (destroyed) { try { book?.destroy(); } catch { /* already gone */ } return; }
          haveLocations = true;
          locsReadyRef.current = true;
          persistLocations(book);
          target = book.locations.cfiFromPercentage(p.percent);
        }
        try {
          await rendition.display(target);
        } catch {
          await rendition.display();
        }
        if (destroyed) { try { book?.destroy(); } catch { /* already gone */ } return; }
        setPhase("ready");

        if (!haveLocations) {
          await book.locations.generate(LOC_CHUNK);
          if (destroyed) { try { book?.destroy(); } catch { /* already gone */ } return; }
          locsReadyRef.current = true;
          persistLocations(book);
          const cur = rendition.currentLocation() as { start?: { cfi?: string } } | null;
          if (cur?.start?.cfi) {
            const v = book.locations.percentageFromCfi(cur.start.cfi);
            if (typeof v === "number" && isFinite(v)) setPercent(v);
          }
        }
      } catch (err) {
        if (!destroyed) {
          setError(String((err as Error)?.message || err));
          setPhase("error");
        }
      }
    })();

    return () => {
      destroyed = true;
      renditionRef.current = null;
      bookRef.current = null;
      keyDoc?.removeEventListener("keydown", onDocKey);
      try { rendition?.destroy(); } catch { /* already gone */ }
      try { book?.destroy(); } catch { /* already gone */ }
    };
  }, [props.editionId]);

  useEffect(() => {
    renditionRef.current?.themes.fontSize(`${fontSize}%`);
  }, [fontSize, phase]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e)) return;
      const r = renditionRef.current;
      if (e.key === "ArrowRight" && r) { e.preventDefault(); void r.next(); }
      else if (e.key === "ArrowLeft" && r) { e.preventDefault(); void r.prev(); }
      else if (e.key === "Escape") { if (tocOpen) setTocOpen(false); else props.onBack(); }
    };
    keyHandler.current = onKey;
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [tocOpen, props.onBack]);

  const chapter = chapterLabel(toc, sectionHref);
  const pillText = [percent != null ? `${Math.round(percent * 100)}%` : "", chapter].filter(Boolean).join(" · ");

  return (
    <div style={readerOverlay}>
      <TopBar title={props.title} meta={phase === "indexing" ? "indexing…" : undefined} saveState={saver.state} onBack={props.onBack}>
        <button className={toolBtnCls} style={toolBtn} aria-label="Smaller text" title="Smaller text" disabled={fontSize <= FONT_MIN} onClick={() => setFontSize((f) => Math.max(FONT_MIN, f - FONT_STEP))}><IconMinus size={15} /></button>
        <span style={{ color: c.muted, fontSize: "0.72rem", minWidth: "2.6rem", textAlign: "center" }}>{fontSize}%</span>
        <button className={toolBtnCls} style={toolBtn} aria-label="Larger text" title="Larger text" disabled={fontSize >= FONT_MAX} onClick={() => setFontSize((f) => Math.min(FONT_MAX, f + FONT_STEP))}><IconPlus size={15} /></button>
        <button className={toolBtnCls} style={toolBtnActive(tocOpen)} aria-label="Contents" title="Contents" onClick={() => setTocOpen((v) => !v)}><IconContents size={15} /></button>
      </TopBar>
      <div style={readerStage}>
        <div ref={hostRef} style={{ position: "absolute", inset: 0 }} />
        {phase !== "ready" && <ReaderMessage text={phase === "error" ? error || "Could not open this EPUB." : phase === "indexing" ? "Indexing book for progress…" : "Loading book…"} onBack={props.onBack} />}
        {phase === "ready" && <TapZones onLeft={() => void renditionRef.current?.prev()} onRight={() => void renditionRef.current?.next()} />}
        <PagePill text={pillText} watch={`${percent ?? ""}|${sectionHref}`} />
        {tocOpen && (
          <div>
            <button aria-label="Close contents" style={{ position: "absolute", inset: 0, zIndex: 15, background: "rgba(0,0,0,0.45)", border: "none", padding: 0, cursor: "pointer" }} onClick={() => setTocOpen(false)} />
            <div style={drawerPanel}>
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0.7rem 1rem", borderBottom: `1px solid ${c.line}` }}>
                <span style={{ fontWeight: 600, fontSize: "0.9rem" }}>Contents</span>
                <button className={toolBtnCls} style={toolBtn} aria-label="Close contents" title="Close" onClick={() => setTocOpen(false)}><IconClose size={15} /></button>
              </div>
              <div style={{ overflowY: "auto", flex: 1 }}>
                {toc.length === 0 && <p style={{ color: c.muted, fontSize: "0.85rem", padding: "1rem" }}>No table of contents.</p>}
                {toc.map((t, i) => {
                  const active = sameChapter(t.href, sectionHref);
                  return (
                    <button key={`${t.href}-${i}`} style={{ ...drawerItem, paddingLeft: `${1 + t.depth * 0.7}rem`, ...(active ? { color: c.text, background: c.accentSoft } : {}) }} onClick={() => { setTocOpen(false); void renditionRef.current?.display(t.href); }}>{t.label}</button>
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
