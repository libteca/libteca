import { useEffect, useRef, useState } from "preact/hooks";
import { api, media, type EditionDetail, type WorkDetail } from "../api";
import { Cover } from "../components/cover";
import { AudioPlayer, type AudioController, type PlayerFile } from "../players/audio";
import { VideoPlayer } from "../players/video";
import { IconCheck, IconPlay, IconBook } from "../components/svg";
import {
  badge, backLink, c, chapterList, chapterRow, ghostBtn, input, muted, primaryBtn,
  progressMini, tabRow, workHead, workMeta, workTitle,
} from "../styles";
import { editionState, fmt, formatLabel } from "../util";

const READER_FORMATS: ReadonlySet<string> = new Set(["epub", "cbz", "pdf"]);

function GenreChips(props: { genres?: string[] }) {
  if (!props.genres?.length) return null;
  return (
    <p style={{ display: "flex", flexWrap: "wrap", gap: "0.35rem", margin: "0.2rem 0 0" }}>
      {props.genres.map((g) => <span key={g} style={badge}>{g}</span>)}
    </p>
  );
}

function readerProgress(e: { isFinished?: boolean; percent?: number; page?: number; pageCount?: number }) {
  if (e.isFinished) return { label: "Finished", pct: 1 };
  if (e.percent && e.percent > 0 && e.percent < 1) return { label: `${Math.round(e.percent * 100)}% read`, pct: e.percent };
  if (e.page && e.page > 0 && e.pageCount) return { label: `Page ${e.page} / ${e.pageCount}`, pct: e.page / e.pageCount };
  return null;
}

export function WorkView(props: { id: number }) {
  const [w, setW] = useState<WorkDetail | null>(null);
  const [edition, setEdition] = useState<number>(0);
  const [videoEdition, setVideoEdition] = useState<number | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);

  const reload = () => api(`/works/${props.id}`).then(setW).catch(() => setW(null));
  useEffect(() => { reload(); }, [props.id]);
  useEffect(() => {
    api("/me").then((u: { isAdmin?: boolean }) => setIsAdmin(!!u.isAdmin)).catch(() => {});
  }, []);
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
    return <VideoPlayer w={w} editionId={videoEdition} onClose={() => setVideoEdition(null)} onSelectEdition={setVideoEdition} />;
  }
  if (isTV) return <EpisodeList w={w} onPlay={setVideoEdition} />;
  if (isMusic) return <TrackList w={w} />;
  if (isMovie) {
    return (
      <div>
        <button style={backLink} onClick={() => history.back()}>{"< Library"}</button>
        <div style={workHead}>
          <div style={{ width: "9.5rem", flexShrink: 0 }}><Cover has={w.hasCover} id={w.id} title={w.title} /></div>
          <div style={workMeta}>
            <h2 style={workTitle}>{w.title}</h2>
            <p style={muted}>{w.author}</p>
            <GenreChips genres={w.genres} />
            <p style={muted}>{fmt(first?.duration || 0)}</p>
            <button style={primaryBtn} onClick={() => setVideoEdition(first.id)}>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <IconPlay size={14} /> {first.position && !first.isFinished ? "Resume" : "Play"}
              </span>
            </button>
            <AddToPlaylist editionId={first.id} />
          </div>
        </div>
        <p style={{ ...muted, maxWidth: "46rem" }}>{w.description}</p>
      </div>
    );
  }
  return <EditionsView w={w} editionId={edition} setEdition={setEdition} isAdmin={isAdmin} reload={reload} />;
}

function BackButton() {
  return <button style={backLink} onClick={() => history.back()}>{"< Library"}</button>;
}

function EpisodeList(props: { w: WorkDetail; onPlay: (id: number) => void }) {
  const eps = [...props.w.editions].sort((a, b) => (a.seasonNum! - b.seasonNum!) || (a.episodeNum! - b.episodeNum!));
  const resumeEp = eps.find((e) => e.position && e.position > 0 && !e.isFinished);
  return (
    <div>
      <BackButton />
      <div style={workHead}>
        <div style={{ width: "9.5rem", flexShrink: 0 }}><Cover has={props.w.hasCover} id={props.w.id} title={props.w.title} /></div>
        <div style={workMeta}>
          <h2 style={workTitle}>{props.w.title}</h2>
          <GenreChips genres={props.w.genres} />
          <p style={muted}>{eps.length} episodes</p>
          {resumeEp && (
            <button style={primaryBtn} onClick={() => props.onPlay(resumeEp.id)}>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <IconPlay size={14} /> Continue S{resumeEp.seasonNum}E{resumeEp.episodeNum}
              </span>
            </button>
          )}
        </div>
      </div>
      <div style={chapterList}>
        {eps.map((e) => {
          const st = editionState(e);
          return (
            <div key={e.id} style={{ display: "flex", alignItems: "center", gap: "0.35rem", borderBottom: `1px solid ${c.lineSoft}` }}>
              <button style={{ ...chapterRow, width: "auto", flex: 1, minWidth: 0, borderBottom: "none", color: e.isFinished ? c.faint : c.textDim }} onClick={() => props.onPlay(e.id)}>
                <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", minWidth: 0 }}>
                  <span style={{ color: c.faint, fontVariantNumeric: "tabular-nums" }}>{e.episodeNum}.</span>
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.title}</span>
                </span>
                <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", flexShrink: 0 }}>
                  <span style={muted}>{fmt(e.duration)}</span>
                  {st.pct > 0 && !e.isFinished && <span style={progressMini(st.pct)} />}
                  {e.isFinished && <span style={{ color: c.ok, display: "inline-flex" }}><IconCheck size={13} /></span>}
                </span>
              </button>
              <AddToPlaylist editionId={e.id} compact />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function TrackList(props: { w: WorkDetail }) {
  const [idx, setIdx] = useState<number | null>(null);
  const ctl = useRef<AudioController | null>(null);
  const tracks = props.w.editions;
  const files: PlayerFile[] = tracks.map((t) => ({ id: t.files[0]?.id || 0, title: t.title, duration: t.duration }));

  const saveTrack = async (i: number, position: number, finished = false) => {
    const t = tracks[i];
    if (!t || position <= 1) return;
    await api(`/progress/${t.id}`, { method: "POST", body: JSON.stringify({ position, duration: t.duration, finished }) });
  };

  const trackAt = (abs: number) => {
    let cum = 0;
    for (let i = 0; i < files.length; i++) {
      if (cum + files[i].duration > abs) return { i, off: abs - cum };
      cum += files[i].duration;
    }
    return { i: files.length - 1, off: 0 };
  };

  return (
    <div>
      <BackButton />
      <div style={workHead}>
        <div style={{ width: "9.5rem", flexShrink: 0 }}><Cover has={props.w.hasCover} id={props.w.id} title={props.w.title} /></div>
        <div style={workMeta}>
          <h2 style={workTitle}>{props.w.title}</h2>
          <p style={muted}>{props.w.author} · {tracks.length} tracks</p>
          <GenreChips genres={props.w.genres} />
        </div>
      </div>
      <div style={chapterList}>
        {tracks.map((t, i) => (
          <div key={t.id} style={{ display: "flex", alignItems: "center", gap: "0.35rem", borderBottom: `1px solid ${c.lineSoft}` }}>
            <button style={{ ...chapterRow, width: "auto", flex: 1, minWidth: 0, borderBottom: "none", color: i === idx ? c.text : c.textDim }} onClick={() => { setIdx(i); ctl.current?.playAt(i, 0); }}>
              <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", minWidth: 0 }}>
                <span style={{ color: c.faint, fontVariantNumeric: "tabular-nums" }}>{i + 1}.</span>
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.title}</span>
              </span>
              <span style={{ ...muted, flexShrink: 0 }}>{fmt(t.duration)}</span>
            </button>
            <AddToPlaylist editionId={t.id} compact />
          </div>
        ))}
      </div>
      {idx != null && (
        <AudioPlayer
          files={files}
          header={props.w.title}
          sub={tracks[idx]?.title}
          artwork={props.w.hasCover ? media(`/covers/${props.w.id}.jpg`) : undefined}
          controllerRef={ctl}
          onIndexChange={setIdx}
          onPos={(abs) => { const { i, off } = trackAt(abs); saveTrack(i, off); }}
          onFileEnded={(i) => saveTrack(i, tracks[i]?.duration || 0, true)}
        />
      )}
    </div>
  );
}

function EditionsView(props: { w: WorkDetail; editionId: number; setEdition: (id: number) => void; isAdmin: boolean; reload: () => void }) {
  const w = props.w;
  const ed = w.editions.find((e) => e.id === props.editionId);
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState(0);
  const ctl = useRef<AudioController | null>(null);

  const files: PlayerFile[] = (ed?.files || []).map((f) => ({ id: f.id, title: `Part ${f.seq}`, duration: f.duration }));
  const playable = files.length > 0;

  const cumBeforeFile = (i: number) => files.slice(0, i).reduce((a, f) => a + f.duration, 0);
  const fileIndexOf = (fileId: number) => (ed?.files || []).findIndex((f) => f.id === fileId);

  const save = async (position: number, finished = false) => {
    if (!ed) return;
    if (position <= 1 && !finished) return;
    await api(`/progress/${ed.id}`, { method: "POST", body: JSON.stringify({ position, duration: ed.duration, finished }) });
  };

  const resumeOrPlay = () => {
    if (!ed) return;
    setPlaying(true);
    if (ed.position && ed.position > 0 && !ed.isFinished) {
      let fid = 0, before = 0, cum = 0;
      const fs = ed.files;
      for (let i = 0; i < fs.length; i++) {
        if (cum + fs[i].duration > ed.position) { fid = i; before = cum; break; }
        cum += fs[i].duration;
        if (i === fs.length - 1) { fid = i; before = cum - fs[i].duration; }
      }
      setTimeout(() => ctl.current?.playAt(fid, Math.max(0, ed.position! - before)), 60);
    } else {
      setTimeout(() => ctl.current?.playAt(0, 0), 60);
    }
  };

  if (!ed) return <p style={muted}>no edition</p>;

  const multi = w.editions.length > 1;
  const inProgress = ed.position && ed.position > 0 && !ed.isFinished;
  const readable = READER_FORMATS.has(ed.format.toLowerCase());
  const rp = readerProgress(ed);
  const currentChapter = (() => {
    if (!ed.chapters.length) return undefined;
    const c = ed.chapters;
    for (let i = c.length - 1; i >= 0; i--) if (pos >= c[i].start) return c[i].title;
    return c[0]?.title;
  })();

  return (
    <div>
      <BackButton />
      <div style={workHead}>
        <div style={{ width: "9.5rem", flexShrink: 0 }}><Cover has={w.hasCover} id={w.id} title={w.title} progress={ed.position && ed.duration ? ed.position / ed.duration : rp?.pct} /></div>
        <div style={workMeta}>
          <h2 style={workTitle}>{w.title}</h2>
          <p style={muted}>{w.author}</p>
          <GenreChips genres={w.genres} />
          {multi && (
            <p style={{ ...muted, fontSize: "0.8rem" }}>
              <span style={badge}>{w.editions.length} editions</span>
              <span style={{ marginLeft: "0.5rem" }}>resume follows this work</span>
            </p>
          )}
          <div style={tabRow}>
            {w.editions.map((e) => {
              const st = readerProgress(e) || editionState(e);
              const sel = e.id === ed.id;
              return (
                <button key={e.id} onClick={() => { props.setEdition(e.id); setPlaying(false); setPos(0); }}
                  title={st.label}
                  style={{
                    display: "inline-flex", gap: "0.55rem", alignItems: "center", cursor: "pointer", fontFamily: "inherit",
                    border: `1px solid ${sel ? c.accent : c.line}`, background: sel ? c.accentSoft : "none",
                    borderRadius: "9px", padding: "0.45rem 0.8rem",
                  }}>
                  <span style={badge}>{formatLabel(e.format)}</span>
                  <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", lineHeight: 1.25 }}>
                    <span style={{ fontSize: "0.78rem", color: sel ? c.text : c.muted }}>{st.label}</span>
                    {st.pct > 0 && !e.isFinished && (
                      <span style={{ width: "4.5rem", height: "3px", borderRadius: "999px", background: c.line, overflow: "hidden", display: "block", marginTop: "0.2rem" }}>
                        <span style={{ display: "block", height: "100%", width: `${st.pct * 100}%`, background: c.accent, borderRadius: "999px" }} />
                      </span>
                    )}
                  </span>
                </button>
              );
            })}
          </div>
          <EditionMenu w={w} edition={ed} isAdmin={props.isAdmin} reload={props.reload} />
          {(ed.format === "audio" || ed.format === "video") && <AddToPlaylist editionId={ed.id} />}
          {playable ? (
            <button style={primaryBtn} onClick={resumeOrPlay}>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <IconPlay size={14} />
                {inProgress ? `Resume · ${fmt(ed.duration - (ed.position || 0))} left` : ed.isFinished ? "Listen again" : "Listen"}
              </span>
            </button>
          ) : readable ? (
            <div style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexWrap: "wrap" }}>
              <button style={primaryBtn} onClick={() => { location.hash = `#/read?edition=${ed.id}&id=${w.id}`; }}>
                <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                  <IconBook size={14} />
                  {ed.isFinished ? "Read again" : rp && rp.pct > 0 ? `Resume · ${rp.label}` : "Read"}
                </span>
              </button>
              {ed.format.toLowerCase() === "pdf" && (
                <a style={{ ...ghostBtn, textDecoration: "none", display: "inline-flex", alignItems: "center", gap: "0.4rem" }} href={media(`/editions/${ed.id}/download`)} download>Download</a>
              )}
            </div>
          ) : (
            <p style={muted}>{formatLabel(ed.format)} edition · in-app reading not available yet</p>
          )}
          {ed.isFinished && <p style={{ ...muted, color: c.ok, display: "flex", gap: "0.35rem", alignItems: "center" }}><IconCheck size={13} /> Finished</p>}
        </div>
      </div>
      {w.description && <p style={{ ...muted, maxWidth: "46rem", marginBottom: "1.4rem" }}>{w.description}</p>}
      {playable && (
        <div style={chapterList}>
          {ed.chapters.map((ch, i) => {
            const active = currentChapter === ch.title;
            return (
              <button key={i} style={{ ...chapterRow, color: active ? c.text : c.textDim }} onClick={() => {
                const fi = fileIndexOf(ch.fileId);
                if (fi < 0) return;
                setPlaying(true);
                setTimeout(() => ctl.current?.playAt(fi, Math.max(0, ch.start - cumBeforeFile(fi))), 0);
              }}>
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{ch.title}</span>
                <span style={{ ...muted, flexShrink: 0 }}>{fmt(ch.end - ch.start)}</span>
              </button>
            );
          })}
          {ed.chapters.length === 0 && files.map((f, i) => (
            <button key={f.id} style={chapterRow} onClick={() => { setPlaying(true); setTimeout(() => ctl.current?.playAt(i, 0), 0); }}>
              <span>Part {i + 1}</span>
              <span style={muted}>{fmt(f.duration)}</span>
            </button>
          ))}
        </div>
      )}
      {playing && playable && (
        <AudioPlayer
          files={files}
          header={w.title}
          sub={currentChapter}
          artwork={w.hasCover ? media(`/covers/${w.id}.jpg`) : undefined}
          controllerRef={ctl}
          onPos={(abs) => { setPos(abs); save(abs); }}
          onQueueEnded={() => { save(ed.duration, true); setPlaying(false); }}
        />
      )}
    </div>
  );
}

function IconDots(p: { size?: number }) {
  return (
    <svg width={p.size ?? 14} height={p.size ?? 14} viewBox="0 0 24 24" fill="currentColor" style={{ display: "block", flexShrink: 0 }}>
      <circle cx="5" cy="12" r="1.8" />
      <circle cx="12" cy="12" r="1.8" />
      <circle cx="19" cy="12" r="1.8" />
    </svg>
  );
}

type WorkLite = { id: number; title: string; author: string | null };

const menuCard: preact.JSX.CSSProperties = {
  position: "absolute", top: "100%", left: 0, marginTop: "0.45rem", zIndex: 40,
  minWidth: "17rem", maxWidth: "22rem", background: c.bgRaised,
  border: `1px solid ${c.line}`, borderRadius: "12px", padding: "0.75rem",
  display: "flex", flexDirection: "column", gap: "0.45rem",
};

const menuRow: preact.JSX.CSSProperties = {
  background: "none", border: "none", cursor: "pointer", fontFamily: "inherit",
  color: c.textDim, fontSize: "0.88rem", textAlign: "left", padding: "0.35rem 0.45rem",
  borderRadius: "8px",
};

function PickerRow(props: { title: string; sub?: string | null; onClick: () => void; danger?: boolean }) {
  return (
    <button type="button" style={{ ...menuRow, color: props.danger ? c.danger : c.textDim }} onClick={props.onClick}>
      <span style={{ display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.title}</span>
      {props.sub ? (
        <span style={{ display: "block", color: c.muted, fontSize: "0.75rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.sub}</span>
      ) : null}
    </button>
  );
}

function WorkPicker(props: {
  libraryId: number;
  excludeId?: number;
  allowNew?: boolean;
  placeholder: string;
  onPick: (work: WorkLite | null, newTitle: string) => void;
  onClose: () => void;
}) {
  const [works, setWorks] = useState<WorkLite[]>([]);
  const [q, setQ] = useState("");
  useEffect(() => {
    api(`/libraries/${props.libraryId}/works`)
      .then((rows: WorkLite[]) => setWorks(Array.isArray(rows) ? rows.filter((r) => r.id !== props.excludeId) : []))
      .catch(() => setWorks([]));
  }, [props.libraryId]);
  const ql = q.trim().toLowerCase();
  const matches = ql
    ? works.filter((w) => w.title.toLowerCase().includes(ql) || (w.author || "").toLowerCase().includes(ql)).slice(0, 8)
    : works.slice(0, 8);
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.35rem" }}>
      <input
        style={input}
        placeholder={props.placeholder}
        value={q}
        autoFocus
        onInput={(e) => setQ((e.target as HTMLInputElement).value)}
      />
      {matches.map((w) => (
        <PickerRow key={w.id} title={w.title} sub={w.author} onClick={() => props.onPick(w, "")} />
      ))}
      {props.allowNew && ql && (
        <PickerRow title={`New work: "${q.trim()}"`} onClick={() => props.onPick(null, q.trim())} />
      )}
      {matches.length === 0 && !(props.allowNew && ql) && <p style={muted}>No matches.</p>}
      <button type="button" style={{ ...menuRow, color: c.muted }} onClick={props.onClose}>Cancel</button>
    </div>
  );
}

type PlaylistLite = { id: number; name: string; songCount: number };

function AddToPlaylist(props: { editionId: number; compact?: boolean }) {
  const [open, setOpen] = useState(false);
  const [lists, setLists] = useState<PlaylistLite[]>([]);
  const [added, setAdded] = useState("");
  const [newName, setNewName] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!open) return;
    setAdded(""); setErr("");
    api("/playlists").then((r: PlaylistLite[]) => setLists(Array.isArray(r) ? r : [])).catch(() => setLists([]));
  }, [open]);

  const addTo = async (pl: PlaylistLite) => {
    const res = await api(`/playlists/${pl.id}/items`, { method: "POST", body: JSON.stringify({ editionId: props.editionId }) });
    if (res.error) { setErr(res.error); return; }
    setAdded(pl.name);
    setTimeout(() => setOpen(false), 700);
  };

  const createAndAdd = async () => {
    if (!newName.trim()) return;
    setErr("");
    const res = await api("/playlists", { method: "POST", body: JSON.stringify({ name: newName.trim(), editionIds: [props.editionId] }) });
    if (res.error) { setErr(res.error); return; }
    setOpen(false);
  };

  const btnStyle: preact.JSX.CSSProperties = props.compact
    ? { ...ghostBtn, padding: "0.25rem 0.6rem", fontSize: "0.78rem" }
    : ghostBtn;

  if (!open) {
    return (
      <button type="button" style={btnStyle} title="Add to playlist" onClick={() => setOpen(true)}>+ Playlist</button>
    );
  }
  return (
    <div style={{ position: "relative" }}>
      <button type="button" style={{ ...btnStyle, borderColor: c.accent }} onClick={() => setOpen(false)}>+ Playlist</button>
      <div style={{ ...menuCard, left: "auto", right: 0 }}>
        {added ? (
          <p style={{ ...muted, margin: 0 }}>Added to {added}</p>
        ) : (
          <>
            {lists.map((pl) => (
              <PickerRow key={pl.id} title={pl.name} sub={`${pl.songCount} items`} onClick={() => addTo(pl)} />
            ))}
            {lists.length === 0 && <p style={muted}>No playlists yet.</p>}
            <div style={{ display: "flex", gap: "0.4rem" }}>
              <input style={{ ...input, flex: 1 }} placeholder="New playlist" value={newName}
                onInput={(e) => setNewName((e.target as HTMLInputElement).value)} />
              <button type="button" style={ghostBtn} onClick={createAndAdd}>Create</button>
            </div>
            {err && <p style={{ color: c.danger, margin: 0, fontSize: "0.8rem" }}>{err}</p>}
          </>
        )}
      </div>
    </div>
  );
}

function EditionMenu(props: { w: WorkDetail; edition: { id: number; title: string }; isAdmin: boolean; reload: () => void }) {  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<"" | "move" | "split" | "merge" | "merge-confirm">("");
  const [mergeTarget, setMergeTarget] = useState<WorkLite | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const post = async (path: string, body: unknown) => {
    setBusy(true);
    setErr("");
    const res = await api(path, { method: "POST", body: JSON.stringify(body) });
    setBusy(false);
    if (res && res.error) { setErr(res.error); return null; }
    return res as { workId?: number; sourceWorkId?: number; sourceDeleted?: boolean } | null;
  };

  const close = () => { setOpen(false); setMode(""); setMergeTarget(null); setErr(""); };

  const afterMove = (res: { sourceWorkId?: number; sourceDeleted?: boolean }) => {
    close();
    if (res.sourceDeleted && res.sourceWorkId === props.w.id) { location.hash = "#/library"; return; }
    props.reload();
  };

  const doMove = async (work: WorkLite | null, newTitle: string) => {
    const res = await post(`/editions/${props.edition.id}/move`, work ? { workId: work.id } : { newTitle });
    if (res) afterMove(res);
  };

  const doSplit = async () => {
    const res = await post(`/editions/${props.edition.id}/split`, {});
    if (res) afterMove(res);
  };

  const doMerge = async () => {
    if (!mergeTarget) return;
    const res = await post(`/works/${props.w.id}/merge`, { intoWorkId: mergeTarget.id });
    if (res) { close(); location.hash = `#/work?id=${mergeTarget.id}`; }
  };

  if (!open) {
    return (
      <button type="button" style={{ ...ghostBtn, display: "inline-flex", alignItems: "center", gap: "0.4rem" }} onClick={() => setOpen(true)}>
        <IconDots /> Manage
      </button>
    );
  }

  return (
    <div style={{ position: "relative" }}>
      <button type="button" style={{ ...ghostBtn, display: "inline-flex", alignItems: "center", gap: "0.4rem", borderColor: c.accent }} onClick={close}>
        <IconDots /> Manage
      </button>
      <div style={menuCard}>
        {mode === "" && (
          <>
            <PickerRow title="Move edition to…" onClick={() => setMode("move")} />
            <PickerRow title="Split into its own work" onClick={() => setMode("split")} />
            {props.isAdmin && <PickerRow title="Merge this work into…" onClick={() => setMode("merge")} />}
            <PickerRow title="Close" onClick={close} />
          </>
        )}
        {mode === "move" && (
          <WorkPicker
            libraryId={props.w.libraryId || 0}
            excludeId={props.w.id}
            allowNew
            placeholder="Search works or type a new title"
            onPick={doMove}
            onClose={() => setMode("")}
          />
        )}
        {mode === "split" && (
          <div style={{ display: "flex", flexDirection: "column", gap: "0.45rem" }}>
            <p style={muted}>Move "{props.edition.title}" into its own work?</p>
            <div style={{ display: "flex", gap: "0.5rem" }}>
              <button type="button" style={primaryBtn} disabled={busy} onClick={doSplit}>{busy ? "Splitting…" : "Split"}</button>
              <button type="button" style={ghostBtn} onClick={() => setMode("")}>Cancel</button>
            </div>
          </div>
        )}
        {mode === "merge" && (
          <WorkPicker
            libraryId={props.w.libraryId || 0}
            excludeId={props.w.id}
            placeholder="Search works in this library"
            onPick={(work) => {
              if (!work) return;
              setMergeTarget(work);
              setMode("merge-confirm");
            }}
            onClose={() => setMode("")}
          />
        )}
        {mode === "merge-confirm" && mergeTarget && (
          <div style={{ display: "flex", flexDirection: "column", gap: "0.45rem" }}>
            <p style={muted}>Merge all editions of "{props.w.title}" into "{mergeTarget.title}"? This work is deleted.</p>
            <div style={{ display: "flex", gap: "0.5rem" }}>
              <button type="button" style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} disabled={busy} onClick={doMerge}>{busy ? "Merging…" : "Merge"}</button>
              <button type="button" style={ghostBtn} onClick={() => setMode("merge")}>Back</button>
            </div>
          </div>
        )}
        {err && <p style={{ color: c.danger, margin: 0, fontSize: "0.8rem" }}>{err}</p>}
      </div>
    </div>
  );
}
