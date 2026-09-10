import { useEffect, useState } from "preact/hooks";
import { api, type Library, type RecentItem, type ResumeItem } from "../api";
import { EmptyState, QuietLoad, Rail, RailCard } from "../components/rail";
import { TypeIcon } from "../components/svg";
import { coverRatio, fmt, typeLabel } from "../util";
import { cardMeta, cardTitle, libTile, libTileIcon, primaryBtn, railTitle } from "../styles";

export function Home() {
  const [resume, setResume] = useState<ResumeItem[]>([]);
  const [recent, setRecent] = useState<RecentItem[]>([]);
  const [libs, setLibs] = useState<Library[]>([]);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Promise.allSettled([
      api("/resume").then((d) => setResume(d.items || [])),
      api("/recent?limit=12").then((d) => setRecent(d.items || [])),
      api("/libraries").then(setLibs),
    ]).then(() => setLoaded(true));
  }, []);

  const watching = resume.filter((r) => r.libraryType === "movies" || r.libraryType === "tv");
  const listening = resume.filter((r) => r.libraryType === "audiobooks" || r.libraryType === "music");
  const heroWatch = watching.length > 0;
  const heroListen = !heroWatch && listening.length > 0;

  return (
    <div>
      {watching.length > 0 && (
        <Rail title="Continue Watching">
          {watching.map((r) => (
            <RailCard
              key={r.editionId}
              id={r.workId}
              title={r.title}
              meta={r.libraryType === "tv" ? `${fmt(r.durationSecs - r.positionSecs)} left` : fmt(r.durationSecs - r.positionSecs) + " left"}
              hasCover={r.hasCover}
              progress={r.percent}
              size={heroWatch ? 12.2 : 11}
            />
          ))}
        </Rail>
      )}
      {listening.length > 0 && (
        <Rail title="Continue Listening">
          {listening.map((r) => (
            <RailCard
              key={r.editionId}
              id={r.workId}
              title={r.title}
              meta={r.author}
              hasCover={r.hasCover}
              progress={r.percent}
              ratio={coverRatio(r.libraryType)}
              size={heroListen ? 12.2 : 11}
            />
          ))}
        </Rail>
      )}
      {recent.length > 0 && (
        <Rail title="Recently Added">
          {recent.map((r) => (
            <RailCard
              key={r.workId}
              id={r.workId}
              title={r.title}
              meta={typeLabel(r.libraryType)}
              hasCover={r.hasCover}
              ratio={coverRatio(r.libraryType)}
            />
          ))}
        </Rail>
      )}
      <section style={{ marginBottom: "2.2rem" }}>
        <h2 style={railTitle}>Libraries</h2>
        {libs.length === 0 ? (
          loaded && (
            <EmptyState title="No libraries yet" hint="Point libteca at a folder of media and it does the rest.">
              <a href="#/admin" style={primaryBtn}>Open Admin</a>
            </EmptyState>
          )
        ) : (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(14.5rem, 1fr))", gap: "0.7rem" }}>
            {libs.map((l) => (
              <a key={l.id} href={`#/library?lib=${l.id}`} className="lib-tile" style={libTile}>
                <span style={libTileIcon}><TypeIcon type={l.type} size={16} /></span>
                <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", minWidth: 0 }}>
                  <span style={{ ...cardTitle, margin: 0 }}>{l.name}</span>
                  <span style={cardMeta}>{typeLabel(l.type)}</span>
                </span>
              </a>
            ))}
          </div>
        )}
      </section>
      {loaded && resume.length === 0 && recent.length === 0 && libs.length > 0 && (
        <EmptyState title="Nothing on the shelves yet" hint="Scan a library and everything you add lands here." />
      )}
      {!loaded && resume.length === 0 && recent.length === 0 && <QuietLoad />}
    </div>
  );
}
