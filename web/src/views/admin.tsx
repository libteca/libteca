import { useEffect, useState } from "preact/hooks";
import { api, getToken, type Library, type ScanEvent } from "../api";
import { useScan } from "../scan";
import { IconScan } from "../components/svg";
import { fmtRel } from "../util";
import {
  backLink, c, ghostBtn, input, loginCard, muted, primaryBtn, sectionTitle, td, th, table,
} from "../styles";

const TYPES = ["audiobooks", "movies", "tv", "music", "books", "comics"];

type JobRow = ScanEvent & { id: number; libraryId: number; startedAt: number; createdAt?: number; error?: string };

export function AdminView() {
  const [libs, setLibs] = useState<Library[]>([]);
  const [name, setName] = useState("");
  const [type, setType] = useState("audiobooks");
  const [path, setPath] = useState("");
  const [msg, setMsg] = useState("");

  const refresh = () => api("/libraries").then(setLibs).catch(() => setLibs([]));
  useEffect(() => { refresh(); }, []);

  const add = async (e: Event) => {
    e.preventDefault();
    const res = await fetch("/api/core/libraries", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${getToken()}` },
      body: JSON.stringify({ name, type, path }),
    });
    const data = await res.json();
    if (data.error) { setMsg(data.error); return; }
    await fetch(`/api/core/libraries/${data.id}/scan`, { method: "POST", headers: { Authorization: `Bearer ${getToken()}` } });
    setMsg("library added, scanning");
    setName(""); setPath("");
    refresh();
  };

  return (
    <div>
      <button style={backLink} onClick={() => history.back()}>{"< Library"}</button>
      <h2 style={sectionTitle}>Admin</h2>

      <h3 style={{ ...sectionTitle, fontSize: "1rem", marginTop: "2rem" }}>Libraries</h3>
      {libs.map((l) => <AdminLibRow key={l.id} lib={l} />)}
      {libs.length === 0 && <p style={muted}>No libraries configured.</p>}

      <form style={{ ...loginCard, marginTop: "1.6rem", marginBottom: "2.4rem" }} onSubmit={add}>
        <h3 style={{ ...sectionTitle, fontSize: "1rem" }}>Add library</h3>
        <input style={input} placeholder="name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <select style={input} value={type} onChange={(e) => setType((e.target as HTMLSelectElement).value)}>
          {TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        <input style={input} placeholder="/path/to/media" value={path} onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
        <button style={primaryBtn} type="submit">Add & scan</button>
        {msg && <p style={muted}>{msg}</p>}
      </form>

      <ScanJobs libs={libs} />
    </div>
  );
}

function AdminLibRow(props: { lib: Library }) {
  const scan = useScan(() => {});
  const line = scan.event && (scan.scanning
    ? `Scanning… ${scan.event.filesSeen} files${scan.event.currentPath ? "" : ""}`
    : scan.event.status === "error" ? `Failed${scan.event.error ? `: ${scan.event.error}` : ""}`
    : scan.event.filesAdded ? `Done · ${scan.event.filesAdded} added` : "");
  return (
    <div style={{ display: "flex", gap: "1rem", alignItems: "center", padding: "0.7rem 0", borderBottom: `1px solid ${c.lineSoft}`, flexWrap: "wrap" }}>
      <span style={{ fontWeight: 600, fontSize: "0.92rem", minWidth: "7rem" }}>{props.lib.name}</span>
      <span style={{ ...muted, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{props.lib.path}</span>
      {line && <span style={{ fontSize: "0.8rem", color: scan.event?.status === "error" ? c.danger : c.muted, flexShrink: 0 }}>{line}</span>}
      <button style={ghostBtn} disabled={scan.scanning} onClick={() => scan.start(props.lib.id)}>
        <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
          <IconScan size={13} />
          {scan.scanning ? "Scanning…" : "Scan"}
        </span>
      </button>
    </div>
  );
}

function ScanJobs(props: { libs: Library[] }) {
  const [jobs, setJobs] = useState<JobRow[]>([]);
  const [open, setOpen] = useState(false);

  const load = () => {
    Promise.all(props.libs.map((l) =>
      api(`/libraries/${l.id}/scan/jobs?limit=10`).then((rows: JobRow[]) => rows.map((r) => ({ ...r, libraryId: l.id }))).catch(() => [])
    )).then((all) => setJobs(all.flat().sort((a, b) => (b.startedAt || b.createdAt || 0) - (a.startedAt || a.createdAt || 0))));
  };

  useEffect(() => { if (open && props.libs.length) load(); }, [open, props.libs]);

  const nameOf = (id: number) => props.libs.find((l) => l.id === id)?.name || id;

  return (
    <section>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "0.9rem" }}>
        <h3 style={{ ...sectionTitle, fontSize: "1rem", margin: 0 }}>Scan Jobs</h3>
        <button style={ghostBtn} onClick={() => { setOpen(true); load(); }}>Refresh</button>
      </div>
      {jobs.length === 0 ? (
        <p style={muted}>{open ? "No scan jobs yet." : "Scan jobs appear here after the first scan."}</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Library</th>
                <th style={th}>Status</th>
                <th style={th}>Seen</th>
                <th style={th}>Added</th>
                <th style={th}>Updated</th>
                <th style={th}>Changed</th>
                <th style={th}>Started</th>
                <th style={th}>Finished</th>
                <th style={th}>Error</th>
              </tr>
            </thead>
            <tbody>
              {jobs.slice(0, 30).map((j) => (
                <tr key={j.id}>
                  <td style={td}>{nameOf(j.libraryId)}</td>
                  <td style={{ ...td, color: j.status === "error" ? c.danger : j.status === "done" ? c.ok : c.accent }}>{j.status}</td>
                  <td style={td}>{j.filesSeen}</td>
                  <td style={td}>{j.filesAdded}</td>
                  <td style={td}>{j.filesUpdated}</td>
                  <td style={td}>{j.worksChanged}</td>
                  <td style={td}>{fmtRel(j.startedAt)}</td>
                  <td style={td}>{j.finishedAt ? fmtRel(j.finishedAt) : "—"}</td>
                  <td style={{ ...td, color: c.danger, maxWidth: "16rem", overflow: "hidden", textOverflow: "ellipsis" }}>{j.error || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
