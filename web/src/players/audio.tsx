import { useEffect, useRef, useState } from "preact/hooks";
import { media } from "../api";
import { fmtClock } from "../util";
import { c, playerBar } from "../styles";
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
  const sleepAt = useRef(0);
  const lastSent = useRef(0);
  const absRef = useRef(0);
  const onPosRef = useRef(props.onPos);
  onPosRef.current = props.onPos;

  const total = props.files.reduce((a, f) => a + f.duration, 0);
  const cumBefore = (i: number) => props.files.slice(0, i).reduce((a, f) => a + f.duration, 0);

  const load = (i: number, offset: number, autoplay = true) => {
    const a = audioRef.current;
    if (!a || !props.files[i]) return;
    if (curIdx.current !== i || !a.src) {
      curIdx.current = i;
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
  });

  const filesKey = props.files.map((f) => f.id).join(",");
  useEffect(() => { load(0, 0, false); }, [filesKey]);

  useEffect(() => {
    if (audioRef.current) audioRef.current.playbackRate = rate;
    localStorage.setItem("libteca-rate", String(rate));
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
      clearInterval(t);
      if (absRef.current > 1) onPosRef.current?.(absRef.current);
    };
  }, []);

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
    const cur = props.files[curIdx.current];
    ms.metadata = new MediaMetadata({
      title: cur?.title || props.header,
      artist: props.header,
      artwork: props.artwork ? [{ src: props.artwork, sizes: "512x512", type: "image/jpeg" }] : [],
    });
    ms.setActionHandler("play", () => { audioRef.current?.play().catch(() => {}); ms.playbackState = "playing"; });
    ms.setActionHandler("pause", () => { audioRef.current?.pause(); ms.playbackState = "paused"; });
    ms.setActionHandler("seekbackward", () => nudge(-30));
    ms.setActionHandler("seekforward", () => nudge(30));
    try { ms.setActionHandler("seekto", (d) => { if (d.seekTime != null) seekAbs(d.seekTime); }); } catch { /* older browsers */ }
    return () => {
      ms.metadata = null;
      for (const a of ["play", "pause", "seekbackward", "seekforward", "seekto"] as const) {
        try { ms.setActionHandler(a, null); } catch { /* not registered */ }
      }
    };
  }, [props.files, props.header]);

  const cycleRate = () => {
    const i = SPEEDS.indexOf(rate);
    setRate(SPEEDS[(i + 1) % SPEEDS.length]);
  };

  return (
    <div style={playerBar}>
      <audio
        ref={audioRef}
        preload="metadata"
        onLoadedMetadata={() => {
          const a = audioRef.current;
          if (a && pendingOffset.current > 0) { a.currentTime = pendingOffset.current; }
          pendingOffset.current = 0;
        }}
        onPlay={() => { setPlaying(true); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "playing"; }}
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
            setPlaying(false);
            props.onQueueEnded?.();
          }
        }}
      />
      {props.artwork && <img src={props.artwork} alt="" style={{ width: "2.4rem", height: "2.4rem", borderRadius: "5px", objectFit: "cover", flexShrink: 0 }} />}
      <div style={{ display: "flex", flexDirection: "column", minWidth: 0, width: "11rem", flexShrink: 1 }}>
        <span style={{ fontSize: "0.85rem", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.header}</span>
        <span style={{ fontSize: "0.73rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {props.sub || props.files[curIdx.current]?.title}
        </span>
      </div>
      <div style={{ display: "flex", gap: "0.15rem", alignItems: "center", flexShrink: 0 }}>
        <button style={{ ...barBtn, color: c.muted }} title="Back 30s" onClick={() => nudge(-30)}><IconBack30 size={18} /></button>
        <button style={barBtn} title="Play or pause" onClick={toggle}>
          {playing ? <IconPause size={20} /> : <IconPlay size={20} />}
        </button>
        <button style={{ ...barBtn, color: c.muted }} title="Forward 30s" onClick={() => nudge(30)}><IconFwd30 size={18} /></button>
      </div>
      <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(abs)}</span>
      <input
        type="range" min={0} max={Math.max(1, Math.floor(total))} step={1} value={Math.floor(abs)}
        className="seek"
        style={{ flex: 1, minWidth: "4rem", accentColor: c.accent }}
        onInput={(e) => seekAbs(Number((e.target as HTMLInputElement).value))}
        aria-label="Seek"
      />
      <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(total)}</span>
      <div style={{ display: "flex", gap: "0.35rem", alignItems: "center", flexShrink: 0 }}>
        <button style={{ ...barBtn, fontSize: "0.75rem", fontWeight: 600, color: c.textDim, width: "auto", padding: "0.4rem 0.55rem", fontVariantNumeric: "tabular-nums" }} title="Playback speed" onClick={cycleRate}>{rate}x</button>
        <div style={{ position: "relative", display: "inline-flex" }}>
          <button style={{ ...barBtn, color: sleepMin || sleepLeft ? c.accent : c.muted }} title="Sleep timer">
            <IconMoon size={15} />
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
        {sleepLeft > 0 && <span style={{ fontSize: "0.72rem", color: c.accent, fontVariantNumeric: "tabular-nums" }}>{fmtClock(sleepLeft / 1000)}</span>}
      </div>
    </div>
  );
}

const barBtn: preact.JSX.CSSProperties = {
  background: "none", border: "none", color: c.text, cursor: "pointer",
  display: "inline-flex", alignItems: "center", justifyContent: "center",
  width: "2.2rem", height: "2.2rem", borderRadius: "50%", padding: 0,
};
