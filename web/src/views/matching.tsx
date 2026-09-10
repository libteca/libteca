import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { Cover } from "../components/cover";
import { EmptyState, QuietLoad } from "../components/rail";
import { IconChevronLeft } from "../components/svg";
import { backLink, badge, c, ghostBtn, input, muted, primaryBtn, sectionTitle, workCover, workHead, workMeta, workTitle } from "../styles";

type InboxItem = {
  id: number; libraryId: number; libraryName: string; libraryType: string;
  title: string; author: string | null; hasCover: boolean;
};

type Candidate = {
  provider: string; id: string; title: string; author: string;
  year: number | null; description: string; coverURL: string;
};

const coverBox = { width: "3.2rem", aspectRatio: "2 / 3", borderRadius: "6px", objectFit: "cover", display: "block", background: c.bgRaised, flexShrink: 0 } as const;

export function MatchingView() {
  const [queue, setQueue] = useState<InboxItem[]>([]);
  const [idx, setIdx] = useState(0);
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [qTitle, setQTitle] = useState("");
  const [qAuthor, setQAuthor] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [loaded, setLoaded] = useState(false);

  useEffect(() => { loadQueue(); }, []);

  const loadQueue = () => {
    api("/matching/inbox").then((r: InboxItem[]) => {
      setQueue(Array.isArray(r) ? r : []);
      setIdx(0);
      setCandidates(null);
      setQTitle("");
      setQAuthor("");
      setMsg("");
    }).catch(() => setQueue([])).finally(() => setLoaded(true));
  };

  const current = queue[idx];

  const runMatch = async () => {
    if (!current) return;
    setBusy(true);
    setMsg("");
    const res = await api(`/works/${current.id}/match`, {
      method: "POST",
      body: JSON.stringify({ title: qTitle || current.title, author: qAuthor || current.author || "" }),
    });
    setBusy(false);
    if (res.error) { setMsg(res.error); return; }
    setCandidates(res.candidates || []);
    if (!res.candidates?.length) setMsg("No candidates. Adjust the query and search again.");
  };

  const apply = async (cand: Candidate) => {
    if (!current) return;
    setBusy(true);
    setMsg("");
    const res = await api(`/works/${current.id}/apply`, {
      method: "POST",
      body: JSON.stringify({ provider: cand.provider, id: cand.id }),
    });
    setBusy(false);
    if (res.error) { setMsg(res.error); return; }
    advance();
  };

  const skip = async () => {
    if (!current) return;
    setBusy(true);
    const res = await api(`/works/${current.id}/skip`, { method: "POST" });
    setBusy(false);
    if (res.error) { setMsg(res.error); return; }
    advance();
  };

  const advance = () => {
    setCandidates(null);
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
      ) : queue.length === 0 ? (
        <EmptyState title="Nothing to match" hint={'Run "Improve metadata" on a library to look up missing descriptions.'} />
      ) : current ? (
        <div>
          <div style={workHead}>
            <div style={{ ...workCover, width: "10rem" }}>
              <Cover has={current.hasCover} id={current.id} title={current.title} />
            </div>
            <div style={workMeta}>
              <h3 style={workTitle}>{current.title}</h3>
              <p style={muted}>{current.author || "Unknown author"}</p>
              <span style={badge}>{current.libraryName}</span>
              <p style={{ ...muted, fontSize: "0.8rem" }}>{idx + 1} of {queue.length}</p>
              <button className="press" style={ghostBtn} disabled={busy} onClick={skip}>Skip</button>
            </div>
          </div>

          <div style={{ display: "flex", gap: "0.6rem", marginBottom: "1.2rem", flexWrap: "wrap" }}>
            <input style={{ ...input, width: "16rem" }} placeholder="Title override"
              value={qTitle} onInput={(e) => setQTitle((e.target as HTMLInputElement).value)} />
            <input style={{ ...input, width: "12rem" }} placeholder="Author override"
              value={qAuthor} onInput={(e) => setQAuthor((e.target as HTMLInputElement).value)} />
            <button className="press" style={ghostBtn} disabled={busy} onClick={runMatch}>
              {busy ? "Searching" : candidates ? "Search again" : "Search"}
            </button>
          </div>

          {msg && <p style={{ ...muted, marginBottom: "1rem" }}>{msg}</p>}

          {candidates && candidates.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: "0.7rem", maxWidth: "46rem" }}>
              {candidates.map((cand, i) => (
                <div key={`${cand.provider}-${cand.id}-${i}`}
                  style={{ display: "flex", gap: "0.9rem", padding: "0.95rem", background: c.bgRaised, border: `1px solid ${c.lineSoft}`, borderRadius: "14px" }}>
                  {cand.coverURL
                    ? <img style={coverBox} src={cand.coverURL} alt="" loading="lazy" />
                    : <div style={{ ...coverBox, width: "3.2rem" }} />}
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ display: "flex", gap: "0.5rem", alignItems: "baseline", flexWrap: "wrap" }}>
                      <span style={{ fontWeight: 600, fontSize: "0.95rem", letterSpacing: "-0.01em" }}>{cand.title}</span>
                      <span style={badge}>{cand.provider}</span>
                      {cand.year != null && <span style={{ ...muted, fontSize: "0.8rem" }}>{cand.year}</span>}
                    </div>
                    {(cand.author || cand.description) && (
                      <p style={{ ...muted, margin: "0.35rem 0 0", fontSize: "0.82rem", maxHeight: "3.2em", overflow: "hidden" }}>
                        {cand.author && <span style={{ color: c.textDim }}>{cand.author}. </span>}
                        {cand.description}
                      </p>
                    )}
                  </div>
                  <button className="press" style={{ ...primaryBtn, flexShrink: 0, alignSelf: "center" }} disabled={busy} onClick={() => apply(cand)}>Apply</button>
                </div>
              ))}
            </div>
          )}

          {idx + 1 < queue.length && (
            <div style={{ marginTop: "2rem" }}>
              <p style={{ ...muted, fontSize: "0.8rem", marginBottom: "0.4rem" }}>Up next</p>
              {queue.slice(idx + 1, idx + 6).map((w) => (
                <p key={w.id} style={{ ...muted, fontSize: "0.85rem", margin: 0, padding: "0.25rem 0" }}>
                  {w.title}{w.author ? ` — ${w.author}` : ""}
                </p>
              ))}
            </div>
          )}
        </div>
      ) : (
        <QuietLoad />
      )}
    </div>
  );
}
