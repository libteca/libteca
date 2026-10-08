import { toast } from "../toast";
import { useEffect, useRef, useState } from "preact/hooks";
import { media, type EditionDetail } from "../api";
import { AudioProgressSaver } from "./audioProgress";

type EditionAudioProps = {
  edition: EditionDetail;
  sessionToken: string;
  position?: number;
  audioRef: { current: HTMLAudioElement | null };
  seekRef: { current: ((position: number) => void) | null };
  onPlaying: (playing: boolean) => void;
  onTime: (position: number, duration: number) => void;
  onProgress: (position: number, finished: boolean) => void;
  onEnded: () => void;
};

export function EditionAudio(props: EditionAudioProps) {
  const files = props.edition.files;
  const total = files.every(file => file.duration > 0) ? files.reduce((sum, file) => sum + file.duration, 0) : 0;
  const at = (position: number) => {
    const target = Math.max(0, (total > 0 ? Math.min(total, position) : position));
    let before = 0;
    for (let i = 0; i < files.length; i++) {
      if (files[i].duration <= 0) return { index: i, offset: Math.max(0, position - before) };
      if (before + files[i].duration > target || i === files.length - 1) return { index: i, offset: target - before };
      before += files[i].duration;
    }
    return { index: 0, offset: 0 };
  };
  const fileVersion = useRef(0);
  const [current, setCurrent] = useState(() => ({ ...at(props.position ?? (props.edition.isFinished ? 0 : props.edition.position || 0)), version: 0 }));
  const [streamError, setStreamError] = useState(false);
  const [resumeBlocked, setResumeBlocked] = useState(!!props.edition.resumeConflict);
  const saver = useRef<AudioProgressSaver | null>(null);
  if (!saver.current) {
    saver.current = new AudioProgressSaver(`/progress/${props.edition.id}`, total, props.sessionToken);
    saver.current.begin();
  }
  const before = files.slice(0, current.index).reduce((sum, file) => sum + file.duration, 0);
  const save = (position: number, finished = false, explicit = false) => {
    props.onProgress(position, finished);
    void saver.current!.save(position, finished, explicit);
  };
  const seek = (position: number) => {
    if (!Number.isFinite(position) || position < 0) return;
    if (!total && position > 0) { toast("Rescan this edition before seeking across an unknown duration", "error"); return; }
    const next = at(position);
    if (next.index === current.index && props.audioRef.current?.readyState) props.audioRef.current.currentTime = next.offset;
    else setCurrent({ ...next, version: ++fileVersion.current });
    props.onTime(Math.max(0, Math.min(total, position)), total);
    save(Math.max(0, Math.min(total, position)), false, true);
  };
  useEffect(() => {
    props.seekRef.current = seek;
    return () => { if (props.seekRef.current === seek) props.seekRef.current = null; };
  });
  if (streamError) return <div role="alert">Audio could not be opened. Its timeline may have changed.<button onClick={() => location.reload()}>Reload current media</button></div>;
  if (resumeBlocked) return <div role="alert">Saved progress refers to changed media. Choose a new starting point.<button onClick={() => { setCurrent({ index: 0, offset: 0, version: ++fileVersion.current }); save(0, false, true); setResumeBlocked(false); }}>Start from beginning</button></div>;
  if (!files[current.index]) return null;
  return <EditionAudioFile
    key={`${current.index}:${files[current.index].id}:${current.version}`}
    fileId={files[current.index].id}
    generation={props.edition.generation}
    onStreamError={() => setStreamError(true)}
    offset={current.offset}
    before={before}
    duration={total}
    audioRef={props.audioRef}
    onPlaying={props.onPlaying}
    onTime={props.onTime}
    onSave={(position, finished) => { if (current.version === fileVersion.current) save(position, finished); }}
    onEnded={() => {
      if (current.version !== fileVersion.current) return;
      const position = before + (files[current.index].duration > 0 ? files[current.index].duration : props.audioRef.current?.currentTime ?? 0);
      if (current.index < files.length - 1) {
        save(position);
        if (files[current.index].duration <= 0) { toast("Rescan this edition before continuing past an unknown duration", "error"); return; }
        setCurrent({ index: current.index + 1, offset: 0, version: ++fileVersion.current });
      } else {
        save(position, true);
        props.onEnded();
      }
    }}
  />;
}

function EditionAudioFile(props: {
  generation?: string; onStreamError: () => void;
  fileId: number; offset: number; before: number; duration: number;
  audioRef: EditionAudioProps["audioRef"];
  onPlaying: EditionAudioProps["onPlaying"];
  onTime: EditionAudioProps["onTime"];
  onSave: (position: number, finished?: boolean) => void;
  onEnded: () => void;
}) {
  const element = useRef<HTMLAudioElement | null>(null);
  const active = useRef(true);
  const loaded = useRef(false);
  const ended = useRef(false);
  const lastPost = useRef(0);
  const callbacks = useRef(props);
  callbacks.current = props;
  const save = () => {
    if (!loaded.current || !element.current || ended.current) return;
    lastPost.current = Date.now();
    callbacks.current.onSave(props.before + element.current.currentTime);
  };
  useEffect(() => {
    const audio = element.current!;
    props.audioRef.current = audio;
    props.onPlaying(false);
    props.onTime(props.before + props.offset, props.duration);
    audio.play().catch(() => { if (active.current) callbacks.current.onPlaying(false); });
    addEventListener("pagehide", save);
    return () => {
      active.current = false;
      removeEventListener("pagehide", save);
      save();
      audio.pause();
      if (props.audioRef.current === audio) props.audioRef.current = null;
    };
  }, []);
  return <audio ref={element} src={media(`/stream/${props.fileId}${props.generation ? `?generation=${encodeURIComponent(props.generation)}` : ""}`)} preload="metadata" style={{ display: "none" }}
    onLoadedMetadata={(event) => {
      if (!active.current) return;
      event.currentTarget.currentTime = props.offset;
      loaded.current = true;
      callbacks.current.onTime(props.before + props.offset, props.duration);
    }}
    onPlay={() => { if (active.current) callbacks.current.onPlaying(true); }}
    onPause={() => { if (active.current) { callbacks.current.onPlaying(false); save(); } }}
    onSeeked={() => { if (active.current) save(); }}
    onTimeUpdate={(event) => {
      if (!active.current || !loaded.current) return;
      callbacks.current.onTime(props.before + event.currentTarget.currentTime, props.duration);
      if (Date.now() - lastPost.current >= 15000) save();
    }}
    onEnded={() => {
      if (!active.current) return;
      ended.current = true;
      callbacks.current.onPlaying(false);
      callbacks.current.onEnded();
    }}
    onError={() => { if (active.current) { loaded.current = false; callbacks.current.onPlaying(false); callbacks.current.onStreamError(); } }}
  />;
}
