import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { Cover } from "../components/cover";
import { backLink, badge, c, ghostBtn, input, muted, primaryBtn, sectionTitle } from "../styles";

type InboxItem = {
  id: number; libraryId: number; libraryName: string; libraryType: string;
  title: string; author: string | null; hasCover: boolean;
};

type Candidate = {
  provider: string; id: string; title: string; author: string;
  year: number | null; description: string; coverURL: string;
};

const coverBox = { width: "3rem", aspectRatio: "2 / 3", borderRadius: "5px", objectFit: "cover", display: "block", background: c.bgRaised, flexShrink: 0 } as const;

export function MatchingView() {
  const [queue, setQueue] = useState<InboxItem[]>([]);
  const [idx, setIdx] = useState(0);
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [qTitle, setQTitle] = useState("");
  const [qAuthor, setQAuthor] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  useEffect(() => { loadQueue(); }, []);

  const loadQueue = () => {
    api("/matching/inbox").then((r: InboxItem[]) => {
      setQueue(Array.isArray(r) ? r : []);
      setIdx(0);
      setCandidates(null);
      setQTitle("");
      setQAuthor("");
      setMsg("");
    }).catch(() => setQueue([]));
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
      <button style={backLink} onClick={() => history.back()}>{"< Admin"}</button>
      <h2 style={sectionTitle}>Metadata inbox</h2>
      {queue.length === 0 ? (
        <p style={muted}>Nothing to match. Run "Improve metadata" on a library to look up missing descriptions.</p>
      ) : current ? (
        <div>
          <div style={{ display: "flex", gap: "1.2rem", alignItems: "flex-start", marginBottom: "1.2rem", flexWrap: "wrap" }}>
            <div style={{ width: "6rem", flexShrink: 0 }}>
              <Cover has={current.hasCover} id={current.id} title={current.title} />
            </div>
            <div style={{ minWidth: "16rem", flex: 1 }}>
              <h3 style={{ margin: 0, fontSize: "1.15rem", letterSpacing: "-0.01em" }}>{current.title}</h3>
              <p style={{ ...muted, margin: "0.25rem 0 0.6rem" }}>{current.author || "unknown author"}</p>
              <span style={badge}>{current.libraryName}</span>{" "}
              <span style={badge}>{current.libraryType}</span>
              <p style={{ ...muted, fontSize: "0.8rem", marginTop: "0.6rem" }}>
                {idx + 1} of {queue.length} in queue
              </p>
            </div>
            <button style={ghostBtn} disabled={busy} onClick={skip}>Skip this work</button>
          </div>

          <div style={{ display: "flex", gap: "0.6rem", marginBottom: "1.2rem", flexWrap: "wrap" }}>
            <input style={{ ...input, width: "16rem" }} placeholder="query title override"
              value={qTitle} onInput={(e) => setQTitle((e.target as HTMLInputElement).value)} />
            <input style={{ ...input, width: "12rem" }} placeholder="author override"
              value={qAuthor} onInput={(e) => setQAuthor((e.target as HTMLInputElement).value)} />
            <button style={ghostBtn} disabled={busy} onClick={runMatch}>
              {busy ? "Searching…" : candidates ? "Search again" : "Search"}
            </button>
          </div>

          {msg && <p style={{ ...muted, marginBottom: "1rem" }}>{msg}</p>}

          {candidates && candidates.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: "0.8rem", maxWidth: "46rem" }}>
              {candidates.map((cand, i) => (
                <div key={`${cand.provider}-${cand.id}-${i}`}
                  style={{ display: "flex", gap: "0.9rem", padding: "0.9rem", background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "11px" }}>
                  {cand.coverURL
                    ? <img style={coverBox} src={cand.coverURL} alt="" loading="lazy" />
                    : <div style={{ ...coverBox, width: "3rem" }} />}
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ display: "flex", gap: "0.5rem", alignItems: "baseline", flexWrap: "wrap" }}>
                      <span style={{ fontWeight: 600, fontSize: "0.95rem" }}>{cand.title}</span>
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
                  <button style={{ ...primaryBtn, flexShrink: 0, alignSelf: "center" }} disabled={busy} onClick={() => apply(cand)}>Apply</button>
                </div>
              ))}
            </div>
          )}

          {idx + 1 < queue.length && (
            <div style={{ marginTop: "2rem" }}>
              <p style={{ ...muted, fontSize: "0.8rem", marginBottom: "0.4rem" }}>Up next</p>
              {queue.slice(idx + 1, idx + 6).map((w) => (
                <p key={w.id} style={{ ...muted, fontSize: "0.85rem", margin: 0 }}>
                  {w.title}{w.author ? ` — ${w.author}` : ""} <span style={{ ...badge, fontSize: "0.66rem" }}>{w.libraryName}</span>
                </p>
              ))}
            </div>
          )}
        </div>
      ) : (
        <p style={muted}>loading…</p>
      )}
    </div>
  );
}
