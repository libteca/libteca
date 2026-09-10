import { useEffect, useState } from "preact/hooks";
import { api, type Library, type RecentItem, type ResumeItem } from "../api";
import { EmptyState, Rail, RailCard } from "../components/rail";
import { TypeIcon } from "../components/svg";
import { fmt, typeLabel } from "../util";
import { c, muted, primaryBtn, sectionTitle, tab } from "../styles";

export function Home() {
  const [resume, setResume] = useState<ResumeItem[]>([]);
  const [recent, setRecent] = useState<RecentItem[]>([]);
  const [libs, setLibs] = useState<Library[]>([]);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    // Contract endpoints may not exist yet on older servers; degrade quietly.
    Promise.allSettled([
      api("/resume").then((d) => setResume(d.items || [])),
      api("/recent?limit=12").then((d) => setRecent(d.items || [])),
      api("/libraries").then(setLibs),
    ]).then(() => setLoaded(true));
  }, []);

  const watching = resume.filter((r) => r.libraryType === "movies" || r.libraryType === "tv");
  const listening = resume.filter((r) => r.libraryType === "audiobooks" || r.libraryType === "music");

  return (
    <div>
      {watching.length > 0 && (
        <Rail title="Continue Watching">
          {watching.map((r) => (
            <RailCard key={r.editionId} id={r.workId} title={r.title} meta={r.libraryType === "tv" ? `S·E · ${fmt(r.durationSecs - r.positionSecs)} left` : fmt(r.durationSecs - r.positionSecs) + " left"} hasCover={r.hasCover} progress={r.percent} />
          ))}
        </Rail>
      )}
      {listening.length > 0 && (
        <Rail title="Continue Listening">
          {listening.map((r) => (
            <RailCard key={r.editionId} id={r.workId} title={r.title} meta={r.author} hasCover={r.hasCover} progress={r.percent} />
          ))}
        </Rail>
      )}
      {recent.length > 0 && (
        <Rail title="Recently Added">
          {recent.map((r) => (
            <RailCard key={r.workId} id={r.workId} title={r.title} meta={typeLabel(r.libraryType)} hasCover={r.hasCover} />
          ))}
        </Rail>
      )}
      <section style={{ marginBottom: "2.2rem" }}>
        <h2 style={sectionTitle}>Libraries</h2>
        {libs.length === 0 ? (
          loaded && (
            <EmptyState title="No libraries yet" hint="Point libteca at a folder of media and it does the rest.">
              <a href="#/admin" style={primaryBtn}>Open Admin</a>
            </EmptyState>
          )
        ) : (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(10rem, 1fr))", gap: "0.9rem" }}>
            {libs.map((l) => (
              <a key={l.id} href={`#/library?lib=${l.id}`} style={{ ...tab, textDecoration: "none", display: "flex", alignItems: "center", gap: "0.6rem", padding: "0.8rem 1rem", justifyContent: "flex-start", borderColor: c.line }}>
                <span style={{ color: c.muted }}><TypeIcon type={l.type} size={18} /></span>
                <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", minWidth: 0 }}>
                  <span style={{ fontWeight: 600, fontSize: "0.9rem", color: "inherit", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", maxWidth: "100%" }}>{l.name}</span>
                  <span style={{ fontSize: "0.75rem", color: c.muted }}>{typeLabel(l.type)}</span>
                </span>
              </a>
            ))}
          </div>
        )}
      </section>
      {loaded && resume.length === 0 && recent.length === 0 && libs.length > 0 && (
        <EmptyState title="Nothing on the shelves yet" hint="Scan a library and everything you add lands here." />
      )}
      {!loaded && resume.length === 0 && recent.length === 0 && (
        <p style={muted}>loading…</p>
      )}
    </div>
  );
}
