import { useEffect, useRef, useState } from "preact/hooks";
import { api, type SearchItem } from "../api";
import { Cover } from "../components/cover";
import { CardProgress, QuietLoad } from "../components/rail";
import { IconPlay, IconSearch, TypeIcon } from "../components/svg";
import { coverRatio, debounce, typeLabel } from "../util";
import { c, card, cardMeta, cardTitleWrap, eyebrow, gridFor, muted, sectionTitle } from "../styles";

const GROUP_ORDER = ["movies", "tv", "music", "audiobooks", "books", "comics", "podcasts"];

function group(items: SearchItem[]) {
  const g = new Map<string, SearchItem[]>();
  for (const it of items) {
    const list = g.get(it.libraryType) || [];
    list.push(it);
    g.set(it.libraryType, list);
  }
  return [...g.entries()].sort((a, b) => {
    const ai = GROUP_ORDER.indexOf(a[0]), bi = GROUP_ORDER.indexOf(b[0]);
    return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
  });
}

function Hi(props: { text: string; q: string }) {
  const q = props.q.trim();
  if (q.length < 2) return <>{props.text}</>;
  const i = props.text.toLowerCase().indexOf(q.toLowerCase());
  if (i < 0) return <>{props.text}</>;
  return <>{props.text.slice(0, i)}<mark>{props.text.slice(i, i + q.length)}</mark>{props.text.slice(i + q.length)}</>;
}

let focusTrigger: (() => void) | null = null;

export function focusSearch() {
  focusTrigger?.();
}

export function SearchBox() {
  const [q, setQ] = useState("");
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<SearchItem[]>([]);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    focusTrigger = () => inputRef.current?.focus();
    return () => { focusTrigger = null; };
  }, []);

  const run = useRef(debounce((query: string) => {
    if (!query.trim()) { setItems([]); setOpen(false); return; }
    api(`/search?q=${encodeURIComponent(query)}`)
      .then((d) => { setItems(d.results || []); setOpen(true); })
      .catch(() => setItems([]));
  }, 250)).current;

  useEffect(() => run.cancel, []);

  useEffect(() => {
    const onDoc = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, []);

  const go = () => {
    if (q.trim()) location.hash = `#/search?q=${encodeURIComponent(q.trim())}`;
    setOpen(false);
  };

  return (
    <div ref={boxRef} style={{ position: "relative", flex: "1 1 11rem", maxWidth: "22rem", minWidth: "7.5rem" }}>
      <div className="search-wrap">
        <span style={{ color: c.muted, display: "inline-flex" }}><IconSearch size={14} /></span>
        <input
          ref={inputRef}
          value={q}
          placeholder="Search"
          onInput={(e) => { const v = (e.target as HTMLInputElement).value; setQ(v); run(v); }}
          onFocus={(e) => { if (items.length && (e.target as HTMLInputElement).value.trim()) setOpen(true); }}
          onKeyDown={(e) => {
            if (e.key === "Enter") { e.preventDefault(); go(); }
            else if (e.key === "Escape") setOpen(false);
          }}
          style={{ background: "none", border: "none", color: c.text, fontFamily: "inherit", fontSize: "0.88rem", outline: "none", width: "100%", padding: 0 }}
          aria-label="Search library"
        />
      </div>
      {open && items.length > 0 && (
        <div style={{
          position: "absolute", top: "calc(100% + 0.4rem)", left: 0, right: 0,
          background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "14px",
          boxShadow: "0 16px 40px rgba(0,0,0,0.55)", overflow: "hidden", zIndex: 50,
          maxHeight: "60vh", overflowY: "auto",
        }}>
          {group(items).map(([type, list]) => (
            <div key={type}>
              <p style={{ ...eyebrow, padding: "0.65rem 0.85rem 0.2rem", margin: 0 }}>{typeLabel(type)}</p>
              {list.slice(0, 5).map((it) => (
                <a key={it.workId} href={`#/work?id=${it.workId}`} onClick={() => setOpen(false)}
                  className="row-hit"
                  style={{ display: "flex", gap: "0.7rem", alignItems: "center", padding: "0.5rem 0.85rem", textDecoration: "none", color: c.text, minHeight: "44px" }}>
                  <span style={{ width: "2.1rem", flexShrink: 0 }}><Cover has={it.hasCover} id={it.workId} title={it.title} progress={it.percent || undefined} ratio={coverRatio(type)} /></span>
                  <span style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
                    <span style={{ fontSize: "0.85rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}><Hi text={it.title} q={q} /></span>
                    {it.author && <span style={{ fontSize: "0.73rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.author}</span>}
                  </span>
                  <span style={{ marginLeft: "auto", color: c.faint, display: "inline-flex", flexShrink: 0 }}><TypeIcon type={type} size={13} /></span>
                </a>
              ))}
            </div>
          ))}
          <button onClick={go} style={{ display: "block", width: "100%", textAlign: "center", background: "none", border: "none", borderTop: `1px solid ${c.lineSoft}`, color: c.accent, fontSize: "0.8rem", padding: "0.65rem", cursor: "pointer", fontFamily: "inherit", minHeight: "36px" }}>
            All results
          </button>
        </div>
      )}
    </div>
  );
}

export function SearchPage(props: { q: string }) {
  const [items, setItems] = useState<SearchItem[]>([]);
  const [done, setDone] = useState(false);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    setDone(false);
    setFailed(false);
    api(`/search?q=${encodeURIComponent(props.q)}`)
      .then((d) => {
        if (d && d.error) { setItems([]); setFailed(true); setDone(true); return; }
        setItems(d.results || []);
        setDone(true);
      })
      .catch(() => { setItems([]); setFailed(true); setDone(true); });
  }, [props.q, retry]);

  return (
    <div>
      <h2 style={sectionTitle}>{props.q}</h2>
      <p style={{ ...muted, marginBottom: "1.4rem" }}>
        {failed
          ? "Search failed."
          : items.length > 0
            ? `${items.length} result${items.length === 1 ? "" : "s"}`
            : done
              ? (props.q.trim().length < 2 ? "Keep typing — search needs at least 2 characters." : "Nothing found.")
              : ""}
        {failed && <button onClick={() => setRetry((n) => n + 1)} style={{ ...muted, background: "none", border: "none", cursor: "pointer", textDecoration: "underline", padding: 0, marginLeft: "0.5rem", font: "inherit" }}>Retry</button>}
      </p>
      {!done && items.length === 0 && <QuietLoad />}
      {group(items).map(([type, list]) => (
        <section key={type} style={{ marginBottom: "2.4rem" }}>
          <p style={eyebrow}>{typeLabel(type)}</p>
          <div style={gridFor(type)} className="cover-grid">
            {list.map((it) => (
              <a key={it.workId} href={`#/work?id=${it.workId}`} className="cover-card" style={card}>
                <div className="cardwrap">
                  <Cover has={it.hasCover} id={it.workId} title={it.title} ratio={coverRatio(type)} />
                  <div className="cardover"><div className="cardplay"><IconPlay size={18} /></div></div>
                  {it.percent ? <CardProgress pct={it.percent} /> : null}
                </div>
                <p style={cardTitleWrap}><Hi text={it.title} q={props.q} /></p>
                <p style={cardMeta}>{it.author}</p>
              </a>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
