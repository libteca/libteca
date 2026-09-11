import { useEffect, useState } from "preact/hooks";
import { api, type Library, type Work } from "../api";
import { useScan } from "../scan";
import { useRefreshMeta } from "../refresh-meta";
import { Cover } from "../components/cover";
import { CardProgress, EmptyState, SkeletonGrid } from "../components/rail";
import { IconChevronDown, IconPlay, TypeIcon } from "../components/svg";
import { coverRatio, typeLabel } from "../util";
import {
  c, card, cardMeta, cardTitleWrap, filterBtn, filterBtnOn, filterTrack, ghostBtn,
  gridFor, libToolbar, muted, selectChevron, selectWrap, tab, tabActive, tabRow,
} from "../styles";

const TYPE_ORDER = ["movies", "tv", "music", "audiobooks", "books", "comics", "podcasts"];

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

export function LibraryView(props: { lib?: number; type?: string }) {
  const [libs, setLibs] = useState<Library[]>([]);
  const [lib, setLib] = useState<number>(props.lib ?? 0);
  const [works, setWorks] = useState<Work[]>([]);
  const [sort, setSort] = useState("title");
  const [dir, setDir] = useState("asc");
  const [filter, setFilter] = useState("all");
  const [loaded, setLoaded] = useState(false);
  const [loadErr, setLoadErr] = useState(false);

  const scan = useScan(() => { refreshWorks(); });
  const meta = useRefreshMeta(() => { refreshWorks(); });

  const refreshWorks = () => {
    if (!lib) {
      setWorks([]);
      setLoaded(true);
      return;
    }
    api(`/libraries/${lib}/works?sort=${sort}&dir=${dir}&filter=${filter}`)
      .then((d) => { setWorks(Array.isArray(d) ? d : []); setLoadErr(!Array.isArray(d)); })
      .catch(() => { setWorks([]); setLoadErr(true); })
      .finally(() => setLoaded(true));
  };

  useEffect(() => { api("/libraries").then((r) => { if (Array.isArray(r)) setLibs(r); }).catch(() => setLibs([])); }, [props.lib]);
  useEffect(() => {
    if (!libs.length || lib) return;
    for (const t of TYPE_ORDER) {
      const found = libs.find((l) => l.type === t);
      if (found) { setLib(found.id); return; }
    }
    setLib(libs[0].id);
  }, [libs]);
  useEffect(() => { if (props.lib) setLib(props.lib); }, [props.lib]);
  useEffect(() => {
    if (!props.type || !libs.length) return;
    const m = libs.find((l) => l.type === props.type);
    if (m && m.id !== lib) setLib(m.id);
  }, [props.type, libs]);
  useEffect(refreshWorks, [lib, sort, dir, filter]);

  const activeLib = libs.find((l) => l.id === lib);
  const types = TYPE_ORDER.filter((t) => libs.some((l) => l.type === t));
  const typeLibs = libs.filter((l) => l.type === activeLib?.type);
  const unread = activeLib?.type === "books" || activeLib?.type === "comics";
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
        <div>
          <div style={tabRow}>
            {types.map((t) => {
              const first = libs.find((l) => l.type === t)!;
              const current = activeLib?.type === t;
              const target = current ? lib : first.id;
              return (
                <a key={t} href={`#/library?lib=${target}`} style={{ textDecoration: "none" }}
                  onClick={() => { if (!current) setLib(first.id); }}>
                  <span style={current ? tabActive : tab}>{typeLabel(t)}</span>
                </a>
              );
            })}
          </div>
          {typeLibs.length > 1 && (
            <div style={tabRow}>
              {typeLibs.map((l) => (
                <a key={l.id} href={`#/library?lib=${l.id}`} style={{ textDecoration: "none" }}
                  onClick={() => setLib(l.id)}>
                  <span style={l.id === lib ? tabActive : tab}>{l.name}</span>
                </a>
              ))}
            </div>
          )}
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
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} aria-label={sort === "added" || sort === "updated" ? (dir === "desc" ? "Newest first" : "Oldest first") : dir === "asc" ? "Ascending" : "Descending"}
            onClick={() => setDir(dir === "asc" ? "desc" : "asc")}>
            {sort === "added" || sort === "updated" ? (dir === "desc" ? "Newest" : "Oldest") : dir === "asc" ? "A–Z" : "Z–A"}
          </button>
          <div style={filterTrack} role="tablist" aria-label="Filter">
            {FILTERS.map((f) => (
              <button
                key={f.value}
                role="tab"
                aria-selected={filter === f.value}
                style={filter === f.value ? filterBtnOn : filterBtn}
                onClick={() => setFilter(f.value)}
              >{f.value === "unplayed" && unread ? "Unread" : f.label}</button>
            ))}
          </div>
          <span style={{ flex: 1, minWidth: "0.4rem" }} />
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} disabled={meta.running || scan.scanning || !lib} onClick={() => { if (lib) meta.start(lib); }}>
            {meta.running ? "Improving" : "Match"}
          </button>
          <button className="press" style={{ ...ghostBtn, border: "none", color: c.muted, padding: "0.35rem 0" }} disabled={scan.scanning || meta.running || !lib} onClick={() => { if (lib) scan.start(lib); }}>
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
        ? loadErr && loaded
          ? <EmptyState title="Couldn't reach the server" hint="The library failed to load.">
              <button className="press" style={ghostBtn} onClick={refreshWorks}>Retry</button>
            </EmptyState>
          : loaded
            ? <EmptyState title={filter === "all" ? "No works yet" : "Nothing matches this filter"}
                icon={<TypeIcon type={activeLib?.type || ""} size={22} />}
                hint={filter === "all" ? "Add a library in Admin and scan." : "Try a different filter."} />
            : <SkeletonGrid square={ratio === "square"} />
        : <div style={gridFor(activeLib?.type)} className="cover-grid">
            {works.map((w) => {
              const meta = w.author || w.subtitle;
              return (
                <a key={w.id} href={`#/work?id=${w.id}`} className="cover-card" style={card}>
                  <div className="cardwrap">
                    <Cover has={w.hasCover} id={w.id} title={w.title} ratio={ratio} />
                    <div className="cardover"><div className="cardplay"><IconPlay size={18} /></div></div>
                    {w.percent && w.percent > 0 ? <CardProgress pct={w.percent} /> : null}
                  </div>
                  <p style={cardTitleWrap}>{w.title}</p>
                  {meta ? <p style={cardMeta}>{meta}</p> : null}
                </a>
              );
            })}
          </div>}
    </div>
  );
}
