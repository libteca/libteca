import { useEffect, useState } from "preact/hooks";
import { api, type Library, type Work } from "../api";
import { useScan } from "../scan";
import { Cover } from "../components/cover";
import { EmptyState } from "../components/rail";
import { IconScan } from "../components/svg";
import {
  card, cardMeta, cardTitle, c, ghostBtn, grid, muted, rowBetween, tab, tabActive,
} from "../styles";

const SORTS = [
  { value: "title", label: "Title" },
  { value: "author", label: "Author" },
  { value: "added", label: "Recently Added" },
  { value: "updated", label: "Recently Updated" },
];

const FILTERS = [
  { value: "all", label: "All" },
  { value: "in_progress", label: "In Progress" },
  { value: "unplayed", label: "Unplayed" },
  { value: "finished", label: "Finished" },
];

export function LibraryView(props: { lib?: number }) {
  const [libs, setLibs] = useState<Library[]>([]);
  const [lib, setLib] = useState<number>(props.lib ?? 0);
  const [works, setWorks] = useState<Work[]>([]);
  const [sort, setSort] = useState("title");
  const [dir, setDir] = useState("asc");
  const [filter, setFilter] = useState("all");
  const [loaded, setLoaded] = useState(false);

  const scan = useScan(() => { refreshWorks(); });
  const refreshWorks = () => {
    if (!lib) return;
    api(`/libraries/${lib}/works?sort=${sort}&dir=${dir}&filter=${filter}`)
      .then(setWorks)
      .catch(() => setWorks([]))
      .finally(() => setLoaded(true));
  };

  useEffect(() => { api("/libraries").then(setLibs).catch(() => setLibs([])); }, []);
  useEffect(() => { if (libs.length && !lib) setLib(libs[0].id); }, [libs]);
  useEffect(() => { if (props.lib) setLib(props.lib); }, [props.lib]);
  useEffect(refreshWorks, [lib, sort, dir, filter]);

  const activeLib = libs.find((l) => l.id === lib);
  const scanLine = scan.event && (scan.scanning
    ? `Scanning… ${scan.event.filesSeen} files${scan.event.worksChanged ? ` · ${scan.event.worksChanged} changed` : ""}`
    : scan.event.status === "error" ? `Scan failed${scan.event.error ? `: ${scan.event.error}` : ""}`
    : scan.event.filesAdded ? `Scan done · ${scan.event.filesAdded} added` : "");

  return (
    <div>
      <div style={rowBetween}>
        <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap" }}>
          {libs.map((l) => (
            <a key={l.id} href={`#/library?lib=${l.id}`} style={{ textDecoration: "none" }}
              onClick={() => setLib(l.id)}>
              <span style={l.id === lib ? tabActive : tab}>{l.name}</span>
            </a>
          ))}
        </div>
        <div style={{ display: "flex", gap: "0.6rem", alignItems: "center" }}>
          <select style={{ background: c.bgRaised, color: c.textDim, border: `1px solid ${c.line}`, borderRadius: "999px", padding: "0.32rem 0.7rem", fontSize: "0.85rem", fontFamily: "inherit" }}
            value={sort} onChange={(e) => setSort((e.target as HTMLSelectElement).value)}>
            {SORTS.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>
          <button style={ghostBtn} title={dir === "asc" ? "Ascending" : "Descending"}
            onClick={() => setDir(dir === "asc" ? "desc" : "asc")}>
            {dir === "asc" ? "A–Z" : "Z–A"}
          </button>
          <div style={{ display: "flex", gap: "0.25rem", border: `1px solid ${c.line}`, borderRadius: "999px", padding: "0.15rem" }}>
            {FILTERS.map((f) => (
              <button key={f.value}
                style={filter === f.value
                  ? { border: "none", borderRadius: "999px", padding: "0.22rem 0.75rem", fontSize: "0.8rem", cursor: "pointer", background: c.text, color: c.bg, fontWeight: 600, fontFamily: "inherit" }
                  : { border: "none", borderRadius: "999px", padding: "0.22rem 0.75rem", fontSize: "0.8rem", cursor: "pointer", background: "none", color: c.muted, fontFamily: "inherit" }}
                onClick={() => setFilter(f.value)}>{f.label}</button>
            ))}
          </div>
          <button style={ghostBtn} disabled={scan.scanning} onClick={() => scan.start(lib)}>
            <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
              <IconScan size={14} />
              {scan.scanning ? "Scanning…" : "Scan"}
            </span>
          </button>
        </div>
      </div>
      {(scanLine || activeLib) && (
        <p style={{ ...muted, marginBottom: "1rem", minHeight: "1.2em" }}>{scanLine || (activeLib ? `${activeLib.path}` : "")}</p>
      )}
      {works.length === 0
        ? loaded
          ? <EmptyState title={filter === "all" ? "No works yet" : "Nothing matches this filter"}
              hint={filter === "all" ? "Add a library in Admin and scan." : "Try a different filter."} />
          : <p style={muted}>loading…</p>
        : <div style={grid}>
            {works.map((w) => (
              <a key={w.id} href={`#/work?id=${w.id}`} style={card}>
                <Cover has={w.hasCover} id={w.id} title={w.title} />
                <p style={cardTitle}>{w.title}</p>
                <p style={cardMeta}>{w.author}</p>
              </a>
            ))}
          </div>}
    </div>
  );
}
