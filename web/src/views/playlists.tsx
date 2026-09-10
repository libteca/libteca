import { useEffect, useRef, useState } from "preact/hooks";
import { api, media } from "../api";
import { IconBack30, IconFwd30, IconPause, IconPlay } from "../components/svg";
import { fmt, fmtClock } from "../util";
import {
  backLink, badge, c, errStyle, ghostBtn, input, linkBtn, muted, primaryBtn, sectionTitle,
} from "../styles";

type Playlist = {
  id: number; name: string; ownerId: number; owner: string;
  songCount: number; durationSecs: number; createdAtMs: number; updatedAtMs: number;
};

type PlaylistItem = {
  editionId: number; position: number; title: string; format: string;
  durationSecs: number; workTitle: string; workAuthor: string | null; hasCover: boolean;
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
  const item = props.items[idx];

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

  const ended = () => {
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
    <div style={{ position: "fixed", bottom: 0, left: 0, right: 0, background: "rgba(18, 19, 22, 0.92)", backdropFilter: "blur(16px)", WebkitBackdropFilter: "blur(16px)", borderTop: `1px solid ${c.lineSoft}`, padding: "0.7rem 1.4rem", display: "flex", gap: "0.9rem", alignItems: "center", zIndex: 30 }}>
      <audio
        ref={audioRef}
        src={item ? media(`/editions/${item.editionId}/download`) : undefined}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onTimeUpdate={(e) => setElapsed((e.target as HTMLAudioElement).currentTime)}
        onLoadedMetadata={(e) => {
          const a = e.target as HTMLAudioElement;
          if (isFinite(a.duration) && a.duration > 0) setDuration(a.duration);
        }}
        onEnded={ended}
      />
      <div style={{ display: "flex", flexDirection: "column", minWidth: 0, width: "13rem", flexShrink: 1 }}>
        <span style={{ fontSize: "0.85rem", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item ? item.title : ""}</span>
        <span style={{ fontSize: "0.73rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {idx + 1} / {props.items.length} · {item?.workTitle}
        </span>
      </div>
      <div style={{ display: "flex", gap: "0.15rem", alignItems: "center", flexShrink: 0 }}>
        <button style={{ ...barBtn, color: c.muted }} title="Previous" onClick={() => jump(idx - 1)}><IconBack30 size={18} /></button>
        <button style={barBtn} title="Play or pause" onClick={toggle}>
          {playing ? <IconPause size={20} /> : <IconPlay size={20} />}
        </button>
        <button style={{ ...barBtn, color: c.muted }} title="Next" onClick={() => jump(idx + 1)}><IconFwd30 size={18} /></button>
      </div>
      <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(elapsed)}</span>
      <span style={{ fontSize: "0.75rem", color: c.faint, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(duration)}</span>
      <button style={{ ...linkBtn, marginLeft: "auto", flexShrink: 0 }} onClick={props.onDone}>Stop</button>
    </div>
  );
}

const barBtn: preact.JSX.CSSProperties = {
  background: "none", border: "none", color: c.text, cursor: "pointer",
  display: "inline-flex", alignItems: "center", justifyContent: "center",
  width: "2.2rem", height: "2.2rem", borderRadius: "50%", padding: 0,
};

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
    const res = await api("/playlists", { method: "POST", body: JSON.stringify({ name: name.trim() }) });
    setBusy(false);
    if (res.error) { setErr(res.error); return; }
    setName("");
    location.hash = `#/playlists?id=${res.id}`;
  };

  const rename = async (id: number) => {
    if (!renameVal.trim()) { setRenaming(null); return; }
    const res = await api(`/playlists/${id}`, { method: "PATCH", body: JSON.stringify({ name: renameVal.trim() }) });
    if (res.error) setErr(res.error);
    setRenaming(null);
    refresh();
  };

  const remove = async (id: number, label: string) => {
    if (!confirm(`Delete playlist "${label}"?`)) return;
    const res = await api(`/playlists/${id}`, { method: "DELETE" });
    if (res.error) setErr(res.error);
    refresh();
  };

  return (
    <div>
      <h2 style={sectionTitle}>Playlists</h2>
      {lists.length === 0
        ? <p style={muted}>No playlists yet. Create one below, or add editions from a work page.</p>
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
  const [err, setErr] = useState("");
  const [playing, setPlaying] = useState(false);
  const [moving, setMoving] = useState(false);

  const refresh = () => api(`/playlists/${props.id}`)
    .then((r: PlaylistDetail & { error?: string }) => setP(r && !r.error ? r : null))
    .catch(() => setP(null));
  useEffect(() => { refresh(); }, [props.id]);

  const removeItem = async (editionId: number) => {
    setErr("");
    const res = await api(`/playlists/${props.id}/items/${editionId}`, { method: "DELETE" });
    if (res.error) setErr(res.error);
    refresh();
  };

  const move = async (editionId: number, position: number) => {
    if (moving) return;
    setMoving(true); setErr("");
    const res = await api(`/playlists/${props.id}/reorder`, { method: "POST", body: JSON.stringify({ editionId, position }) });
    setMoving(false);
    if (res.error) setErr(res.error);
    refresh();
  };

  if (!p) return <p style={muted}>loading…</p>;

  const audioItems = p.items.filter((it) => it.format === "audio");

  return (
    <div style={{ paddingBottom: playing ? "5rem" : 0 }}>
      <button style={backLink} onClick={() => { location.hash = "#/playlists"; }}>{"< Playlists"}</button>
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
        <p style={muted}>Empty playlist. Add editions from a work page.</p>
      ) : (
        <div style={{ borderTop: `1px solid ${c.lineSoft}` }}>
          {p.items.map((it, i) => (
            <div key={it.editionId} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}` }}>
              <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", minWidth: 0 }}>
                <span style={{ color: c.faint, fontVariantNumeric: "tabular-nums" }}>{i + 1}.</span>
                <span style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: c.textDim }}>{it.title}</span>
                  <span style={{ ...muted, fontSize: "0.75rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {it.workTitle}{it.workAuthor ? ` · ${it.workAuthor}` : ""}
                  </span>
                </span>
              </span>
              <span style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexShrink: 0 }}>
                <span style={badge}>{it.format}</span>
                <span style={muted}>{it.durationSecs > 0 ? fmt(it.durationSecs) : "—"}</span>
                <span style={{ display: "flex", gap: "0.1rem" }}>
                  <button style={{ ...linkBtn, padding: "0.15rem 0.3rem" }} disabled={i === 0 || moving} title="Move up" onClick={() => move(it.editionId, it.position - 1)}>↑</button>
                  <button style={{ ...linkBtn, padding: "0.15rem 0.3rem" }} disabled={i === p.items.length - 1 || moving} title="Move down" onClick={() => move(it.editionId, it.position + 1)}>↓</button>
                </span>
                <button style={{ ...linkBtn, color: c.danger, padding: "0.15rem 0.3rem" }} title="Remove" onClick={() => removeItem(it.editionId)}>✕</button>
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
