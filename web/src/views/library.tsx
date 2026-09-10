import { useEffect, useState } from "preact/hooks";
import { api, type Library, type Work } from "../api";
import { useScan } from "../scan";
import { useRefreshMeta } from "../refresh-meta";
import { Cover } from "../components/cover";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconChevronDown } from "../components/svg";
import { coverRatio } from "../util";
import {
  c, card, cardMeta, cardTitleWrap, filterBtn, filterBtnOn, filterTrack, ghostBtn,
  gridFor, libToolbar, muted, selectChevron, selectWrap, tab, tabActive,
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
  const meta = useRefreshMeta(() => { refreshWorks(); });

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
  const ratio = coverRatio(activeLib?.type);
  const scanLine = scan.event && (scan.scanning
    ? `Scanning ${scan.event.filesSeen} files${scan.event.worksChanged ? ` · ${scan.event.worksChanged} changed` : ""}`
    : scan.event.status === "error" ? `Scan failed${scan.event.error ? `: ${scan.event.error}` : ""}`
    : scan.event.filesAdded ? `Scan done · ${scan.event.filesAdded} added` : "");

  const metaLine = meta.running
    ? `Improving metadata${meta.event?.total ? ` · ${meta.event.matched || 0} of ${meta.event.total}` : ""}`
    : meta.event?.status === "done"
      ? `Matched ${meta.event.matched ?? 0} · auto-applied ${meta.event.autoApplied ?? 0}`
      : meta.event?.status === "error"
        ? (meta.event.error || "Metadata refresh failed")
        : "";

  return (
    <div>
      <div style={libToolbar}>
        <div style={{ display: "flex", gap: "0.35rem", flexWrap: "wrap" }}>
          {libs.map((l) => (
            <a key={l.id} href={`#/library?lib=${l.id}`} style={{ textDecoration: "none" }}
              onClick={() => setLib(l.id)}>
              <span style={l.id === lib ? tabActive : tab}>{l.name}</span>
            </a>
          ))}
        </div>
        <div style={{ display: "flex", gap: "0.5rem", alignItems: "center", flexWrap: "wrap" }}>
          <div style={selectWrap}>
            <select
              className="pill"
              aria-label="Sort"
              value={sort}
              onChange={(e) => setSort((e.target as HTMLSelectElement).value)}
            >
              {SORTS.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
            <span style={selectChevron}><IconChevronDown size={12} /></span>
          </div>
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} aria-label={dir === "asc" ? "Ascending" : "Descending"}
            onClick={() => setDir(dir === "asc" ? "desc" : "asc")}>
            {dir === "asc" ? "A–Z" : "Z–A"}
          </button>
          <div style={filterTrack} role="tablist" aria-label="Filter">
            {FILTERS.map((f) => (
              <button
                key={f.value}
                role="tab"
                aria-selected={filter === f.value}
                style={filter === f.value ? filterBtnOn : filterBtn}
                onClick={() => setFilter(f.value)}
              >{f.label}</button>
            ))}
          </div>
          <span style={{ flex: 1, minWidth: "0.4rem" }} />
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} disabled={meta.running || scan.scanning} onClick={() => { if (lib) meta.start(lib); }}>
            {meta.running ? "Improving" : "Match"}
          </button>
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} disabled={scan.scanning} onClick={() => scan.start(lib)}>
            {scan.scanning ? "Scanning" : "Scan"}
          </button>
        </div>
      </div>
      {scanLine && (
        <p style={{ ...muted, marginBottom: "0.85rem" }}>{scanLine}</p>
      )}
      {metaLine && (
        <p style={{ ...muted, marginBottom: "0.85rem" }}>
          {metaLine}
          {meta.event?.status === "done" && <> · <a href="#/matching" style={{ color: c.accent }}>review inbox</a></>}
        </p>
      )}
      {works.length === 0
        ? loaded
          ? <EmptyState title={filter === "all" ? "No works yet" : "Nothing matches this filter"}
              hint={filter === "all" ? "Add a library in Admin and scan." : "Try a different filter."} />
          : <QuietLoad />
        : <div style={gridFor(activeLib?.type)}>
            {works.map((w) => (
              <a key={w.id} href={`#/work?id=${w.id}`} className="cover-card" style={card}>
                <Cover has={w.hasCover} id={w.id} title={w.title} ratio={ratio} />
                <p style={cardTitleWrap}>{w.title}</p>
                <p style={cardMeta}>{w.author}</p>
              </a>
            ))}
          </div>}
    </div>
  );
}
