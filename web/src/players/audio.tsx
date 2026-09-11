import { useEffect, useRef, useState } from "preact/hooks";
import { media } from "../api";
import { fmtClock } from "../util";
import { c, mono, playerBar } from "../styles";
import { toast } from "../toast";
import { IconBack30, IconFwd30, IconMoon, IconPause, IconPlay } from "../components/svg";

export type PlayerFile = { id: number; title: string; duration: number };

export type AudioController = { playAt: (index: number, offset: number) => void };

const SPEEDS = [1, 1.25, 1.5, 1.75, 2, 0.75];
const SLEEPS = [0, 5, 15, 30, 60];

export function AudioPlayer(props: {
  files: PlayerFile[];
  header: string;
  sub?: string;
  artwork?: string;
  controllerRef?: { current: AudioController | null };
  onPos?: (abs: number) => void;
  onIndexChange?: (index: number) => void;
  onFileEnded?: (index: number) => void;
  onQueueEnded?: () => void;
}) {
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const curIdx = useRef(0);
  const pendingOffset = useRef(0);
  const [playing, setPlaying] = useState(false);
  const [abs, setAbs] = useState(0);
  const [rate, setRate] = useState(() => Number(localStorage.getItem("libteca-rate")) || 1);
  const [sleepMin, setSleepMin] = useState(0);
  const [sleepLeft, setSleepLeft] = useState(0);
  const [hoverT, setHoverT] = useState<number | null>(null);
  const [chOpen, setChOpen] = useState(false);
  const [idxV, setIdxV] = useState(0);
  const sleepAt = useRef(0);
  const lastSent = useRef(0);
  const absRef = useRef(0);
  const queueEnded = useRef(false);
  const chWrap = useRef<HTMLDivElement | null>(null);
  const onPosRef = useRef(props.onPos);
  onPosRef.current = props.onPos;

  const total = props.files.reduce((a, f) => a + f.duration, 0);
  const cumBefore = (i: number) => props.files.slice(0, i).reduce((a, f) => a + f.duration, 0);

  const load = (i: number, offset: number, autoplay = true) => {
    const a = audioRef.current;
    if (!a || !props.files[i]) return;
    if (curIdx.current !== i || !a.src) {
      curIdx.current = i;
      setIdxV(i);
      props.onIndexChange?.(i);
      pendingOffset.current = offset;
      a.src = media(`/stream/${props.files[i].id}`);
      if (autoplay) a.play().catch(() => {});
    } else if (a.readyState === 0) {
      pendingOffset.current = offset;
      if (autoplay) a.play().catch(() => {});
    } else {
      a.currentTime = offset;
      if (autoplay) a.play().catch(() => {});
    }
  };

  useEffect(() => {
    props.controllerRef && (props.controllerRef.current = { playAt: (i, off) => load(i, off) });
    return () => { if (props.controllerRef) props.controllerRef.current = null; };
  });

  const filesKey = props.files.map((f) => f.id).join(",");
  useEffect(() => { load(0, 0, false); }, [filesKey]);

  useEffect(() => {
    if (audioRef.current) audioRef.current.playbackRate = rate;
    try { localStorage.setItem("libteca-rate", String(rate)); } catch { /* storage unavailable */ }
  }, [rate]);

  useEffect(() => {
    if (!sleepMin) { sleepAt.current = 0; setSleepLeft(0); return; }
    sleepAt.current = Date.now() + sleepMin * 60000;
  }, [sleepMin]);

  useEffect(() => {
    const t = window.setInterval(() => {
      if (!sleepAt.current) return;
      const left = sleepAt.current - Date.now();
      if (left <= 0) {
        sleepAt.current = 0;
        setSleepMin(0);
        setSleepLeft(0);
        audioRef.current?.pause();
      } else setSleepLeft(left);
    }, 500);
    return () => {
      audioRef.current?.pause();
      clearInterval(t);
      if (absRef.current > 1 && !queueEnded.current) onPosRef.current?.(absRef.current);
    };
  }, []);

  useEffect(() => {
    if (!chOpen) return;
    const onDown = (e: Event) => { if (!chWrap.current?.contains(e.target as Node)) setChOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setChOpen(false); };
    addEventListener("pointerdown", onDown);
    addEventListener("keydown", onKey);
    chWrap.current?.querySelector("[data-active]")?.scrollIntoView({ block: "nearest" });
    return () => { removeEventListener("pointerdown", onDown); removeEventListener("keydown", onKey); };
  }, [chOpen]);

  const seekAbs = (target: number) => {
    let cum = 0;
    for (let i = 0; i < props.files.length; i++) {
      if (cum + props.files[i].duration > target || i === props.files.length - 1) {
        load(i, Math.max(0, target - cum));
        return;
      }
      cum += props.files[i].duration;
    }
  };

  const nudge = (delta: number) => {
    const a = audioRef.current;
    if (!a) return;
    const target = cumBefore(curIdx.current) + a.currentTime + delta;
    seekAbs(Math.max(0, Math.min(total - 0.5, target)));
  };

  const toggle = () => {
    const a = audioRef.current;
    if (!a) return;
    if (a.paused) a.play().catch(() => {}); else a.pause();
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.key === " ") { e.preventDefault(); toggle(); }
      else if (e.key === "ArrowLeft") { e.preventDefault(); nudge(-30); }
      else if (e.key === "ArrowRight") { e.preventDefault(); nudge(30); }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [total]);

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    const ms = navigator.mediaSession;
    const cur = props.files[idxV];
    ms.metadata = new MediaMetadata({
      title: cur?.title || props.header,
      artist: props.header,
      artwork: props.artwork ? [{ src: props.artwork, sizes: "512x512", type: "image/jpeg" }] : [],
    });
    ms.setActionHandler("play", () => { audioRef.current?.play().catch(() => {}); });
    ms.setActionHandler("pause", () => { audioRef.current?.pause(); });
    ms.setActionHandler("seekbackward", () => nudge(-30));
    ms.setActionHandler("seekforward", () => nudge(30));
    try { ms.setActionHandler("seekto", (d) => { if (d.seekTime != null) seekAbs(d.seekTime); }); } catch { /* older browsers */ }
    return () => {
      ms.metadata = null;
      for (const a of ["play", "pause", "seekbackward", "seekforward", "seekto"] as const) {
        try { ms.setActionHandler(a, null); } catch { /* not registered */ }
      }
    };
  }, [props.files, props.header, idxV]);

  const cycleRate = () => {
    const i = SPEEDS.indexOf(rate);
    setRate(SPEEDS[(i + 1) % SPEEDS.length]);
  };

  const scrubHover = (e: PointerEvent) => {
    const el = e.currentTarget as HTMLElement;
    const r = el.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    setHoverT(frac * total);
  };

  const pct = total > 0 ? Math.min(100, (abs / total) * 100) : 0;

  return (
    <div className="player-bar" style={{
      ...playerBar,
      left: "1.5rem",
      right: "1.5rem",
      bottom: "calc(1rem + env(safe-area-inset-bottom))",
      borderRadius: "16px",
      background: "rgba(18,19,22,0.86)",
      backdropFilter: "blur(24px) saturate(160%)",
      WebkitBackdropFilter: "blur(24px) saturate(160%)",
      borderTop: "none",
      border: "1px solid rgba(255,255,255,0.07)",
      boxShadow: "0 18px 50px rgba(0,0,0,0.55)",
      maxWidth: "62rem",
      margin: "0 auto",
      padding: 0,
      flexDirection: "column",
      flexWrap: "nowrap",
      gap: 0,
      minHeight: 0,
    }}>
      <audio
        ref={audioRef}
        preload="metadata"
        onLoadedMetadata={() => {
          const a = audioRef.current;
          if (a && pendingOffset.current > 0) { a.currentTime = pendingOffset.current; }
          pendingOffset.current = 0;
        }}
        onPlay={() => { queueEnded.current = false; setPlaying(true); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "playing"; }}
        onError={() => { setPlaying(false); toast("Playback failed — the audio could not be loaded", "error"); }}
        onPause={() => {
          setPlaying(false);
          if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "paused";
          const a = audioRef.current;
          if (a && props.onPos && a.currentTime > 1) props.onPos(cumBefore(curIdx.current) + a.currentTime);
        }}
        onTimeUpdate={() => {
          const a = audioRef.current;
          if (!a) return;
          const pos = cumBefore(curIdx.current) + a.currentTime;
          absRef.current = pos;
          setAbs(pos);
          if (onPosRef.current && pos - lastSent.current > 10) { lastSent.current = pos; onPosRef.current(pos); }
          if ("mediaSession" in navigator && navigator.mediaSession.setPositionState && total > 0) {
            try { navigator.mediaSession.setPositionState({ duration: total, playbackRate: rate, position: Math.min(pos, total) }); } catch { /* invalid state */ }
          }
        }}
        onEnded={() => {
          const i = curIdx.current;
          props.onFileEnded?.(i);
          if (i < props.files.length - 1) load(i + 1, 0);
          else {
            queueEnded.current = true;
            setPlaying(false);
            props.onQueueEnded?.();
          }
        }}
      />
      <div style={{ display: "flex", alignItems: "center", gap: "0.9rem", padding: "0.7rem 0.95rem 0.25rem", flexWrap: "wrap" }}>
        {props.artwork && <img src={props.artwork} alt="" className="ap-art" style={{ boxShadow: c.coverShadow }} />}
        <div style={{ display: "flex", flexDirection: "column", minWidth: 0, flex: 1 }}>
          <span style={{ fontSize: "0.95rem", fontWeight: 600, letterSpacing: "-0.01em", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.header}</span>
          <span style={{ fontSize: "0.78rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {props.sub || props.files[idxV]?.title}
          </span>
        </div>
        <div style={{ display: "flex", gap: "0.15rem", alignItems: "center", flexShrink: 0 }}>
          <button className="press ap-btn" style={{ ...barBtn, color: c.textDim }} aria-label="Back 30 seconds" title="Back 30s" onClick={() => nudge(-30)}><IconBack30 size={19} /></button>
          <button className="press" style={{ ...barBtn, width: "46px", height: "46px", background: c.accent, color: "#fff", boxShadow: c.accentGlow }} aria-label={playing ? "Pause" : "Play"} title="Play or pause" onClick={toggle}>
            {playing ? <IconPause size={22} /> : <IconPlay size={22} />}
          </button>
          <button className="press ap-btn" style={{ ...barBtn, color: c.textDim }} aria-label="Forward 30 seconds" title="Forward 30s" onClick={() => nudge(30)}><IconFwd30 size={19} /></button>
        </div>
        <div style={{ display: "flex", gap: "0.3rem", alignItems: "center", flexShrink: 0 }}>
          <button className="press ap-pill" style={speedPill} aria-label="Playback speed" title="Playback speed" onClick={cycleRate}>{rate}x</button>
          <div style={{ position: "relative", display: "inline-flex" }}>
            <button className="press ap-btn" style={{ ...barBtn, color: sleepMin || sleepLeft ? c.accent : c.textDim }} aria-label="Sleep timer" title="Sleep timer">
              <IconMoon size={17} />
            </button>
            <select
              value={sleepMin}
              onChange={(e) => setSleepMin(Number((e.target as HTMLSelectElement).value))}
              aria-label="Sleep timer"
              style={{ position: "absolute", inset: 0, opacity: 0, cursor: "pointer", appearance: "none" as const, width: "100%", height: "100%" }}
            >
              {SLEEPS.map((m) => <option key={m} value={m}>{m === 0 ? "Off" : `${m} min`}</option>)}
            </select>
          </div>
          {sleepLeft > 0 && (
            <span style={{ fontFamily: mono, fontSize: "0.66rem", lineHeight: 1.2, color: c.accent, background: c.accentSoft, borderRadius: "999px", padding: "0.16rem 0.5rem", fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(sleepLeft / 1000)}</span>
          )}
          {props.files.length > 1 && (
            <div ref={chWrap} style={{ position: "relative", display: "inline-flex" }}>
              <button className="press ap-btn" style={{ ...barBtn, color: chOpen ? c.text : c.textDim }} aria-label="Chapter list" title="Chapters" aria-expanded={chOpen} onClick={() => setChOpen(!chOpen)}>
                <IconChapters size={18} />
              </button>
              {chOpen && (
                <div style={{ position: "absolute", right: 0, bottom: "calc(100% + 0.55rem)", zIndex: 2, width: "19rem", maxWidth: "calc(100vw - 3rem)", maxHeight: "40vh", overflowY: "auto", padding: "0.3rem", borderRadius: "12px", background: "rgba(18,19,22,0.92)", backdropFilter: "blur(24px) saturate(160%)", WebkitBackdropFilter: "blur(24px) saturate(160%)", border: "1px solid rgba(255,255,255,0.09)", boxShadow: "0 18px 50px rgba(0,0,0,0.55)" }}>
                  {props.files.map((f, i) => {
                    const active = i === idxV;
                    return (
                      <button key={f.id} data-active={active ? "" : undefined} className="row-hit" style={{ display: "flex", alignItems: "center", gap: "0.55rem", width: "100%", background: "none", border: "none", borderBottom: "none", padding: "0.45rem 0.5rem", borderRadius: "8px", cursor: "pointer", textAlign: "left" as const, color: active ? c.text : c.textDim }} onClick={() => { seekAbs(cumBefore(i)); setChOpen(false); }}>
                        <span style={{ width: "3px", alignSelf: "stretch", borderRadius: "999px", background: active ? c.accent : "transparent", flexShrink: 0 }} />
                        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: "0.82rem" }}>{f.title}</span>
                        <span style={{ fontSize: "0.72rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(f.duration)}</span>
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: "0.65rem", padding: "0.1rem 0.95rem 0.8rem" }}>
        <span style={timeTxt}>{fmtClock(abs)}</span>
        <div
          className="ap-seek"
          style={{ position: "relative", flex: 1, minWidth: "4rem", height: "22px", display: "flex", alignItems: "center", touchAction: "none" }}
          onPointerMove={scrubHover}
          onPointerLeave={() => setHoverT(null)}
        >
          {hoverT != null && total > 0 && (
            <div style={{
              position: "absolute", left: `clamp(0px, calc(${(hoverT / total) * 100}% - 1.9rem), calc(100% - 3.8rem))`, top: "-1.5rem",
              width: "3.8rem", textAlign: "center", fontSize: "0.72rem", fontVariantNumeric: "tabular-nums",
              color: c.text, background: "rgba(12,13,15,0.94)", border: "1px solid rgba(255,255,255,0.09)",
              borderRadius: "6px", padding: "0.18rem 0", pointerEvents: "none", zIndex: 2,
            }}>{fmtClock(hoverT)}</div>
          )}
          <div className="ap-track" style={{ position: "absolute", left: 0, right: 0, top: "50%", transform: "translateY(-50%)", borderRadius: "999px", background: "rgba(255,255,255,0.15)" }} />
          <div className="ap-track" style={{ position: "absolute", left: 0, width: `${pct}%`, top: "50%", transform: "translateY(-50%)", borderRadius: "999px", background: c.accent }} />
          <div className="ap-thumb" style={{ position: "absolute", left: `calc(${pct}% - 6px)`, top: "50%", transform: "translateY(-50%)", width: "12px", height: "12px", borderRadius: "50%", background: "#fff", boxShadow: "0 1px 4px rgba(0,0,0,0.5)" }} />
          <input
            type="range" min={0} max={Math.max(1, Math.floor(total))} step={1} value={Math.min(Math.floor(abs), Math.max(1, Math.floor(total)))}
            className="seek"
            aria-label="Seek"
            style={{ position: "absolute", inset: 0, width: "100%", height: "22px", margin: 0, opacity: 0, cursor: "pointer" }}
            onInput={(e) => seekAbs(Number((e.target as HTMLInputElement).value))}
          />
        </div>
        <span style={timeTxt}>{fmtClock(total)}</span>
      </div>
      <style>{`
        .ap-btn { background: transparent; transition: background 140ms ease, color 140ms ease; }
        .ap-btn:hover { background: rgba(255,255,255,0.08); }
        .ap-pill { background: rgba(255,255,255,0.05); border: 1px solid rgba(255,255,255,0.09); transition: background 140ms ease, border-color 140ms ease; }
        .ap-pill:hover { background: rgba(255,255,255,0.1); border-color: rgba(255,255,255,0.16); }
        .ap-art { width: 56px; height: 56px; border-radius: 10px; object-fit: cover; flex-shrink: 0; transition: transform 180ms ease; }
        .ap-art:hover { transform: scale(1.04); }
        .ap-track { height: 4px; transition: height 150ms ease; }
        .ap-seek:hover .ap-track, .ap-seek:focus-within .ap-track { height: 6px; }
        .ap-thumb { transition: box-shadow 150ms ease; }
        .ap-seek:hover .ap-thumb, .ap-seek:focus-within .ap-thumb { box-shadow: 0 0 12px rgba(255,255,255,0.9), 0 1px 4px rgba(0,0,0,0.5); }
        @media (max-width: 639px) { .ap-art { width: 48px; height: 48px; } }
      `}</style>
    </div>
  );
}

function IconChapters(p: { size?: number }) {
  return (
    <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" style={{ display: "block", flexShrink: 0 }}>
      <path d="M4 6h16M4 12h16M4 18h10" />
    </svg>
  );
}

const barBtn: preact.JSX.CSSProperties = {
  border: "none", color: c.text, cursor: "pointer",
  display: "inline-flex", alignItems: "center", justifyContent: "center",
  width: "44px", height: "44px", borderRadius: "50%", padding: 0, flexShrink: 0,
};

const speedPill: preact.JSX.CSSProperties = {
  height: "30px", minWidth: "46px", padding: "0 0.7rem", borderRadius: "999px",
  color: c.textDim, cursor: "pointer", display: "inline-flex", alignItems: "center",
  justifyContent: "center", flexShrink: 0, fontFamily: mono, fontSize: "0.72rem",
  fontWeight: 600, fontVariantNumeric: "tabular-nums",
};

const timeTxt: preact.JSX.CSSProperties = {
  fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0,
};
