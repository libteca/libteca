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
  const total = files.reduce((sum, file) => sum + file.duration, 0);
  const at = (position: number) => {
    const target = Math.max(0, Math.min(total, position));
    let before = 0;
    for (let i = 0; i < files.length; i++) {
      if (before + files[i].duration > target || i === files.length - 1) return { index: i, offset: target - before };
      before += files[i].duration;
    }
    return { index: 0, offset: 0 };
  };
  const fileVersion = useRef(0);
  const [current, setCurrent] = useState(() => ({ ...at(props.position ?? (props.edition.isFinished ? 0 : props.edition.position || 0)), version: 0 }));
  const saver = useRef<AudioProgressSaver | null>(null);
  if (!saver.current) {
    saver.current = new AudioProgressSaver(`/progress/${props.edition.id}`, total, props.sessionToken);
    saver.current.begin();
  }
  const before = files.slice(0, current.index).reduce((sum, file) => sum + file.duration, 0);
  const save = (position: number, finished = false) => {
    props.onProgress(position, finished);
    void saver.current!.save(position, finished);
  };
  const seek = (position: number) => {
    const next = at(position);
    if (next.index === current.index && props.audioRef.current?.readyState) props.audioRef.current.currentTime = next.offset;
    else setCurrent({ ...next, version: ++fileVersion.current });
    props.onTime(Math.max(0, Math.min(total, position)), total);
    save(Math.max(0, Math.min(total, position)));
  };
  useEffect(() => {
    props.seekRef.current = seek;
    return () => { if (props.seekRef.current === seek) props.seekRef.current = null; };
  });
  if (!files[current.index]) return null;
  return <EditionAudioFile
    key={`${current.index}:${files[current.index].id}:${current.version}`}
    fileId={files[current.index].id}
    offset={current.offset}
    before={before}
    duration={total}
    audioRef={props.audioRef}
    onPlaying={props.onPlaying}
    onTime={props.onTime}
    onSave={(position, finished) => { if (current.version === fileVersion.current) save(position, finished); }}
    onEnded={() => {
      if (current.version !== fileVersion.current) return;
      const position = before + files[current.index].duration;
      if (current.index < files.length - 1) {
        save(position);
        setCurrent({ index: current.index + 1, offset: 0, version: ++fileVersion.current });
      } else {
        save(total, true);
        props.onEnded();
      }
    }}
  />;
}

function EditionAudioFile(props: {
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
  return <audio ref={element} src={media(`/stream/${props.fileId}`)} preload="metadata" style={{ display: "none" }}
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
    onError={() => { if (active.current) callbacks.current.onPlaying(false); }}
  />;
}
