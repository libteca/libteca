import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, media } from "../api";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconBack30, IconChevronDown, IconChevronLeft, IconChevronUp, IconFwd30, IconMusic, IconPause, IconPlay, IconX } from "../components/svg";
import { toast } from "../toast";
import { fmt, fmtClock } from "../util";
import {
  backLink, badge, c, errStyle, ghostBtn, iconBtn, input, linkBtn, muted, playerBar, primaryBtn, sectionTitle,
} from "../styles";

type Playlist = {
  id: number; name: string; ownerId: number; owner: string;
  songCount: number; durationSecs: number; createdAtMs: number; updatedAtMs: number;
};

type PlaylistItem = {
  editionId: number; position: number; title: string; format: string;
  durationSecs: number; workId: number; workTitle: string; workAuthor: string | null; hasCover: boolean;
};

type PlaylistDetail = Playlist & { items: PlaylistItem[] };

// Sequential playback for a playlist's audio editions. Items are editions,
// not files, so each src is the edition download (first file) — music tracks
// are one file per edition.
function PlaylistPlayer(props: { items: PlaylistItem[]; onDone: () => void }) {
  const [idx, setIdx] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const [duration, setDuration] = useState(0);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const autoplay = useRef(false);
  const lastPostRef = useRef(0);
  const item = props.items[idx];
  const itemRef = useRef(item);
  itemRef.current = item;

  useEffect(() => {
    const post = () => {
      const a = audioRef.current;
      const it = itemRef.current;
      if (!a || !it || a.ended || a.currentTime <= 1) return;
      fetch(`/api/core/progress/${it.editionId}`, {
        method: "POST", keepalive: true,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${getToken()}` },
        body: JSON.stringify({
          position: a.currentTime,
          duration: isFinite(a.duration) && a.duration > 0 ? a.duration : (it.durationSecs || 0),
          finished: false,
        }),
      }).catch(() => {});
    };
    addEventListener("pagehide", post);
    return () => {
      removeEventListener("pagehide", post);
      post();
    };
  }, []);

  useEffect(() => {
    const a = audioRef.current;
    if (!a) return;
    setElapsed(0);
    setDuration(item?.durationSecs || 0);
    if (autoplay.current) a.play().catch(() => {});
  }, [idx]);

  const jump = (i: number) => {
    if (i < 0 || i >= props.items.length) return;
    autoplay.current = true;
    setIdx(i);
  };

  const saveProgress = (finished: boolean) => {
    const a = audioRef.current;
    if (!a || !item) return;
    const pos = a.currentTime;
    if (pos <= 1 && !finished) return;
    lastPostRef.current = Date.now();
    api(`/progress/${item.editionId}`, {
      method: "POST",
      body: JSON.stringify({
        position: pos,
        duration: isFinite(a.duration) && a.duration > 0 ? a.duration : (item.durationSecs || 0),
        finished,
      }),
    }).catch(() => {});
  };

  const ended = () => {
    saveProgress(true);
    if (idx < props.items.length - 1) {
      autoplay.current = true;
      setIdx(idx + 1);
    } else {
      setPlaying(false);
      autoplay.current = false;
      props.onDone();
    }
  };

  const toggle = () => {
    const a = audioRef.current;
    if (!a) return;
    if (a.paused) { autoplay.current = true; a.play().catch(() => {}); } else a.pause();
  };

  return (
    <div className="player-bar" style={playerBar}>
      <audio
        ref={audioRef}
        src={item ? media(`/editions/${item.editionId}/download`) : undefined}
        onPlay={() => setPlaying(true)}
        onPause={() => {
          setPlaying(false);
          const a = audioRef.current;
          if (a && !a.ended) saveProgress(false);
        }}
        onTimeUpdate={(e) => {
          setElapsed((e.target as HTMLAudioElement).currentTime);
          if (Date.now() - lastPostRef.current >= 15000) saveProgress(false);
        }}
        onLoadedMetadata={(e) => {
          const a = e.target as HTMLAudioElement;
          if (isFinite(a.duration) && a.duration > 0) setDuration(a.duration);
        }}
        onEnded={ended}
      />
      <div style={{ display: "flex", flexDirection: "column", minWidth: 0, width: "13rem", flexShrink: 1 }}>
        <span style={{ fontSize: "0.88rem", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item ? item.title : ""}</span>
        <span style={{ fontSize: "0.75rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {idx + 1} / {props.items.length} · {item?.workTitle}
        </span>
      </div>
      <div style={{ display: "flex", gap: "0.1rem", alignItems: "center", flexShrink: 0 }}>
        <button className="press" style={{ ...iconBtn, color: c.muted }} aria-label="Previous" title="Previous" onClick={() => jump(idx - 1)}><IconBack30 size={18} /></button>
        <button className="press" style={{ ...iconBtn, color: c.text }} aria-label={playing ? "Pause" : "Play"} title="Play or pause" onClick={toggle}>
          {playing ? <IconPause size={22} /> : <IconPlay size={22} />}
        </button>
        <button className="press" style={{ ...iconBtn, color: c.muted }} aria-label="Next" title="Next" onClick={() => jump(idx + 1)}><IconFwd30 size={18} /></button>
      </div>
      <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(elapsed)}</span>
      <input
        type="range" min={0} max={Math.max(1, Math.floor(duration))} step={1} value={Math.floor(elapsed)}
        className="seek"
        style={{
          flex: 1, minWidth: "4rem",
          background: `linear-gradient(90deg, ${c.accent} ${duration > 0 ? (elapsed / duration) * 100 : 0}%, ${c.line} ${duration > 0 ? (elapsed / duration) * 100 : 0}%)`,
          backgroundSize: "100% 5px", backgroundRepeat: "no-repeat", backgroundPosition: "center", borderRadius: "999px",
        }}
        onInput={(e) => {
          const a = audioRef.current;
          if (a) a.currentTime = Number((e.target as HTMLInputElement).value);
        }}
        aria-label="Seek"
      />
      <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(duration)}</span>
      <button className="press" style={{ ...linkBtn, marginLeft: "0.2rem", flexShrink: 0 }} onClick={() => { saveProgress(false); props.onDone(); }}>Stop</button>
    </div>
  );
}

export function PlaylistsView(props: { id?: number }) {
  if (props.id != null) return <PlaylistDetail id={props.id} />;
  return <PlaylistList />;
}

function PlaylistList() {
  const [lists, setLists] = useState<Playlist[]>([]);
  const [name, setName] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState<number | null>(null);
  const [renameVal, setRenameVal] = useState("");

  const refresh = () => api("/playlists").then((r: Playlist[]) => setLists(Array.isArray(r) ? r : [])).catch(() => setLists([]));
  useEffect(() => { refresh(); }, []);

  const create = async (e: Event) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true); setErr("");
    try {
      const res = await api("/playlists", { method: "POST", body: JSON.stringify({ name: name.trim() }) });
      if (res.error) { setErr(res.error); return; }
      toast(`Playlist "${name.trim()}" created`, "success");
      setName("");
      location.hash = `#/playlists?id=${res.id}`;
    } catch {
      setErr("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const rename = async (id: number) => {
    if (!renameVal.trim()) { setRenaming(null); return; }
    try {
      const res = await api(`/playlists/${id}`, { method: "PATCH", body: JSON.stringify({ name: renameVal.trim() }) });
      if (res.error) setErr(res.error);
    } catch {
      setErr("Couldn't reach the server.");
    }
    setRenaming(null);
    refresh();
  };

  const remove = async (id: number, label: string) => {
    if (!confirm(`Delete playlist "${label}"?`)) return;
    try {
      const res = await api(`/playlists/${id}`, { method: "DELETE" });
      if (res.error) setErr(res.error);
    } catch {
      setErr("Couldn't reach the server.");
    }
    refresh();
  };

  return (
    <div>
      <h2 style={sectionTitle}>Playlists</h2>
      {lists.length === 0
        ? <EmptyState title="No playlists yet" icon={<IconMusic size={22} />} hint="Create one below, or add editions from a work page." />
        : (
          <div style={{ borderTop: `1px solid ${c.lineSoft}` }}>
            {lists.map((p) => (
              <div key={p.id} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}`, flexWrap: "wrap" }}>
                {renaming === p.id ? (
                  <form style={{ display: "flex", gap: "0.4rem" }} onSubmit={(e) => { e.preventDefault(); rename(p.id); }}>
                    <input style={input} autoFocus value={renameVal} onInput={(e) => setRenameVal((e.target as HTMLInputElement).value)} />
                    <button style={primaryBtn} type="submit">Save</button>
                    <button style={ghostBtn} type="button" onClick={() => setRenaming(null)}>Cancel</button>
                  </form>
                ) : (
                  <a href={`#/playlists?id=${p.id}`} style={{ textDecoration: "none", color: c.text, minWidth: 0 }}>
                    <span style={{ fontWeight: 600 }}>{p.name}</span>
                    <span style={{ ...muted, marginLeft: "0.7rem" }}>{p.songCount} items · {fmt(p.durationSecs)} · {p.owner}</span>
                  </a>
                )}
                <span style={{ display: "flex", gap: "0.3rem", flexShrink: 0 }}>
                  <button style={linkBtn} onClick={() => { setRenaming(p.id); setRenameVal(p.name); }}>Rename</button>
                  <button style={{ ...linkBtn, color: c.danger }} onClick={() => remove(p.id, p.name)}>Delete</button>
                </span>
              </div>
            ))}
          </div>
        )}
      <form style={{ display: "flex", gap: "0.6rem", flexWrap: "wrap", marginTop: "1.6rem", alignItems: "center" }} onSubmit={create}>
        <input style={{ ...input, flex: 1, minWidth: "12rem" }} placeholder="New playlist name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <button style={primaryBtn} type="submit" disabled={busy}>{busy ? "Creating…" : "Create"}</button>
      </form>
      {err && <p style={errStyle}>{err}</p>}
    </div>
  );
}

function PlaylistDetail(props: { id: number }) {
  const [p, setP] = useState<PlaylistDetail | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [err, setErr] = useState("");
  const [playing, setPlaying] = useState(false);
  const [moving, setMoving] = useState(false);

  const refresh = () => api(`/playlists/${props.id}`)
    .then((r: PlaylistDetail & { error?: string }) => {
      if (r && !r.error) setP(r);
      else setNotFound(true);
    })
    .catch(() => setNotFound(true));
  useEffect(() => { refresh(); }, [props.id]);

  const removeItem = async (editionId: number) => {
    setErr("");
    try {
      const res = await api(`/playlists/${props.id}/items/${editionId}`, { method: "DELETE" });
      if (res.error) setErr(res.error);
    } catch {
      setErr("Couldn't reach the server.");
    }
    refresh();
  };

  const move = async (editionId: number, position: number) => {
    if (moving) return;
    setMoving(true); setErr("");
    try {
      const res = await api(`/playlists/${props.id}/reorder`, { method: "POST", body: JSON.stringify({ editionId, position }) });
      if (res.error) setErr(res.error);
    } catch {
      setErr("Couldn't reach the server.");
    } finally {
      setMoving(false);
    }
    refresh();
  };

  if (notFound) {
    return (
      <div>
        <button className="press" style={backLink} onClick={() => { location.hash = "#/playlists"; }}><IconChevronLeft size={16} /> Playlists</button>
        <EmptyState title="Playlist not found" hint="It may have been deleted.">
          <a style={linkBtn} href="#/playlists">Back to playlists</a>
        </EmptyState>
      </div>
    );
  }
  if (!p) return <QuietLoad />;

  const audioItems = p.items.filter((it) => ["audio", "m4b", "mp3"].includes(it.format));

  return (
    <div style={{ paddingBottom: playing ? "5rem" : 0 }}>
      <button className="press" style={backLink} onClick={() => { location.hash = "#/playlists"; }}><IconChevronLeft size={16} /> Playlists</button>
      <div style={{ display: "flex", gap: "1rem", alignItems: "center", flexWrap: "wrap", marginBottom: "1.2rem" }}>
        <h2 style={{ ...sectionTitle, margin: 0 }}>{p.name}</h2>
        <span style={muted}>{p.songCount} items · {fmt(p.durationSecs)} · {p.owner}</span>
        {audioItems.length > 0 && (
          <button style={primaryBtn} onClick={() => setPlaying(true)}>
            <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
              <IconPlay size={14} /> Play all ({audioItems.length})
            </span>
          </button>
        )}
      </div>
      {err && <p style={errStyle}>{err}</p>}
      {p.items.length === 0 ? (
        <EmptyState title="Empty playlist" icon={<IconMusic size={22} />} hint="Add editions from a work page." />
      ) : (
        <div style={{ borderTop: `1px solid ${c.lineSoft}` }}>
          {p.items.map((it, i) => (
            <div key={it.editionId} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}`, flexWrap: "wrap" }}>
              <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", minWidth: 0 }}>
                <span style={{ color: c.faint, fontVariantNumeric: "tabular-nums" }}>{i + 1}.</span>
                {it.workId ? (
                  <a href={`#/work?id=${it.workId}`} style={{ display: "flex", flexDirection: "column", minWidth: 0, textDecoration: "none", color: "inherit" }}>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: c.textDim }}>{it.title}</span>
                    <span style={{ ...muted, fontSize: "0.75rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {it.workTitle}{it.workAuthor ? ` · ${it.workAuthor}` : ""}
                    </span>
                  </a>
                ) : (
                  <span style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: c.textDim }}>{it.title}</span>
                    <span style={{ ...muted, fontSize: "0.75rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {it.workTitle}{it.workAuthor ? ` · ${it.workAuthor}` : ""}
                    </span>
                  </span>
                )}
              </span>
              <span style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexShrink: 0 }}>
                <span style={badge}>{it.format}</span>
                <span style={muted}>{it.durationSecs > 0 ? fmt(it.durationSecs) : "—"}</span>
                <span style={{ display: "flex", gap: "0.05rem" }}>
                  <button className="press" style={iconBtn} disabled={i === 0 || moving} aria-label="Move up" title="Move up" onClick={() => move(it.editionId, it.position - 1)}><IconChevronUp size={14} /></button>
                  <button className="press" style={iconBtn} disabled={i === p.items.length - 1 || moving} aria-label="Move down" title="Move down" onClick={() => move(it.editionId, it.position + 1)}><IconChevronDown size={14} /></button>
                </span>
                <button className="press" style={{ ...iconBtn, color: c.danger }} aria-label="Remove" title="Remove" onClick={() => removeItem(it.editionId)}><IconX size={14} /></button>
              </span>
            </div>
          ))}
        </div>
      )}
      {playing && audioItems.length > 0 && (
        <PlaylistPlayer items={audioItems} onDone={() => setPlaying(false)} />
      )}
    </div>
  );
}
