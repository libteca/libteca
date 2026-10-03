import { useEffect, useRef, useState } from "preact/hooks";
import { OPMLImport } from "../opml";
import { SessionAudio } from "../players/sessionAudio";
import { api, getToken, media } from "../api";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconCheck, IconChevronLeft, IconPause, IconPlay, IconPodcast, IconScan } from "../components/svg";
import { toast } from "../toast";
import { fmt, fmtClock, fmtRel } from "../util";
import {
  backLink, badge, c, cardMeta, cardTitleWrap, errStyle, ghostBtn, gridSquare, iconBtn, input, muted, playerBar, primaryBtn,
  progressMini, sectionTitle, workTitle,
} from "../styles";

type Podcast = {
  id: number; feedUrl: string; title: string; author: string | null; description: string | null;
  hasCover: boolean; coverUrl: string; autoDownload: boolean; maxEpisodes: number;
  lastFetchAt: number | null; createdAt: number; episodeCount: number; downloadedCount: number;
};

type Episode = {
  id: number; podcastId: number; title: string | null; description: string | null;
  pubDate: number | null; durationSecs: number | null; enclosureBytes: number | null;
  downloadedAt: number | null; hasFile: boolean; streamUrl?: string;
  positionSecs?: number; percent?: number; isFinished?: boolean;
};

type PodcastDetailBody = Podcast & { episodes: Episode[]; changed?: boolean };

function tokened(url: string) {
  return `${url}${url.includes("?") ? "&" : "?"}token=${encodeURIComponent(getToken())}`;
}

const coverBox: preact.JSX.CSSProperties = {
  width: "100%", aspectRatio: "1 / 1", borderRadius: "10px", overflow: "hidden",
  boxShadow: c.coverShadow, background: c.bgRaised, display: "flex",
  alignItems: "center", justifyContent: "center", flexShrink: 0,
};

const letter: preact.JSX.CSSProperties = {
  color: c.textDim, fontSize: "2rem", fontWeight: 700,
  fontFamily: "'Iowan Old Style', Georgia, serif",
};

function PodcastCover(props: { pod: Podcast; size?: string }) {
  return (
    <div className="cover-box" style={{ ...coverBox, width: props.size || "100%" }}>
      {props.pod.hasCover && props.pod.coverUrl
        ? <img src={tokened(props.pod.coverUrl)} alt="" loading="lazy" style={{ width: "100%", height: "100%", objectFit: "cover" }} />
        : <span style={letter}>{props.pod.title.charAt(0).toUpperCase()}</span>}
    </div>
  );
}

export function PodcastsView() {
  const [pods, setPods] = useState<Podcast[]>([]);
  const [loadErr, setLoadErr] = useState(false);
  const [selected, setSelected] = useState<number | null>(null);
  const [feedUrl, setFeedUrl] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = () => api("/podcasts")
    .then((r: Podcast[]) => {
      if (!Array.isArray(r)) throw new Error("Invalid podcast list");
      setPods(r);
      setLoadErr(false);
    })
    .catch(() => setLoadErr(true));
  useEffect(() => { refresh(); }, []);

  const subscribe = async (e: Event) => {
    e.preventDefault();
    setErr("");
    let parsed: URL;
    try {
      parsed = new URL(feedUrl.trim());
    } catch {
      setErr("Enter a valid feed URL.");
      return;
    }
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      setErr("The feed URL must start with http:// or https://.");
      return;
    }
    setBusy(true);
    try {
      const res: PodcastDetailBody | { error: string; podcastId?: number } = await api("/podcasts", { method: "POST", body: JSON.stringify({ feedUrl: feedUrl.trim() }) });
      if ("error" in res) {
        if (res.podcastId != null) { setFeedUrl(""); setSelected(res.podcastId); toast("Already subscribed"); refresh(); return; }
        setErr(res.error);
        toast(res.error, "error");
        return;
      }
      setFeedUrl("");
      setSelected(res.id);
      toast(`Subscribed to ${res.title}`, "success");
      refresh();
    } catch {
      setErr("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };


  if (selected != null) {
    return <PodcastShow key={selected} id={selected} onBack={() => { setSelected(null); refresh(); }} onChanged={refresh} />;
  }

  return (
    <div>
      <h2 style={sectionTitle}>Podcasts</h2>

      {loadErr ? (
        <EmptyState title="Couldn't load podcasts">
          <button className="press btnp" style={primaryBtn} type="button" onClick={refresh}>Retry</button>
        </EmptyState>
      ) : pods.length === 0 ? (
        <EmptyState title="No subscriptions yet" icon={<IconPodcast size={22} />} hint="Add a feed URL or import an OPML file." />
      ) : (
        <div style={gridSquare} className="cover-grid">
          {pods.map((p) => (
            <button key={p.id} type="button" onClick={() => setSelected(p.id)} className="cover-card" style={{ background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", display: "block", color: "inherit", fontFamily: "inherit" }}>
              <PodcastCover pod={p} />
              <span style={cardTitleWrap}>{p.title}</span>
              <span style={cardMeta}>
                {p.episodeCount} episodes · {p.downloadedCount} downloaded
              </span>
              {p.autoDownload && <span style={{ display: "block", margin: 0 }}><span style={badge}>auto</span></span>}
            </button>
          ))}
        </div>
      )}

      <form style={{ display: "flex", gap: "0.6rem", flexWrap: "wrap", marginTop: "2.4rem", alignItems: "center" }} onSubmit={subscribe}>
        <input style={{ ...input, flex: 1, minWidth: "14rem" }} type="url" required placeholder="https://example.com/feed.xml" value={feedUrl} onInput={(e) => setFeedUrl((e.target as HTMLInputElement).value)} />
        <button className="press btnp" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Subscribing…" : "Subscribe"}</button>
      </form>
      {err && <p style={errStyle}>{err}</p>}

      <OPMLImport onDone={refresh} />
    </div>
  );
}

function PodcastShow(props: { id: number; onBack: () => void; onChanged: () => void }) {
  const [pod, setPod] = useState<PodcastDetailBody | null>(null);
  const [loadErr, setLoadErr] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [confirmDel, setConfirmDel] = useState(false);
  const [busy, setBusy] = useState(false);
  const [playing, setPlaying] = useState<number | null>(null);
  const [paused, setPaused] = useState(true);
  const [elapsed, setElapsed] = useState(0);
  const [duration, setDuration] = useState(0);
  const [maxEp, setMaxEp] = useState("");
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const [playback, setPlayback] = useState(0);
  const positions = useRef(new Map<number, { position: number; duration: number; finished: boolean }>());

  const load = () => api(`/podcasts/${props.id}`)
    .then((r: PodcastDetailBody) => {
      if (!r || !Array.isArray(r.episodes) || typeof r.title !== "string") throw new Error("Invalid podcast detail");
      setPod(r);
      setMaxEp(String(r.maxEpisodes));
      setLoadErr(false);
    })
    .catch(() => { setPod(null); setLoadErr(true); });
  useEffect(() => { setPlaying(null); load(); }, [props.id]);

  const refreshFeed = async () => {
    setErr(""); setMsg(""); setBusy(true);
    try {
      const res: PodcastDetailBody | { error: string } = await api(`/podcasts/${props.id}/refresh`, { method: "POST" });
      if ("error" in res) { setErr(res.error); return; }
      setPod(res);
      setMsg(res.changed ? "New episodes found" : "Up to date");
    } catch {
      setErr("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const patch = async (body: object) => {
    setErr(""); setMsg(""); setBusy(true);
    try {
      const res: PodcastDetailBody | { error: string } = await api(`/podcasts/${props.id}`, { method: "PATCH", body: JSON.stringify(body) });
      if ("error" in res) { setErr(res.error); return; }
      setPod(res);
      setMaxEp(String(res.maxEpisodes));
      props.onChanged();
    } catch {
      setErr("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    if (busy) return;
    setErr(""); setBusy(true);
    try {
      const res: { error?: string } = await api(`/podcasts/${props.id}`, { method: "DELETE" });
      if (res.error) { setErr(res.error); setConfirmDel(false); return; }
    } catch {
      setErr("Couldn't reach the server.");
      setConfirmDel(false);
      return;
    } finally {
      setBusy(false);
    }
    props.onBack();
    props.onChanged();
  };

  const play = (ep: Episode) => {
    if (!ep.streamUrl) return;
    const element = audioRef.current;
    if (playing === ep.id && element && !element.ended) {
      if (element.paused) element.play().catch(() => {});
      else element.pause();
      return;
    }
    setPlaying(ep.id);
    setPlayback((value) => value + 1);
  };

  const eps = (pod?.episodes || []).map((episode) => {
    const local = positions.current.get(episode.id);
    return local ? { ...episode, positionSecs: local.position, isFinished: local.finished, percent: local.duration > 0 ? local.position / local.duration : episode.percent } : episode;
  });
  const currentEpisode = eps.find((episode) => episode.id === playing);
  const resumeEp = eps.find((e) => e.hasFile && e.positionSecs && e.positionSecs > 0 && !e.isFinished);

  return (
    <div style={{ paddingBottom: playing != null ? "5.2rem" : 0 }}>
      <button className="press" style={backLink} onClick={props.onBack}><IconChevronLeft size={16} /> Podcasts</button>
      {loadErr && !pod ? (
        <EmptyState title="Couldn't load this podcast">
          <button className="press btnp" style={primaryBtn} type="button" onClick={load}>Retry</button>
        </EmptyState>
      ) : pod ? (
        <div style={{ display: "flex", gap: "2rem", alignItems: "flex-start", flexWrap: "wrap" }}>
          <PodcastCover pod={pod} size="12rem" />
          <div style={{ display: "flex", flexDirection: "column", gap: "0.65rem", alignItems: "flex-start", minWidth: 0, flex: 1 }}>
            <h2 style={workTitle}>{pod.title}</h2>
            {pod.author && <p style={muted}>{pod.author}</p>}
            <p style={muted}>
              {pod.episodeCount} episodes · {pod.downloadedCount} downloaded
              {pod.lastFetchAt ? ` · fetched ${fmtRel(pod.lastFetchAt)}` : ""}
            </p>
            <div style={{ display: "flex", gap: "0.9rem", alignItems: "center", flexWrap: "wrap" }}>
              <label style={{ display: "flex", gap: "0.45rem", alignItems: "center", fontSize: "0.88rem", color: c.textDim, cursor: "pointer" }}>
                <input type="checkbox" checked={pod.autoDownload} style={{ accentColor: c.accent }}
                  onChange={(e) => patch({ autoDownload: (e.target as HTMLInputElement).checked })} />
                auto-download
              </label>
              <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                <input style={{ ...input, padding: "0.35rem 0.6rem", fontSize: "0.85rem", width: "8rem" }} type="number" min={1} max={1000} value={maxEp}
                  onInput={(e) => setMaxEp((e.target as HTMLInputElement).value)} aria-label="Max episodes to keep" />
                <button style={ghostBtn} type="button" onClick={() => {
                  const n = Number(maxEp);
                  if (n >= 1 && n <= 1000) patch({ maxEpisodes: n });
                }}>Keep max</button>
              </span>
              <button style={ghostBtn} type="button" disabled={busy} onClick={refreshFeed}>
                <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                  <IconScan size={13} />
                  {busy ? "Refreshing…" : "Refresh"}
                </span>
              </button>
              {confirmDel ? (
                <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                  <button style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} type="button" disabled={busy} onClick={del}>Confirm delete</button>
                  <button style={ghostBtn} type="button" onClick={() => setConfirmDel(false)}>Cancel</button>
                </span>
              ) : (
                <button style={ghostBtn} type="button" onClick={() => setConfirmDel(true)}>Delete</button>
              )}
            </div>
            {msg && <p style={muted}>{msg}</p>}
            {err && <p style={errStyle}>{err}</p>}
            {resumeEp && (
              <button className="press btnp" style={primaryBtn} type="button" onClick={() => play(resumeEp)}>
                <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
                  <IconPlay size={14} /> Resume
                  {resumeEp.durationSecs ? ` · ${fmt(Math.max(0, resumeEp.durationSecs - (resumeEp.positionSecs || 0)))} left` : ""}
                </span>
              </button>
            )}
          </div>
        </div>
      ) : (
        <QuietLoad />
      )}

      {currentEpisode?.streamUrl && <SessionAudio
        key={`${currentEpisode.id}:${playback}`}
        src={media(currentEpisode.streamUrl)}
        progressPath={`/podcasts/episodes/${currentEpisode.id}/progress`}
        duration={currentEpisode.durationSecs || 0}
        position={currentEpisode.isFinished ? 0 : currentEpisode.positionSecs}
        audioRef={audioRef}
        onPlaying={(value) => setPaused(!value)}
        onTime={(position, total) => { setElapsed(position); setDuration(total); }}
        onProgress={(position, total, finished) => positions.current.set(currentEpisode.id, { position, duration: total, finished })}
        onEnded={() => {
          const epId = currentEpisode.id;
          setPod((value) => value ? { ...value, episodes: value.episodes.map((episode) => episode.id === epId ? { ...episode, isFinished: true, percent: 1 } : episode) } : value);
        }}
      />}

      <div style={{ marginTop: "1.2rem" }}>
        {eps.map((ep) => {
          const active = playing === ep.id;
          const inProg = !ep.isFinished && (ep.positionSecs || 0) > 0;
          return (
            <div key={ep.id} style={{ display: "flex", gap: "0.9rem", alignItems: "center", padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}` }}>
              <button
                type="button"
                disabled={!ep.streamUrl}
                onClick={() => play(ep)}
                aria-label={active && !paused ? `Pause ${ep.title || "episode"}` : `Play ${ep.title || "episode"}`}
                style={{
                  background: "none", border: "none", cursor: ep.streamUrl ? "pointer" : "default",
                  color: ep.streamUrl ? c.text : c.faint, padding: 0, display: "inline-flex",
                  alignItems: "center", justifyContent: "center", width: "36px", height: "36px", flexShrink: 0,
                }}
              >
                {active && !paused ? <IconPause size={16} /> : <IconPlay size={16} />}
              </button>
              <span style={{ flex: 1, minWidth: 0 }}>
                <span style={{ display: "block", fontSize: "0.92rem", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: active ? c.text : c.textDim }}>
                  {ep.title || `Episode ${ep.id}`}
                </span>
                <span style={{ display: "block", fontSize: "0.78rem", color: c.muted }}>
                  {ep.pubDate ? fmtRel(ep.pubDate) : "unknown date"}
                  {ep.durationSecs ? ` · ${fmt(ep.durationSecs)}` : ""}
                  {inProg && ep.durationSecs ? ` · ${fmt(Math.max(0, ep.durationSecs - (ep.positionSecs || 0)))} left` : ""}
                </span>
              </span>
              {inProg && <span style={progressMini(ep.percent || 0)} />}
              {ep.isFinished && <span style={{ color: c.ok, display: "inline-flex", padding: "0.4rem" }}><IconCheck size={13} /></span>}
              {ep.hasFile ? <span style={badge}>downloaded</span> : <span style={{ ...muted, fontSize: "0.75rem" }}>not downloaded</span>}
            </div>
          );
        })}
        {pod && eps.length === 0 && <p style={muted}>No episodes yet. Refresh the feed to fetch the catalog.</p>}
      </div>
      {playing != null && pod && (
        <div className="player-bar" style={playerBar}>
          <div style={{ width: "3rem", flexShrink: 0 }}><PodcastCover pod={pod} /></div>
          <div style={{ display: "flex", flexDirection: "column", minWidth: 0, width: "12rem", flexShrink: 1 }}>
            <span style={{ fontSize: "0.88rem", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {eps.find((e) => e.id === playing)?.title || pod.title}
            </span>
            <span style={{ fontSize: "0.75rem", color: c.muted, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{pod.title}</span>
          </div>
          <button
            className="press"
            type="button"
            style={{ ...iconBtn, color: c.text }}
            aria-label={paused ? "Play" : "Pause"}
            onClick={() => { const ep = eps.find((e) => e.id === playing); if (ep) play(ep); }}
          >
            {paused ? <IconPlay size={22} /> : <IconPause size={22} />}
          </button>
          <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(elapsed)}</span>
          <input
            type="range" min={0} max={Math.max(1, Math.floor(duration))} step={1} value={Math.floor(elapsed)}
            className="seek"
            style={{
              flex: 1, minWidth: "4rem",
              background: `linear-gradient(90deg, ${c.accent} ${duration > 0 ? (elapsed / duration) * 100 : 0}%, ${c.line} ${duration > 0 ? (elapsed / duration) * 100 : 0}%)`,
              backgroundSize: "100% 5px", backgroundRepeat: "no-repeat", backgroundPosition: "center", borderRadius: "999px",
            }}
            onInput={(e) => {
              const a = audioRef.current;
              if (a) a.currentTime = Number((e.target as HTMLInputElement).value);
            }}
            aria-label="Seek"
          />
          <span style={{ fontSize: "0.75rem", color: c.muted, fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>{fmtClock(duration)}</span>
        </div>
      )}
    </div>
  );
}
