import { useEffect, useRef, useState } from "preact/hooks";
import { api, media, type WorkDetail } from "../api";
import { fmtClock } from "../util";
import { backLink, c, ghostBtn, iconBtn, muted, videoEl } from "../styles";
import { IconCC, IconChevronLeft, IconFullscreen, IconVolume, IconVolumeOff } from "../components/svg";

export function VideoPlayer(props: {
  w: WorkDetail;
  editionId: number;
  onClose: () => void;
  onSelectEdition: (id: number) => void;
}) {
  const ed = props.w.editions.find((e) => e.id === props.editionId)!;
  const ref = useRef<HTMLVideoElement | null>(null);
  const saved = useRef(0);
  const [subs, setSubs] = useState(false);
  const [ccOn, setCcOn] = useState(false);
  const [isMuted, setIsMuted] = useState(false);
  const [time, setTime] = useState(0);
  const fileId = ed.files[0].id;

  useEffect(() => {
    let alive = true;
    fetch(media(`/subtitles/${fileId}`), { method: "HEAD" })
      .then((r) => { if (alive) setSubs(r.ok); })
      .catch(() => {});
    return () => { alive = false; };
  }, [fileId]);

  useEffect(() => {
    const t = window.setInterval(() => { if (ref.current) saved.current = ref.current.currentTime; }, 1000);
    return () => { clearInterval(t); };
  }, []);

  const save = async (pos: number, finished = false) => {
    if (pos <= 0) return;
    await api(`/progress/${ed.id}`, { method: "POST", body: JSON.stringify({ position: pos, duration: ed.duration, finished }) });
  };

  const toggleCC = () => {
    const v = ref.current;
    if (!v || v.textTracks.length === 0) return;
    const tt = v.textTracks[0];
    const on = tt.mode !== "showing";
    tt.mode = on ? "showing" : "hidden";
    setCcOn(on);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const v = ref.current;
      if (!v) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.key === " ") { e.preventDefault(); v.paused ? v.play().catch(() => {}) : v.pause(); }
      else if (e.key === "ArrowLeft") { e.preventDefault(); v.currentTime = Math.max(0, v.currentTime - 10); }
      else if (e.key === "ArrowRight") { e.preventDefault(); v.currentTime = Math.min(v.duration || Infinity, v.currentTime + 10); }
      else if (e.key === "f" || e.key === "F") { if (document.fullscreenElement) document.exitFullscreen(); else v.requestFullscreen?.(); }
      else if (e.key === "m" || e.key === "M") { v.muted = !v.muted; setIsMuted(v.muted); }
      else if (e.key === "c" || e.key === "C") { toggleCC(); }
      else if (e.key === "Escape" && !document.fullscreenElement) { save(saved.current); props.onClose(); }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    const ms = navigator.mediaSession;
    ms.metadata = new MediaMetadata({
      title: ed.title || props.w.title,
      artist: props.w.author || undefined,
      artwork: props.w.hasCover ? [{ src: media(`/covers/${props.w.id}.jpg`), sizes: "512x512", type: "image/jpeg" }] : [],
    });
    ms.setActionHandler("play", () => { ref.current?.play().catch(() => {}); ms.playbackState = "playing"; });
    ms.setActionHandler("pause", () => { ref.current?.pause(); ms.playbackState = "paused"; });
    ms.setActionHandler("seekbackward", () => { if (ref.current) ref.current.currentTime = Math.max(0, ref.current.currentTime - 10); });
    ms.setActionHandler("seekforward", () => { if (ref.current) ref.current.currentTime += 10; });
    try { ms.setActionHandler("seekto", (d) => { if (ref.current && d.seekTime != null) ref.current.currentTime = d.seekTime; }); } catch { /* older browsers */ }
    return () => {
      ms.metadata = null;
      for (const a of ["play", "pause", "seekbackward", "seekforward", "seekto"] as const) {
        try { ms.setActionHandler(a, null); } catch { /* not registered */ }
      }
    };
  }, [ed.id]);

  const next = props.w.editions.find((e) =>
    e.seasonNum !== undefined && e.id !== ed.id && e.seasonNum === ed.seasonNum && e.episodeNum === (ed.episodeNum || 0) + 1);

  return (
    <div style={{ position: "fixed", inset: 0, zIndex: 35, background: "#000", display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "0.8rem", padding: "0.35rem 1.1rem", minHeight: "3.2rem" }}>
        <button className="press" style={{ ...backLink, marginBottom: 0, color: "#f5f5f7" }} onClick={() => { save(saved.current); props.onClose(); }}>
          <IconChevronLeft size={16} /> {props.w.title}
        </button>
        <p style={{ ...muted, marginLeft: "auto", fontSize: "0.82rem" }}>
          {ed.title}{time > 0 ? ` · ${fmtClock(time)} / ${fmtClock(ed.duration)}` : null}
        </p>
      </div>
      <div style={{ flex: 1, minHeight: 0, background: "#000" }}>
        <video
          ref={ref}
          controls
          autoPlay
          playsInline
          poster={props.w.hasCover ? media(`/covers/${props.w.id}.jpg`) : undefined}
          src={media(`/stream/${fileId}`)}
          style={videoEl}
          onLoadedMetadata={() => {
            const v = ref.current;
            if (v && ed.position && ed.position > 0 && ed.position < ed.duration - 5) v.currentTime = ed.position;
          }}
          onTimeUpdate={() => {
            const v = ref.current;
            if (!v) return;
            saved.current = v.currentTime;
            setTime(v.currentTime);
            if ("mediaSession" in navigator && navigator.mediaSession.setPositionState) {
              try {
                navigator.mediaSession.setPositionState({ duration: ed.duration || v.duration, playbackRate: v.playbackRate, position: Math.min(v.currentTime, ed.duration || v.duration) });
              } catch { /* invalid state */ }
            }
          }}
          onPause={() => { save(saved.current); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "paused"; }}
          onPlay={() => { if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "playing"; }}
          onEnded={() => { save(ed.duration, true); if (next) props.onSelectEdition(next.id); else props.onClose(); }}
        >
          {subs ? <track kind="subtitles" src={media(`/subtitles/${fileId}`)} srcLang="en" label="Subtitles" /> : null}
        </video>
      </div>
      <div style={{ display: "flex", gap: "0.45rem", padding: "0.65rem 1.1rem 0.85rem", alignItems: "center" }}>
        <button className="press" style={ghostBtn} onClick={() => { save(saved.current); props.onClose(); }}>Save & close</button>
        {subs ? (
          <button className="press" style={ccOn ? { ...iconBtn, color: c.accent } : iconBtn} aria-label="Subtitles" title="Subtitles (c)" onClick={toggleCC}>
            <IconCC size={16} />
          </button>
        ) : null}
        <button className="press" style={iconBtn} aria-label={isMuted ? "Unmute" : "Mute"} title="Mute (m)" onClick={() => { const v = ref.current; if (v) { v.muted = !v.muted; setIsMuted(v.muted); } }}>
          {isMuted ? <IconVolumeOff size={16} /> : <IconVolume size={16} />}
        </button>
        <button className="press" style={iconBtn} aria-label="Fullscreen" title="Fullscreen (f)" onClick={() => { const v = ref.current; if (document.fullscreenElement) document.exitFullscreen(); else v?.requestFullscreen?.(); }}>
          <IconFullscreen size={16} />
        </button>
        <span style={{ flex: 1 }} />
        {next ? (
          <button className="press" style={ghostBtn} onClick={() => { save(saved.current); props.onSelectEdition(next.id); }}>
            Next episode
          </button>
        ) : null}
      </div>
    </div>
  );
}
