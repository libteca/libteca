import { useEffect, useState } from "preact/hooks";
import { api, getToken, type Library, type ScanEvent } from "../api";
import { useScan } from "../scan";
import { IconScan } from "../components/svg";
import { fmtRel } from "../util";
import {
  backLink, badge, c, ghostBtn, input, loginCard, mono, muted, primaryBtn, sectionTitle, td, th, table,
} from "../styles";

const TYPES = ["audiobooks", "movies", "tv", "music", "books", "comics"];

type JobRow = ScanEvent & { id: number; libraryId: number; startedAt: number; createdAt?: number; error?: string };

type AdminUser = { id: number; name: string; isAdmin: boolean; createdAtMs: number };

type TokenRow = {
  id: number; userId: number; label: string;
  createdAtMs: number; lastSeenAtMs: number | null; revokedAtMs: number | null;
};

export function AdminView() {
  const [libs, setLibs] = useState<Library[]>([]);
  const [name, setName] = useState("");
  const [type, setType] = useState("audiobooks");
  const [path, setPath] = useState("");
  const [msg, setMsg] = useState("");
  const [users, setUsers] = useState<AdminUser[]>([]);

  const refresh = () => api("/libraries").then(setLibs).catch(() => setLibs([]));
  const refreshUsers = () => api("/users").then((r: AdminUser[]) => setUsers(Array.isArray(r) ? r : [])).catch(() => setUsers([]));
  useEffect(() => { refresh(); refreshUsers(); }, []);

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

      <UsersSection users={users} onChanged={refreshUsers} />
      <TokensSection users={users} />

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

function UsersSection(props: { users: AdminUser[]; onChanged: () => void }) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [msg, setMsg] = useState("");

  const create = async (e: Event) => {
    e.preventDefault();
    const res: { error?: string } = await api("/users", { method: "POST", body: JSON.stringify({ name, password, isAdmin }) });
    if (res.error) { setMsg(res.error); return; }
    setMsg("user created"); setName(""); setPassword(""); setIsAdmin(false);
    props.onChanged();
  };

  return (
    <section style={{ marginTop: "2.4rem", marginBottom: "2.4rem" }}>
      <h3 style={{ ...sectionTitle, fontSize: "1rem" }}>Users</h3>
      {props.users.length === 0 ? (
        <p style={muted}>No users.</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Name</th>
                <th style={th}>Role</th>
                <th style={th}>Created</th>
                <th style={th}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {props.users.map((u) => <UserRow key={u.id} u={u} onChanged={props.onChanged} />)}
            </tbody>
          </table>
        </div>
      )}

      <form style={{ ...loginCard, marginTop: "1.6rem" }} onSubmit={create}>
        <h3 style={{ ...sectionTitle, fontSize: "1rem" }}>Add user</h3>
        <input style={input} placeholder="name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <input style={input} type="password" placeholder="password (min 8 chars)" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        <label style={{ display: "flex", gap: "0.45rem", alignItems: "center", fontSize: "0.88rem", color: c.textDim, cursor: "pointer" }}>
          <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin((e.target as HTMLInputElement).checked)} style={{ accentColor: c.accent }} />
          admin
        </label>
        <button style={primaryBtn} type="submit">Create user</button>
        {msg && <p style={muted}>{msg}</p>}
      </form>
    </section>
  );
}

function UserRow(props: { u: AdminUser; onChanged: () => void }) {
  const [confirmDel, setConfirmDel] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [pass, setPass] = useState("");
  const [msg, setMsg] = useState("");

  const reset = async () => {
    const res: { error?: string } = await api(`/users/${props.u.id}/password`, { method: "POST", body: JSON.stringify({ password: pass }) });
    if (res.error) { setMsg(res.error); return; }
    setMsg("password updated"); setResetOpen(false); setPass("");
  };

  const del = async () => {
    const res: { error?: string } = await api(`/users/${props.u.id}`, { method: "DELETE" });
    if (res.error) { setMsg(res.error); setConfirmDel(false); return; }
    props.onChanged();
  };

  return (
    <tr>
      <td style={{ ...td, color: c.text, fontWeight: 600 }}>{props.u.name}</td>
      <td style={td}>{props.u.isAdmin ? <span style={badge}>admin</span> : "—"}</td>
      <td style={td}>{fmtRel(props.u.createdAtMs) || "—"}</td>
      <td style={td}>
        {msg ? (
          <span style={{ ...muted, fontSize: "0.8rem" }}>{msg}</span>
        ) : resetOpen ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center", flexWrap: "wrap" }}>
            <input style={{ ...input, padding: "0.35rem 0.6rem", fontSize: "0.85rem", width: "11rem" }} type="password" placeholder="new password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
            <button style={ghostBtn} type="button" onClick={reset}>Save</button>
            <button style={ghostBtn} type="button" onClick={() => { setResetOpen(false); setPass(""); }}>Cancel</button>
          </span>
        ) : confirmDel ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} type="button" onClick={del}>Confirm delete</button>
            <button style={ghostBtn} type="button" onClick={() => setConfirmDel(false)}>Cancel</button>
          </span>
        ) : (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button style={ghostBtn} type="button" onClick={() => setResetOpen(true)}>Reset password</button>
            <button style={ghostBtn} type="button" onClick={() => setConfirmDel(true)}>Delete</button>
          </span>
        )}
      </td>
    </tr>
  );
}

function TokensSection(props: { users: AdminUser[] }) {
  const [rows, setRows] = useState<TokenRow[]>([]);
  const [label, setLabel] = useState("");
  const [issued, setIssued] = useState("");
  const [msg, setMsg] = useState("");

  const load = () => api("/tokens").then((r: TokenRow[]) => setRows(Array.isArray(r) ? r : [])).catch(() => setRows([]));
  useEffect(() => { load(); }, []);

  const create = async (e: Event) => {
    e.preventDefault();
    const res: { token?: string; error?: string } = await api("/tokens", { method: "POST", body: JSON.stringify({ label }) });
    if (res.error) { setMsg(res.error); return; }
    setIssued(res.token || ""); setMsg(""); setLabel("");
    load();
  };

  const revoke = async (id: number) => {
    const res: { error?: string } = await api(`/tokens/${id}`, { method: "DELETE" });
    if (res.error) { setMsg(res.error); return; }
    load();
  };

  const nameOf = (id: number) => props.users.find((u) => u.id === id)?.name || `#${id}`;

  return (
    <section style={{ marginBottom: "2.4rem" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "0.9rem" }}>
        <h3 style={{ ...sectionTitle, fontSize: "1rem", margin: 0 }}>Tokens</h3>
        <button style={ghostBtn} onClick={load}>Refresh</button>
      </div>
      {rows.length === 0 ? (
        <p style={muted}>No tokens yet.</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Label</th>
                <th style={th}>User</th>
                <th style={th}>Created</th>
                <th style={th}>Last seen</th>
                <th style={th}>Status</th>
                <th style={th}>Action</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id}>
                  <td style={{ ...td, color: c.text }}>{t.label}</td>
                  <td style={td}>{nameOf(t.userId)}</td>
                  <td style={td}>{fmtRel(t.createdAtMs) || "—"}</td>
                  <td style={td}>{fmtRel(t.lastSeenAtMs) || "—"}</td>
                  <td style={{ ...td, color: t.revokedAtMs ? c.danger : c.ok }}>{t.revokedAtMs ? "revoked" : "active"}</td>
                  <td style={td}>
                    {t.revokedAtMs ? "—" : (
                      <button style={ghostBtn} type="button" onClick={() => revoke(t.id)}>Revoke</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <form style={{ ...loginCard, marginTop: "1.6rem" }} onSubmit={create}>
        <h3 style={{ ...sectionTitle, fontSize: "1rem" }}>Issue token</h3>
        <input style={input} placeholder="label (e.g. phone)" value={label} onInput={(e) => setLabel((e.target as HTMLInputElement).value)} />
        <button style={primaryBtn} type="submit">Issue</button>
        {msg && <p style={muted}>{msg}</p>}
      </form>

      {issued && (
        <div style={{ marginTop: "1rem", background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "9px", padding: "0.9rem" }}>
          <p style={muted}>Copy this token now — it will not be shown again.</p>
          <code style={{ fontFamily: mono, fontSize: "0.82rem", wordBreak: "break-all", color: c.textDim }}>{issued}</code>
          <div style={{ marginTop: "0.6rem" }}>
            <button style={ghostBtn} type="button" onClick={() => setIssued("")}>Dismiss</button>
          </div>
        </div>
      )}
    </section>
  );
}
