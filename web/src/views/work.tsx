import { useEffect, useRef, useState } from "preact/hooks";
import { api, media, type EditionDetail, type WorkDetail } from "../api";
import { Cover } from "../components/cover";
import { AudioPlayer, type AudioController, type PlayerFile } from "../players/audio";
import { VideoPlayer } from "../players/video";
import { IconCheck, IconPlay } from "../components/svg";
import {
  badge, backLink, c, chapterList, chapterRow, ghostBtn, muted, primaryBtn,
  progressMini, tabRow, workHead, workMeta, workTitle,
} from "../styles";
import { editionState, fmt, formatLabel } from "../util";

export function WorkView(props: { id: number }) {
  const [w, setW] = useState<WorkDetail | null>(null);
  const [edition, setEdition] = useState<number>(0);
  const [videoEdition, setVideoEdition] = useState<number | null>(null);

  useEffect(() => { api(`/works/${props.id}`).then(setW).catch(() => setW(null)); }, [props.id]);
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
            <p style={muted}>{fmt(first?.duration || 0)}</p>
            <button style={primaryBtn} onClick={() => setVideoEdition(first.id)}>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <IconPlay size={14} /> {first.position && !first.isFinished ? "Resume" : "Play"}
              </span>
            </button>
          </div>
        </div>
        <p style={{ ...muted, maxWidth: "46rem" }}>{w.description}</p>
      </div>
    );
  }
  return <EditionsView w={w} editionId={edition} setEdition={setEdition} />;
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
            <button key={e.id} style={{ ...chapterRow, color: e.isFinished ? c.faint : c.textDim }} onClick={() => props.onPlay(e.id)}>
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
        </div>
      </div>
      <div style={chapterList}>
        {tracks.map((t, i) => (
          <button key={t.id} style={{ ...chapterRow, color: i === idx ? c.text : c.textDim }} onClick={() => { setIdx(i); ctl.current?.playAt(i, 0); }}>
            <span style={{ display: "flex", gap: "0.7rem", alignItems: "baseline", minWidth: 0 }}>
              <span style={{ color: c.faint, fontVariantNumeric: "tabular-nums" }}>{i + 1}.</span>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.title}</span>
            </span>
            <span style={{ ...muted, flexShrink: 0 }}>{fmt(t.duration)}</span>
          </button>
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

function EditionsView(props: { w: WorkDetail; editionId: number; setEdition: (id: number) => void }) {
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
        <div style={{ width: "9.5rem", flexShrink: 0 }}><Cover has={w.hasCover} id={w.id} title={w.title} progress={ed.position && ed.duration ? ed.position / ed.duration : undefined} /></div>
        <div style={workMeta}>
          <h2 style={workTitle}>{w.title}</h2>
          <p style={muted}>{w.author}</p>
          {multi && (
            <p style={{ ...muted, fontSize: "0.8rem" }}>
              <span style={badge}>{w.editions.length} editions</span>
              <span style={{ marginLeft: "0.5rem" }}>resume follows this work</span>
            </p>
          )}
          <div style={tabRow}>
            {w.editions.map((e) => {
              const st = editionState(e);
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
          {playable ? (
            <button style={primaryBtn} onClick={resumeOrPlay}>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <IconPlay size={14} />
                {inProgress ? `Resume · ${fmt(ed.duration - (ed.position || 0))} left` : ed.isFinished ? "Listen again" : "Listen"}
              </span>
            </button>
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
