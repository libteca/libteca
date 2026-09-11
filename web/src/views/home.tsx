import { useEffect, useState } from "preact/hooks";
import { api, media, type Library, type NextUpItem, type RecentItem, type ResumeItem } from "../api";
import { Cover } from "../components/cover";
import { EmptyState, QuietLoad, Rail, RailCard } from "../components/rail";
import {
  IconBook,
  IconComic,
  IconFilm,
  IconHeadphones,
  IconMusic,
  IconPlay,
  IconPodcast,
  IconTv,
  TypeIcon,
} from "../components/svg";
import { coverRatio, fmt, typeLabel } from "../util";
import { c, eyebrow, libTile, libTileIcon, muted, primaryBtn, railTitle, workTitle } from "../styles";

const HERO_LABEL = { watching: "Continue watching", listening: "Continue listening", reading: "Continue reading" };
const HERO_ACTION = { watching: "Play", listening: "Listen", reading: "Read" };

function libIcon(type: string) {
  switch (type) {
    case "movies": return <IconFilm size={17} />;
    case "tv": return <IconTv size={17} />;
    case "music": return <IconMusic size={17} />;
    case "audiobooks": return <IconHeadphones size={17} />;
    case "books": return <IconBook size={17} />;
    case "comics": return <IconComic size={17} />;
    case "podcasts": return <IconPodcast size={17} />;
    default: return <TypeIcon type={type} size={17} />;
  }
}

export function Home() {
  const [resume, setResume] = useState<ResumeItem[]>([]);
  const [recent, setRecent] = useState<RecentItem[]>([]);
  const [nextUp, setNextUp] = useState<NextUpItem[]>([]);
  const [libs, setLibs] = useState<Library[]>([]);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    Promise.allSettled([
      api("/resume").then((d) => setResume(d.items || [])),
      api("/recent?limit=12").then((d) => setRecent(d.items || [])),
      api("/nextup").then((d) => setNextUp(d.items || [])),
      api("/libraries").then(setLibs),
    ]).then(() => setLoaded(true));
  }, []);

  const watching = resume.filter((r) => r.libraryType === "movies" || r.libraryType === "tv");
  const listening = resume.filter((r) => r.libraryType === "audiobooks" || r.libraryType === "music");
  const reading = resume.filter((r) => r.libraryType === "books" || r.libraryType === "comics");
  const hero = resume[0];
  const restWatch = watching.slice(hero && hero === watching[0] ? 1 : 0);
  const restListen = listening.slice(hero && hero === listening[0] ? 1 : 0);
  const restRead = reading.slice(hero && hero === reading[0] ? 1 : 0);
  const heroKind: keyof typeof HERO_LABEL = !hero
    ? "watching"
    : hero.libraryType === "movies" || hero.libraryType === "tv" ? "watching"
    : hero.libraryType === "books" || hero.libraryType === "comics" ? "reading"
    : "listening";
  const heroSub = !hero ? "" : heroKind === "reading"
    ? (hero.percent > 0 ? `${Math.round(hero.percent * 100)}% through` : "")
    : `${fmt(Math.max(0, hero.durationSecs - hero.positionSecs))} left`;

  return (
    <div>
      {hero && (
        <a
          href={`#/work?id=${hero.workId}`}
          className="press"
          style={{
            position: "relative",
            isolation: "isolate",
            display: "flex",
            flexWrap: "wrap",
            gap: "2.2rem",
            alignItems: "center",
            overflow: "hidden",
            borderRadius: "18px",
            margin: "0.4rem 0 2.75rem",
            padding: "2.4rem 2.2rem",
            textDecoration: "none",
            color: "inherit",
          }}
        >
          {hero.hasCover && (
            <img
              src={media(`/covers/${hero.workId}.jpg`)}
              alt=""
              style={{
                position: "absolute",
                zIndex: -1,
                inset: 0,
                width: "100%",
                height: "22rem",
                objectFit: "cover",
                filter: "blur(48px) saturate(1.3)",
                opacity: 0.45,
                transform: "scale(1.2)",
                pointerEvents: "none",
                maskImage: "linear-gradient(to bottom, rgba(0,0,0,0.95) 30%, rgba(0,0,0,0.4) 68%, transparent 96%)",
                WebkitMaskImage: "linear-gradient(to bottom, rgba(0,0,0,0.95) 30%, rgba(0,0,0,0.4) 68%, transparent 96%)",
              }}
            />
          )}
          <div style={{ width: "13rem", flexShrink: 0, maxWidth: "42%" }}>
            <Cover has={hero.hasCover} id={hero.workId} title={hero.title} progress={hero.percent} ratio={coverRatio(hero.libraryType)} />
          </div>
          <div style={{ minWidth: "12rem", flex: 1 }}>
            <p style={eyebrow}>{HERO_LABEL[heroKind]}</p>
            <h1 style={{ ...workTitle, fontSize: "clamp(2rem, 4.5vw, 3.2rem)", fontWeight: 650, margin: "0 0 0.6rem" }}>{hero.title}</h1>
            {(hero.author || heroSub) && (
              <p style={{ margin: 0, fontSize: "0.95rem", color: c.textDim, letterSpacing: "-0.01em" }}>
                {[hero.author, heroSub].filter(Boolean).join("  ·  ")}
              </p>
            )}
            <div style={{ display: "flex", alignItems: "center", gap: "0.6rem", marginTop: "1.5rem", flexWrap: "wrap" }}>
              <span style={primaryBtn}>
                <IconPlay size={15} /> {HERO_ACTION[heroKind]}
              </span>
              <span className="navlink">Details</span>
            </div>
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
              size={12}
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
              size={12}
            />
          ))}
        </Rail>
      )}
      {restRead.length > 0 && (
        <Rail title="Continue Reading">
          {restRead.map((r) => (
            <RailCard
              key={r.editionId}
              id={r.workId}
              title={r.title}
              meta={r.percent > 0 ? `${Math.round(r.percent * 100)}%` : r.author}
              hasCover={r.hasCover}
              progress={r.percent}
              ratio={coverRatio(r.libraryType)}
              size={12}
            />
          ))}
        </Rail>
      )}
      {nextUp.length > 0 && (
        <Rail title="Next Up">
          {nextUp.map((n) => (
            <RailCard
              key={n.editionId}
              id={n.workId}
              title={n.title}
              meta={`S${n.seasonNum}E${n.episodeNum}${n.episodeTitle ? ` · ${n.episodeTitle}` : ""}`}
              hasCover={n.hasCover}
              size={12}
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
              size={12}
            />
          ))}
        </Rail>
      )}
      <section style={{ marginBottom: "2.6rem" }}>
        <h2 style={railTitle}>Libraries</h2>
        {libs.length === 0 ? (
          loaded && (
            <EmptyState title="No libraries yet" hint="Point libteca at a folder of media and it does the rest.">
              <a href="#/admin" style={primaryBtn}>Open Admin</a>
            </EmptyState>
          )
        ) : (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(3, minmax(0, 1fr))", gap: "0.9rem" }}>
            {libs.map((l) => (
              <a key={l.id} href={`#/library?lib=${l.id}`} className="press" style={libTile}>
                <span style={libTileIcon}>{libIcon(l.type)}</span>
                <span style={{ minWidth: 0 }}>
                  <span style={{
                    display: "block",
                    fontWeight: 600,
                    fontSize: "0.94rem",
                    letterSpacing: "-0.012em",
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}>{l.name}</span>
                  <span style={{ ...muted, fontSize: "0.78rem" }}>{typeLabel(l.type)}</span>
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
