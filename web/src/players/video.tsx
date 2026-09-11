import { useEffect, useRef, useState } from "preact/hooks";
import { api, media, type PlaybackInfo, type WorkDetail } from "../api";
import { fmtClock } from "../util";
import { c } from "../styles";
import {
  IconBack30, IconCC, IconChevronLeft, IconChevronRight, IconFwd30, IconFullscreen,
  IconPause, IconPlay, IconSpinner, IconVolume, IconVolumeOff,
} from "../components/svg";

const RATES = [1, 1.25, 1.5, 2];

type Thumbs = {
  Width: number; Height: number; TileWidth: number; TileHeight: number;
  Interval: number; TileCount: number;
};

function IconPip(p: { size?: number }) {
  return (
    <svg width={p.size ?? 16} height={p.size ?? 16} viewBox="0 0 24 24"
      fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
      style={{ display: "block", flexShrink: 0 }}>
      <rect x="3" y="5" width="18" height="14" rx="2.5" />
      <rect x="12" y="12" width="7" height="5" rx="1" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function VideoPlayer(props: {
  w: WorkDetail;
  editionId: number;
  onClose: () => void;
  onSelectEdition: (id: number) => void;
}) {
  const ed = props.w.editions.find((e) => e.id === props.editionId)!;
  const ref = useRef<HTMLVideoElement | null>(null);
  const saved = useRef(0);
  const edRef = useRef(ed);
  edRef.current = ed;
  const lastSave = useRef({ pos: 0, fin: false });
  const hlsRef = useRef<{ destroy: () => void } | null>(null);
  const hideT = useRef<number | undefined>(undefined);
  const clickT = useRef<number | undefined>(undefined);
  const [subs, setSubs] = useState(false);
  const [ccOn, setCcOn] = useState(false);
  const [isMuted, setIsMuted] = useState(false);
  const [vol, setVol] = useState(1);
  const [time, setTime] = useState(0);
  const [dur, setDur] = useState(ed.duration || 0);
  const [buffered, setBuffered] = useState(0);
  const [src, setSrc] = useState<string | undefined>(undefined);
  const [playing, setPlaying] = useState(false);
  const [uiVis, setUiVis] = useState(true);
  const [rate, setRate] = useState(1);
  const [glyph, setGlyph] = useState<"" | "play" | "pause">("");
  const [skip, setSkip] = useState<"" | "back" | "fwd">("");
  const [hoverT, setHoverT] = useState<number | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [thumbs, setThumbs] = useState<Thumbs | null>(null);
  const [pip, setPip] = useState(false);
  const pipOK = typeof document !== "undefined" && document.pictureInPictureEnabled;
  const fileId = ed.files[0].id;

  const total = dur || ed.duration || 0;
  const chapters = (ed.chapters || []).filter((ch) => ch.end > ch.start && ch.start < total);

  useEffect(() => {
    let alive = true;
    let hlsSid = "";
    setSrc(undefined);
    saved.current = 0;
    lastSave.current = { pos: 0, fin: false };
    const boot = async () => {
      let info: PlaybackInfo = { mode: "direct", fileId };
      try {
        const p = await api(`/editions/${props.editionId}/playback`);
        if (p && (p.mode === "direct" || p.mode === "hls")) info = p;
      } catch {}
      if (!alive) return;
      if (info.mode === "hls" && info.sessionId) {
        hlsSid = info.sessionId;
        const start = ed.position && ed.position > 0 && !ed.isFinished ? `&start=${Math.floor(ed.position)}` : "";
        const url = media(`/hls/${info.sessionId}/index.m3u8`) + start;
        const v = ref.current;
        if (v && v.canPlayType("application/vnd.apple.mpegurl")) {
          setSrc(url);
          return;
        }
        const { default: Hls } = await import("hls.js");
        if (!alive) return;
        if (Hls.isSupported() && v) {
          const hls = new Hls();
          if (!alive) {
            hls.destroy();
            return;
          }
          hlsRef.current = hls;
          hls.loadSource(url);
          hls.attachMedia(v);
          return;
        }
        setSrc(url);
        return;
      }
      setSrc(media(`/stream/${info.fileId || fileId}`));
    };
    void boot();
    return () => {
      alive = false;
      hlsRef.current?.destroy();
      hlsRef.current = null;
      if (hlsSid) {
        void fetch(media(`/hls/${hlsSid}`), { method: "DELETE", keepalive: true }).catch(() => {});
      }
    };
  }, [props.editionId, fileId]);

  useEffect(() => {
    let alive = true;
    fetch(media(`/subtitles/${fileId}`), { method: "HEAD" })
      .then((r) => { if (alive) setSubs(r.ok); })
      .catch(() => {});
    return () => { alive = false; };
  }, [fileId]);

  useEffect(() => {
    setThumbs(null);
    if (!ed.files[0].videoCodec) return;
    let alive = true;
    api(`/editions/${props.editionId}/thumbs`)
      .then((m) => { if (alive && m && m.TileCount > 0 && m.Width > 0 && m.Height > 0 && m.Interval > 0) setThumbs(m as Thumbs); })
      .catch(() => {});
    return () => { alive = false; };
  }, [props.editionId]);

  useEffect(() => {
    const v = ref.current;
    if (!v || !document.pictureInPictureEnabled) return;
    const sync = () => setPip(document.pictureInPictureElement === v);
    v.addEventListener("enterpictureinpicture", sync);
    v.addEventListener("leavepictureinpicture", sync);
    return () => {
      v.removeEventListener("enterpictureinpicture", sync);
      v.removeEventListener("leavepictureinpicture", sync);
    };
  }, []);

  useEffect(() => {
    setTime(0);
    setBuffered(0);
    setDur(edRef.current.duration || 0);
    setPlaying(false);
    setWaiting(false);
    setHoverT(null);
  }, [props.editionId]);

  useEffect(() => {
    const t = window.setInterval(() => { if (ref.current) saved.current = ref.current.currentTime; }, 1000);
    return () => {
      clearInterval(t);
      const pos = ref.current?.currentTime || saved.current;
      if (pos > 0) void save(pos);
    };
  }, []);

  const save = async (pos: number, finished = false) => {
    if (pos <= 0 && !finished) return;
    if (lastSave.current.fin && !finished) return;
    if (lastSave.current.fin === finished && Math.abs(lastSave.current.pos - pos) < 0.5) return;
    lastSave.current = { pos, fin: finished };
    const cur = edRef.current;
    await api(`/progress/${cur.id}`, { method: "POST", body: JSON.stringify({ position: pos, duration: cur.duration, finished }) });
  };

  const showUI = () => {
    setUiVis(true);
    if (hideT.current) clearTimeout(hideT.current);
    const v = ref.current;
    if (v && !v.paused) {
      hideT.current = window.setTimeout(() => setUiVis(false), 2400);
    }
  };

  useEffect(() => () => { if (hideT.current) clearTimeout(hideT.current); if (clickT.current) clearTimeout(clickT.current); }, []);

  const flash = (g: "play" | "pause") => {
    setGlyph(g);
    window.setTimeout(() => setGlyph(""), 550);
  };

  const flashSkip = (s: "back" | "fwd") => {
    setSkip(s);
    window.setTimeout(() => setSkip(""), 500);
  };

  const togglePlay = () => {
    const v = ref.current;
    if (!v) return;
    if (v.paused) { void v.play().then(() => flash("play")).catch(() => {}); }
    else { v.pause(); flash("pause"); }
  };

  const seekBy = (delta: number) => {
    const v = ref.current;
    if (!v) return;
    const lim = total > 0 ? total : v.duration;
    let t = v.currentTime + delta;
    if (isFinite(lim) && lim > 0) t = Math.min(t, lim);
    v.currentTime = Math.max(0, t);
    saved.current = v.currentTime;
    setTime(v.currentTime);
    flashSkip(delta < 0 ? "back" : "fwd");
  };

  const seekTo = (t: number) => {
    const v = ref.current;
    if (!v || !isFinite(t)) return;
    const lim = total > 0 ? total : v.duration;
    if (isFinite(lim) && lim > 0) t = Math.min(t, lim);
    v.currentTime = Math.max(0, t);
    saved.current = v.currentTime;
    setTime(v.currentTime);
  };

  const toggleFullscreen = () => {
    const v = ref.current;
    if (!v) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else if (v.requestFullscreen) void v.requestFullscreen();
    else {
      const el = v as HTMLVideoElement & { webkitEnterFullscreen?: () => void };
      el.webkitEnterFullscreen?.();
    }
  };

  const togglePip = () => {
    const v = ref.current;
    if (!v) return;
    if (document.pictureInPictureElement) void document.exitPictureInPicture().catch(() => {});
    else void v.requestPictureInPicture().catch(() => {});
  };

  const cycleRate = () => {
    const v = ref.current;
    if (!v) return;
    const next = RATES[(RATES.indexOf(rate) + 1) % RATES.length];
    v.playbackRate = next;
    setRate(next);
  };

  const toggleCC = () => {
    const v = ref.current;
    if (!v || v.textTracks.length === 0) return;
    const tt = v.textTracks[0];
    const on = tt.mode !== "showing";
    tt.mode = on ? "showing" : "hidden";
    setCcOn(on);
  };

  const setVolume = (n: number) => {
    const v = ref.current;
    if (!v) return;
    v.volume = n;
    v.muted = n === 0;
    setVol(n);
    setIsMuted(n === 0);
  };

  const eps = [...props.w.editions].filter((e) => e.seasonNum !== undefined)
    .sort((a, b) => (a.seasonNum! - b.seasonNum!) || (a.episodeNum! - b.episodeNum!));
  const epIdx = eps.findIndex((e) => e.id === ed.id);
  const prevEp = epIdx > 0 ? eps[epIdx - 1] : null;
  const next = eps[epIdx >= 0 && epIdx < eps.length - 1 ? epIdx + 1 : -1] || null;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const v = ref.current;
      if (!v) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      showUI();
      if (e.key === " ") { e.preventDefault(); togglePlay(); }
      else if (e.key === "ArrowLeft") { e.preventDefault(); seekBy(-10); }
      else if (e.key === "ArrowRight") { e.preventDefault(); seekBy(10); }
      else if (e.key === "J" || e.key === "j") { seekBy(-10); }
      else if (e.key === "K" || e.key === "k") { togglePlay(); }
      else if (e.key === "L" || e.key === "l") { seekBy(10); }
      else if (e.key === "f" || e.key === "F") { toggleFullscreen(); }
      else if (e.key === "m" || e.key === "M") { setVolume(isMuted ? 1 : 0); }
      else if (e.key === "c" || e.key === "C") { toggleCC(); }
      else if (e.key === "Escape" && !document.fullscreenElement) { void save(saved.current); props.onClose(); }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [isMuted, rate, total]);

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    const ms = navigator.mediaSession;
    ms.metadata = new MediaMetadata({
      title: ed.title || props.w.title,
      artist: props.w.author || undefined,
      artwork: props.w.hasCover ? [{ src: media(`/covers/${props.w.id}.jpg`), sizes: "512x512", type: "image/jpeg" }] : [],
    });
    ms.setActionHandler("play", () => { ref.current?.play().catch(() => {}); });
    ms.setActionHandler("pause", () => { ref.current?.pause(); });
    ms.setActionHandler("seekbackward", () => seekBy(-10));
    ms.setActionHandler("seekforward", () => seekBy(10));
    try { ms.setActionHandler("seekto", (d) => { if (ref.current && d.seekTime != null) ref.current.currentTime = d.seekTime; }); } catch { /* older browsers */ }
    return () => {
      ms.metadata = null;
      for (const a of ["play", "pause", "seekbackward", "seekforward", "seekto"] as const) {
        try { ms.setActionHandler(a, null); } catch { /* not registered */ }
      }
    };
  }, [ed.id]);

  const scrubPct = total > 0 ? Math.min(1, time / total) : 0;
  const bufPct = total > 0 ? Math.min(1, buffered / total) : 0;

  const scrubHover = (e: PointerEvent) => {
    const el = e.currentTarget as HTMLElement;
    const r = el.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    setHoverT(frac * total);
  };

  const thumbBox = (() => {
    if (!thumbs || hoverT == null || total <= 0) return null;
    const perSheet = thumbs.TileWidth * thumbs.TileHeight;
    const frame = Math.max(0, Math.min(Math.floor(hoverT / thumbs.Interval), thumbs.TileCount * perSheet - 1));
    const sheet = Math.floor(frame / perSheet);
    const inSheet = frame % perSheet;
    const col = inSheet % thumbs.TileWidth;
    const row = Math.floor(inSheet / thumbs.TileWidth);
    const w = thumbs.Width;
    const h = thumbs.Height;
    return {
      left: `clamp(0px, calc(${(hoverT / total) * 100}% - ${w / 2}px), calc(100% - ${w}px))`,
      width: `${w}px`,
      height: `${h}px`,
      backgroundImage: `url(${media(`/editions/${props.editionId}/thumbs/${sheet}.jpg`)})`,
      backgroundSize: `${w * thumbs.TileWidth}px ${h * thumbs.TileHeight}px`,
      backgroundPosition: `-${col * w}px -${row * h}px`,
    };
  })();

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 35, background: "#000", cursor: uiVis ? "default" : "none" }}
      onMouseMove={showUI}
    >
      <style>{`
        .vbtn { background: none; border: none; color: rgba(245,245,247,0.9); cursor: pointer;
          width: 44px; height: 44px; border-radius: 12px; display: inline-flex; align-items: center; justify-content: center;
          transition: background 130ms ease, color 130ms ease, transform 130ms var(--ease); }
        .vbtn:hover { background: rgba(255,255,255,0.1); color: #fff; }
        .vbtn:active { transform: scale(0.94); }
        .vbtn.on { color: ${c.accent}; background: rgba(10,132,255,0.16); }
        .vscrub { position: relative; height: 20px; display: flex; align-items: center; touch-action: none; }
        .vtrack { position: absolute; left: 0; right: 0; height: 3.5px; border-radius: 999px;
          background: rgba(255,255,255,0.16); transition: height 140ms var(--ease); }
        .vscrub:hover .vtrack, .vscrub:focus-within .vtrack { height: 7px; }
        .vbuf { position: absolute; left: 0; height: 3.5px; border-radius: 999px; background: rgba(255,255,255,0.32);
          transition: height 140ms var(--ease); }
        .vscrub:hover .vbuf, .vscrub:focus-within .vbuf { height: 7px; }
        .vfill { position: absolute; left: 0; height: 3.5px; border-radius: 999px; background: ${c.accent};
          transition: height 140ms var(--ease); }
        .vscrub:hover .vfill, .vscrub:focus-within .vfill { height: 7px; }
        .vthumb { position: absolute; width: 13px; height: 13px; border-radius: 50%; background: #fff;
          box-shadow: 0 2px 10px rgba(0,0,0,0.55); transform: translate(-50%, -50%) scale(0.75); top: 50%;
          transition: transform 140ms var(--ease), box-shadow 140ms var(--ease); pointer-events: none; }
        .vscrub:hover .vthumb, .vscrub:focus-within .vthumb { transform: translate(-50%, -50%) scale(1.25);
          box-shadow: 0 2px 16px rgba(10,132,255,0.65); }
        .vchap { position: absolute; top: 50%; width: 3px; height: 3px; border-radius: 1px;
          background: rgba(12,13,15,0.9); transform: translate(-50%, -50%); pointer-events: none; }
        .vscrub:hover .vchap { height: 7px; width: 3.5px; }
        .vvol { width: 0; opacity: 0; overflow: hidden; transition: width 180ms var(--ease), opacity 180ms ease; }
        .vvolwrap:hover .vvol, .vvolwrap:focus-within .vvol { width: 4.2rem; opacity: 1; }
      `}</style>

      <div style={{ position: "absolute", inset: 0 }} onClick={(e) => {
        if ((e.target as HTMLElement).tagName !== "VIDEO") return;
        if (!uiVis) { showUI(); return; }
        if (clickT.current) clearTimeout(clickT.current);
        clickT.current = window.setTimeout(() => { clickT.current = undefined; togglePlay(); }, 240);
      }} onDblClick={(e) => {
        const el = e.target as HTMLElement;
        if (el.tagName !== "VIDEO") return;
        if (clickT.current) { clearTimeout(clickT.current); clickT.current = undefined; }
        const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const x = e.clientX - r.left;
        if (x < r.width * 0.35) seekBy(-10);
        else if (x > r.width * 0.65) seekBy(10);
        else toggleFullscreen();
      }}>
        <video
          ref={ref}
          playsInline
          autoPlay
          poster={props.w.hasCover ? media(`/covers/${props.w.id}.jpg`) : undefined}
          src={src}
          style={{ width: "100%", height: "100%", background: "#000", display: "block", objectFit: "contain" }}
          onLoadedMetadata={() => {
            const v = ref.current;
            if (!v) return;
            setDur(v.duration || ed.duration || 0);
            if (ed.position && ed.position > 0 && ed.position < (ed.duration || Infinity) - 5 && v.src && !v.src.startsWith("blob:") && !v.canPlayType("application/vnd.apple.mpegurl")) {
              v.currentTime = ed.position;
            }
          }}
          onTimeUpdate={() => {
            const v = ref.current;
            if (!v) return;
            saved.current = v.currentTime;
            setTime(v.currentTime);
            try {
              if (v.buffered.length > 0) setBuffered(v.buffered.end(v.buffered.length - 1));
            } catch { /* noop */ }
            if ("mediaSession" in navigator && navigator.mediaSession.setPositionState) {
              try {
                navigator.mediaSession.setPositionState({ duration: total || v.duration, playbackRate: v.playbackRate, position: Math.min(v.currentTime, total || v.duration) });
              } catch { /* invalid state */ }
            }
          }}
          onWaiting={() => setWaiting(true)}
          onPlaying={() => setWaiting(false)}
          onCanPlay={() => setWaiting(false)}
          onError={() => { setWaiting(false); setPlaying(false); }}
          onPlay={() => { setPlaying(true); setWaiting(false); showUI(); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "playing"; }}
          onPause={() => { setPlaying(false); setUiVis(true); void save(saved.current); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "paused"; }}
          onEnded={() => { setPlaying(false); setUiVis(true); void save(total, true); if (next) props.onSelectEdition(next.id); else props.onClose(); }}
        >
          {subs ? <track kind="subtitles" src={media(`/subtitles/${fileId}`)} srcLang="en" label="Subtitles" /> : null}
        </video>

        {waiting && src && (
          <div style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: "none" }}>
            <span className="spin" style={{ color: "rgba(255,255,255,0.85)" }}><IconSpinner size={36} /></span>
          </div>
        )}

        {(glyph || skip) && !waiting && src && (
          <div aria-hidden style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: "none" }}>
            <div style={{
              width: skip ? "104px" : "72px", height: skip ? "104px" : "72px", borderRadius: "50%",
              display: "flex", alignItems: "center", justifyContent: "center", gap: skip ? 10 : 0,
              background: "rgba(0,0,0,0.55)", backdropFilter: "blur(8px)", WebkitBackdropFilter: "blur(8px)",
              color: "rgba(255,255,255,0.95)", animation: "libteca-glyph 0.55s var(--ease) forwards",
            }}>
              {skip === "back" ? <><IconBack30 size={30} /><span style={{ fontSize: "0.8rem", fontWeight: 650 }}>10</span></> :
                skip === "fwd" ? <><span style={{ fontSize: "0.8rem", fontWeight: 650 }}>10</span><IconFwd30 size={30} /></> :
                glyph === "play" ? <IconPlay size={30} /> : <IconPause size={30} />}
            </div>
          </div>
        )}

        {src && !playing && !glyph && !skip && !waiting && (
          <div style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: "none" }}>
            <div style={{
              width: "84px", height: "84px", borderRadius: "50%", display: "flex", alignItems: "center", justifyContent: "center",
              background: "rgba(0,0,0,0.5)", backdropFilter: "blur(10px)", WebkitBackdropFilter: "blur(10px)",
              color: "rgba(255,255,255,0.96)", border: "1px solid rgba(255,255,255,0.14)",
            }}>
              <IconPlay size={34} />
            </div>
          </div>
        )}

        {uiVis && (
          <>
            <div style={{
              position: "absolute", top: 0, left: 0, right: 0, padding: "0.7rem 1rem 2rem",
              display: "flex", alignItems: "flex-start", gap: "0.8rem",
              background: "linear-gradient(to bottom, rgba(0,0,0,0.75), transparent)",
            }}>
              <button className="vbtn press" style={{ color: "rgba(245,245,247,0.94)" }}
                onClick={() => { void save(saved.current); props.onClose(); }}
                aria-label="Back">
                <IconChevronLeft size={20} />
              </button>
              <div style={{ minWidth: 0, paddingTop: "0.3rem" }}>
                <div style={{ fontSize: "1.05rem", fontWeight: 650, letterSpacing: "-0.015em", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: "48vw" }}>
                  {ed.seasonNum !== undefined ? ed.title || props.w.title : props.w.title}
                </div>
                <div style={{ color: "rgba(245,245,247,0.55)", fontSize: "0.8rem", marginTop: "0.1rem", whiteSpace: "nowrap" }}>
                  {ed.seasonNum !== undefined ? `${props.w.title} · S${ed.seasonNum}E${ed.episodeNum} · ` : ""}
                  {props.w.author ? `${props.w.author} · ` : ""}
                  {fmtClock(Math.max(0, total - time))} left
                </div>
              </div>
            </div>

            <div style={{
              position: "absolute", bottom: 0, left: 0, right: 0, padding: "2.2rem 1rem calc(0.8rem + env(safe-area-inset-bottom))",
              background: "linear-gradient(to top, rgba(0,0,0,0.82), transparent)",
              display: "flex", flexDirection: "column", gap: "0.1rem",
            }}>
              <div className="vscrub" onPointerMove={scrubHover} onPointerLeave={() => setHoverT(null)}>
                {hoverT != null && total > 0 && (
                  <div style={{
                    position: "absolute", left: `clamp(0px, calc(${(hoverT / total) * 100}% - 1.9rem), calc(100% - 3.8rem))`, top: "-1.7rem",
                    width: "3.8rem", textAlign: "center", fontSize: "0.74rem", fontVariantNumeric: "tabular-nums",
                    color: c.text, background: "rgba(12,13,15,0.92)", borderRadius: "7px", padding: "0.18rem 0", pointerEvents: "none",
                    boxShadow: "0 6px 18px rgba(0,0,0,0.5)",
                  }}>{fmtClock(hoverT)}</div>
                )}
                {hoverT != null && thumbBox && (
                  <div style={{
                    position: "absolute", ...thumbBox,
                    bottom: "calc(100% + 2rem)", overflow: "hidden", pointerEvents: "none",
                    borderRadius: "8px", background: "#000", border: "1px solid rgba(255,255,255,0.14)",
                    boxShadow: "0 6px 18px rgba(0,0,0,0.5)",
                  }} />
                )}
                <div className="vtrack" />
                <div className="vbuf" style={{ width: `${bufPct * 100}%` }} />
                <div className="vfill" style={{ width: `${scrubPct * 100}%` }} />
                {chapters.map((ch, i) => (
                  <div key={i} className="vchap" style={{ left: `${(ch.start / total) * 100}%` }} />
                ))}
                <div className="vthumb" style={{ left: `${scrubPct * 100}%` }} />
                <input
                  type="range" className="seek" min={0} max={total > 0 ? total : 1} step={0.1} value={total > 0 ? Math.min(time, total) : 0}
                  aria-label="Seek"
                  style={{ position: "absolute", inset: 0, width: "100%", height: "20px", margin: 0, opacity: 0, cursor: "pointer" }}
                  onInput={(e) => seekTo(Number((e.target as HTMLInputElement).value))}
                />
              </div>

              <div style={{ display: "flex", alignItems: "center", gap: "0.05rem" }}>
                <button className="vbtn press" onClick={togglePlay} aria-label={playing ? "Pause" : "Play"}>
                  {playing ? <IconPause size={22} /> : <IconPlay size={22} />}
                </button>
                {prevEp && <button className="vbtn press" aria-label="Previous episode" onClick={() => { void save(saved.current); props.onSelectEdition(prevEp.id); }}><IconChevronLeft size={19} /></button>}
                {next && <button className="vbtn press" aria-label="Next episode" onClick={() => { void save(saved.current); props.onSelectEdition(next.id); }}><IconChevronRight size={19} /></button>}
                <span className="vvolwrap" style={{ display: "inline-flex", alignItems: "center" }}>
                  <button className="vbtn press" aria-label={isMuted ? "Unmute" : "Mute"} onClick={() => setVolume(isMuted ? 1 : 0)}>
                    {isMuted ? <IconVolumeOff size={19} /> : <IconVolume size={19} />}
                  </button>
                  <span className="vvol" style={{ display: "inline-flex", alignItems: "center" }}>
                    <input
                      type="range" className="seek" min={0} max={1} step={0.02} value={isMuted ? 0 : vol} aria-label="Volume"
                      style={{ width: "4rem" }}
                      onInput={(e) => setVolume(Number((e.target as HTMLInputElement).value))}
                    />
                  </span>
                </span>
                <span style={{ color: "rgba(245,245,247,0.65)", fontSize: "0.8rem", fontVariantNumeric: "tabular-nums", margin: "0 0.6rem 0 0.35rem", whiteSpace: "nowrap" }}>
                  {fmtClock(time)} <span style={{ opacity: 0.45 }}>/ {fmtClock(total)}</span>
                </span>
                <span style={{ flex: 1 }} />
                {subs && <button className={`vbtn press${ccOn ? " on" : ""}`} aria-label="Subtitles (c)" title="Subtitles (c)" onClick={toggleCC}><IconCC size={20} /></button>}
                <button className="vbtn press" onClick={cycleRate} aria-label="Playback speed" title="Playback speed"
                  style={{ width: "auto", padding: "0 0.7rem", fontSize: "0.8rem", fontWeight: 650, fontVariantNumeric: "tabular-nums" }}>
                  {rate}x
                </button>
                {pipOK && (
                  <button className={`vbtn press${pip ? " on" : ""}`} onClick={togglePip} aria-label="Picture in picture" title="Picture in picture">
                    <IconPip size={20} />
                  </button>
                )}
                <button className="vbtn press" aria-label="Fullscreen (f)" title="Fullscreen (f)" onClick={toggleFullscreen}><IconFullscreen size={20} /></button>
              </div>
            </div>
          </>
        )}
      </div>
      <style>{`@keyframes libteca-glyph { 0% { opacity: 1; transform: scale(0.92); } 70% { opacity: 1; } 100% { opacity: 0; transform: scale(1.08); } }`}</style>
    </div>
  );
}
