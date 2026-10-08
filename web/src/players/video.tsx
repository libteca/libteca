import { mediaOperationId } from "../mediaProgressLock";
import { getMediaIdentity } from "../mediaProgressIdentity";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, apiChecked, getToken, media, type EditionDetail, type PlaybackInfo, type WorkDetail } from "../api";
import { fmtClock } from "../util";
import { createMediaTimeline, resolveEditionPosition, resolveEndedFile, resolveFilePosition } from "../contracts/mediaTimeline";
import { VideoProgressSaver } from "./videoProgress";
import { c, ghostBtn, muted } from "../styles";
import {
  IconBack30, IconCC, IconChevronLeft, IconChevronRight, IconFwd30, IconFullscreen,
  IconPause, IconPlay, IconSpinner, IconVolume, IconVolumeOff,
} from "../components/svg";

const RATES = [1, 1.25, 1.5, 2];

function restoreWhenSeekable(video: HTMLVideoElement, target: number): boolean {
  for (let i = 0; i < video.seekable.length; i++) {
    if (target >= video.seekable.start(i) && target <= video.seekable.end(i)) {
      video.currentTime = target;
      return true;
    }
  }
  return target === 0;
}

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

function IconExitFullscreen(p: { size?: number }) {
  return (
    <svg width={p.size ?? 16} height={p.size ?? 16} viewBox="0 0 24 24"
      fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
      style={{ display: "block", flexShrink: 0 }}>
      <path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" />
    </svg>
  );
}

type VideoPlayerProps = {
  w: WorkDetail;
  editionId: number;
  onClose: () => void;
  onSelectEdition: (id: number) => void;
  timelineContext?: { generation: string; start: number; total: number; onSeek: (position: number) => void; onEnded: () => boolean; saver: VideoProgressSaver };
  localProgress?: Map<number, { position: number; isFinished: boolean }>;
  onProgress?: (editionId: number, patch: { position: number; duration: number; finished: boolean }) => void;
};

export function VideoPlayer(props: VideoPlayerProps) {
  const edition = props.w.editions.find((e) => e.id === props.editionId);
  if (edition?.available === false || edition?.files.length === 0) return <div role="alert">{edition.unavailableReason ?? "This edition has no available media files"}<button onClick={props.onClose}>Close</button></div>;
  if (edition?.generation) return <TimelineVideoPlayer key={`${edition.id}:${edition.generation}`} {...props} />;
  const fileId = props.w.editions.find((edition) => edition.id === props.editionId)?.files[0]?.id ?? 0;
  return <VideoPlayerSession key={`${props.editionId}:${fileId}`} {...props} />;
}

function TimelineVideoPlayer(props: VideoPlayerProps) {
  const edition = props.w.editions.find((e) => e.id === props.editionId)!;
  const timeline = createMediaTimeline(edition.id, edition.generation!, edition.files.map((f) => ({ fileId: f.id, durationSecs: f.duration > 0 ? f.duration : null })));
  const local = props.localProgress?.get(edition.id) ?? { position: edition.position ?? 0, isFinished: !!edition.isFinished };
  const version = useRef(0);
  const [position, setPosition] = useState(() => {
    try {
      if (!edition.isFinished && edition.resumeFileId !== undefined && edition.resumeFileOffset !== undefined && edition.progressGeneration !== edition.generation) return resolveFilePosition(timeline,timeline.generation,edition.resumeFileId,edition.resumeFileOffset);
      return resolveEditionPosition(timeline, timeline.generation, local.isFinished ? 0 : local.position);
    }
    catch { return resolveFilePosition(timeline,timeline.generation,timeline.files[0].fileId,0); }
  });
  const shared = useRef<VideoProgressSaver | null>(null);
  const base = useRef<Promise<{revision:number;resetGeneration:number}> | null>(null);
  if (!shared.current) shared.current = new VideoProgressSaver(`/progress/${edition.id}`, timeline.totalDurationSecs ?? 0, async (patch) => {
    let body: object = { ...patch, expectedGeneration: timeline.generation };
    if (!getMediaIdentity().ownerId) {
      base.current ??= apiChecked(`/progress/${edition.id}`);
      const current = await base.current;
      body = { ...patch, expectedGeneration: timeline.generation, revision: current.revision, resetGeneration: current.resetGeneration };
    }
    const result = await apiChecked<{revision:number;resetGeneration:number}>(`/progress/${edition.id}`, {method:"POST",body:JSON.stringify(body)});
    if (!getMediaIdentity().ownerId && result) base.current = Promise.resolve(result);
    return result;
  });
  const [problem,setProblem] = useState("");
  const [resumeBlocked, setResumeBlocked] = useState(!!edition.resumeConflict);
  if (resumeBlocked) return <div role="alert">Saved progress refers to changed media. Choose a new starting point.<button onClick={() => { void shared.current!.save(0, false, true); setPosition(resolveFilePosition(timeline,timeline.generation,timeline.files[0].fileId,0)); setResumeBlocked(false); }}>Start from beginning</button><button onClick={props.onClose}>Close</button></div>;
  const selected = edition.files.find((f) => f.id === position.fileId)!;
  const timelineFile = timeline.files.find((f) => f.fileId === position.fileId)!;
  const start = timelineFile.startSecs;
  if (start === null) return <div role="alert">This part has no known timeline offset. Rescan the edition before playing it.<button onClick={props.onClose}>Close</button></div>;
  const detail = { ...edition, position: position.fileOffsetSecs, files: [selected], chapters: edition.chapters };
  const w = { ...props.w, editions: props.w.editions.map((e) => e.id === edition.id ? detail : e) };
  const seek = (n: number) => {
    try { const resolved = resolveEditionPosition(timeline,timeline.generation,n); version.current++; setPosition(resolved); }
    catch { setProblem("That position needs a known duration. Rescan the edition before seeking there."); }
  };
  return <>
    {problem && <div role="alert">{problem}</div>}
    <VideoPlayerSession key={`${edition.id}:${timeline.generation}:${selected.id}:${version.current}`} {...props} w={w}
      localProgress={new Map([[edition.id,{position:position.fileOffsetSecs,isFinished:false}]])}
      timelineContext={{ generation:timeline.generation,start,total:timeline.totalDurationSecs ?? 0,saver:shared.current!,onSeek:seek,onEnded:() => {
        const index=timeline.files.findIndex((f)=>f.fileId===selected.id);
        if (index+1>=timeline.files.length) return false;
        try { const resolved = resolveEndedFile(timeline,timeline.generation,selected.id).position; version.current++; setPosition(resolved); return true; }
        catch { setProblem("The next part needs a known duration. Rescan the edition before continuing."); return true; }
      } }} />
  </>;
}

function VideoPlayerSession(props: VideoPlayerProps) {
  const found = props.w.editions.find((e) => e.id === props.editionId);
  const ed: EditionDetail = found ?? { id: props.editionId, format: "video", title: props.w.title, duration: 0, files: [], chapters: [] };
  const ref = useRef<HTMLVideoElement | null>(null);
  const saved = useRef(0);
  const sessionAlive = useRef(true);
  const completed = useRef(false);
  const live = useRef(false);
  const edRef = useRef(ed);
  edRef.current = ed;
  const progressRef = useRef<{ key: string; saver: VideoProgressSaver } | null>(null);
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
  const [active, setActive] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [uiVis, setUiVis] = useState(true);
  const [rate, setRate] = useState(1);
  const [glyph, setGlyph] = useState<"" | "play" | "pause">("");
  const [skip, setSkip] = useState<"" | "back" | "fwd">("");
  const [hoverT, setHoverT] = useState<number | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [thumbs, setThumbs] = useState<Thumbs | null>(null);
  const [fatal, setFatal] = useState("");
  const [bootKey, setBootKey] = useState(0);
  const modeRef = useRef<"direct" | "hls">("direct");
  const resumeRef = useRef(0);
  const baseProgress = useRef<Promise<{revision:number; resetGeneration:number}> | null>(null);
  const selectedMode = useRef<"auto" | "hls">("auto");
  const [pip, setPip] = useState(false);
  const [fsOn, setFsOn] = useState(false);
  const pipOK = typeof document !== "undefined" && document.pictureInPictureEnabled;
  const fileId = ed.files[0]?.id ?? 0;
  const progressKey = `${ed.id}:${fileId}:${bootKey}`;
  if (progressRef.current?.key !== progressKey) {
    const editionId = ed.id;
    progressRef.current = {
      key: progressKey,
      saver: props.timelineContext?.saver ?? new VideoProgressSaver(`/progress/${editionId}`, ed.duration, async (patch) => {
        const controller = new AbortController();
        const timer = window.setTimeout(() => controller.abort(), 15000);
        try {
          let body: object = patch;
          if (props.timelineContext && getMediaIdentity().ownerId) {
            body = { ...patch, position: patch.position + props.timelineContext.start, duration: props.timelineContext.total, expectedGeneration: props.timelineContext.generation };
          } else if (props.timelineContext) {
            baseProgress.current ??= apiChecked(`/progress/${editionId}`);
            const base = await baseProgress.current;
            body = { ...patch, position: patch.position + props.timelineContext.start, duration: props.timelineContext.total,
              revision: base.revision, resetGeneration: base.resetGeneration, expectedGeneration: props.timelineContext.generation };
          }
          const result = await apiChecked<{revision:number;resetGeneration:number}>(`/progress/${editionId}`, {
            method: "POST", body: JSON.stringify(body), signal: controller.signal,
          });
          if (props.timelineContext && result) baseProgress.current = Promise.resolve(result);
          return result;
        } finally {
          clearTimeout(timer);
        }
      }),
    };
  }

  const propsRef = useRef(props);
  propsRef.current = props;
  const durRef = useRef(dur);
  durRef.current = dur;

  const report = (position: number, finished: boolean) => {
    propsRef.current.onProgress?.(edRef.current.id, { position: position + (props.timelineContext?.start ?? 0), duration: props.timelineContext?.total || durRef.current || edRef.current.duration || 0, finished });
  };

  const save = (pos: number, finished = false, explicit = false) => {
    if (completed.current && !finished) return Promise.resolve();
    report(pos, finished);
    if (props.timelineContext) return props.timelineContext.saver.save(pos + props.timelineContext.start, finished, explicit);
    return progressRef.current!.saver.save(pos, finished, explicit);
  };


  const total = dur || ed.duration || 0;
  const chapters = (ed.chapters || []).filter((ch) => ch.end > ch.start && ch.start < total);

  useEffect(() => {
    sessionAlive.current = true;
    let alive = true;
    let hlsSid = "";
    setSrc(undefined);
    setActive(false);
    setFatal("");
    const retryPosition = bootKey > 0 ? saved.current : null;
    saved.current = 0;
    live.current = false;
    const local = props.localProgress?.get(props.editionId);
    const resumeFrom = local ?? { position: ed.position ?? 0, isFinished: !!ed.isFinished };
    resumeRef.current = retryPosition ?? (resumeFrom.position > 0 && !resumeFrom.isFinished ? resumeFrom.position : 0);
    const boot = async () => {
      let info: PlaybackInfo = { mode: "direct", fileId };
      try {
        const p = props.timelineContext
          ? await apiChecked(`/editions/${props.editionId}/playback-sessions`, { method:"POST", body:JSON.stringify({contractVersion:1,requestId:mediaOperationId(),generation:props.timelineContext.generation,fileId,fileOffsetSecs:resumeRef.current,mode:selectedMode.current}) })
          : await api(`/editions/${props.editionId}/playback`);
        if (p && (p.mode === "direct" || p.mode === "hls")) info = p;
      } catch (error) { if (props.timelineContext) throw error; }
      if (!alive) {
        if (info.sessionId) void apiChecked(`/hls/${info.sessionId}`,{method:"DELETE"}).catch(() => {});
        return;
      }
      modeRef.current = info.mode;
      setActive(true);
      if (info.mode === "hls" && info.sessionId) {
        hlsSid = info.sessionId;
        const url = media(`/hls/${info.sessionId}/index.m3u8`);
        const v = ref.current;
        if (v && v.canPlayType("application/vnd.apple.mpegurl")) {
          setSrc(url);
          return;
        }
        const { default: Hls } = await import("hls.js");
        if (!alive) return;
        if (Hls.isSupported() && v) {
          const hls = new Hls({ startPosition: resumeRef.current });
          if (!alive) {
            hls.destroy();
            return;
          }
          hlsRef.current = hls;
          let networkRetries = 0;
          let mediaRetries = 0;
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (!alive || !data.fatal) return;
            if (data.type === Hls.ErrorTypes.NETWORK_ERROR && networkRetries++ < 1) {
              hls.startLoad();
              return;
            }
            if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRetries++ < 1) {
              hls.recoverMediaError();
              return;
            }
            hls.destroy();
            if (hlsRef.current === hls) hlsRef.current = null;
            setWaiting(false);
            setFatal("Playback failed. Reload playback to create a new session.");
          });
          hls.loadSource(url);
          hls.attachMedia(v);
          return;
        }
        setSrc(url);
        return;
      }
      setSrc(media(`/stream/${info.fileId || fileId}${props.timelineContext ? `?generation=${encodeURIComponent(props.timelineContext.generation)}` : ""}`));
    };
    boot().catch((error: unknown) => {
      if (!alive) return;
      setWaiting(false);
      setFatal(error instanceof Error ? error.message : "Playback could not start");
    });
    return () => {
      alive = false;
      hlsRef.current?.destroy();
      hlsRef.current = null;
      if (hlsSid) {
        void fetch(media(`/hls/${hlsSid}`), {
          method: "DELETE", keepalive: true,
          headers: { Authorization: `Bearer ${getToken()}` },
        }).catch(() => {});
      }
    };
  }, [props.editionId, fileId, bootKey]);

  useEffect(() => {
    let alive = true;
    fetch(media(`/subtitles/${fileId}${props.timelineContext ? `?generation=${encodeURIComponent(props.timelineContext.generation)}` : ""}`), { method: "HEAD" })
      .then((r) => { if (alive) setSubs(r.ok); })
      .catch(() => {});
    return () => { alive = false; };
  }, [fileId]);

  useEffect(() => {
    setThumbs(null);
    if (!ed.files[0]?.videoCodec) return;
    let alive = true;
    api(`/editions/${props.editionId}/thumbs${props.timelineContext ? `?fileId=${fileId}&generation=${encodeURIComponent(props.timelineContext.generation)}` : ""}`)
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
    const sync = () => setFsOn(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", sync);
    return () => document.removeEventListener("fullscreenchange", sync);
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
    const leave = () => { if (sessionAlive.current && live.current && !completed.current) void save(ref.current?.currentTime ?? saved.current); };
    addEventListener("pagehide", leave);
    const p = window.setInterval(() => {
      const v = ref.current;
      if (v && !v.paused && live.current) void save(v.currentTime);
    }, 15000);
    return () => {
      removeEventListener("pagehide", leave);
      clearInterval(t);
      clearInterval(p);
      sessionAlive.current = false;
      if (live.current && !completed.current) {
        const pos = ref.current?.currentTime ?? saved.current;
        void save(pos);
      }
    };
  }, []);

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
    let t = v.currentTime + (props.timelineContext?.start ?? 0) + delta;
    if (isFinite(lim) && lim > 0) t = Math.min(t, lim);
    if (props.timelineContext) { seekTo(Math.max(0,t));flashSkip(delta < 0 ? "back" : "fwd");return; }
    v.currentTime = Math.max(0, t);
    saved.current = v.currentTime;
    setTime(v.currentTime);
    if (live.current) void save(v.currentTime, false, true);
    flashSkip(delta < 0 ? "back" : "fwd");
  };

  const seekTo = (t: number) => {
    const v = ref.current;
    if (!v || !isFinite(t)) return;
    const lim = total > 0 ? total : v.duration;
    if (isFinite(lim) && lim > 0) t = Math.min(t, lim);
    if (props.timelineContext) {
      const start=props.timelineContext.start;
      const duration=ed.files[0]?.duration || 0;
      if (!duration) { props.timelineContext.onSeek(Math.max(0,t)); return; }
      if (t < start || (duration>0 && t >= start+duration && t<total)) {
        void save(v.currentTime,false);
        props.timelineContext.onSeek(Math.max(0,t));return;
      }
      t-=start;
    }
    v.currentTime = Math.max(0, t);
    saved.current = v.currentTime;
    setTime(v.currentTime + (props.timelineContext?.start ?? 0));
    if (live.current) void save(v.currentTime, false, true);
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
    try { ms.setActionHandler("seekto", (d) => { if (ref.current && d.seekTime != null) seekTo(d.seekTime); }); } catch { /* older browsers */ }
    return () => {
      ms.metadata = null;
      for (const a of ["play", "pause", "seekbackward", "seekforward", "seekto"] as const) {
        try { ms.setActionHandler(a, null); } catch { /* not registered */ }
      }
    };
  }, [ed.id]);

  const scrubPct = total > 0 ? Math.min(1, time / total) : 0;
  const bufPct = total > 0 ? Math.min(1, buffered / total) : 0;

  if (fatal || !found || fileId === 0) {
    return (
      <div style={{ position: "fixed", inset: 0, zIndex: 35, background: "#000", display: "flex", flexDirection: "column", gap: "1rem", alignItems: "center", justifyContent: "center", padding: "1rem" }}>
        <p style={{ ...muted, margin: 0, fontSize: "0.95rem", maxWidth: "26rem", textAlign: "center", lineHeight: 1.5 }}>{fatal || "This edition is no longer available."}</p>
        <div style={{ display: "flex", gap: "0.6rem" }}>
          {fatal ? (
            <button className="press" style={ghostBtn} onClick={() => {
              const v = ref.current;
              if (v && live.current) {
                saved.current = v.currentTime;
                void save(v.currentTime, false, true);
              }
              setFatal("");
              setBootKey((k) => k + 1);
            }}>Retry</button>
          ) : null}
          <button className="press" style={ghostBtn} onClick={props.onClose}>Back</button>
        </div>
      </div>
    );
  }

  const scrubHover = (e: PointerEvent) => {
    const el = e.currentTarget as HTMLElement;
    const r = el.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    setHoverT(frac * total);
  };

  const thumbBox = (() => {
    if (!thumbs || hoverT == null || total <= 0 || (props.timelineContext && (hoverT < props.timelineContext.start || hoverT >= props.timelineContext.start + (ed.files[0]?.duration || 0)))) return null;
    const perSheet = thumbs.TileWidth * thumbs.TileHeight;
    const frame = Math.max(0, Math.min(Math.floor((hoverT - (props.timelineContext?.start ?? 0)) / thumbs.Interval), thumbs.TileCount * perSheet - 1));
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
      backgroundImage: `url(${media(`/editions/${props.editionId}/thumbs/${sheet}.jpg${props.timelineContext ? `?fileId=${fileId}&generation=${encodeURIComponent(props.timelineContext.generation)}` : ""}`)})`,
      backgroundSize: `${w * thumbs.TileWidth}px ${h * thumbs.TileHeight}px`,
      backgroundPosition: `-${col * w}px -${row * h}px`,
    };
  })();

  return (
    <div
      className="vp-root"
      style={{ position: "fixed", inset: 0, zIndex: 35, background: "#000", cursor: uiVis ? "default" : "none" }}
      onMouseMove={showUI}
    >
      <style>{`
        .vp-root { animation: vp-in 240ms var(--ease); }
        @keyframes vp-in { from { opacity: 0; transform: scale(1.015); } }
        .vbtn { background: none; border: none; color: rgba(245,245,247,0.9); cursor: pointer;
          width: 44px; height: 44px; border-radius: 12px; display: inline-flex; align-items: center; justify-content: center;
          transition: background 130ms ease, color 130ms ease, transform 130ms var(--ease); }
        .vbtn:hover { background: rgba(255,255,255,0.1); color: #fff; }
        .vbtn:active { transform: scale(0.94); }
        .vbtn.on { color: ${c.accent}; background: rgba(10,132,255,0.16); }
        .vui { transition: opacity 240ms var(--ease), transform 240ms var(--ease), visibility 0s linear 0s; }
        .vui-hide { opacity: 0; visibility: hidden; pointer-events: none;
          transition: opacity 190ms ease, transform 190ms ease, visibility 0s linear 190ms; }
        .vui-top.vui-hide { transform: translateY(-0.5rem); }
        .vui-bot.vui-hide { transform: translateY(0.5rem); }
        .vbar { padding-left: 1rem; padding-right: 1rem; }
        .vscrub { position: relative; height: 24px; display: flex; align-items: center; touch-action: none; }
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
          background: rgba(12,13,15,0.9); box-shadow: 0 0 0 1px rgba(255,255,255,0.12);
          transform: translate(-50%, -50%); pointer-events: none; transition: height 140ms var(--ease); }
        .vscrub:hover .vchap { height: 7px; }
        .vvol { width: 0; opacity: 0; overflow: hidden; transition: width 180ms var(--ease), opacity 180ms ease; }
        .vvolwrap:hover .vvol, .vvolwrap:focus-within .vvol { width: 4.2rem; opacity: 1; }
        @media (max-width: 640px) { .vbar { padding-left: 0.45rem; padding-right: 0.45rem; } }
        @media (prefers-reduced-motion: reduce) {
          .vp-root { animation: none; }
          .vui, .vui-hide { transition: none; }
          .vbtn { transition: none; }
        }
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
            if (!sessionAlive.current) return;
            const v = ref.current;
            if (!v) return;
            live.current = true;
            setDur(props.timelineContext?.total || (v.duration ? v.duration + (props.timelineContext?.start ?? 0) : ed.duration) || 0);
            const target = resumeRef.current;
            if (target <= 0) return;
            if (modeRef.current === "direct") {
              // Direct files resume on EVERY browser: the old condition
              // consulted the browser's unrelated HLS capability instead of
              // the mode actually selected for this resource.
              if (target < (v.duration || Infinity)) {
                v.currentTime = target;
                resumeRef.current = 0;
              }
              return;
            }
            // Native HLS: the seekable range grows as segments arrive, so
            // the restore retries until the target lies inside it.
            const started = Date.now();
            const tryRestore = () => {
              const cur = ref.current;
              if (!cur || resumeRef.current <= 0) return;
              if (restoreWhenSeekable(cur, resumeRef.current)) {
                resumeRef.current = 0;
                return;
              }
              if (Date.now() - started > 10000) return;
              window.setTimeout(tryRestore, 250);
            };
            tryRestore();
          }}
          onTimeUpdate={() => {
            if (!sessionAlive.current) return;
            const v = ref.current;
            if (!v) return;
            live.current = true;
            saved.current = v.currentTime;
            setTime(v.currentTime + (props.timelineContext?.start ?? 0));
            try {
              if (v.buffered.length > 0) setBuffered(v.buffered.end(v.buffered.length - 1) + (props.timelineContext?.start ?? 0));
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
          onError={() => {
            if (!sessionAlive.current) return;
            setWaiting(false);
            setPlaying(false);
            if (src && props.timelineContext && modeRef.current === "direct") {
              resumeRef.current = ref.current?.currentTime ?? saved.current;
              selectedMode.current = "hls";
              setBootKey((n)=>n+1);
            } else if (src) setFatal("Playback failed — the stream could not be loaded.");
          }}
          onPlay={() => { setPlaying(true); setWaiting(false); showUI(); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "playing"; }}
          onPause={() => { if (!sessionAlive.current || completed.current) return; setPlaying(false); setUiVis(true); if (live.current) void save(saved.current); if ("mediaSession" in navigator) navigator.mediaSession.playbackState = "paused"; }}
          onSeeked={() => {
            if (!sessionAlive.current) return;
            const v = ref.current;
            if (!v || !live.current) return;
            saved.current = v.currentTime;
            setTime(v.currentTime + (props.timelineContext?.start ?? 0));
            void save(v.currentTime, false, true);
          }}
          onEnded={() => {
            if (!sessionAlive.current || completed.current) return;
            setPlaying(false); setUiVis(true);
            const finalPosition = props.timelineContext ? ref.current?.currentTime ?? saved.current : total;
            if (props.timelineContext?.onEnded()) { void save(finalPosition); completed.current = true; return; }
            void save(finalPosition, true); completed.current = true;
            if (next) props.onSelectEdition(next.id); else props.onClose();
          }}
        >
          {subs ? <track kind="subtitles" src={media(`/subtitles/${fileId}${props.timelineContext ? `?generation=${encodeURIComponent(props.timelineContext.generation)}` : ""}`)} srcLang="en" label="Subtitles" /> : null}
        </video>

        {waiting && active && (
          <div style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: "none" }}>
            <span className="spin" style={{ color: "rgba(255,255,255,0.9)", filter: "drop-shadow(0 2px 8px rgba(0,0,0,0.5))" }}><IconSpinner size={38} /></span>
          </div>
        )}

        {(glyph || skip) && !waiting && active && (
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

        {active && !playing && !glyph && !skip && !waiting && (
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

        <div className={`vui vui-top${uiVis ? "" : " vui-hide"}`} style={{
          position: "absolute", top: 0, left: 0, right: 0, paddingTop: "0.7rem", paddingBottom: "2rem",
          display: "flex", alignItems: "flex-start", gap: "0.8rem",
          background: "linear-gradient(to bottom, rgba(0,0,0,0.75), transparent)",
        }}>
          <button className="vbtn press" style={{ color: "rgba(245,245,247,0.94)" }}
            onClick={() => { void save(saved.current); props.onClose(); }}
            aria-label="Back">
            <IconChevronLeft size={20} />
          </button>
          <div style={{ minWidth: 0, paddingTop: "0.3rem" }}>
            <div style={{ fontSize: "1.05rem", fontWeight: 650, letterSpacing: "-0.015em", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: "48vw", textShadow: "0 1px 10px rgba(0,0,0,0.6)" }}>
              {ed.seasonNum !== undefined ? ed.title || props.w.title : props.w.title}
            </div>
            <div style={{ color: "rgba(245,245,247,0.55)", fontSize: "0.8rem", marginTop: "0.1rem", whiteSpace: "nowrap", textShadow: "0 1px 8px rgba(0,0,0,0.6)" }}>
              {ed.seasonNum !== undefined ? `${props.w.title} · S${ed.seasonNum}E${ed.episodeNum} · ` : ""}
              {props.w.author ? `${props.w.author} · ` : ""}
              {fmtClock(Math.max(0, total - time))} left
            </div>
          </div>
        </div>

        <div className={`vui vui-bot vbar${uiVis ? "" : " vui-hide"}`} style={{
          position: "absolute", bottom: 0, left: 0, right: 0, paddingTop: "2.2rem", paddingBottom: "calc(0.8rem + env(safe-area-inset-bottom))",
          background: "linear-gradient(to top, rgba(0,0,0,0.82), transparent)",
          display: "flex", flexDirection: "column", gap: "0.1rem",
        }}>
              <div className="vscrub" onPointerMove={scrubHover} onPointerLeave={() => setHoverT(null)}>
                {hoverT != null && total > 0 && (
                  <div style={{
                    position: "absolute", left: `clamp(0px, calc(${(hoverT / total) * 100}% - 1.9rem), calc(100% - 3.8rem))`, top: "-1.7rem",
                    width: "3.8rem", textAlign: "center", fontSize: "0.74rem", fontVariantNumeric: "tabular-nums",
                    color: c.text, background: "rgba(12,13,15,0.72)", backdropFilter: "blur(12px) saturate(160%)", WebkitBackdropFilter: "blur(12px) saturate(160%)",
                    border: "1px solid rgba(255,255,255,0.09)", borderRadius: "7px", padding: "0.18rem 0", pointerEvents: "none",
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
                {chapters.filter((ch) => ch.start > 1).map((ch, i) => (
                  <div key={i} className="vchap" style={{ left: `${(ch.start / total) * 100}%` }} />
                ))}
                <div className="vthumb" style={{ left: `${scrubPct * 100}%` }} />
                <input
                  type="range" className="seek" min={0} max={total > 0 ? total : 1} step={0.1} value={total > 0 ? Math.min(time, total) : 0}
                  disabled={total <= 0}
                  aria-label="Seek"
                  style={{ position: "absolute", inset: 0, width: "100%", height: "24px", margin: 0, opacity: 0, cursor: total > 0 ? "pointer" : "default" }}
                  onInput={(e) => seekTo(Number((e.target as HTMLInputElement).value))}
                />
              </div>

              <div style={{ display: "flex", alignItems: "center", gap: "0.05rem" }}>
                <button className="vbtn press" onClick={togglePlay} aria-label={playing ? "Pause" : "Play"}>
                  {playing ? <IconPause size={22} /> : <IconPlay size={22} />}
                </button>
                {prevEp && <button className="vbtn press" aria-label="Previous episode" onClick={() => { void save(saved.current); props.onSelectEdition(prevEp.id); }}><IconChevronLeft size={20} /></button>}
                {next && <button className="vbtn press" aria-label="Next episode" onClick={() => { void save(saved.current); props.onSelectEdition(next.id); }}><IconChevronRight size={20} /></button>}
                <span className="vvolwrap" style={{ display: "inline-flex", alignItems: "center" }}>
                  <button className="vbtn press" aria-label={isMuted ? "Unmute" : "Mute"} onClick={() => setVolume(isMuted ? 1 : 0)}>
                    {isMuted ? <IconVolumeOff size={20} /> : <IconVolume size={20} />}
                  </button>
                  <span className="vvol" style={{ display: "inline-flex", alignItems: "center" }}>
                    <input
                      type="range" className="seek" min={0} max={1} step={0.02} value={isMuted ? 0 : vol} aria-label="Volume"
                      style={{ width: "4rem" }}
                      onInput={(e) => setVolume(Number((e.target as HTMLInputElement).value))}
                    />
                  </span>
                </span>
                <span style={{ color: "rgba(245,245,247,0.88)", fontSize: "0.8rem", fontWeight: 600, fontVariantNumeric: "tabular-nums", margin: "0 0.6rem 0 0.35rem", whiteSpace: "nowrap" }}>
                  {fmtClock(time)} <span style={{ color: "rgba(245,245,247,0.4)", fontWeight: 400 }}>/ {fmtClock(total)}</span>
                </span>
                <span style={{ flex: 1 }} />
                {subs && <button className={`vbtn press${ccOn ? " on" : ""}`} aria-label="Subtitles (c)" title="Subtitles (c)" onClick={toggleCC}><IconCC size={20} /></button>}
                <button className={`vbtn press${rate !== 1 ? " on" : ""}`} onClick={cycleRate} aria-label="Playback speed" title="Playback speed"
                  style={{ width: "auto", minWidth: "2.9rem", padding: "0 0.7rem", fontSize: "0.8rem", fontWeight: 650, fontVariantNumeric: "tabular-nums" }}>
                  {rate}x
                </button>
                {pipOK && (
                  <button className={`vbtn press${pip ? " on" : ""}`} onClick={togglePip} aria-label="Picture in picture" title="Picture in picture">
                    <IconPip size={20} />
                  </button>
                )}
                <button className={`vbtn press${fsOn ? " on" : ""}`} aria-label={fsOn ? "Exit fullscreen (f)" : "Fullscreen (f)"} title={fsOn ? "Exit fullscreen (f)" : "Fullscreen (f)"} onClick={toggleFullscreen}>
                  {fsOn ? <IconExitFullscreen size={20} /> : <IconFullscreen size={20} />}
                </button>
              </div>
            </div>
      </div>
      <style>{`@keyframes libteca-glyph { 0% { opacity: 1; transform: scale(0.92); } 70% { opacity: 1; } 100% { opacity: 0; transform: scale(1.08); } }`}</style>
    </div>
  );
}
