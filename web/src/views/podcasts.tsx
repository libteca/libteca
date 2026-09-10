import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, media } from "../api";
import { IconPause, IconPlay, IconScan } from "../components/svg";
import { fmt, fmtRel } from "../util";
import {
  backLink, badge, c, errStyle, ghostBtn, grid, input, muted, primaryBtn, sectionTitle,
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
};

type PodcastDetailBody = Podcast & { episodes: Episode[]; changed?: boolean };

function tokened(url: string) {
  return `${url}${url.includes("?") ? "&" : "?"}token=${encodeURIComponent(getToken())}`;
}

const coverBox: preact.JSX.CSSProperties = {
  width: "100%", aspectRatio: "1 / 1", borderRadius: "8px", overflow: "hidden",
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
  const [selected, setSelected] = useState<number | null>(null);
  const [feedUrl, setFeedUrl] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = () => api("/podcasts").then((r: Podcast[]) => setPods(Array.isArray(r) ? r : [])).catch(() => setPods([]));
  useEffect(() => { refresh(); }, []);

  const subscribe = async (e: Event) => {
    e.preventDefault();
    setErr(""); setMsg(""); setBusy(true);
    const res: PodcastDetailBody | { error: string } = await api("/podcasts", { method: "POST", body: JSON.stringify({ feedUrl }) });
    setBusy(false);
    if ("error" in res) { setErr(res.error); return; }
    setFeedUrl("");
    setSelected(res.id);
    refresh();
  };

  const importOPML = async (file: File) => {
    setErr(""); setMsg("Importing…");
    const opml = await file.text();
    const res: { subscribed: number; exists: number; failed: number; error?: string } =
      await api("/podcasts/import-opml", { method: "POST", body: JSON.stringify({ opml }) });
    if (res.error) { setErr(res.error); setMsg(""); return; }
    setMsg(`Imported ${res.subscribed} new, ${res.exists} already subscribed, ${res.failed} failed`);
    refresh();
  };

  if (selected != null) {
    return <PodcastShow id={selected} onBack={() => setSelected(null)} onChanged={refresh} />;
  }

  return (
    <div>
      <h2 style={sectionTitle}>Podcasts</h2>

      {pods.length === 0 ? (
        <p style={muted}>No subscriptions yet. Add a feed URL above or import an OPML file.</p>
      ) : (
        <div style={{ ...grid, gridTemplateColumns: "repeat(auto-fill, minmax(8.5rem, 1fr))" }}>
          {pods.map((p) => (
            <button key={p.id} type="button" onClick={() => setSelected(p.id)} style={{ background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", display: "block", color: "inherit", fontFamily: "inherit" }}>
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
        <input style={{ ...input, flex: 1, minWidth: "14rem" }} type="url" placeholder="https://example.com/feed.xml" value={feedUrl} onInput={(e) => setFeedUrl((e.target as HTMLInputElement).value)} />
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
  const [maxEp, setMaxEp] = useState("");
  const audioRef = useRef<HTMLAudioElement | null>(null);

  const load = () => api(`/podcasts/${props.id}`)
    .then((r: PodcastDetailBody) => { setPod(r); setMaxEp(String(r.maxEpisodes)); })
    .catch(() => setPod(null));
  useEffect(() => { setPlaying(null); load(); }, [props.id]);

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
    setPlaying(ep.id);
    a.src = media(ep.streamUrl);
    a.play().catch(() => setPaused(true));
  };

  const eps = pod?.episodes || [];

  return (
    <div>
      <button style={backLink} onClick={props.onBack}>{"< Podcasts"}</button>
      {pod ? (
        <div style={{ display: "flex", gap: "1.6rem", alignItems: "flex-start", flexWrap: "wrap" }}>
          <PodcastCover pod={pod} size="8.5rem" />
          <div style={{ display: "flex", flexDirection: "column", gap: "0.6rem", alignItems: "flex-start", minWidth: 0, flex: 1 }}>
            <h2 style={{ margin: 0, fontSize: "1.6rem", letterSpacing: "-0.02em" }}>{pod.title}</h2>
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
          </div>
        </div>
      ) : (
        <p style={muted}>Loading…</p>
      )}

      <audio
        ref={audioRef} preload="none"
        style={{ display: playing == null ? "none" : "block", width: "100%", marginTop: "1.4rem", marginBottom: "0.6rem" }}
        onPlay={() => setPaused(false)}
        onPause={() => setPaused(true)}
        onEnded={() => setPaused(true)}
      />

      <div style={{ marginTop: "1.2rem" }}>
        {eps.map((ep) => {
          const active = playing === ep.id;
          return (
            <div key={ep.id} style={{ display: "flex", gap: "0.9rem", alignItems: "center", padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}` }}>
              <button
                type="button"
                disabled={!ep.streamUrl}
                onClick={() => play(ep)}
                aria-label={active && !paused ? `Pause ${ep.title || "episode"}` : `Play ${ep.title || "episode"}`}
                style={{
                  background: "none", border: "none", cursor: ep.streamUrl ? "pointer" : "default",
                  color: ep.streamUrl ? c.text : c.faint, padding: "0.4rem", display: "inline-flex",
                  alignItems: "center", justifyContent: "center",
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
                </span>
              </span>
              {ep.hasFile ? <span style={badge}>downloaded</span> : <span style={{ ...muted, fontSize: "0.75rem" }}>not downloaded</span>}
            </div>
          );
        })}
        {pod && eps.length === 0 && <p style={muted}>No episodes yet. Refresh the feed to fetch the catalog.</p>}
      </div>
    </div>
  );
}
