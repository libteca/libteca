import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, media } from "../api";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconCheck, IconChevronLeft, IconPause, IconPlay, IconPodcast, IconScan } from "../components/svg";
import { toast } from "../toast";
import { fmt, fmtClock, fmtRel } from "../util";
import {
  backLink, badge, c, errStyle, ghostBtn, gridSquare, iconBtn, input, muted, playerBar, primaryBtn,
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
    <div style={{ ...coverBox, width: props.size || "100%" }}>
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
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = () => api("/podcasts")
    .then((r: Podcast[]) => { setPods(Array.isArray(r) ? r : []); setLoadErr(false); })
    .catch(() => setLoadErr(true));
  useEffect(() => { refresh(); }, []);

  const subscribe = async (e: Event) => {
    e.preventDefault();
    setErr(""); setMsg("");
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

  const importOPML = async (file: File) => {
    setErr(""); setMsg("Importing…");
    try {
      const opml = await file.text();
      const res: { subscribed: number; exists: number; failed: number; error?: string } =
        await api("/podcasts/import-opml", { method: "POST", body: JSON.stringify({ opml }) });
      if (res.error) { setErr(res.error); setMsg(""); toast(res.error, "error"); return; }
      setMsg(`Imported ${res.subscribed} new, ${res.exists} already subscribed, ${res.failed} failed`);
      toast(`OPML imported — ${res.subscribed} subscribed, ${res.exists} already there`, res.failed > 0 ? "default" : "success");
      refresh();
    } catch {
      setErr("Import failed — couldn't reach the server.");
      setMsg("");
      toast("Import failed — couldn't reach the server.", "error");
    }
  };

  if (selected != null) {
    return <PodcastShow id={selected} onBack={() => setSelected(null)} onChanged={refresh} />;
  }

  return (
    <div>
      <h2 style={sectionTitle}>Podcasts</h2>

      {loadErr ? (
        <EmptyState title="Couldn't load podcasts">
          <button style={primaryBtn} type="button" onClick={refresh}>Retry</button>
        </EmptyState>
      ) : pods.length === 0 ? (
        <EmptyState title="No subscriptions yet" icon={<IconPodcast size={22} />} hint="Add a feed URL or import an OPML file." />
      ) : (
        <div style={gridSquare}>
          {pods.map((p) => (
            <button key={p.id} type="button" onClick={() => setSelected(p.id)} className="cover-card" style={{ background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", display: "block", color: "inherit", fontFamily: "inherit" }}>
              <PodcastCover pod={p} />
              <p style={{ margin: "0.5rem 0 0", fontWeight: 600, fontSize: "0.88rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.title}</p>
              <p style={{ margin: 0, color: c.muted, fontSize: "0.78rem" }}>
                {p.episodeCount} episodes · {p.downloadedCount} downloaded
              </p>
              {p.autoDownload && <p style={{ margin: 0 }}><span style={badge}>auto</span></p>}
            </button>
          ))}
        </div>
      )}

      <form style={{ display: "flex", gap: "0.6rem", flexWrap: "wrap", marginTop: "2rem", alignItems: "center" }} onSubmit={subscribe}>
        <input style={{ ...input, flex: 1, minWidth: "14rem" }} type="url" required placeholder="https://example.com/feed.xml" value={feedUrl} onInput={(e) => setFeedUrl((e.target as HTMLInputElement).value)} />
        <button style={primaryBtn} type="submit" disabled={busy}>{busy ? "Subscribing…" : "Subscribe"}</button>
      </form>
      {err && <p style={errStyle}>{err}</p>}

      <div style={{ display: "flex", gap: "0.6rem", alignItems: "center", marginTop: "1.2rem", flexWrap: "wrap" }}>
        <label style={{ ...ghostBtn, cursor: "pointer" }}>
          Import OPML
          <input
            type="file" accept=".opml,application/xml,text/xml,text/x-opml"
            style={{ display: "none" }}
            onChange={(e) => {
              const f = (e.target as HTMLInputElement).files?.[0];
              if (f) importOPML(f);
              (e.target as HTMLInputElement).value = "";
            }}
          />
        </label>
        <a style={{ ...ghostBtn, textDecoration: "none" }} href={media("/podcasts/export-opml")} download="libteca-podcasts.opml">Export OPML</a>
        {msg && <span style={muted}>{msg}</span>}
      </div>
    </div>
  );
}

function PodcastShow(props: { id: number; onBack: () => void; onChanged: () => void }) {
  const [pod, setPod] = useState<PodcastDetailBody | null>(null);
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
  const seekRef = useRef(0);
  const lastPostRef = useRef(0);
  const playingRef = useRef<number | null>(null);
  playingRef.current = playing;

  const load = () => api(`/podcasts/${props.id}`)
    .then((r: PodcastDetailBody) => { setPod(r); setMaxEp(String(r.maxEpisodes)); })
    .catch(() => setPod(null));
  useEffect(() => { setPlaying(null); load(); }, [props.id]);

  const saveProgress = (epId: number, pos: number, finished = false) => {
    if (playingRef.current !== epId) return;
    const a = audioRef.current;
    const ep = (pod?.episodes || []).find((x) => x.id === epId);
    const dur = a && isFinite(a.duration) && a.duration > 0 ? a.duration : (ep?.durationSecs || 0);
    if (pos <= 1 && !finished) return;
    lastPostRef.current = Date.now();
    api(`/podcasts/episodes/${epId}/progress`, { method: "POST", body: JSON.stringify({ position: pos, duration: dur, finished }) }).catch(() => {});
  };

  useEffect(() => {
    const onUnload = () => {
      const a = audioRef.current;
      const epId = playingRef.current;
      if (!a || epId == null || a.currentTime <= 1 || a.ended) return;
      fetch(`/api/core/podcasts/episodes/${epId}/progress`, {
        method: "POST", keepalive: true,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${getToken()}` },
        body: JSON.stringify({ position: a.currentTime, duration: isFinite(a.duration) ? a.duration : 0, finished: false }),
      }).catch(() => {});
    };
    addEventListener("pagehide", onUnload);
    return () => {
      removeEventListener("pagehide", onUnload);
      onUnload();
    };
  }, []);

  const refreshFeed = async () => {
    setErr(""); setMsg(""); setBusy(true);
    const res: PodcastDetailBody | { error: string } = await api(`/podcasts/${props.id}/refresh`, { method: "POST" });
    setBusy(false);
    if ("error" in res) { setErr(res.error); return; }
    setPod(res);
    setMsg(res.changed ? "New episodes found" : "Up to date");
  };

  const patch = async (body: object) => {
    setErr(""); setMsg("");
    const res: PodcastDetailBody | { error: string } = await api(`/podcasts/${props.id}`, { method: "PATCH", body: JSON.stringify(body) });
    if ("error" in res) { setErr(res.error); return; }
    setPod(res);
    setMaxEp(String(res.maxEpisodes));
    props.onChanged();
  };

  const del = async () => {
    setErr("");
    const res: { error?: string } = await api(`/podcasts/${props.id}`, { method: "DELETE" });
    if (res.error) { setErr(res.error); setConfirmDel(false); return; }
    props.onBack();
    props.onChanged();
  };

  const play = (ep: Episode) => {
    const a = audioRef.current;
    if (!a || !ep.streamUrl) return;
    if (playing === ep.id) {
      if (a.paused) a.play().catch(() => {});
      else a.pause();
      return;
    }
    if (playing != null) saveProgress(playing, a.currentTime);
    setPlaying(ep.id);
    a.src = media(ep.streamUrl);
    seekRef.current = ep.positionSecs && ep.positionSecs > 0 && !ep.isFinished ? ep.positionSecs : 0;
    a.play().catch(() => setPaused(true));
  };

  const eps = pod?.episodes || [];
  const resumeEp = eps.find((e) => e.hasFile && e.positionSecs && e.positionSecs > 0 && !e.isFinished);

  return (
    <div style={{ paddingBottom: playing != null ? "5.2rem" : 0 }}>
      <button className="press" style={backLink} onClick={props.onBack}><IconChevronLeft size={16} /> Podcasts</button>
      {pod ? (
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
                  <button style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} type="button" onClick={del}>Confirm delete</button>
                  <button style={ghostBtn} type="button" onClick={() => setConfirmDel(false)}>Cancel</button>
                </span>
              ) : (
                <button style={ghostBtn} type="button" onClick={() => setConfirmDel(true)}>Delete</button>
              )}
            </div>
            {msg && <p style={muted}>{msg}</p>}
            {err && <p style={errStyle}>{err}</p>}
            {resumeEp && (
              <button style={primaryBtn} type="button" onClick={() => play(resumeEp)}>
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

      <audio
        ref={audioRef} preload="none"
        style={{ display: "none" }}
        onLoadedMetadata={() => {
          const a = audioRef.current;
          if (!a) return;
          if (seekRef.current > 0) { a.currentTime = seekRef.current; seekRef.current = 0; }
          if (isFinite(a.duration) && a.duration > 0) setDuration(a.duration);
        }}
        onPlay={() => setPaused(false)}
        onPause={() => {
          setPaused(true);
          const a = audioRef.current;
          if (a && playingRef.current != null && !a.ended) saveProgress(playingRef.current, a.currentTime);
        }}
        onTimeUpdate={() => {
          const a = audioRef.current;
          if (!a || playingRef.current == null) return;
          setElapsed(a.currentTime);
          if (isFinite(a.duration) && a.duration > 0) setDuration(a.duration);
          if (Date.now() - lastPostRef.current >= 15000) saveProgress(playingRef.current, a.currentTime);
        }}
        onEnded={() => {
          setPaused(true);
          if (playingRef.current == null) return;
          const epId = playingRef.current;
          saveProgress(epId, audioRef.current?.currentTime || 0, true);
          setPod((p) => p ? { ...p, episodes: p.episodes.map((e) => e.id === epId ? { ...e, isFinished: true, percent: 1 } : e) } : p);
        }}
      />

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
