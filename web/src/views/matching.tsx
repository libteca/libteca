import { useEffect, useState } from "preact/hooks";
import type { CSSProperties } from "preact";
import { api } from "../api";
import { Cover } from "../components/cover";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconChevronLeft } from "../components/svg";
import {
  backLink, badge, c, eyebrow, font, ghostBtn, input, mono, muted, panel, primaryBtn,
  sectionTitle, workCover, workHead, workMeta, workTitle,
} from "../styles";
import { toast } from "../toast";

type InboxItem = {
  id: number; libraryId: number; libraryName: string; libraryType: string;
  title: string; author: string | null; hasCover: boolean;
};

type Candidate = {
  provider: string; id: string; title: string; author: string;
  year: number | null; description: string; coverURL: string;
};

const matchSplit: CSSProperties = { display: "flex", gap: "1.4rem", alignItems: "flex-start", marginTop: "1.4rem" };

const inboxRail: CSSProperties = {
  width: "16rem", flexShrink: 0, position: "sticky", top: "calc(3.4rem + 1.35rem)",
  maxHeight: "calc(100vh - 7rem)", overflowY: "auto",
  display: "flex", flexDirection: "column", gap: "0.3rem",
};

const railStack: CSSProperties = {
  width: "100%", display: "flex", flexDirection: "column", gap: "0.3rem",
  order: 2, marginTop: "1.6rem",
};

const railHead: CSSProperties = {
  display: "flex", alignItems: "center", justifyContent: "space-between",
  padding: "0 0.55rem 0.45rem",
};

const inboxItemBtn: CSSProperties = {
  display: "flex", gap: "0.7rem", alignItems: "center",
  padding: "0.5rem 0.55rem", borderRadius: "12px",
  border: "1px solid transparent", background: "none",
  textAlign: "left", cursor: "pointer", fontFamily: font,
  color: c.textDim, minWidth: 0, width: "100%",
};

const inboxItemOn: CSSProperties = {
  ...inboxItemBtn,
  background: c.bgHover,
  border: `1px solid ${c.lineSoft}`,
  color: c.text,
};

const railThumb: CSSProperties = { width: "1.8rem", flexShrink: 0 };

const itemTitle: CSSProperties = {
  fontSize: "0.86rem", fontWeight: 550, letterSpacing: "-0.01em", color: "inherit",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
};

const itemMeta: CSSProperties = {
  fontSize: "0.74rem", color: c.muted, margin: "0.15rem 0 0",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
};

const toolbar: CSSProperties = {
  display: "flex", gap: "0.6rem", alignItems: "center", flexWrap: "wrap",
  background: c.bg, border: `1px solid ${c.lineSoft}`, borderRadius: "12px",
  padding: "0.7rem 0.8rem", marginBottom: "1.2rem",
};

const candThumb: CSSProperties = {
  width: "4.2rem", aspectRatio: "2 / 3", borderRadius: "6px", objectFit: "cover",
  display: "block", background: c.bgHover, flexShrink: 0,
};

const candCard: CSSProperties = {
  display: "flex", gap: "1rem", alignItems: "flex-start",
  padding: "0.95rem 1rem", background: c.bg,
  border: `1px solid ${c.lineSoft}`, borderRadius: "14px",
};

const clamp3: CSSProperties = {
  display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden",
};

function useWide() {
  const [wide, setWide] = useState(() => window.matchMedia("(min-width: 900px)").matches);
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 900px)");
    const on = () => setWide(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return wide;
}

export function MatchingView() {
  const [queue, setQueue] = useState<InboxItem[]>([]);
  const [idx, setIdx] = useState(0);
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [qTitle, setQTitle] = useState("");
  const [qAuthor, setQAuthor] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [loadErr, setLoadErr] = useState(false);
  const [kind, setKind] = useState("");
  const wide = useWide();

  useEffect(() => { loadQueue(); }, []);

  const loadQueue = () => {
    api("/matching/inbox").then((r: InboxItem[]) => {
      setQueue(Array.isArray(r) ? r : []);
      setLoadErr(false);
      setIdx(0);
      setCandidates(null);
      setKind("");
      setQTitle("");
      setQAuthor("");
      setMsg("");
    }).catch(() => { setQueue([]); setLoadErr(true); }).finally(() => setLoaded(true));
  };

  const current = queue[idx];

  const pick = (i: number) => {
    if (busy) return;
    setIdx(i);
    setCandidates(null);
    setKind("");
    setQTitle("");
    setQAuthor("");
    setMsg("");
  };

  const runMatch = async () => {
    if (!current) return;
    setBusy(true);
    setMsg("");
    try {
      const res = await api(`/works/${current.id}/match`, {
        method: "POST",
        body: JSON.stringify({ title: qTitle || current.title, author: qAuthor || current.author || "" }),
      });
      if (res.error) { setMsg(res.error); return; }
      setKind(res.kind || "");
      setCandidates(res.candidates || []);
      if (!res.candidates?.length) setMsg("No candidates. Adjust the query and search again.");
    } catch {
      setMsg("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const apply = async (cand: Candidate) => {
    if (!current) return;
    setBusy(true);
    setMsg("");
    try {
      const res = await api(`/works/${current.id}/apply`, {
        method: "POST",
        body: JSON.stringify({ provider: cand.provider, id: cand.id }),
      });
      if (res.error) { setMsg(res.error); return; }
      toast("Metadata applied", "success");
      advance();
    } catch {
      setMsg("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const applyEpisodes = async (cand: Candidate) => {
    if (!current) return;
    setBusy(true);
    setMsg("");
    try {
      const res = await api(`/works/${current.id}/apply-episodes`, {
        method: "POST",
        body: JSON.stringify({ provider: cand.provider, id: cand.id }),
      });
      if (res.error) { setMsg(res.error); return; }
      toast("Episode metadata applied", "success");
      advance();
    } catch {
      setMsg("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const skip = async () => {
    if (!current) return;
    setBusy(true);
    try {
      const res = await api(`/works/${current.id}/skip`, { method: "POST" });
      if (res.error) { setMsg(res.error); return; }
      toast("Skipped");
      advance();
    } catch {
      setMsg("Couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  const advance = () => {
    setCandidates(null);
    setKind("");
    setQTitle("");
    setQAuthor("");
    setMsg("");
    if (idx + 1 < queue.length) {
      setIdx(idx + 1);
    } else {
      loadQueue();
    }
  };

  return (
    <div>
      <button className="press" style={backLink} onClick={() => history.back()}><IconChevronLeft size={16} /> Admin</button>
      <h2 style={sectionTitle}>Metadata inbox</h2>
      {!loaded ? (
        <QuietLoad />
      ) : loadErr ? (
        <EmptyState title="Couldn't load the inbox">
          <button style={primaryBtn} type="button" onClick={loadQueue}>Retry</button>
        </EmptyState>
      ) : queue.length === 0 ? (
        <EmptyState title="Nothing to match" hint={'Run "Improve metadata" on a library to look up missing descriptions.'} />
      ) : current ? (
        <div style={{ ...matchSplit, flexDirection: wide ? "row" : "column" }}>
          <aside style={wide ? inboxRail : railStack}>
            <div style={railHead}>
              <span style={{ ...eyebrow, margin: 0 }}>Inbox</span>
              <span style={badge}>{queue.length}</span>
            </div>
            {queue.map((w, i) => (
              <button key={w.id} type="button" style={i === idx ? inboxItemOn : inboxItemBtn} disabled={busy} onClick={() => pick(i)}>
                <div style={railThumb}>
                  <Cover has={w.hasCover} id={w.id} title={w.title} />
                </div>
                <div style={{ minWidth: 0 }}>
                  <div style={itemTitle}>{w.title}</div>
                  <div style={itemMeta}>{w.author || "Unknown author"}</div>
                </div>
              </button>
            ))}
          </aside>

          <section style={{ ...panel, marginTop: 0, flex: 1, minWidth: 0, order: wide ? 0 : 1 }}>
            <div style={workHead}>
              <div style={{ ...workCover, width: wide ? "9rem" : "7rem" }}>
                <Cover has={current.hasCover} id={current.id} title={current.title} />
              </div>
              <div style={workMeta}>
                <h3 style={workTitle}>{current.title}</h3>
                <p style={muted}>{current.author || "Unknown author"}</p>
                <div style={{ display: "flex", gap: "0.5rem", alignItems: "center", flexWrap: "wrap" }}>
                  <span style={badge}>{current.libraryName}</span>
                  <span style={{ fontFamily: mono, fontSize: "0.74rem", color: c.faint }}>{idx + 1} / {queue.length}</span>
                </div>
              </div>
            </div>

            <div style={toolbar}>
              <input style={{ ...input, width: "14rem" }} placeholder="Title override"
                value={qTitle} onInput={(e) => setQTitle((e.target as HTMLInputElement).value)} />
              <input style={{ ...input, width: "11rem" }} placeholder="Author override"
                value={qAuthor} onInput={(e) => setQAuthor((e.target as HTMLInputElement).value)} />
              <button className="press" style={ghostBtn} disabled={busy} onClick={runMatch}>
                {busy ? "Searching" : candidates ? "Search again" : "Search"}
              </button>
              <button className="press" style={{ ...ghostBtn, marginLeft: "auto" }} disabled={busy} onClick={skip}>Skip</button>
            </div>

            {msg && <p style={{ ...muted, marginBottom: "1.1rem" }}>{msg}</p>}

            {candidates && candidates.length > 0 && (
              <div>
                <p style={{ ...eyebrow, margin: "0 0 0.7rem" }}>Candidates</p>
                <div style={{ display: "flex", flexDirection: "column", gap: "0.7rem" }}>
                  {candidates.map((cand, i) => (
                    <div key={`${cand.provider}-${cand.id}-${i}`} style={candCard}>
                      {cand.coverURL
                        ? <img style={candThumb} src={cand.coverURL} alt="" loading="lazy" />
                        : <div style={{ ...candThumb, background: c.line }} />}
                      <div style={{ minWidth: 0, flex: 1 }}>
                        <div style={{ display: "flex", gap: "0.5rem", alignItems: "baseline", flexWrap: "wrap" }}>
                          <span style={{ fontWeight: 600, fontSize: "0.95rem", letterSpacing: "-0.01em" }}>{cand.title}</span>
                          <span style={badge}>{cand.provider}</span>
                          {cand.year != null && <span style={{ ...muted, fontSize: "0.8rem" }}>{cand.year}</span>}
                        </div>
                        {(cand.author || cand.description) && (
                          <p style={{ ...muted, margin: "0.35rem 0 0", fontSize: "0.82rem", lineHeight: 1.5, ...clamp3 }}>
                            {cand.author && <span style={{ color: c.textDim }}>{cand.author}. </span>}
                            {cand.description}
                          </p>
                        )}
                      </div>
                      <span style={{ display: "flex", flexDirection: "column", gap: "0.45rem", flexShrink: 0, alignSelf: "center" }}>
                        <button className="press" style={primaryBtn} disabled={busy} onClick={() => apply(cand)}>Apply</button>
                        {kind === "tv" && (
                          <button className="press" style={ghostBtn} disabled={busy} onClick={() => applyEpisodes(cand)}>Apply episodes</button>
                        )}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {idx + 1 < queue.length && (
              <div style={{ marginTop: "1.8rem" }}>
                <p style={{ ...eyebrow, margin: "0 0 0.5rem" }}>Up next</p>
                {queue.slice(idx + 1, idx + 6).map((w) => (
                  <p key={w.id} style={{ ...muted, fontSize: "0.85rem", margin: 0, padding: "0.25rem 0" }}>
                    {w.title}{w.author ? ` — ${w.author}` : ""}
                  </p>
                ))}
              </div>
            )}
          </section>
        </div>
      ) : (
        <QuietLoad />
      )}
    </div>
  );
}
