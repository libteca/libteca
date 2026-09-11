import { useEffect, useState } from "preact/hooks";
import type { CSSProperties, ComponentChildren } from "preact";
import { api, type Library, type ScanEvent } from "../api";
import { useScan } from "../scan";
import { IconChevronDown, IconChevronLeft, IconScan } from "../components/svg";
import { fmtRel } from "../util";
import {
  backLink, badge, c, fieldLabel, formBlock, formNote, formNoteErr, ghostBtn, input, mono,
  muted, panel, panelHead, preBlock, primaryBtn, railTitle, sectionTitle, selectChevron,
  selectWrap, table, td, th,
} from "../styles";

const TYPES = ["audiobooks", "movies", "tv", "music", "books", "comics"];

const truncPath = (p: string) => (p.length > 42 ? `…${p.slice(-41)}` : p);

const tableWrap: CSSProperties = { overflowX: "auto" };
const thRight: CSSProperties = { ...th, textAlign: "right" };
const tdRight: CSSProperties = { ...td, textAlign: "right" };
const libRow: CSSProperties = {
  display: "flex", gap: "0.9rem", alignItems: "center", minHeight: "44px",
  padding: "0.4rem 0", borderBottom: `1px solid ${c.lineSoft}`, flexWrap: "wrap",
};
const libPath: CSSProperties = {
  fontFamily: mono, fontSize: "0.78rem", color: c.muted, flex: 1, minWidth: "6rem",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
};
const statusDot: CSSProperties = { width: "6px", height: "6px", borderRadius: "50%", display: "inline-block", flexShrink: 0 };

type JobRow = ScanEvent & { id: number; libraryId: number; startedAt: number; createdAt?: number; error?: string };

type AdminUser = { id: number; name: string; isAdmin: boolean; createdAtMs: number };

type TokenRow = {
  id: number; userId: number; label: string;
  createdAtMs: number; lastSeenAtMs: number | null; revokedAtMs: number | null;
};

function Field(props: { label: string; children: ComponentChildren }) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: "0.35rem", minWidth: 0 }}>
      <span style={fieldLabel}>{props.label}</span>
      {props.children}
    </label>
  );
}

function StatusTag(props: { status: string; tone: string }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "0.4rem", fontFamily: mono, fontSize: "0.76rem", color: props.tone }}>
      <span style={{ ...statusDot, background: props.tone }} />
      {props.status}
    </span>
  );
}

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
    setMsg("");
    try {
      const res = await api("/libraries", { method: "POST", body: JSON.stringify({ name, type, path }) });
      if (res.error) { setMsg(res.error); return; }
      let scanRes: { error?: string };
      try {
        scanRes = await api(`/libraries/${res.id}/scan`, { method: "POST" });
      } catch {
        scanRes = { error: "network error" };
      }
      setName(""); setPath("");
      refresh();
      if (scanRes.error) { setMsg(`Library added, but the scan failed to start: ${scanRes.error}`); return; }
      setMsg("library added, scanning");
    } catch {
      setMsg("Failed to add library.");
    }
  };

  const addOk = msg === "library added, scanning";

  return (
    <div>
      <button className="press" style={backLink} onClick={() => history.back()}><IconChevronLeft size={16} /> Library</button>
      <h2 style={sectionTitle}>Admin</h2>

      <section style={panel}>
        <h3 style={railTitle}>Libraries</h3>
        {libs.map((l) => <AdminLibRow key={l.id} lib={l} onRemoved={refresh} />)}
        {libs.length === 0 && <p style={muted}>No libraries configured.</p>}

        <form style={formBlock} onSubmit={add}>
          <Field label="Name">
            <input style={input} placeholder="Name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
          </Field>
          <Field label="Type">
            <div style={{ ...selectWrap, width: "100%" }}>
              <select className="pill" style={{ width: "100%" }} value={type} onChange={(e) => setType((e.target as HTMLSelectElement).value)} aria-label="Library type">
                {TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              </select>
              <span style={selectChevron}><IconChevronDown size={12} /></span>
            </div>
          </Field>
          <Field label="Path">
            <input style={input} placeholder="/path/to/media" value={path} onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
          </Field>
          <button style={primaryBtn} type="submit">Add & scan</button>
          {msg && <p style={addOk ? formNote : formNoteErr}>{msg}</p>}
        </form>
      </section>

      <UsersSection users={users} onChanged={refreshUsers} />
      <TokensSection users={users} />
      <ImportSection />

      <ScanJobs libs={libs} />
    </div>
  );
}

function AdminLibRow(props: { lib: Library; onRemoved: () => void }) {
  const scan = useScan(() => {});
  const [err, setErr] = useState("");
  const line = scan.event && (scan.scanning
    ? `Scanning… ${scan.event.filesSeen} files${scan.event.currentPath ? ` · ${truncPath(scan.event.currentPath)}` : ""}`
    : scan.event.status === "error" ? `Failed${scan.event.error ? `: ${scan.event.error}` : ""}`
    : scan.event.filesAdded ? `Done · ${scan.event.filesAdded} added` : "");
  const remove = async () => {
    if (!window.confirm(`Remove ${props.lib.name}?`)) return;
    setErr("");
    try {
      const res: { error?: string } = await api(`/libraries/${props.lib.id}`, { method: "DELETE" });
      if (res.error) { setErr(res.error); return; }
    } catch {
      setErr("Failed to remove library.");
      return;
    }
    props.onRemoved();
  };
  return (
    <div style={libRow}>
      <span style={{ fontWeight: 600, fontSize: "0.92rem", flexShrink: 0 }}>{props.lib.name}</span>
      <span style={badge}>{props.lib.type}</span>
      <span style={libPath} title={props.lib.path}>{props.lib.path}</span>
      {line && <span style={{ fontSize: "0.78rem", color: scan.event?.status === "error" ? c.danger : c.muted, flexShrink: 0 }}>{line}</span>}
      {err && <span style={{ fontSize: "0.78rem", color: c.danger, flexShrink: 0 }}>{err}</span>}
      <span style={{ display: "inline-flex", gap: "0.5rem", marginLeft: "auto", flexShrink: 0 }}>
        <button className="press" style={ghostBtn} disabled={scan.scanning} onClick={() => scan.start(props.lib.id)}>
          <IconScan size={13} />
          {scan.scanning ? "Scanning…" : "Scan"}
        </button>
        <button className="press" style={{ ...ghostBtn, color: c.danger }} disabled={scan.scanning} onClick={remove}>Remove</button>
      </span>
    </div>
  );
}

function ScanJobs(props: { libs: Library[] }) {
  const [jobs, setJobs] = useState<JobRow[]>([]);
  const [open, setOpen] = useState(true);

  const load = () => {
    Promise.all(props.libs.map((l) =>
      api(`/libraries/${l.id}/scan/jobs?limit=10`).then((rows: JobRow[]) => rows.map((r) => ({ ...r, libraryId: l.id }))).catch(() => [])
    )).then((all) => setJobs(all.flat().sort((a, b) => (b.startedAt || b.createdAt || 0) - (a.startedAt || a.createdAt || 0))));
  };

  useEffect(() => { if (open && props.libs.length) load(); }, [open, props.libs]);

  const nameOf = (id: number) => props.libs.find((l) => l.id === id)?.name || `#${id}`;

  return (
    <section style={panel}>
      <div style={panelHead}>
        <h3 style={{ ...railTitle, margin: 0 }}>Scan Jobs</h3>
        <button className="press" style={ghostBtn} onClick={() => { setOpen(true); load(); }}>Refresh</button>
      </div>
      {jobs.length === 0 ? (
        <p style={muted}>{open ? "No scan jobs yet." : "Scan jobs appear here after the first scan."}</p>
      ) : (
        <div style={tableWrap}>
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
              {jobs.slice(0, 30).map((j) => {
                const tone = j.status === "done" ? c.ok : j.status === "error" ? c.danger : j.status === "running" ? c.accent : c.muted;
                return (
                  <tr key={j.id}>
                    <td style={{ ...td, color: c.text }}>{nameOf(j.libraryId)}</td>
                    <td style={td}><StatusTag status={j.status} tone={tone} /></td>
                    <td style={td}>{j.filesSeen}</td>
                    <td style={td}>{j.filesAdded}</td>
                    <td style={td}>{j.filesUpdated}</td>
                    <td style={td}>{j.worksChanged}</td>
                    <td style={td}>{fmtRel(j.startedAt)}</td>
                    <td style={td}>{j.finishedAt ? fmtRel(j.finishedAt) : "—"}</td>
                    <td style={{ ...td, color: c.danger, maxWidth: "16rem", overflow: "hidden", textOverflow: "ellipsis" }}>{j.error || "—"}</td>
                  </tr>
                );
              })}
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
  const [busy, setBusy] = useState(false);

  const create = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api("/users", { method: "POST", body: JSON.stringify({ name, password, isAdmin }) });
      if (res.error) { setMsg(res.error); return; }
      setMsg("user created"); setName(""); setPassword(""); setIsAdmin(false);
      props.onChanged();
    } catch {
      setMsg("Failed to create user.");
    } finally {
      setBusy(false);
    }
  };

  const createOk = msg === "user created";

  return (
    <section style={panel}>
      <div style={panelHead}>
        <h3 style={{ ...railTitle, margin: 0 }}>Users</h3>
        {props.users.length > 0 && <span style={badge}>{props.users.length}</span>}
      </div>
      {props.users.length === 0 ? (
        <p style={muted}>No users.</p>
      ) : (
        <div style={tableWrap}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Name</th>
                <th style={th}>Role</th>
                <th style={th}>Created</th>
                <th style={thRight}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {props.users.map((u) => <UserRow key={u.id} u={u} onChanged={props.onChanged} />)}
            </tbody>
          </table>
        </div>
      )}

      <form style={formBlock} onSubmit={create}>
        <Field label="Name">
          <input style={input} placeholder="name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </Field>
        <Field label="Password">
          <input style={input} type="password" placeholder="min 8 chars" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        </Field>
        <label style={{ display: "flex", gap: "0.45rem", alignItems: "center", fontSize: "0.88rem", color: c.textDim, cursor: "pointer" }}>
          <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin((e.target as HTMLInputElement).checked)} style={{ accentColor: c.accent }} />
          admin
        </label>
        <button style={primaryBtn} type="submit" disabled={busy}>{busy ? "Creating…" : "Create user"}</button>
        {msg && <p style={createOk ? formNote : formNoteErr}>{msg}</p>}
      </form>
    </section>
  );
}

function UserRow(props: { u: AdminUser; onChanged: () => void }) {
  const [confirmDel, setConfirmDel] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [pass, setPass] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const reset = async () => {
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api(`/users/${props.u.id}/password`, { method: "POST", body: JSON.stringify({ password: pass }) });
      if (res.error) { setMsg(res.error); return; }
      setMsg("password updated"); setResetOpen(false); setPass("");
    } catch {
      setMsg("Failed to update password.");
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    setBusy(true);
    try {
      const res: { error?: string } = await api(`/users/${props.u.id}`, { method: "DELETE" });
      if (res.error) { setMsg(res.error); setConfirmDel(false); return; }
      props.onChanged();
    } catch {
      setMsg("Failed to delete user.");
      setConfirmDel(false);
    } finally {
      setBusy(false);
    }
  };

  const ok = msg === "password updated";

  return (
    <tr>
      <td style={{ ...td, color: c.text, fontWeight: 600 }}>{props.u.name}</td>
      <td style={td}>{props.u.isAdmin ? <span style={badge}>admin</span> : "—"}</td>
      <td style={td}>{fmtRel(props.u.createdAtMs) || "—"}</td>
      <td style={tdRight}>
        {msg ? (
          <span style={{ ...muted, fontSize: "0.8rem", color: ok ? c.muted : c.danger }}>{msg}</span>
        ) : resetOpen ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center", flexWrap: "wrap" }}>
            <input style={{ ...input, padding: "0.35rem 0.6rem", fontSize: "0.85rem", width: "11rem" }} type="password" placeholder="new password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={reset}>Save</button>
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => { setResetOpen(false); setPass(""); }}>Cancel</button>
          </span>
        ) : confirmDel ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button className="press" style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} type="button" disabled={busy} onClick={del}>Confirm delete</button>
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => setConfirmDel(false)}>Cancel</button>
          </span>
        ) : (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button className="press" style={ghostBtn} type="button" onClick={() => setResetOpen(true)}>Reset password</button>
            <button className="press" style={ghostBtn} type="button" onClick={() => setConfirmDel(true)}>Delete</button>
          </span>
        )}
      </td>
    </tr>
  );
}

type ImportPlan = {
  source: string;
  libraries: { name: string; type: string; new: boolean; works: number; editions: number; skipped: number }[];
  works: number; editions: number; files: number; progressRows: number; playlists: number;
  users: { name: string; isAdmin: boolean; exists: boolean; tempPassword?: string }[];
  warnings: string[];
};

function formatPlan(p: ImportPlan): string {
  const lines: string[] = [
    `${p.libraries.length} libraries · ${p.works} works · ${p.editions} editions · ${p.files} files · ${p.progressRows} progress rows${p.playlists > 0 ? ` · ${p.playlists} playlists` : ""}`,
  ];
  for (const l of p.libraries) {
    lines.push(`${l.new ? "+" : "="} ${l.name} (${l.type}) — ${l.works} works, ${l.editions} editions${l.skipped > 0 ? `, ${l.skipped} skipped` : ""}`);
  }
  for (const u of p.users) {
    if (u.tempPassword) lines.push(`user ${u.name}: temp password ${u.tempPassword} (reset after first login)`);
  }
  for (const w of p.warnings) {
    lines.push(`! ${w}`);
  }
  return lines.join("\n");
}

function ImportSection() {
  const [source, setSource] = useState("abs");
  const [path, setPath] = useState("");
  const [dryRun, setDryRun] = useState(true);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmRun, setConfirmRun] = useState(false);
  const [msg, setMsg] = useState("");

  const run = async (dry: boolean) => {
    setBusy(true);
    setMsg("");
    try {
      const body = source === "abs" ? { dataDir: path, dryRun: dry } : { dbPath: path, dryRun: dry };
      const res = await api(`/import/${source}`, { method: "POST", body: JSON.stringify(body) });
      if (res.error) { setMsg(res.error); return; }
      setPlan(res.plan);
      if (!dry) setConfirmRun(false);
    } catch {
      setMsg("Import failed — couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section style={panel}>
      <h3 style={railTitle}>Import</h3>
      <p style={{ ...muted, marginBottom: "0.2rem" }}>Migrate an existing Audiobookshelf or Kavita instance. The foreign database is only read.</p>
      <div style={formBlock}>
        <Field label="Source">
          <div style={{ ...selectWrap, width: "100%" }}>
            <select className="pill" style={{ width: "100%" }} value={source} onChange={(e) => { setSource((e.target as HTMLSelectElement).value); setPlan(null); }} aria-label="Import source">
              <option value="abs">Audiobookshelf (data dir)</option>
              <option value="kavita">Kavita (app.db)</option>
            </select>
            <span style={selectChevron}><IconChevronDown size={12} /></span>
          </div>
        </Field>
        <Field label="Path">
          <input
            style={input}
            placeholder={source === "abs" ? "/config/dir (contains abs_database.db)" : "/path/to/app.db"}
            value={path}
            onInput={(e) => setPath((e.target as HTMLInputElement).value)}
          />
        </Field>
        <label style={{ display: "flex", gap: "0.45rem", alignItems: "center", fontSize: "0.88rem", color: c.textDim, cursor: "pointer" }}>
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun((e.target as HTMLInputElement).checked)} style={{ accentColor: c.accent }} />
          Dry run (plan only, no changes)
        </label>
        <div style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexWrap: "wrap" }}>
          {confirmRun && !dryRun ? (
            <>
              <button className="press" style={{ ...ghostBtn, color: c.danger, borderColor: c.danger }} type="button" disabled={busy} onClick={() => run(false)}>Import now</button>
              <button className="press" style={ghostBtn} type="button" onClick={() => setConfirmRun(false)}>Cancel</button>
            </>
          ) : (
            <button className="press" style={primaryBtn} type="button" disabled={busy || !path} onClick={() => (dryRun ? run(true) : setConfirmRun(true))}>
              {busy ? "Working…" : dryRun ? "Run plan" : "Import"}
            </button>
          )}
        </div>
        {msg && <p style={formNoteErr}>{msg}</p>}
      </div>

      {plan && (
        <div style={{ marginTop: "1.3rem", maxWidth: "46rem" }}>
          <p style={{ ...fieldLabel, marginBottom: "0.5rem" }}>Plan</p>
          <pre style={preBlock}>{formatPlan(plan)}</pre>
        </div>
      )}
    </section>
  );
}

function TokensSection(props: { users: AdminUser[] }) {
  const [rows, setRows] = useState<TokenRow[]>([]);
  const [label, setLabel] = useState("");
  const [issued, setIssued] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const load = () => api("/tokens").then((r: TokenRow[]) => setRows(Array.isArray(r) ? r : [])).catch(() => setRows([]));
  useEffect(() => { load(); }, []);

  const create = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setMsg("");
    try {
      const res: { token?: string; error?: string } = await api("/tokens", { method: "POST", body: JSON.stringify({ label }) });
      if (res.error) { setMsg(res.error); return; }
      setIssued(res.token || ""); setMsg(""); setLabel("");
      load();
    } catch {
      setMsg("Failed to issue token.");
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (id: number) => {
    if (busy) return;
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api(`/tokens/${id}`, { method: "DELETE" });
      if (res.error) { setMsg(res.error); return; }
      load();
    } catch {
      setMsg("Failed to revoke token.");
    } finally {
      setBusy(false);
    }
  };

  const nameOf = (id: number) => props.users.find((u) => u.id === id)?.name || `#${id}`;

  return (
    <section style={panel}>
      <div style={panelHead}>
        <h3 style={{ ...railTitle, margin: 0 }}>Tokens</h3>
        <button className="press" style={ghostBtn} onClick={load}>Refresh</button>
      </div>
      {rows.length === 0 ? (
        <p style={muted}>No tokens yet.</p>
      ) : (
        <div style={tableWrap}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Label</th>
                <th style={th}>User</th>
                <th style={th}>Created</th>
                <th style={th}>Last seen</th>
                <th style={th}>Status</th>
                <th style={thRight}>Action</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id}>
                  <td style={{ ...td, color: c.text }}>{t.label}</td>
                  <td style={td}>{nameOf(t.userId)}</td>
                  <td style={td}>{fmtRel(t.createdAtMs) || "—"}</td>
                  <td style={td}>{fmtRel(t.lastSeenAtMs) || "—"}</td>
                  <td style={td}><StatusTag status={t.revokedAtMs ? "revoked" : "active"} tone={t.revokedAtMs ? c.danger : c.ok} /></td>
                  <td style={tdRight}>
                    {t.revokedAtMs ? "—" : (
                      <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => revoke(t.id)}>Revoke</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <form style={formBlock} onSubmit={create}>
        <Field label="Label">
          <input style={input} placeholder="e.g. phone" value={label} onInput={(e) => setLabel((e.target as HTMLInputElement).value)} />
        </Field>
        <button style={primaryBtn} type="submit" disabled={busy}>{busy ? "Issuing…" : "Issue"}</button>
        {msg && <p style={formNoteErr}>{msg}</p>}
      </form>

      {issued && (
        <div style={{ ...formBlock, maxWidth: "none" }}>
          <p style={formNote}>Copy this token now — it will not be shown again.</p>
          <code style={{ fontFamily: mono, fontSize: "0.82rem", wordBreak: "break-all", color: c.textDim }}>{issued}</code>
          <div>
            <button className="press" style={ghostBtn} type="button" onClick={() => setIssued("")}>Dismiss</button>
          </div>
        </div>
      )}
    </section>
  );
}
