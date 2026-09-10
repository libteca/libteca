import { useEffect, useRef, useState } from "preact/hooks";
import { api, media, type SearchItem } from "../api";
import { Cover } from "../components/cover";
import { IconSearch, TypeIcon } from "../components/svg";
import { debounce, typeLabel } from "../util";
import { c, input, muted, sectionTitle } from "../styles";

const GROUP_ORDER = ["movies", "tv", "music", "audiobooks", "books", "comics"];

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

export function SearchBox() {
  const [q, setQ] = useState("");
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<SearchItem[]>([]);
  const boxRef = useRef<HTMLDivElement | null>(null);

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
    <div ref={boxRef} style={{ position: "relative", flex: "1 1 12rem", maxWidth: "26rem", minWidth: "8rem" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", ...input, padding: "0.4rem 0.7rem", borderRadius: "999px" }}>
        <span style={{ color: c.muted, display: "inline-flex" }}><IconSearch size={14} /></span>
        <input
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
          background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "12px",
          boxShadow: "0 12px 32px rgba(0,0,0,0.55)", overflow: "hidden", zIndex: 50,
          maxHeight: "60vh", overflowY: "auto",
        }}>
          {group(items).map(([type, list]) => (
            <div key={type}>
              <p style={{ margin: 0, padding: "0.5rem 0.8rem 0.25rem", fontSize: "0.7rem", textTransform: "uppercase", letterSpacing: "0.08em", color: c.faint }}>{typeLabel(type)}</p>
              {list.slice(0, 5).map((it) => (
                <a key={it.workId} href={`#/work?id=${it.workId}`} onClick={() => setOpen(false)}
                  style={{ display: "flex", gap: "0.7rem", alignItems: "center", padding: "0.45rem 0.8rem", textDecoration: "none", color: c.text }}>
                  <span style={{ width: "1.5rem", flexShrink: 0 }}><Cover has={it.hasCover} id={it.workId} title={it.title} /></span>
                  <span style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
                    <span style={{ fontSize: "0.85rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.title}</span>
                    {it.author && <span style={{ fontSize: "0.73rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.author}</span>}
                  </span>
                  <span style={{ marginLeft: "auto", color: c.faint, display: "inline-flex", flexShrink: 0 }}><TypeIcon type={type} size={13} /></span>
                </a>
              ))}
            </div>
          ))}
          <button onClick={go} style={{ display: "block", width: "100%", textAlign: "center", background: "none", border: "none", borderTop: `1px solid ${c.lineSoft}`, color: c.accent, fontSize: "0.8rem", padding: "0.55rem", cursor: "pointer", fontFamily: "inherit" }}>
            All results for “{q.trim()}”
          </button>
        </div>
      )}
    </div>
  );
}

export function SearchPage(props: { q: string }) {
  const [items, setItems] = useState<SearchItem[]>([]);
  const [done, setDone] = useState(false);

  useEffect(() => {
    setDone(false);
    api(`/search?q=${encodeURIComponent(props.q)}`)
      .then((d) => setItems(d.results || []))
      .catch(() => setItems([]))
      .finally(() => setDone(true));
  }, [props.q]);

  return (
    <div>
      <h2 style={sectionTitle}>Search</h2>
      <p style={{ ...muted, marginBottom: "1.4rem" }}>
        {items.length > 0 ? `${items.length} result${items.length === 1 ? "" : "s"} for “${props.q}”` : done ? `Nothing found for “${props.q}”` : "searching…"}
      </p>
      {group(items).map(([type, list]) => (
        <section key={type} style={{ marginBottom: "1.8rem" }}>
          <p style={{ margin: "0 0 0.8rem", fontSize: "0.75rem", textTransform: "uppercase", letterSpacing: "0.08em", color: c.faint }}>{typeLabel(type)}</p>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(8.5rem, 1fr))", gap: "1.2rem" }}>
            {list.map((it) => (
              <a key={it.workId} href={`#/work?id=${it.workId}`} style={{ textDecoration: "none", color: "inherit" }}>
                <Cover has={it.hasCover} id={it.workId} title={it.title} progress={it.percent || undefined} />
                <p style={{ margin: "0.5rem 0 0", fontWeight: 600, fontSize: "0.88rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.title}</p>
                <p style={{ margin: 0, color: c.muted, fontSize: "0.78rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.author}</p>
              </a>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
