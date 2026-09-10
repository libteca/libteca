import { useEffect, useState } from "preact/hooks";
import { api, type Library, type RecentItem, type ResumeItem } from "../api";
import { Cover } from "../components/cover";
import { EmptyState, QuietLoad, Rail, RailCard } from "../components/rail";
import { coverRatio, fmt, typeLabel } from "../util";
import { c, muted, primaryBtn, railTitle, workTitle } from "../styles";

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
  const hero = watching[0] || listening[0];
  const restWatch = watching.slice(hero && hero === watching[0] ? 1 : 0);
  const restListen = listening.slice(hero && hero === listening[0] ? 1 : 0);

  return (
    <div>
      {hero && (
        <a href={`#/work?id=${hero.workId}`} className="press" style={{
          display: "flex", gap: "2rem", alignItems: "center",
          margin: "0.4rem 0 2.6rem", textDecoration: "none", color: "inherit",
        }}>
          <div style={{ width: "10.5rem", flexShrink: 0, maxWidth: "38%" }}>
            <Cover has={hero.hasCover} id={hero.workId} title={hero.title} progress={hero.percent} ratio={coverRatio(hero.libraryType)} />
          </div>
          <div style={{ minWidth: 0 }}>
            <p style={{ margin: "0 0 0.45rem", fontSize: "0.72rem", letterSpacing: "0.08em", textTransform: "uppercase", color: c.faint, fontWeight: 600 }}>
              {watching[0] === hero ? "Continue watching" : "Continue listening"}
            </p>
            <h1 style={{ ...workTitle, fontSize: "clamp(1.8rem, 4vw, 2.8rem)", margin: "0 0 0.5rem" }}>{hero.title}</h1>
            <p style={muted}>
              {hero.author ? `${hero.author}  ·  ` : ""}
              {fmt(Math.max(0, hero.durationSecs - hero.positionSecs))} left
            </p>
          </div>
        </a>
      )}
      {restWatch.length > 0 && (
        <Rail title="Continue Watching">
          {restWatch.map((r) => (
            <RailCard
              key={r.editionId}
              id={r.workId}
              title={r.title}
              meta={fmt(r.durationSecs - r.positionSecs) + " left"}
              hasCover={r.hasCover}
              progress={r.percent}
            />
          ))}
        </Rail>
      )}
      {restListen.length > 0 && (
        <Rail title="Continue Listening">
          {restListen.map((r) => (
            <RailCard
              key={r.editionId}
              id={r.workId}
              title={r.title}
              meta={r.author}
              hasCover={r.hasCover}
              progress={r.percent}
              ratio={coverRatio(r.libraryType)}
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
          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.35rem 1.6rem" }}>
            {libs.map((l) => (
              <a key={l.id} href={`#/library?lib=${l.id}`} className="press" style={{
                color: c.textDim, textDecoration: "none", fontSize: "0.95rem", letterSpacing: "-0.01em",
                padding: "0.35rem 0", minHeight: "36px", display: "inline-flex", alignItems: "baseline", gap: "0.45rem",
              }}>
                {l.name}
                <span style={{ ...muted, fontSize: "0.75rem" }}>{typeLabel(l.type)}</span>
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
