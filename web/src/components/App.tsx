import { useEffect, useRef, useState } from "preact/hooks";

type Work = {
  id: number; title: string; author: string | null; subtitle: string | null;
  hasCover: boolean; editions: { id: number; format: string; duration: number; files: number }[];
};

type WorkDetail = {
  id: number; title: string; author: string | null; description: string | null; hasCover: boolean;
  editions: {
    id: number; format: string; title: string; duration: number; position?: number; isFinished?: boolean;
    seasonNum?: number; episodeNum?: number;
    files: { id: number; seq: number; duration: number; size: number }[];
    chapters: { title: string; start: number; end: number; fileId: number }[];
  }[];
};

type Library = { id: number; name: string; type: string; path: string };

let token = "";
const api = async (path: string, opts: RequestInit = {}) => {
  const res = await fetch(`/api/core${path}`, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(opts.headers || {}) },
  });
  if (res.status === 401) { location.hash = "#/"; throw new Error("unauthorized"); }
  return res.json();
};

export function App() {
  const [ready, setReady] = useState(false);
  const [view, setView] = useState<{ name: string; id?: number }>({ name: "library" });
  const [me, setMe] = useState<{ name: string; isAdmin: boolean } | null>(null);

  useEffect(() => {
    token = localStorage.getItem("libteca-token") || "";
    if (!token) { setReady(true); return; }
    api("/me").then((u) => { setMe(u); setReady(true); }).catch(() => { setReady(true); });
    const onHash = () => parseHash(setView);
    addEventListener("hashchange", onHash);
    parseHash(setView);
    return () => removeEventListener("hashchange", onHash);
  }, []);

  if (!ready) return <div style={center}>loading…</div>;
  if (!token) return <Login onLogin={() => { setView({ name: "library" }); location.hash = "#/library"; location.reload(); }} />;
  if (!me) return <div style={center}>checking…</div>;

  return (
    <div style={page}>
      <header style={header}>
        <a href="#/library" style={brand}>libteca</a>
        <nav style={nav}>
          {me.isAdmin && <a href="#/admin" style={navLink}>Admin</a>}
          <button style={linkBtn} onClick={() => { localStorage.removeItem("libteca-token"); token = ""; location.hash = "#/"; location.reload(); }}>
            {me.name} · sign out
          </button>
        </nav>
      </header>
      {view.name === "library" && <LibraryView />}
      {view.name === "work" && view.id != null && <WorkView id={view.id} />}
      {view.name === "admin" && <AdminView />}
    </div>
  );
}

function parseHash(setView: (v: { name: string; id?: number }) => void) {
  const h = location.hash.replace(/^#\//, "");
  const [name, qs] = h.split("?");
  const id = new URLSearchParams(qs || "").get("id");
  if (name === "work" && id) setView({ name: "work", id: Number(id) });
  else if (name === "admin") setView({ name: "admin" });
  else setView({ name: "library" });
}

function Login(props: { onLogin: () => void }) {
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e: Event) => {
    e.preventDefault();
    const res = await fetch("/api/core/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: name, password: pass }),
    });
    const data = await res.json();
    if (!data.token) { setErr("invalid credentials"); return; }
    localStorage.setItem("libteca-token", data.token);
    props.onLogin();
  };
  return (
    <div style={loginWrap}>
      <form style={loginCard} onSubmit={submit}>
        <h1 style={loginTitle}>libteca</h1>
        <input style={input} placeholder="username" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <input style={input} type="password" placeholder="password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
        {err && <p style={errStyle}>{err}</p>}
        <button style={primaryBtn} type="submit">Sign in</button>
      </form>
    </div>
  );
}

function LibraryView() {
  const [libs, setLibs] = useState<Library[]>([]);
  const [lib, setLib] = useState<number>(0);
  const [works, setWorks] = useState<Work[]>([]);
  const [msg, setMsg] = useState("");

  useEffect(() => { api("/libraries").then(setLibs); }, []);
  useEffect(() => { if (libs.length && !lib) setLib(libs[0].id); }, [libs]);
  useEffect(() => { if (lib) api(`/libraries/${lib}/works`).then(setWorks); }, [lib]);

  return (
    <div>
      <div style={rowBetween}>
        <div style={tabRow}>
          {libs.map((l) => (
            <button key={l.id} style={l.id === lib ? tabActive : tab} onClick={() => setLib(l.id)}>{l.name}</button>
          ))}
        </div>
        <button style={ghostBtn} onClick={async () => { setMsg("scanning…"); await api(`/libraries/${lib}/scan`, { method: "POST" }); setTimeout(async () => { setWorks(await api(`/libraries/${lib}/works`)); setMsg(""); }, 4000); }}>Scan</button>
      </div>
      {msg && <p style={muted}>{msg}</p>}
      {works.length === 0 && <p style={muted}>No works yet. Add a library in Admin and scan.</p>}
      <div style={grid}>
        {works.map((w) => (
          <a key={w.id} href={`#/work?id=${w.id}`} style={card}>
            <Cover has={w.hasCover} id={w.id} title={w.title} />
            <p style={cardTitle}>{w.title}</p>
            <p style={cardMeta}>{w.author}</p>
          </a>
        ))}
      </div>
    </div>
  );
}

function Cover(props: { has: boolean; id: number; title: string }) {
  if (!props.has) {
    return (
      <div style={coverPlaceholder}>
        <span style={coverLetter}>{props.title.charAt(0).toUpperCase()}</span>
      </div>
    );
  }
  return <img style={coverImg} src={`/api/core/covers/${props.id}.jpg`} alt="" loading="lazy" />;
}

function WorkView(props: { id: number }) {
  const [w, setW] = useState<WorkDetail | null>(null);
  const [edition, setEdition] = useState<number>(0);
  const [videoEdition, setVideoEdition] = useState<number | null>(null);

  useEffect(() => { api(`/works/${props.id}`).then(setW); }, [props.id]);
  useEffect(() => {
    if (!w) return;
    const withPos = w.editions.find((e) => e.position && e.position > 0 && !e.isFinished);
    setEdition(withPos ? withPos.id : (w.editions[0]?.id || 0));
  }, [w]);

  if (!w) return <p style={muted}>loading…</p>;
  const first = w.editions[0];
  const isTV = w.editions.some((e) => e.seasonNum !== undefined);
  const isMusic = first && first.format === "audio" && !first.chapters?.length && w.editions.length > 1;
  const isMovie = first && first.format === "video" && !isTV;

  if (videoEdition) {
    return <VideoPlayer w={w} editionId={videoEdition} onClose={() => setVideoEdition(null)} />;
  }
  if (isTV) return <EpisodeList w={w} onPlay={setVideoEdition} />;
  if (isMusic) return <TrackList w={w} />;
  if (isMovie) {
    return (
      <div>
        <a href="#/library" style={backLink}>‹ Library</a>
        <div style={workHead}>
          <Cover has={w.hasCover} id={w.id} title={w.title} />
          <div style={workMeta}>
            <h2 style={workTitle}>{w.title}</h2>
            <p style={muted}>{w.author}</p>
            <p style={muted}>{fmt(first?.duration || 0)}</p>
            <button style={primaryBtn} onClick={() => setVideoEdition(first.id)}>Play</button>
          </div>
        </div>
        <p style={muted}>{w.description}</p>
      </div>
    );
  }
  return <AudiobookView w={w} edition={edition} setEdition={setEdition} onPick={null as any} />;
}

function VideoPlayer(props: { w: WorkDetail; editionId: number; onClose: () => void }) {
  const ed = props.w.editions.find((e) => e.id === props.editionId)!;
  const ref = useRef<HTMLVideoElement | null>(null);
  const saved = useRef(0);
  useEffect(() => {
    const t = window.setInterval(async () => {
      if (!ref.current) return;
      saved.current = ref.current.currentTime;
    }, 1000);
    return () => { clearInterval(t); };
  }, []);
  const save = async (pos: number, finished = false) => {
    if (pos <= 0) return;
    await api(`/progress/${ed.id}`, { method: "POST", body: JSON.stringify({ position: pos, duration: ed.duration, finished }) });
  };
  const next = props.w.editions.find((e) => e.seasonNum !== undefined && e.id !== ed.id && (e.seasonNum === ed.seasonNum) && (e.episodeNum === (ed.episodeNum || 0) + 1));
  return (
    <div>
      <div style={rowBetween}>
        <button style={backLink} onClick={props.onClose}>‹ {props.w.title}</button>
        <p style={muted}>{ed.title}</p>
      </div>
      <video
        ref={ref}
        style={videoEl}
        controls
        autoPlay
        poster={props.w.hasCover ? `/api/core/covers/${props.w.id}.jpg` : undefined}
        src={`/api/core/stream/${ed.files[0].id}`}
        onLoadedMetadata={() => { if (ref.current && ed.position && ed.position > 0 && ed.position < ed.duration - 5) ref.current.currentTime = ed.position; }}
        onPause={() => save(ref.current?.currentTime || 0)}
        onTimeUpdate={() => { saved.current = ref.current?.currentTime || 0; }}
        onEnded={() => { save(ed.duration, true); if (next) props.onClose(); }}
      />
      <div style={{ display: "flex", gap: "0.8rem", marginTop: "0.8rem" }}>
        <button style={ghostBtn} onClick={() => { save(saved.current); props.onClose(); }}>Save & close</button>
        {next && <button style={ghostBtn} onClick={() => { save(saved.current); props.editionId = next.id; location.hash = `#/work?id=${props.w.id}`; }}>Next episode ›</button>}
      </div>
    </div>
  );
}

function EpisodeList(props: { w: WorkDetail; onPlay: (id: number) => void }) {
  const eps = [...props.w.editions].sort((a, b) => (a.seasonNum! - b.seasonNum!) || (a.episodeNum! - b.episodeNum!));
  return (
    <div>
      <a href="#/library" style={backLink}>‹ Library</a>
      <div style={workHead}>
        <Cover has={props.w.hasCover} id={props.w.id} title={props.w.title} />
        <div style={workMeta}>
          <h2 style={workTitle}>{props.w.title}</h2>
          <p style={muted}>{eps.length} episodes</p>
        </div>
      </div>
      <div style={chapterList}>
        {eps.map((e) => (
          <button key={e.id} style={chapterRow} onClick={() => props.onPlay(e.id)}>
            <span>{e.episodeNum}. {e.title}</span>
            <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline" }}>
              <span style={muted}>{fmt(e.duration)}</span>
              {e.position && !e.isFinished ? <span style={progressMini(e.position / e.duration)} /> : null}
              {e.isFinished ? <span style={muted}>✓</span> : null}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

function TrackList(props: { w: WorkDetail }) {
  const [playing, setPlaying] = useState<{ fileId: number; editionId: number } | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const save = async (editionId: number, pos: number, dur: number) => {
    if (pos > 1) await api(`/progress/${editionId}`, { method: "POST", body: JSON.stringify({ position: pos, duration: dur, finished: pos >= dur - 3 }) });
  };
  return (
    <div>
      <a href="#/library" style={backLink}>‹ Library</a>
      <div style={workHead}>
        <Cover has={props.w.hasCover} id={props.w.id} title={props.w.title} />
        <div style={workMeta}>
          <h2 style={workTitle}>{props.w.title}</h2>
          <p style={muted}>{props.w.author}</p>
        </div>
      </div>
      <div style={chapterList}>
        {props.w.editions.map((t, i) => (
          <button key={t.id} style={chapterRow} onClick={() => { setPlaying({ fileId: t.files[0].id, editionId: t.id }); setTimeout(() => audioRef.current?.play().catch(() => {}), 50); }}>
            <span>{i + 1}. {t.title}</span>
            <span style={muted}>{fmt(t.duration)}</span>
          </button>
        ))}
      </div>
      {playing && (
        <div style={playerBar}>
          <audio
            ref={audioRef}
            style={{ width: "100%" }}
            controls
            src={`/api/core/stream/${playing.fileId}`}
            onEnded={() => {
              const idx = props.w.editions.findIndex((e) => e.id === playing.editionId);
              save(playing.editionId, props.w.editions[idx]?.duration || 0, props.w.editions[idx]?.duration || 0);
              if (idx >= 0 && idx < props.w.editions.length - 1) {
                const nt = props.w.editions[idx + 1];
                setPlaying({ fileId: nt.files[0].id, editionId: nt.id });
                setTimeout(() => audioRef.current?.play().catch(() => {}), 80);
              }
            }}
            onPause={() => { const ed = props.w.editions.find((e) => e.id === playing.editionId); if (ed) save(ed.id, audioRef.current?.currentTime || 0, ed.duration); }}
          />
        </div>
      )}
    </div>
  );
}

function AudiobookView(props: { w: WorkDetail; edition: number; setEdition: (n: number) => void; onPick: null }) {
  const [playing, setPlaying] = useState<{ fileId: number; offset: number } | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const saveTimer = useRef<number | null>(null);
  const w = props.w;
  const ed = w.editions.find((e) => e.id === props.edition);
  const cumBefore = (fileId: number) => {
    if (!ed) return 0;
    let cum = 0;
    for (const f of ed.files) { if (f.id === fileId) return cum; cum += f.duration; }
    return 0;
  };
  const play = async (fileId: number, offset: number) => {
    setPlaying({ fileId, offset });
    setTimeout(() => {
      if (audioRef.current) {
        audioRef.current.currentTime = offset;
        audioRef.current.play().catch(() => {});
      }
    }, 50);
  };
  const positionNow = () => {
    if (!audioRef.current || !playing) return 0;
    return cumBefore(playing.fileId) + audioRef.current.currentTime;
  };
  const save = async () => {
    if (!ed) return;
    const pos = positionNow();
    if (pos <= 0) return;
    await api(`/progress/${ed.id}`, { method: "POST", body: JSON.stringify({ position: pos, duration: ed.duration }) });
  };
  useEffect(() => {
    saveTimer.current = window.setInterval(save, 15000);
    return () => { if (saveTimer.current) clearInterval(saveTimer.current); save(); };
  }, [ed?.id, playing?.fileId]);
  if (!ed) return <p style={muted}>no edition</p>;
  return (
    <div>
      <a href="#/library" style={backLink}>‹ Library</a>
      <div style={workHead}>
        <Cover has={w.hasCover} id={w.id} title={w.title} />
        <div style={workMeta}>
          <h2 style={workTitle}>{w.title}</h2>
          <p style={muted}>{w.author}</p>
          {w.editions.length > 1 && (
            <div style={tabRow}>
              {w.editions.map((e) => (
                <button key={e.id} style={e.id === ed.id ? tabActive : tab} onClick={() => props.setEdition(e.id)}>{e.format}</button>
              ))}
            </div>
          )}
          <button style={primaryBtn} onClick={() => {
            if (ed.position && ed.position > 0 && !ed.isFinished) {
              let fid = ed.files[0].id;
              let before = 0;
              let cum = 0;
              for (const f of ed.files) { if (cum + f.duration > ed.position) { fid = f.id; before = cum; break; } cum += f.duration; }
              play(fid, Math.max(0, ed.position - before));
            } else play(ed.files[0].id, 0);
          }}>
            {ed.position && ed.position > 0 && !ed.isFinished ? `Resume · ${fmt(ed.duration - ed.position)} left` : "Play"}
          </button>
          {ed.isFinished && <p style={muted}>Finished</p>}
        </div>
      </div>
      <div style={chapterList}>
        {ed.chapters.map((c, i) => (
          <button key={i} style={chapterRow} onClick={() => play(c.fileId, c.start - cumBefore(c.fileId))}>
            <span>{c.title}</span>
            <span style={muted}>{fmt(c.end - c.start)}</span>
          </button>
        ))}
        {ed.chapters.length === 0 && ed.files.map((f, i) => (
          <button key={f.id} style={chapterRow} onClick={() => play(f.id, 0)}>
            <span>Part {i + 1}</span>
            <span style={muted}>{fmt(f.duration)}</span>
          </button>
        ))}
      </div>
      {playing && (
        <div style={playerBar}>
          <audio
            ref={audioRef}
            style={{ width: "100%" }}
            controls
            src={`/api/core/stream/${playing.fileId}`}
            onEnded={async () => {
              const idx = ed.files.findIndex((f) => f.id === playing.fileId);
              if (idx >= 0 && idx < ed.files.length - 1) play(ed.files[idx + 1].id, 0);
              else await api(`/progress/${ed.id}`, { method: "POST", body: JSON.stringify({ position: ed.duration, duration: ed.duration, finished: true }) });
            }}
          />
        </div>
      )}
    </div>
  );
}

function AdminView() {
  const [libs, setLibs] = useState<Library[]>([]);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [msg, setMsg] = useState("");

  const refresh = () => api("/libraries").then(setLibs);
  useEffect(() => { refresh(); }, []);

  const add = async (e: Event) => {
    e.preventDefault();
    const res = await fetch("/api/core/libraries", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ name, type: "audiobooks", path }),
    });
    const data = await res.json();
    if (data.error) { setMsg(data.error); return; }
    await fetch(`/api/core/libraries/${data.id}/scan`, { method: "POST", headers: { Authorization: `Bearer ${token}` } });
    setMsg("library added, scanning");
    setName(""); setPath("");
    refresh();
  };

  return (
    <div>
      <a href="#/library" style={backLink}>‹ Library</a>
      <h2 style={sectionTitle}>Libraries</h2>
      {libs.map((l) => (
        <div key={l.id} style={adminRow}>
          <span style={cardTitle}>{l.name}</span>
          <span style={muted}>{l.path}</span>
          <button style={ghostBtn} onClick={async () => {
            await api(`/libraries/${l.id}/scan`, { method: "POST" });
            setMsg(`scanning ${l.name}…`);
          }}>Scan</button>
        </div>
      ))}
      <form style={loginCard} onSubmit={add}>
        <h3 style={sectionTitle}>Add library</h3>
        <input style={input} placeholder="name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <input style={input} placeholder="/path/to/audiobooks" value={path} onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
        <button style={primaryBtn} type="submit">Add & scan</button>
        {msg && <p style={muted}>{msg}</p>}
      </form>
    </div>
  );
}

function fmt(secs: number) {
  if (!isFinite(secs) || secs < 0) secs = 0;
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = Math.floor(secs % 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m ${s}s`;
}

const page: preact.JSX.CSSProperties = {
  fontFamily: "system-ui, -apple-system, sans-serif",
  color: "#1d1d1f", background: "#fff",
  minHeight: "100vh", margin: 0, padding: "0 1.25rem 6rem",
  lineHeight: 1.5,
};

const center: preact.JSX.CSSProperties = { fontFamily: "system-ui", padding: "4rem", textAlign: "center", color: "#6e6e73" };

const header: preact.JSX.CSSProperties = {
  display: "flex", justifyContent: "space-between", alignItems: "center",
  padding: "0.9rem 0", borderBottom: "1px solid #e5e5ea", marginBottom: "1.6rem",
};

const brand: preact.JSX.CSSProperties = { fontSize: "1.15rem", fontWeight: 700, textDecoration: "none", color: "#1d1d1f" };

const nav: preact.JSX.CSSProperties = { display: "flex", gap: "1rem", alignItems: "center" };

const navLink: preact.JSX.CSSProperties = { color: "#0066cc", textDecoration: "none", fontSize: "0.9rem" };

const linkBtn: preact.JSX.CSSProperties = { background: "none", border: "none", color: "#6e6e73", fontSize: "0.85rem", cursor: "pointer" };

const loginWrap: preact.JSX.CSSProperties = {
  minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center",
  fontFamily: "system-ui, -apple-system, sans-serif", background: "#f5f5f7",
};

const loginCard: preact.JSX.CSSProperties = {
  display: "flex", flexDirection: "column", gap: "0.7rem",
  background: "#fff", border: "1px solid #e5e5ea", borderRadius: "14px",
  padding: "1.8rem", width: "20rem",
};

const loginTitle: preact.JSX.CSSProperties = { margin: "0 0 0.6rem", fontSize: "1.4rem" };

const input: preact.JSX.CSSProperties = {
  padding: "0.55rem 0.75rem", borderRadius: "8px", border: "1px solid #d2d2d7",
  fontSize: "0.95rem", fontFamily: "inherit",
};

const primaryBtn: preact.JSX.CSSProperties = {
  background: "#0071e3", color: "#fff", border: "none", borderRadius: "8px",
  padding: "0.55rem 1.1rem", fontSize: "0.95rem", fontWeight: 600, cursor: "pointer",
  width: "fit-content", alignSelf: "flex-start",
};

const ghostBtn: preact.JSX.CSSProperties = {
  background: "none", border: "1px solid #d2d2d7", borderRadius: "8px",
  padding: "0.4rem 0.9rem", fontSize: "0.85rem", cursor: "pointer", color: "#1d1d1f",
};

const errStyle: preact.JSX.CSSProperties = { color: "#c0392b", margin: 0, fontSize: "0.85rem" };

const muted: preact.JSX.CSSProperties = { color: "#6e6e73", fontSize: "0.88rem", margin: 0 };

const rowBetween: preact.JSX.CSSProperties = { display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "1rem" };

const tabRow: preact.JSX.CSSProperties = { display: "flex", gap: "0.4rem", flexWrap: "wrap" };

const tab: preact.JSX.CSSProperties = {
  background: "none", border: "1px solid #d2d2d7", borderRadius: "999px",
  padding: "0.3rem 0.9rem", fontSize: "0.85rem", cursor: "pointer",
};

const tabActive: preact.JSX.CSSProperties = { ...tab, background: "#1d1d1f", color: "#fff", borderColor: "#1d1d1f" };

const grid: preact.JSX.CSSProperties = {
  display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(8.5rem, 1fr))", gap: "1.2rem",
};

const card: preact.JSX.CSSProperties = { textDecoration: "none", color: "inherit" };

const coverImg: preact.JSX.CSSProperties = {
  width: "100%", aspectRatio: "2 / 3", objectFit: "cover",
  borderRadius: "8px", boxShadow: "0 1px 4px rgba(0,0,0,0.15)",
};

const coverPlaceholder: preact.JSX.CSSProperties = {
  width: "100%", aspectRatio: "2 / 3", borderRadius: "8px",
  background: "#6e6e73", display: "flex", alignItems: "center", justifyContent: "center",
};

const coverLetter: preact.JSX.CSSProperties = {
  color: "#fff", fontSize: "2.4rem", fontWeight: 700, fontFamily: "'Iowan Old Style', Georgia, serif",
};

const cardTitle: preact.JSX.CSSProperties = { margin: "0.5rem 0 0", fontWeight: 600, fontSize: "0.9rem" };

const cardMeta: preact.JSX.CSSProperties = { margin: 0, color: "#6e6e73", fontSize: "0.8rem" };

const workHead: preact.JSX.CSSProperties = { display: "flex", gap: "1.6rem", marginBottom: "1.6rem" };

const workMeta: preact.JSX.CSSProperties = { display: "flex", flexDirection: "column", gap: "0.7rem", alignItems: "flex-start" };

const workTitle: preact.JSX.CSSProperties = { margin: 0, fontSize: "1.6rem" };

const backLink: preact.JSX.CSSProperties = { display: "inline-block", color: "#0066cc", textDecoration: "none", marginBottom: "1.2rem", fontSize: "0.9rem" };

const sectionTitle: preact.JSX.CSSProperties = { margin: "0 0 0.9rem", fontSize: "1.2rem" };

const chapterList: preact.JSX.CSSProperties = { borderTop: "1px solid #e5e5ea" };

const chapterRow: preact.JSX.CSSProperties = {
  display: "flex", justifyContent: "space-between", width: "100%",
  padding: "0.7rem 0.2rem", borderBottom: "1px solid #e5e5ea",
  background: "none", borderLeft: "none", borderRight: "none", borderTop: "none",
  cursor: "pointer", fontFamily: "inherit", fontSize: "0.92rem",
};

const videoEl: preact.JSX.CSSProperties = {
  width: "100%", maxHeight: "70vh", background: "#000", borderRadius: "12px", display: "block",
};

function progressMini(pct: number): preact.JSX.CSSProperties {
  return { width: "3rem", height: "3px", borderRadius: "999px", background: `linear-gradient(90deg, #0071e3 ${Math.min(100, pct * 100)}%, #d2d2d7 ${Math.min(100, pct * 100)}%)`, alignSelf: "center" };
}

const playerBar: preact.JSX.CSSProperties = {
  position: "fixed", bottom: 0, left: 0, right: 0,
  background: "rgba(255,255,255,0.95)", backdropFilter: "blur(12px)",
  borderTop: "1px solid #e5e5ea", padding: "0.7rem 1.25rem",
  display: "flex", gap: "1rem", alignItems: "center",
};

const adminRow: preact.JSX.CSSProperties = {
  display: "flex", gap: "1rem", alignItems: "center",
  padding: "0.7rem 0", borderBottom: "1px solid #e5e5ea",
};
