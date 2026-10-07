import { useEffect, useRef } from "preact/hooks";
import { apiWithDeadline, getToken } from "../api";
import { VideoProgressSaver as MediaProgressSaver } from "./videoProgress";

type SessionAudioProps = {
  src: string;
  progressPath: string;
  duration: number;
  position?: number;
  audioRef: { current: HTMLAudioElement | null };
  onPlaying: (playing: boolean) => void;
  onTime: (position: number, duration: number) => void;
  onEnded?: () => void;
  onProgress?: (position: number, duration: number, finished: boolean) => void;
};

export function SessionAudio(props: SessionAudioProps) {
  const audio = useRef<HTMLAudioElement | null>(null);
  const active = useRef(true);
  const completed = useRef(false);
  const lastPost = useRef(0);
  const duration = useRef(props.duration);
  const resume = useRef(props.position || 0);
  const metaOK = useRef(false);
  const token = useRef(getToken());
  const callbacks = useRef(props);
  callbacks.current = props;
  const saver = useRef<MediaProgressSaver | null>(null);
  if (!saver.current) {
    const path = props.progressPath;
    saver.current = new MediaProgressSaver(path, props.duration, async (patch) => {
      if (getToken() !== token.current) throw new Error("Playback session changed");
      const result = await apiWithDeadline(path, {
        method: "POST", keepalive: true,
        body: JSON.stringify({ ...patch, duration: duration.current }),
      });
      if (result?.error) throw new Error(result.error);
    });
  }

  const save = (element: HTMLAudioElement, finished = completed.current, explicit = false) => {
    if (Number.isFinite(element.duration) && element.duration > 0) duration.current = element.duration;
    lastPost.current = Date.now();
    const position = element.currentTime;
    if (!metaOK.current && position === 0 && !finished) return;
    callbacks.current.onProgress?.(position, duration.current, finished);
    void saver.current!.save(position, finished, explicit);
  };

  useEffect(() => {
    const element = audio.current;
    if (!element) return;
    props.audioRef.current = element;
    props.onPlaying(false);
    props.onTime(props.position || 0, props.duration);
    element.play().catch(() => { if (active.current) callbacks.current.onPlaying(false); });
    const onLeave = () => save(element);
    addEventListener("pagehide", onLeave);
    return () => {
      active.current = false;
      removeEventListener("pagehide", onLeave);
      save(element);
      element.pause();
      if (props.audioRef.current === element) props.audioRef.current = null;
    };
  }, []);

  return <audio
    ref={audio}
    src={props.src}
    preload="metadata"
    style={{ display: "none" }}
    onLoadedMetadata={(event) => {
      if (!active.current) return;
      const element = event.currentTarget;
      metaOK.current = true;
      if (resume.current > 0) element.currentTime = resume.current;
      resume.current = 0;
      if (Number.isFinite(element.duration) && element.duration > 0) duration.current = element.duration;
      callbacks.current.onTime(element.currentTime, duration.current);
    }}
    onPlay={() => { if (active.current) callbacks.current.onPlaying(true); }}
    onPause={(event) => {
      if (!active.current) return;
      callbacks.current.onPlaying(false);
      if (!event.currentTarget.ended) save(event.currentTarget);
    }}
    onTimeUpdate={(event) => {
      if (!active.current) return;
      const element = event.currentTarget;
      callbacks.current.onTime(element.currentTime, duration.current);
      if (Date.now() - lastPost.current >= 15000) save(element);
    }}
    onSeeked={(event) => { if (active.current) save(event.currentTarget, completed.current, true); }}
    onEnded={(event) => {
      if (!active.current) return;
      completed.current = true;
      save(event.currentTarget, true);
      callbacks.current.onPlaying(false);
      callbacks.current.onEnded?.();
    }}
    onError={() => { if (active.current) callbacks.current.onPlaying(false); }}
  />;
}
